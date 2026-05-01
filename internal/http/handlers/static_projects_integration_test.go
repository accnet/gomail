package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/staticprojects"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupStaticTestDB creates an in-memory SQLite database for static project tests.
func setupStaticTestDB(t *testing.T) *gorm.DB {
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	database.AutoMigrate(&db.User{}, &db.StaticProject{}, &db.Domain{}, &db.AuditLog{})
	return database
}

// setupStaticTestServer creates a test server with static project routes.
func setupStaticTestServer(t *testing.T, database *gorm.DB) (*gin.Engine, *staticprojects.Service, uuid.UUID) {
	cfg := config.Config{
		StaticSitesRoot:              t.TempDir(),
		StaticSitesMaxArchiveBytes:   10 * 1024 * 1024,
		StaticSitesMaxExtractedBytes: 50 * 1024 * 1024,
		StaticSitesMaxFileCount:      1000,
		StaticSitesBaseDomain:        "example.com",
		TraefikPublicIP:              "1.2.3.4",
		TraefikDynamicConfDir:        t.TempDir(),
	}

	svc := staticprojects.NewService(database, cfg)
	handler := NewStaticProjectsHandler(svc)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	protected := r.Group("/api", func(c *gin.Context) {
		uid := c.Request.Header.Get("testUserID")
		if uid == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		id := uuid.MustParse(uid)
		c.Set("user", db.User{ID: id, IsSuperAdmin: false, MaxWebsites: 5})
	})
	WireStaticProjectRoutes(protected, handler)

	userID := uuid.New()
	database.Create(&db.User{ID: userID, Email: "test@example.com", Name: "Test User", MaxWebsites: 5})

	return r, svc, userID
}

// createTestZip creates a zip archive in memory for testing.
func createTestZip(files map[string]string) []byte {
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	for name, content := range files {
		f, _ := w.Create(name)
		f.Write([]byte(content))
	}
	w.Close()
	return buf.Bytes()
}

// staticUploadRequest builds a multipart POST request to deploy a ZIP.
func staticUploadRequest(t *testing.T, url string, name string, zipData []byte, userID uuid.UUID) *http.Request {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	if name != "" {
		writer.WriteField("name", name)
	}
	part, _ := writer.CreateFormFile("file", "site.zip")
	part.Write(zipData)
	writer.Close()

	req := httptest.NewRequest("POST", url, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("testUserID", userID.String())
	return req
}

// parseProjectResp parses a static project JSON response (not wrapped in "data").
func parseProjectResp(t *testing.T, data []byte) staticprojects.ProjectResponse {
	var resp staticprojects.ProjectResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("unmarshal project: %v", err)
	}
	return resp
}

// parseErrorCode parses the error code from the JSON error response.
func parseErrorCode(t *testing.T, data []byte) string {
	var errResp struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &errResp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	return errResp.Code
}

// ---- T72: Integration test for deploy/list/redeploy/delete ----

func TestStaticProjects_DeployAndList(t *testing.T) {
	database := setupStaticTestDB(t)
	r, _, userID := setupStaticTestServer(t, database)

	zipData := createTestZip(map[string]string{
		"index.html": "<html><body>Hello</body></html>",
		"style.css":  "body { color: red; }",
	})

	// Deploy
	req := staticUploadRequest(t, "/api/static-projects/deploy", "My Site", zipData, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("deploy status = %d, body = %s", w.Code, w.Body.String())
	}

	project := parseProjectResp(t, w.Body.Bytes())
	if project.Name != "My Site" {
		t.Errorf("name = %q, want %q", project.Name, "My Site")
	}
	if project.Status != "published" {
		t.Errorf("status = %q, want %q", project.Status, "published")
	}
	if project.UIState != "live" {
		t.Errorf("ui_state = %q, want %q", project.UIState, "live")
	}
	if project.Subdomain == "" {
		t.Error("subdomain should not be empty")
	}
	var deployLogs int64
	if err := database.Model(&db.AuditLog{}).Where("type = ?", "static_project.deploy").Count(&deployLogs).Error; err != nil {
		t.Fatal(err)
	}
	if deployLogs == 0 {
		t.Fatal("expected static_project.deploy audit log")
	}

	// List
	req = httptest.NewRequest("GET", "/api/static-projects", nil)
	req.Header.Set("testUserID", userID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d", w.Code)
	}

	var projects []staticprojects.ProjectResponse
	if err := json.Unmarshal(w.Body.Bytes(), &projects); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(projects) != 1 {
		t.Errorf("list count = %d, want 1", len(projects))
	}
}

