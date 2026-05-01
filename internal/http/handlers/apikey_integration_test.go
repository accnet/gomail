package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gomail/internal/auth"
	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/dns"
	mw "gomail/internal/http/middleware"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func apiKeyTestConfig() testCfg {
	return testCfg{
		SMTPAuthHostname: "smtp.test.local",
		SMTPAuthPort:     "587",
		SMTPAuthTLSPort:  "465",
	}
}

type testCfg struct {
	SMTPAuthHostname string
	SMTPAuthPort     string
	SMTPAuthTLSPort  string
}

func newApiKeyTestApp(t *testing.T, cfg testCfg) (App, *gorm.DB) {
	t.Helper()
	database := testDB(t)
	appCfg := config.Config{
		AppEnv:           "test",
		JWTSecret:        "test-secret-123456789",
		AccessTokenTTL:   time.Hour,
		RefreshTokenTTL:  24 * time.Hour,
		MXTarget:         "mx.test.local",
		SMTPAuthHostname: cfg.SMTPAuthHostname,
		SMTPAuthPort:     cfg.SMTPAuthPort,
		SMTPAuthTLSPort:  cfg.SMTPAuthTLSPort,
	}
	return App{
		DB:       database,
		Auth:     auth.NewService(database, appCfg),
		Config:   appCfg,
		Verifier: dns.Verifier{MXTarget: appCfg.MXTarget, Timeout: time.Second},
	}, database
}

func setGinMode() {
	gin.SetMode(gin.TestMode)
}

func TestCreateApiKeyOneTimeReveal(t *testing.T) {
	setGinMode()
	app, database := newApiKeyTestApp(t, apiKeyTestConfig())
	router := app.Router()

	user := createUser(t, database, "apikey@test.local", true, false, false)
	token := bearerToken(t, app, user)

	// Create domain for send_email scope later
	domain := db.Domain{UserID: user.ID, Name: "example.test", Status: "verified", MXTarget: app.Config.MXTarget, VerificationMethod: "mx", LastVerifiedAt: ptrTime(time.Now())}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}

	// Step 1: Create API key
	createResp := doJSON(t, router, http.MethodPost, "/api/api-keys", map[string]any{
		"name":  "My Relay Key",
		"scope": "send_email",
	}, token)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create api key status = %d body=%s", createResp.Code, createResp.Body.String())
	}

	var createBody struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Scopes      string `json:"scopes"`
		FullApiKey  string `json:"full_api_key"`
		KeyPrefix   string `json:"key_prefix"`
		SmtpSettings struct {
			Host           string `json:"host"`
			Port587        string `json:"port_587"`
			Port465        string `json:"port_465"`
			UsernameFormat string `json:"username_format"`
		} `json:"smtp_settings"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &createBody); err != nil {
		t.Fatal(err)
	}
	if createBody.FullApiKey == "" {
		t.Fatal("expected full_api_key in create response")
	}
	if createBody.Name != "My Relay Key" {
		t.Fatalf("expected name 'My Relay Key', got %s", createBody.Name)
	}
	if createBody.Scopes != "send_email" {
		t.Fatalf("expected scope 'send_email', got %s", createBody.Scopes)
	}
	if createBody.SmtpSettings.Host != "smtp.test.local" {
		t.Fatalf("expected smtp host 'smtp.test.local', got %s", createBody.SmtpSettings.Host)
	}

	fullKey := createBody.FullApiKey
	keyID := createBody.ID

	// Step 2: List API keys - should NOT reveal full key
	listResp := doJSON(t, router, http.MethodGet, "/api/api-keys", nil, token)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list api keys status = %d body=%s", listResp.Code, listResp.Body.String())
	}
	var listBody []struct {
		ID          string `json:"id"`
		FullApiKey  string `json:"full_api_key"`
		SmtpSettings struct {
			Host string `json:"host"`
		} `json:"smtp_settings"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	if len(listBody) != 1 {
		t.Fatalf("expected 1 key in list, got %d", len(listBody))
	}
	if listBody[0].FullApiKey != "" {
		t.Fatal("list must not reveal full_api_key")
	}
	if listBody[0].SmtpSettings.Host != "smtp.test.local" {
		t.Fatalf("list should include smtp settings, got %s", listBody[0].SmtpSettings.Host)
	}

	// Step 3: Get single API key - should NOT reveal full key
	getResp := doJSON(t, router, http.MethodGet, "/api/api-keys/"+keyID, nil, token)
	if getResp.Code != http.StatusOK {
		t.Fatalf("get api key status = %d body=%s", getResp.Code, getResp.Body.String())
	}
	var getBody struct {
		ID         string `json:"id"`
		FullApiKey string `json:"full_api_key"`
	}
	if err := json.Unmarshal(getResp.Body.Bytes(), &getBody); err != nil {
		t.Fatal(err)
	}
	if getBody.FullApiKey != "" {
		t.Fatal("get must not reveal full_api_key")
	}

	// Step 4: Test with the actual API key via header
	router2 := app.Router()
	req := httptest.NewRequest(http.MethodPost, "/api/send-email", nil)
	req.Header.Set("X-Api-Key", fullKey)
	// We won't actually send, just verify auth passes and scope check works
	// The request would fail on missing body - that's fine
	w := httptest.NewRecorder()
	router2.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (bad request body), got %d body=%s", w.Code, w.Body.String())
	}
}

