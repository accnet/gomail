package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gomail/internal/auth"
	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/dns"

	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		t.Fatal(err)
	}
	return database
}

func testConfig() config.Config {
	return config.Config{
		AppEnv:                  "test",
		JWTSecret:               "test-secret-123456789",
		AccessTokenTTL:          time.Hour,
		RefreshTokenTTL:         24 * time.Hour,
		MXTarget:                "mx.test.local",
		DKIMKeyEncryptionSecret: "test-dkim-encryption-secret-32-bytes",
	}
}

type verifyStub struct {
	ok     bool
	errMsg string
}

func (v verifyStub) Verify(_ interface{}, _ string) (bool, string) {
	return v.ok, v.errMsg
}

func newTestApp(t *testing.T) (App, *gorm.DB) {
	t.Helper()
	database := testDB(t)
	cfg := testConfig()
	return App{
		DB:       database,
		Auth:     auth.NewService(database, cfg),
		Config:   cfg,
		Verifier: dns.Verifier{MXTarget: cfg.MXTarget, Timeout: time.Second},
	}, database
}

func createUser(t *testing.T, database *gorm.DB, email string, active bool, admin bool, super bool) db.User {
	t.Helper()
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	user := db.User{
		Email:               email,
		Name:                email,
		PasswordHash:        hash,
		IsActive:            active,
		IsAdmin:             admin,
		IsSuperAdmin:        super,
		MaxDomains:          10,
		MaxInboxes:          10,
		MaxMessageSizeMB:    25,
		MaxAttachmentSizeMB: 25,
		MaxStorageBytes:     1 << 30,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	return user
}

func bearerToken(t *testing.T, app App, user db.User) string {
	t.Helper()
	token, err := app.Auth.AccessToken(user)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func doJSON(t *testing.T, router http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestLoginBootstrapProfileFlow(t *testing.T) {
	app, database := newTestApp(t)
	router := app.Router()

	user := createUser(t, database, "user@test.local", true, false, false)

	loginResp := doJSON(t, router, http.MethodPost, "/api/auth/login", map[string]any{
		"email":    user.Email,
		"password": "password123",
	}, "")
	if loginResp.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", loginResp.Code, loginResp.Body.String())
	}

	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginResp.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	if loginBody.AccessToken == "" {
		t.Fatal("expected access token in login response")
	}

	meResp := doJSON(t, router, http.MethodGet, "/api/me", nil, loginBody.AccessToken)
	if meResp.Code != http.StatusOK {
		t.Fatalf("me status = %d body=%s", meResp.Code, meResp.Body.String())
	}

	var meBody db.User
	if err := json.Unmarshal(meResp.Body.Bytes(), &meBody); err != nil {
		t.Fatal(err)
	}
	if meBody.ID != user.ID {
		t.Fatalf("me user id = %s want %s", meBody.ID, user.ID)
	}
	if meBody.Email != user.Email {
		t.Fatalf("me email = %s want %s", meBody.Email, user.Email)
	}
}

func TestEventsStreamAcceptsQueryToken(t *testing.T) {
	app, database := newTestApp(t)
	router := app.Router()

	user := createUser(t, database, "events@test.local", true, false, false)
	token := bearerToken(t, app, user)

	req := httptest.NewRequest(http.MethodGet, "/api/events/stream?token="+token, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("events stream status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestAuthDomainEmailAdminFlow(t *testing.T) {
	app, database := newTestApp(t)
	app.Verifier = dns.Verifier{MXTarget: app.Config.MXTarget, Timeout: time.Second, Resolver: stubResolver{target: app.Config.MXTarget}}
	router := app.Router()

	super := createUser(t, database, "admin-flow@test.local", true, true, true)
	superToken := bearerToken(t, app, super)

	testEmail := "pending-user@test.local"
	testPass := "password123"

	registerResp := doJSON(t, router, http.MethodPost, "/api/auth/register", map[string]any{
		"name":     "Pending User",
		"email":    testEmail,
		"password": testPass,
	}, "")
	if registerResp.Code != http.StatusCreated {
		t.Fatalf("register status = %d body=%s", registerResp.Code, registerResp.Body.String())
	}

	loginResp := doJSON(t, router, http.MethodPost, "/api/auth/login", map[string]any{
		"email":    testEmail,
		"password": testPass,
	}, "")
	if loginResp.Code != http.StatusForbidden {
		t.Fatalf("inactive login status = %d body=%s", loginResp.Code, loginResp.Body.String())
	}

	var user db.User
	if err := database.Where("email = ?", testEmail).First(&user).Error; err != nil {
		t.Fatal(err)
	}

	statusResp := doJSON(t, router, http.MethodPatch, "/api/admin/users/"+user.ID.String()+"/status", map[string]any{"is_active": true}, superToken)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("status patch = %d body=%s", statusResp.Code, statusResp.Body.String())
	}

	loginResp = doJSON(t, router, http.MethodPost, "/api/auth/login", map[string]any{
		"email":    testEmail,
		"password": testPass,
	}, "")
	if loginResp.Code != http.StatusOK {
		t.Fatalf("active login status = %d body=%s", loginResp.Code, loginResp.Body.String())
	}
	var loginBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginResp.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}

	domainResp := doJSON(t, router, http.MethodPost, "/api/domains", map[string]any{"name": "flow-example.test"}, loginBody.AccessToken)
	if domainResp.Code != http.StatusCreated {
		t.Fatalf("create domain = %d body=%s", domainResp.Code, domainResp.Body.String())
	}
	var domain db.Domain
	if err := database.Where("name = ?", "flow-example.test").First(&domain).Error; err != nil {
		t.Fatal(err)
	}

	verifyResp := doJSON(t, router, http.MethodPost, "/api/domains/"+domain.ID.String()+"/verify", nil, loginBody.AccessToken)
	if verifyResp.Code != http.StatusOK {
		t.Fatalf("verify domain = %d body=%s", verifyResp.Code, verifyResp.Body.String())
	}

	inboxResp := doJSON(t, router, http.MethodPost, "/api/inboxes", map[string]any{
		"domain_id":  domain.ID,
		"local_part": "hello",
	}, loginBody.AccessToken)
	if inboxResp.Code != http.StatusCreated {
		t.Fatalf("create inbox = %d body=%s", inboxResp.Code, inboxResp.Body.String())
	}
	var inbox db.Inbox
	if err := database.Where("address = ?", "hello@flow-example.test").First(&inbox).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		row := db.Email{
			ID:                uuid.New(),
			InboxID:           inbox.ID,
			MessageID:         "m-" + uuid.NewString(),
			FromAddress:       "sender@test.local",
			ToAddress:         inbox.Address,
			Subject:           "Subject",
			ReceivedAt:        time.Now().Add(time.Duration(i) * time.Minute),
			RawSizeBytes:      100,
			Snippet:           "snippet",
			TextBody:          "hello",
			HTMLBodySanitized: "<p>hello</p>",
			IsRead:            i == 0,
		}
		if err := database.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}

	emailResp := doJSON(t, router, http.MethodGet, "/api/emails?page=1&page_size=2&unread=true", nil, loginBody.AccessToken)
	if emailResp.Code != http.StatusOK {
		t.Fatalf("list emails = %d body=%s", emailResp.Code, emailResp.Body.String())
	}
	var listBody struct {
		Items      []db.Email `json:"items"`
		Pagination struct {
			Page       int  `json:"page"`
			PageSize   int  `json:"page_size"`
			Total      int  `json:"total"`
			TotalPages int  `json:"total_pages"`
			HasNext    bool `json:"has_next"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(emailResp.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody.Items) != 2 || listBody.Pagination.Total != 2 || listBody.Pagination.PageSize != 2 || listBody.Pagination.TotalPages != 1 {
		t.Fatalf("unexpected list body: %+v", listBody)
	}

	quotaResp := doJSON(t, router, http.MethodPatch, "/api/admin/users/"+user.ID.String()+"/quotas", map[string]any{"max_storage_bytes": int64(2048)}, superToken)
	if quotaResp.Code != http.StatusOK {
		t.Fatalf("quota patch = %d body=%s", quotaResp.Code, quotaResp.Body.String())
	}

	normalAdmin := createUser(t, database, "manager@test.local", true, true, false)
	adminToken := bearerToken(t, app, normalAdmin)
	forbiddenResp := doJSON(t, router, http.MethodGet, "/api/admin/users", nil, adminToken)
	if forbiddenResp.Code != http.StatusForbidden {
		t.Fatalf("expected super admin restriction, got %d", forbiddenResp.Code)
	}

	var statusLogs, quotaLogs int64
	if err := database.Model(&db.AuditLog{}).Where("type = ?", "user.status").Count(&statusLogs).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Model(&db.AuditLog{}).Where("type = ?", "user.quota").Count(&quotaLogs).Error; err != nil {
		t.Fatal(err)
	}
	if statusLogs == 0 || quotaLogs == 0 {
		t.Fatalf("expected audit logs, got status=%d quota=%d", statusLogs, quotaLogs)
	}
}

func TestDomainEmailAuthGenerateDKIM(t *testing.T) {
	app, database := newTestApp(t)
	app.Config.SMTPRelayPublicIP = "203.0.113.10"
	router := app.Router()

	user := createUser(t, database, "auth-domain@test.local", true, false, false)
	token := bearerToken(t, app, user)
	domain := db.Domain{
		UserID:             user.ID,
		Name:               "auth-example.test",
		Status:             db.DomainStatusVerified,
		VerificationMethod: "mx",
		MXTarget:           app.Config.MXTarget,
	}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}

	getResp := doJSON(t, router, http.MethodGet, "/api/domains/"+domain.ID.String()+"/email-auth", nil, token)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get email auth status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	if !bytes.Contains(getResp.Body.Bytes(), []byte("v=spf1 ip4:203.0.113.10 mx -all")) {
		t.Fatalf("expected SPF instruction, body=%s", getResp.Body.String())
	}

	genResp := doJSON(t, router, http.MethodPost, "/api/domains/"+domain.ID.String()+"/email-auth/dkim/generate", map[string]string{
		"selector": "gomail1",
	}, token)
	if genResp.Code != http.StatusOK {
		t.Fatalf("generate dkim status = %d body=%s", genResp.Code, genResp.Body.String())
	}
	if bytes.Contains(genResp.Body.Bytes(), []byte("PRIVATE KEY")) {
		t.Fatal("private DKIM key leaked in response")
	}
	if !bytes.Contains(genResp.Body.Bytes(), []byte("gomail1._domainkey.auth-example.test")) {
		t.Fatalf("expected DKIM record name, body=%s", genResp.Body.String())
	}
	if !bytes.Contains(genResp.Body.Bytes(), []byte("v=DKIM1; k=rsa; p=")) {
		t.Fatalf("expected DKIM record value, body=%s", genResp.Body.String())
	}
	var authRow db.DomainEmailAuth
	if err := database.First(&authRow, "domain_id = ?", domain.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(authRow.DKIMPrivateKeyPEM, "enc:v1:") {
		t.Fatalf("expected encrypted private key, got %q", authRow.DKIMPrivateKeyPEM[:min(len(authRow.DKIMPrivateKeyPEM), 16)])
	}
	if strings.Contains(authRow.DKIMPrivateKeyPEM, "PRIVATE KEY") {
		t.Fatal("stored DKIM key contains plaintext private key")
	}
}

type stubResolver struct {
	target string
}

func (s stubResolver) LookupMX(_ context.Context, _ string) ([]*net.MX, error) {
	return []*net.MX{{Host: s.target + ".", Pref: 10}}, nil
}

func (s stubResolver) LookupTXT(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