func TestStaticProjects_Redeploy(t *testing.T) {
	database := setupStaticTestDB(t)
	r, _, userID := setupStaticTestServer(t, database)

	// Deploy initial
	zipData := createTestZip(map[string]string{"index.html": "<html>v1</html>"})
	req := staticUploadRequest(t, "/api/static-projects/deploy", "My Site", zipData, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	project := parseProjectResp(t, w.Body.Bytes())

	// Redeploy
	zipData2 := createTestZip(map[string]string{"index.html": "<html>v2</html>"})
	redeployURL := fmt.Sprintf("/api/static-projects/%s/redeploy", project.ID)
	req = staticUploadRequest(t, redeployURL, "", zipData2, userID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("redeploy status = %d, body = %s", w.Code, w.Body.String())
	}

	redeployed := parseProjectResp(t, w.Body.Bytes())
	if redeployed.Status != "published" {
		t.Errorf("redeploy status = %q, want %q", redeployed.Status, "published")
	}
}

func TestStaticProjects_ToggleAndDelete(t *testing.T) {
	database := setupStaticTestDB(t)
	r, _, userID := setupStaticTestServer(t, database)

	// Deploy
	zipData := createTestZip(map[string]string{"index.html": "<html>test</html>"})
	req := staticUploadRequest(t, "/api/static-projects/deploy", "TestSite", zipData, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	project := parseProjectResp(t, w.Body.Bytes())
	projectID := project.ID

	// Toggle disable
	toggleBody := `{"is_active": false}`
	req = httptest.NewRequest("PATCH", fmt.Sprintf("/api/static-projects/%s/status", projectID), bytes.NewBufferString(toggleBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("testUserID", userID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("toggle status = %d, body = %s", w.Code, w.Body.String())
	}

	toggled := parseProjectResp(t, w.Body.Bytes())
	if toggled.IsActive {
		t.Error("expected is_active to be false after disable")
	}
	if toggled.UIState != "disabled" {
		t.Errorf("ui_state = %q, want %q", toggled.UIState, "disabled")
	}

	// Delete
	req = httptest.NewRequest("DELETE", fmt.Sprintf("/api/static-projects/%s", projectID), nil)
	req.Header.Set("testUserID", userID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", w.Code, w.Body.String())
	}

	// List should be empty
	req = httptest.NewRequest("GET", "/api/static-projects", nil)
	req.Header.Set("testUserID", userID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var projects []staticprojects.ProjectResponse
	json.Unmarshal(w.Body.Bytes(), &projects)
	if len(projects) != 0 {
		t.Errorf("list count after delete = %d, want 0", len(projects))
	}
}

func TestStaticProjects_QuotaExceeded(t *testing.T) {
	database := setupStaticTestDB(t)
	cfg := config.Config{
		StaticSitesRoot:              t.TempDir(),
		StaticSitesMaxArchiveBytes:   10 * 1024 * 1024,
		StaticSitesMaxExtractedBytes: 50 * 1024 * 1024,
		StaticSitesMaxFileCount:      1000,
		StaticSitesBaseDomain:        "example.com",
	}

	svc := staticprojects.NewService(database, cfg)
	handler := NewStaticProjectsHandler(svc)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	protected := r.Group("/api", func(c *gin.Context) {
		uid := c.Request.Header.Get("testUserID")
		if uid == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		id := uuid.MustParse(uid)
		c.Set("user", db.User{ID: id, IsSuperAdmin: false, MaxWebsites: 1})
	})
	WireStaticProjectRoutes(protected, handler)

	userID := uuid.New()
	database.Create(&db.User{ID: userID, Email: "quota@test.com", MaxWebsites: 1})

	// First deploy (should succeed)
	zipData := createTestZip(map[string]string{"index.html": "<html>first</html>"})
	req := staticUploadRequest(t, "/api/static-projects/deploy", "First", zipData, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("first deploy status = %d", w.Code)
	}

	// Second deploy (should fail with quota exceeded)
	zipData2 := createTestZip(map[string]string{"index.html": "<html>second</html>"})
	req = staticUploadRequest(t, "/api/static-projects/deploy", "Second", zipData2, userID)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("second deploy status = %d, want 403", w.Code)
	}

	errCode := parseErrorCode(t, w.Body.Bytes())
	if errCode != "website_quota_exceeded" {
		t.Errorf("error code = %q, want %q", errCode, "website_quota_exceeded")
	}
}

// ---- T73: Static server host resolve ----

func TestStaticServerHostResolve(t *testing.T) {
	database := setupStaticTestDB(t)

	projectID := uuid.New()
	userID := uuid.New()
	rootFolder := filepath.Join(os.TempDir(), "static-test", projectID.String())
	os.MkdirAll(rootFolder, 0755)
	os.WriteFile(filepath.Join(rootFolder, "index.html"), []byte("<html>Hello</html>"), 0644)
	os.WriteFile(filepath.Join(rootFolder, "style.css"), []byte("body{}"), 0644)

	database.Create(&db.User{ID: userID, Email: "host@test.com", MaxWebsites: 5})
	database.Create(&db.StaticProject{
		ID:         projectID,
		UserID:     userID,
		Name:       "Test",
		Subdomain:  "testproj",
		RootFolder: rootFolder,
		Status:     "published",
		IsActive:   true,
	})

	resolver := staticprojects.NewHostResolver(database, "example.com", "")

	// Test resolve by subdomain
	project, err := resolver.Resolve("testproj.example.com")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if project == nil {
		t.Fatal("expected project to be resolved")
	}
	if project.ID != projectID {
		t.Errorf("resolved project ID = %v, want %v", project.ID, projectID)
	}
	if !project.IsActive {
		t.Error("expected project to be active")
	}

	// Test resolve with port
	project, err = resolver.Resolve("testproj.example.com:443")
	if err != nil {
		t.Fatalf("Resolve with port error: %v", err)
	}
	if project == nil {
		t.Fatal("expected project to be resolved with port")
	}

	// Test resolve nonexistent
	project, err = resolver.Resolve("nope.other.com")
	if err != nil {
		t.Fatalf("Resolve nonexistent error: %v", err)
	}
	if project != nil {
		t.Error("expected nil for nonexistent project")
	}
}

func TestStaticServer_DisabledProject(t *testing.T) {
	database := setupStaticTestDB(t)

	projectID := uuid.New()
	userID := uuid.New()
	rootFolder := filepath.Join(os.TempDir(), "static-test-disabled", projectID.String())
	os.MkdirAll(rootFolder, 0755)
	os.WriteFile(filepath.Join(rootFolder, "index.html"), []byte("<html>Disabled</html>"), 0644)

	database.Create(&db.User{ID: userID, Email: "disabled@test.com", MaxWebsites: 5})
	// Use raw SQL to bypass GORM zero-value handling for IsActive=false
	database.Exec(`INSERT INTO static_projects (id, user_id, name, subdomain, root_folder, staging_folder, status, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, projectID, userID, "Disabled Site", "disabled-site", rootFolder, rootFolder, "published", false)

	resolver := staticprojects.NewHostResolver(database, "example.com", "")
	project, err := resolver.Resolve("disabled-site.example.com")
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if project == nil {
		t.Fatal("expected project to be found even if disabled")
	}
	if project.IsActive {
		t.Error("expected project to show as inactive")
	}
}

// ---- SPA fallback helper test ----
func TestSpaFallbackCondition(t *testing.T) {
	// Test the SPA fallback logic used in static-server
	tests := []struct {
		name    string
		path    string
		accept  string
		method  string
		wantSPA bool
	}{
		{name: "root HTML nav", path: "/", accept: "text/html", method: "GET", wantSPA: true},
		{name: "route HTML nav", path: "/about", accept: "text/html", method: "GET", wantSPA: true},
		{name: "JS asset", path: "/app.js", accept: "text/html", method: "GET", wantSPA: false},
		{name: "CSS file", path: "/style.css", accept: "text/html, */*", method: "GET", wantSPA: false},
		{name: "image", path: "/logo.png", accept: "text/html", method: "GET", wantSPA: false},
		{name: "POST not SPA", path: "/form", accept: "text/html", method: "POST", wantSPA: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate the logic from static-server
			hasExt := filepath.Ext(tt.path) != ""
			isHTMLNav := tt.method == "GET" && (tt.accept == "text/html" || tt.accept == "text/html, */*") && !hasExt
			if isHTMLNav != tt.wantSPA {
				t.Errorf("SPA fallback for path=%q method=%s accept=%q: got %v, want %v",
					tt.path, tt.method, tt.accept, isHTMLNav, tt.wantSPA)
			}
		})
	}
}

// ---- T74: Domain binding integration ----

func TestStaticProjects_DomainBinding(t *testing.T) {
	database := setupStaticTestDB(t)
	r, svc, userID := setupStaticTestServer(t, database)

	// Create a verified domain for the test user
	domainID := uuid.New()
	database.Create(&db.Domain{
		ID:     domainID,
		UserID: userID,
		Name:   "custom.com",
		Status: "verified",
	})

	// Deploy a project
	zipData := createTestZip(map[string]string{"index.html": "<html>domain test</html>"})
	req := staticUploadRequest(t, "/api/static-projects/deploy", "Domain Site", zipData, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	project := parseProjectResp(t, w.Body.Bytes())
	projectID := project.ID

	// Get available domains
	req = httptest.NewRequest("GET", fmt.Sprintf("/api/static-projects/%s/available-domains", projectID), nil)
	req.Header.Set("testUserID", userID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("available-domains status = %d", w.Code)
	}

	var domains []db.Domain
	if err := json.Unmarshal(w.Body.Bytes(), &domains); err != nil {
		t.Fatalf("unmarshal domains: %v", err)
	}
	if len(domains) == 0 {
		t.Fatal("expected at least one available domain")
	}
	if domains[0].Name != "custom.com" {
		t.Errorf("domain name = %q, want %q", domains[0].Name, "custom.com")
	}

	// Assign domain
	assignBody := fmt.Sprintf(`{"domain_id": "%s"}`, domainID)
	req = httptest.NewRequest("PATCH", fmt.Sprintf("/api/static-projects/%s/domain", projectID), bytes.NewBufferString(assignBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("testUserID", userID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("assign domain status = %d, body = %s", w.Code, w.Body.String())
	}

	assigned := parseProjectResp(t, w.Body.Bytes())
	if assigned.AssignedDomain != "custom.com" {
		t.Errorf("assigned_domain = %q, want %q", assigned.AssignedDomain, "custom.com")
	}

	// Check domain IP
	req = httptest.NewRequest("POST", fmt.Sprintf("/api/static-projects/%s/domain/check-ip", projectID), nil)
	req.Header.Set("testUserID", userID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("check-ip status = %d, body = %s", w.Code, w.Body.String())
	}

	// Unassign domain
	req = httptest.NewRequest("DELETE", fmt.Sprintf("/api/static-projects/%s/domain", projectID), nil)
	req.Header.Set("testUserID", userID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unassign domain status = %d, body = %s", w.Code, w.Body.String())
	}

	unassigned := parseProjectResp(t, w.Body.Bytes())
	if unassigned.AssignedDomain != "" {
		t.Errorf("after unassign, assigned_domain = %q, want empty", unassigned.AssignedDomain)
	}

	// Cleanup temp dirs
	paths := svc.Storage.ProjectPaths(projectID)
	os.RemoveAll(paths.Live)
	os.RemoveAll(paths.Staging)
}

func TestStaticProjects_GetByID(t *testing.T) {
	database := setupStaticTestDB(t)
	r, _, userID := setupStaticTestServer(t, database)

	// Deploy first
	zipData := createTestZip(map[string]string{"index.html": "<html>get test</html>"})
	req := staticUploadRequest(t, "/api/static-projects/deploy", "GetTest", zipData, userID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	project := parseProjectResp(t, w.Body.Bytes())

	// Get by ID
	req = httptest.NewRequest("GET", fmt.Sprintf("/api/static-projects/%s", project.ID), nil)
	req.Header.Set("testUserID", userID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("get by id status = %d", w.Code)
	}

	got := parseProjectResp(t, w.Body.Bytes())
	if got.ID != project.ID {
		t.Errorf("ID = %v, want %v", got.ID, project.ID)
	}
	if got.Name != "GetTest" {
		t.Errorf("Name = %q, want %q", got.Name, "GetTest")
	}

	// Non-owner should not see
	otherUserID := uuid.New()
	database.Create(&db.User{ID: otherUserID, Email: "other@test.com", MaxWebsites: 5})
	req = httptest.NewRequest("GET", fmt.Sprintf("/api/static-projects/%s", project.ID), nil)
	req.Header.Set("testUserID", otherUserID.String())
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("non-owner get status = %d, want 404", w.Code)
	}
}

// Test archive validation - path traversal should be rejected at the API level.
func TestStaticProjects_InvalidArchive(t *testing.T) {
	database := setupStaticTestDB(t)
	r, _, userID := setupStaticTestServer(t, database)

	// Create a zip with path traversal
	buf := new(bytes.Buffer)
	w := zip.NewWriter(buf)
	f, _ := w.Create("../evil.txt")
	f.Write([]byte("malicious"))
	w.Close()
	zipData := buf.Bytes()

	req := staticUploadRequest(t, "/api/static-projects/deploy", "Evil", zipData, userID)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code == http.StatusCreated {
		t.Fatal("deploy with path traversal should have failed")
	}

	// Verify it's an invalid_archive error
	errCode := parseErrorCode(t, resp.Body.Bytes())
	if errCode != "invalid_archive" && errCode != "forbidden_file_type" {
		t.Errorf("expected invalid_archive or forbidden_file_type, got %q", errCode)
	}
}