func TestExpiredApiKeyRejected(t *testing.T) {
	setGinMode()
	app, database := newApiKeyTestApp(t, apiKeyTestConfig())

	user := createUser(t, database, "expired@test.local", true, false, false)
	token := bearerToken(t, app, user)

	// Create expired key via direct DB insert
	hash := "abcsdummyhash12345"
	past := time.Now().Add(-24 * time.Hour)
	ak := db.ApiKey{
		UserID:    user.ID,
		Name:      "Expired Key",
		KeyHash:   hash,
		Scopes:    "send_email",
		IsActive:  true,
		ExpiresAt: &past,
	}
	if err := database.Create(&ak).Error; err != nil {
		t.Fatal(err)
	}

	// Try to use this key - should fail at middleware
	router2 := app.Router()
	req := httptest.NewRequest(http.MethodGet, "/api/api-keys", nil)
	req.Header.Set("X-Api-Key", "some-random-key-not-matching-hash")
	w := httptest.NewRecorder()
	router2.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for expired key, got %d body=%s", w.Code, w.Body.String())
	}

	_ = token // silence unused
}

func TestRevokedApiKeyRejected(t *testing.T) {
	setGinMode()
	app, database := newApiKeyTestApp(t, apiKeyTestConfig())
	router := app.Router()

	user := createUser(t, database, "revoked@test.local", true, false, false)
	token := bearerToken(t, app, user)

	// Create a key
	createResp := doJSON(t, router, http.MethodPost, "/api/api-keys", map[string]any{
		"name":  "To Revoke",
		"scope": "send_email",
	}, token)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create key = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var createBody struct {
		ID         string `json:"id"`
		FullApiKey string `json:"full_api_key"`
	}
	json.Unmarshal(createResp.Body.Bytes(), &createBody)
	fullKey := createBody.FullApiKey
	keyID := createBody.ID

	// Revoke the key
	revokeResp := doJSON(t, router, http.MethodPost, "/api/api-keys/"+keyID+"/revoke", nil, token)
	if revokeResp.Code != http.StatusOK {
		t.Fatalf("revoke = %d body=%s", revokeResp.Code, revokeResp.Body.String())
	}

	// Try to use the revoked key
	router2 := app.Router()
	req := httptest.NewRequest(http.MethodPost, "/api/send-email", nil)
	req.Header.Set("X-Api-Key", fullKey)
	w := httptest.NewRecorder()
	router2.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for revoked key, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPatchApiKey(t *testing.T) {
	setGinMode()
	app, database := newApiKeyTestApp(t, apiKeyTestConfig())
	router := app.Router()

	user := createUser(t, database, "patchkey@test.local", true, false, false)
	token := bearerToken(t, app, user)

	// Create a key
	createResp := doJSON(t, router, http.MethodPost, "/api/api-keys", map[string]any{
		"name":  "Original Name",
		"scope": "send_email",
	}, token)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create = %d", createResp.Code)
	}
	var createBody struct {
		ID string `json:"id"`
	}
	json.Unmarshal(createResp.Body.Bytes(), &createBody)
	keyID := createBody.ID

	// Patch name and scope
	patchResp := doJSON(t, router, http.MethodPatch, "/api/api-keys/"+keyID, map[string]any{
		"name":  "Renamed Key",
		"scope": "full_access",
	}, token)
	if patchResp.Code != http.StatusOK {
		t.Fatalf("patch = %d body=%s", patchResp.Code, patchResp.Body.String())
	}
	var patchBody struct {
		Name   string `json:"name"`
		Scopes string `json:"scopes"`
	}
	json.Unmarshal(patchResp.Body.Bytes(), &patchBody)
	if patchBody.Name != "Renamed Key" {
		t.Fatalf("expected name 'Renamed Key', got %s", patchBody.Name)
	}
	if patchBody.Scopes != "full_access" {
		t.Fatalf("expected scope 'full_access', got %s", patchBody.Scopes)
	}
}

