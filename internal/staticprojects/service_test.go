package staticprojects

import (
	"archive/zip"
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/storage"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// ---- T70: ZIP validators ----

func TestValidateZipEntry_PathTraversal(t *testing.T) {
	tests := []struct {
		name    string
		zipPath string
		wantErr bool
	}{
		{name: "normal file", zipPath: "index.html", wantErr: false},
		{name: "dotdot prefix", zipPath: "../etc/passwd", wantErr: true},
		{name: "absolute path", zipPath: "/etc/passwd", wantErr: true},
		{name: "dotdot in middle", zipPath: "sub/../../../etc/passwd", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := createZipFile(tt.zipPath, []byte("data"))
			err := (&Service{}).validateZipEntry(f)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateZipEntry(%q) error = %v, wantErr = %v", tt.zipPath, err, tt.wantErr)
			}
		})
	}
}

func TestValidateZipEntry_Symlink(t *testing.T) {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	h := &zip.FileHeader{
		Name:   "evil.sh",
		Method: zip.Store,
	}
	h.SetMode(0777 | os.ModeSymlink)
	w.CreateHeader(h)
	w.Close()

	reader, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	err := (&Service{}).validateZipEntry(reader.File[0])
	if err == nil {
		t.Error("expected symlink to be rejected")
	}
}

func TestValidateZipEntry_NullByteName(t *testing.T) {
	f := createZipFile("bad\x00name.html", []byte("data"))
	err := (&Service{}).validateZipEntry(f)
	if err == nil {
		t.Error("expected null-byte filename to be rejected")
	}
}

func TestValidateZipEntry_ForbiddenExtension(t *testing.T) {
	svc := &Service{}
	forbidden := []string{"shell.php", "script.phtml", "test.py", "run.sh", "app.exe", "config.htaccess"}
	for _, name := range forbidden {
		t.Run(name, func(t *testing.T) {
			f := createZipFile(name, []byte("data"))
			err := svc.validateZipEntry(f)
			if err == nil {
				t.Errorf("expected %s to be rejected", name)
			}
			if !strings.Contains(err.Error(), "forbidden") {
				t.Errorf("expected forbidden error, got: %v", err)
			}
		})
	}
}

func TestValidateZipEntry_AllowedExtension(t *testing.T) {
	svc := &Service{}
	allowed := []string{"index.html", "style.css", "app.js", "image.png", "font.woff2", "data.json", "site.webmanifest"}
	for _, name := range allowed {
		t.Run(name, func(t *testing.T) {
			f := createZipFile(name, []byte("data"))
			err := svc.validateZipEntry(f)
			if err != nil {
				t.Errorf("expected %s to be allowed, got: %v", name, err)
			}
		})
	}
}