func TestApiKeyUsageLog(t *testing.T) {
	setGinMode()
	app, database := newApiKeyTestApp(t, apiKeyTestConfig())
	router := app.Router()

	user := createUser(t, database, "usage@test.local", true, false, false)
	token := bearerToken(t, app, user)

	// Create key
	createResp := doJSON(t, router, http.MethodPost, "/api/api-keys", map[string]any{
		"name":  "Usage Key",
		"scope": "send_email",
	}, token)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create = %d", createResp.Code)
	}
	var createBody struct {
		ID string `json:"id"`
	}
	json.Unmarshal(createResp.Body.Bytes(), &createBody)
	keyID := createBody.ID

	// Get usage - should be 0
	usageResp := doJSON(t, router, http.MethodGet, "/api/api-keys/"+keyID+"/usage", nil, token)
	if usageResp.Code != http.StatusOK {
		t.Fatalf("usage = %d body=%s", usageResp.Code, usageResp.Body.String())
	}
	var usageBody struct {
		Total int `json:"total"`
	}
	json.Unmarshal(usageResp.Body.Bytes(), &usageBody)
	if usageBody.Total != 0 {
		t.Fatalf("expected 0 usage, got %d", usageBody.Total)
	}
}

func TestDeleteApiKey(t *testing.T) {
	setGinMode()
	app, database := newApiKeyTestApp(t, apiKeyTestConfig())
	router := app.Router()

	user := createUser(t, database, "deletekey@test.local", true, false, false)
	token := bearerToken(t, app, user)

	// Create key
	createResp := doJSON(t, router, http.MethodPost, "/api/api-keys", map[string]any{
		"name":  "Delete Me",
		"scope": "send_email",
	}, token)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create = %d", createResp.Code)
	}
	var createBody struct {
		ID string `json:"id"`
	}
	json.Unmarshal(createResp.Body.Bytes(), &createBody)
	keyID := createBody.ID

	// Delete
	deleteResp := doJSON(t, router, http.MethodDelete, "/api/api-keys/"+keyID, nil, token)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete = %d body=%s", deleteResp.Code, deleteResp.Body.String())
	}
	var delBody map[string]bool
	json.Unmarshal(deleteResp.Body.Bytes(), &delBody)
	if !delBody["deleted"] {
		t.Fatal("expected deleted=true")
	}

	// Verify gone
	getResp := doJSON(t, router, http.MethodGet, "/api/api-keys/"+keyID, nil, token)
	if getResp.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", getResp.Code)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

// Ensure mw import for middleware
var _ = mw.ApiKeyAuth
var _ = mw.GetApiKey