func TestDetectPublishRoot(t *testing.T) {
	tests := []struct {
		name    string
		files   []string
		want    string
		wantErr bool
	}{
		{name: "root index.html", files: []string{"index.html", "style.css"}, want: "", wantErr: false},
		{name: "single nested folder", files: []string{"myapp/index.html", "myapp/style.css"}, want: "myapp", wantErr: false},
		{name: "multiple candidates", files: []string{"app1/index.html", "app2/index.html", "app1/style.css"}, wantErr: true},
		{name: "no index.html", files: []string{"style.css", "app.js"}, wantErr: true},
		{name: "nested more than 1 level", files: []string{"deep/nested/index.html", "deep/nested/style.css"}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := detectPublishRoot(tt.files)
			if (err != nil) != tt.wantErr {
				t.Errorf("detectPublishRoot() error = %v, wantErr = %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("detectPublishRoot() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractAndValidateLimits(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		maxBytes   int64
		maxFiles   int
		wantErrMsg string
	}{
		{
			name:       "extracted size limit",
			files:      map[string]string{"index.html": strings.Repeat("x", 32)},
			maxBytes:   10,
			maxFiles:   10,
			wantErrMsg: "extracted size exceeds",
		},
		{
			name:       "file count limit",
			files:      map[string]string{"index.html": "ok", "a.css": "a", "b.css": "b"},
			maxBytes:   1024,
			maxFiles:   2,
			wantErrMsg: "file count exceeds",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &Service{
				Config: config.Config{
					StaticSitesRoot:              t.TempDir(),
					StaticSitesMaxArchiveBytes:   1024 * 1024,
					StaticSitesMaxExtractedBytes: tt.maxBytes,
					StaticSitesMaxFileCount:      tt.maxFiles,
				},
			}
			svc.Storage = storageManagerForTest(svc.Config.StaticSitesRoot)
			projectID := uuid.New()
			if err := svc.Storage.EnsureProjectDirs(projectID); err != nil {
				t.Fatal(err)
			}
			_, _, _, err := svc.extractAndValidate(projectID, zipBytes(tt.files))
			if err == nil || !strings.Contains(err.Error(), tt.wantErrMsg) {
				t.Fatalf("extractAndValidate error = %v, want %q", err, tt.wantErrMsg)
			}
		})
	}
}

func TestDeployRejectsOversizedArchive(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&db.User{}, &db.StaticProject{}); err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	if err := database.Create(&db.User{ID: userID, Email: "oversized@example.com", MaxWebsites: 1}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewService(database, config.Config{
		StaticSitesRoot:              t.TempDir(),
		StaticSitesMaxArchiveBytes:   16,
		StaticSitesMaxExtractedBytes: 1024,
		StaticSitesMaxFileCount:      10,
	})

	_, err = svc.DeployStream(nil, userID, "Too Big", bytes.NewReader(zipBytes(map[string]string{
		"index.html": strings.Repeat("x", 128),
	})), "site.zip")
	if err == nil || !strings.Contains(err.Error(), "archive size") {
		t.Fatalf("DeployStream error = %v, want archive size error", err)
	}
}

func TestPublishAtomicPreservesExistingLiveOnFailure(t *testing.T) {
	svc := NewService(nil, config.Config{StaticSitesRoot: t.TempDir()})
	projectID := uuid.New()
	paths := svc.Storage.ProjectPaths(projectID)
	if err := os.MkdirAll(paths.Live, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Live, "index.html"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	err := svc.publishAtomic(projectID, "")
	if err == nil {
		t.Fatal("expected publishAtomic to fail when staging is missing")
	}
	got, readErr := os.ReadFile(filepath.Join(paths.Live, "index.html"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old" {
		t.Fatalf("live content = %q, want old", got)
	}
}

func TestPublishAtomicPreservesOldThumbnail(t *testing.T) {
	svc := NewService(nil, config.Config{StaticSitesRoot: t.TempDir()})
	projectID := uuid.New()
	paths := svc.Storage.ProjectPaths(projectID)
	if err := os.MkdirAll(paths.Live, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Live, "thumbnail.png"), []byte("thumb"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(paths.Staging, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Staging, "index.html"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := svc.publishAtomic(projectID, ""); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(paths.Live, "thumbnail.png"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "thumb" {
		t.Fatalf("thumbnail = %q, want thumb", got)
	}
}

// ---- T71: Service helpers ----

func TestComputeUIState(t *testing.T) {
	tests := []struct {
		name    string
		project db.StaticProject
		want    UIState
	}{
		{name: "live", project: db.StaticProject{IsActive: true, Status: "published"}, want: UIStateLive},
		{name: "disabled", project: db.StaticProject{IsActive: false}, want: UIStateDisabled},
		{name: "disabled even if published", project: db.StaticProject{IsActive: false, Status: "published"}, want: UIStateDisabled},
		{name: "deploying (draft)", project: db.StaticProject{IsActive: true, Status: "draft"}, want: UIStateDeploying},
		{name: "deploying (uploaded)", project: db.StaticProject{IsActive: true, Status: "deploying"}, want: UIStateDeploying},
		{name: "failed", project: db.StaticProject{IsActive: true, Status: "publish_failed"}, want: UIStateFailed},
		{name: "unknown status defaults to failed", project: db.StaticProject{IsActive: true, Status: "unknown"}, want: UIStateFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeUIState(&tt.project)
			if got != tt.want {
				t.Errorf("ComputeUIState() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQuotaInfoAndGenerateSubdomain(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&db.User{}, &db.StaticProject{}); err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	if err := database.Create(&db.User{ID: userID, Email: "quota-info@example.com", MaxWebsites: 3}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&db.StaticProject{UserID: userID, Name: "One", Subdomain: "one", Status: "published"}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewService(database, config.Config{StaticSitesRoot: t.TempDir()})
	used, max, err := svc.QuotaInfo(userID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 1 || max != 3 {
		t.Fatalf("QuotaInfo = used %d max %d, want used 1 max 3", used, max)
	}

	subdomain, err := svc.generateSubdomain(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(subdomain) != 8 {
		t.Fatalf("subdomain length = %d, want 8", len(subdomain))
	}
	if subdomain == "one" {
		t.Fatal("generated subdomain collided with existing project")
	}
}

func TestCheckDomainIPLocalhost(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&db.User{}, &db.StaticProject{}); err != nil {
		t.Fatal(err)
	}
	userID := uuid.New()
	projectID := uuid.New()
	if err := database.Create(&db.User{ID: userID, Email: "dns@example.com", MaxWebsites: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&db.StaticProject{
		ID:             projectID,
		UserID:         userID,
		Name:           "DNS",
		Subdomain:      "dns",
		AssignedDomain: "localhost",
		Status:         "published",
		IsActive:       true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewService(database, config.Config{StaticSitesRoot: t.TempDir(), TraefikPublicIP: "127.0.0.1"})
	ok, msg, err := svc.CheckDomainIP(projectID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("CheckDomainIP ok = false, msg = %q", msg)
	}
}

func TestDomainAssignmentAndActiveSSL(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&db.User{}, &db.Domain{}, &db.StaticProject{}); err != nil {
		t.Fatal(err)
	}

	userID := uuid.New()
	otherUserID := uuid.New()
	projectID := uuid.New()
	verifiedID := uuid.New()
	pendingID := uuid.New()
	otherDomainID := uuid.New()
	confDir := t.TempDir()

	rows := []any{
		&db.User{ID: userID, Email: "owner@example.com", MaxWebsites: 2},
		&db.User{ID: otherUserID, Email: "other@example.com", MaxWebsites: 2},
		&db.Domain{ID: verifiedID, UserID: userID, Name: "site.example.com", Status: "verified"},
		&db.Domain{ID: pendingID, UserID: userID, Name: "pending.example.com", Status: "pending"},
		&db.Domain{ID: otherDomainID, UserID: otherUserID, Name: "other.example.com", Status: "verified"},
		&db.StaticProject{ID: projectID, UserID: userID, Name: "Site", Subdomain: "site", Status: "published", IsActive: true},
	}
	for _, row := range rows {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(database, config.Config{
		StaticSitesRoot:       t.TempDir(),
		TraefikDynamicConfDir: confDir,
		StaticServerAddr:      ":8090",
	})

	available, err := svc.AvailableDomains(userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(available) != 1 || available[0].ID != verifiedID {
		t.Fatalf("AvailableDomains = %#v, want only verified owner domain", available)
	}

	if _, err := svc.AssignDomain(userID, projectID, pendingID); err != ErrDomainNotVerified {
		t.Fatalf("AssignDomain pending error = %v, want ErrDomainNotVerified", err)
	}
	if _, err := svc.AssignDomain(userID, projectID, otherDomainID); err != ErrDomainNotVerified {
		t.Fatalf("AssignDomain other owner error = %v, want ErrDomainNotVerified", err)
	}

	project, err := svc.AssignDomain(userID, projectID, verifiedID)
	if err != nil {
		t.Fatal(err)
	}
	if project.AssignedDomain != "site.example.com" || project.DomainBindingStatus != "assigned" {
		t.Fatalf("assigned domain = %q status = %q", project.AssignedDomain, project.DomainBindingStatus)
	}
	if _, err := svc.ActiveSSL(userID, projectID); err != ErrSSLConditionNotMet {
		t.Fatalf("ActiveSSL before DNS check error = %v, want ErrSSLConditionNotMet", err)
	}

	if err := database.Model(&db.Domain{}).Where("id = ?", verifiedID).Update("a_record_status", db.ARecordStatusVerified).Error; err != nil {
		t.Fatal(err)
	}
	project, err = svc.ActiveSSL(userID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if project.DomainBindingStatus != "ssl_active" || project.DomainLastDNSResult != "ok" {
		t.Fatalf("ActiveSSL via verified domain status = %q dns = %q", project.DomainBindingStatus, project.DomainLastDNSResult)
	}

	if err := database.Model(&db.StaticProject{}).Where("id = ?", projectID).Updates(map[string]any{
		"domain_binding_status":  db.DomainBindingStatusAssigned,
		"domain_tls_enabled_at":  nil,
		"domain_last_dns_result": "",
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := database.Model(&db.StaticProject{}).Where("id = ?", projectID).Update("domain_last_dns_result", "ok").Error; err != nil {
		t.Fatal(err)
	}
	project, err = svc.ActiveSSL(userID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if project.DomainBindingStatus != "ssl_active" || project.DomainTLSEnabledAt == nil {
		t.Fatalf("ActiveSSL status = %q tls_at = %v", project.DomainBindingStatus, project.DomainTLSEnabledAt)
	}
	configPath := filepath.Join(confDir, "static-"+projectID.String()+".yaml")
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("expected Traefik config to be written: %v", err)
	}

	project, err = svc.UnassignDomain(userID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if project.AssignedDomain != "" || project.DomainID != nil {
		t.Fatalf("unassigned domain = %q id = %v", project.AssignedDomain, project.DomainID)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected Traefik config cleanup, err = %v", err)
	}
}

func TestDomainAssignmentAndActiveSSLWithCommandProvisioner(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&db.User{}, &db.Domain{}, &db.StaticProject{}); err != nil {
		t.Fatal(err)
	}

	userID := uuid.New()
	projectID := uuid.New()
	domainID := uuid.New()
	tempDir := t.TempDir()
	commandLog := filepath.Join(tempDir, "ssl-command.log")
	issueScript := filepath.Join(tempDir, "issue.sh")
	cleanupScript := filepath.Join(tempDir, "cleanup.sh")

	if err := os.WriteFile(issueScript, []byte("#!/usr/bin/env bash\nprintf 'issue:%s\n' \"$1\" >> \""+commandLog+"\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cleanupScript, []byte("#!/usr/bin/env bash\nprintf 'cleanup:%s\n' \"$1\" >> \""+commandLog+"\"\n"), 0755); err != nil {
		t.Fatal(err)
	}

	rows := []any{
		&db.User{ID: userID, Email: "owner@example.com", MaxWebsites: 2},
		&db.Domain{ID: domainID, UserID: userID, Name: "site.example.com", Status: "verified", ARecordStatus: db.ARecordStatusVerified},
		&db.StaticProject{ID: projectID, UserID: userID, Name: "Site", Subdomain: "site", Status: "published", IsActive: true},
	}
	for _, row := range rows {
		if err := database.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := NewService(database, config.Config{
		StaticSitesRoot:              t.TempDir(),
		StaticSitesSSLProvider:       "command",
		StaticSitesSSLIssueCommand:   issueScript,
		StaticSitesSSLCleanupCommand: cleanupScript,
	})

	if _, err := svc.AssignDomain(userID, projectID, domainID); err != nil {
		t.Fatal(err)
	}
	project, err := svc.ActiveSSL(userID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if project.DomainBindingStatus != db.DomainBindingStatusSSLActive {
		t.Fatalf("status = %q", project.DomainBindingStatus)
	}

	if _, err := svc.UnassignDomain(userID, projectID); err != nil {
		t.Fatal(err)
	}

	logData, err := os.ReadFile(commandLog)
	if err != nil {
		t.Fatal(err)
	}
	got := string(logData)
	if !strings.Contains(got, "issue:site.example.com") || !strings.Contains(got, "cleanup:site.example.com") {
		t.Fatalf("command log = %q", got)
	}
}

func TestThumbnailWorkerProcessesActivePublishedProjects(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&db.User{}, &db.StaticProject{}); err != nil {
		t.Fatal(err)
	}

	userID := uuid.New()
	activeID := uuid.New()
	disabledID := uuid.New()
	root := t.TempDir()
	activeRoot := filepath.Join(root, "active")
	disabledRoot := filepath.Join(root, "disabled")
	if err := os.MkdirAll(activeRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(disabledRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&db.User{ID: userID, Email: "thumb@example.com", MaxWebsites: 2}).Error; err != nil {
		t.Fatal(err)
	}
	rows := []db.StaticProject{
		{ID: activeID, UserID: userID, Name: "Active", Subdomain: "active", RootFolder: activeRoot, Status: "published", ThumbnailStatus: "pending", IsActive: true},
		{ID: disabledID, UserID: userID, Name: "Disabled", Subdomain: "disabled", RootFolder: disabledRoot, Status: "published", ThumbnailStatus: "pending", IsActive: false},
	}
	for i := range rows {
		if err := database.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Model(&db.StaticProject{}).Where("id = ?", disabledID).Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}

	worker := NewThumbnailWorker(database, root, func(string) string { return "" })
	worker.processPending()

	var active db.StaticProject
	if err := database.First(&active, "id = ?", activeID).Error; err != nil {
		t.Fatal(err)
	}
	if active.ThumbnailStatus != "ready" {
		t.Fatalf("active thumbnail status = %q, want ready", active.ThumbnailStatus)
	}
	if _, err := os.Stat(filepath.Join(activeRoot, "thumbnail.png")); err != nil {
		t.Fatalf("expected active thumbnail file: %v", err)
	}
	thumbFile, err := os.Open(filepath.Join(activeRoot, "thumbnail.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer thumbFile.Close()
	thumbImg, err := png.DecodeConfig(thumbFile)
	if err != nil {
		t.Fatal(err)
	}
	if thumbImg.Width != 1280 || thumbImg.Height != 720 {
		t.Fatalf("thumbnail dimensions = %dx%d, want 1280x720", thumbImg.Width, thumbImg.Height)
	}

	var disabled db.StaticProject
	if err := database.First(&disabled, "id = ?", disabledID).Error; err != nil {
		t.Fatal(err)
	}
	if disabled.ThumbnailStatus != "pending" {
		t.Fatalf("disabled thumbnail status = %q, want pending", disabled.ThumbnailStatus)
	}
}

func TestThumbnailWorkerRefreshesLegacyReadyPlaceholder(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&db.User{}, &db.StaticProject{}); err != nil {
		t.Fatal(err)
	}

	userID := uuid.New()
	projectID := uuid.New()
	root := t.TempDir()
	projectRoot := filepath.Join(root, "legacy")
	if err := os.MkdirAll(projectRoot, 0755); err != nil {
		t.Fatal(err)
	}
	thumbnailPath := filepath.Join(projectRoot, "thumbnail.png")
	legacyPNG := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
		0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
		0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x60, 0x00, 0x00, 0x00,
		0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc, 0x33, 0x00, 0x00, 0x00, 0x00, 0x49,
		0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(thumbnailPath, legacyPNG, 0644); err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&db.User{ID: userID, Email: "legacy@example.com", MaxWebsites: 1}).Error; err != nil {
		t.Fatal(err)
	}
	project := db.StaticProject{ID: projectID, UserID: userID, Name: "Legacy", Subdomain: "legacy", RootFolder: projectRoot, Status: "published", ThumbnailStatus: "ready", ThumbnailPath: thumbnailPath, IsActive: true}
	if err := database.Create(&project).Error; err != nil {
		t.Fatal(err)
	}

	worker := NewThumbnailWorker(database, root, func(string) string { return "" })
	worker.processPending()

	thumbFile, err := os.Open(thumbnailPath)
	if err != nil {
		t.Fatal(err)
	}
	defer thumbFile.Close()
	thumbImg, err := png.DecodeConfig(thumbFile)
	if err != nil {
		t.Fatal(err)
	}
	if thumbImg.Width != 1280 || thumbImg.Height != 720 {
		t.Fatalf("thumbnail dimensions = %dx%d, want 1280x720", thumbImg.Width, thumbImg.Height)
	}
}

// ---- helpers ----

func zipBytes(files map[string]string) []byte {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	for name, content := range files {
		f, _ := w.Create(name)
		f.Write([]byte(content))
	}
	w.Close()
	return buf.Bytes()
}

func storageManagerForTest(root string) *storage.StaticSitesManager {
	return storage.NewStaticSitesManager(root)
}

// createZipFile creates an in-memory ZIP with the given file and returns the zip.File.
func createZipFile(name string, content []byte) *zip.File {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	f, _ := w.Create(name)
	f.Write(content)
	w.Close()

	reader, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	return reader.File[0]
}
