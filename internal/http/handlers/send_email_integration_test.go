package handlers

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gomail/internal/db"
	mw "gomail/internal/http/middleware"

	"github.com/gin-gonic/gin"
)

func stubSender(expectedTo, expectedFrom, expectedSubject, expectedBody string) func(to, from, subject, body string) error {
	return func(to, from, subject, body string) error {
		if to != expectedTo {
			return fmt.Errorf("unexpected to: %s", to)
		}
		if from != expectedFrom {
			return fmt.Errorf("unexpected from: %s", from)
		}
		if subject != expectedSubject {
			return fmt.Errorf("unexpected subject: %s", subject)
		}
		if body != expectedBody {
			return fmt.Errorf("unexpected body: %s", body)
		}
		return nil
	}
}

func failingStubSender() func(to, from, subject, body string) error {
	return func(to, from, subject, body string) error {
		return fmt.Errorf("sender offline")
	}
}

func TestSendEmailFailsWhenSenderIsNotConfigured(t *testing.T) {
	setGinMode()
	database := testDB(t)

	user := db.User{
		Email:        "sender-missing@test.local",
		Name:         "Sender Missing",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "test-send-key-missing-sender"
	hash := sha256.Sum256([]byte(fullKey))
	ak := db.ApiKey{
		UserID:   user.ID,
		Name:     "Send Key",
		KeyHash:  hex.EncodeToString(hash[:]),
		Scopes:   "send_email",
		IsActive: true,
	}
	if err := database.Create(&ak).Error; err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	apiGroup := r.Group("/api")
	apiGroup.Use(mw.ApiKeyAuth(database))
	RegisterTestSendRoute(apiGroup, database, mw.ApiKeyAuth(database), nil)

	body := map[string]string{
		"to":      "test@example.com",
		"subject": "Test Subject",
		"body":    "Hello World",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/send-email", bytesFromString(string(payload)))
	req.Header.Set("X-Api-Key", fullKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSendEmailWithValidApiKey(t *testing.T) {
	setGinMode()
	database := testDB(t)

	// Create user and API key directly
	user := db.User{
		Email:        "sender@test.local",
		Name:         "Sender",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "test-send-key-1234567890abcdef"
	hash := sha256.Sum256([]byte(fullKey))
	ak := db.ApiKey{
		UserID:   user.ID,
		Name:     "Send Key",
		KeyHash:  hex.EncodeToString(hash[:]),
		Scopes:   "send_email",
		IsActive: true,
	}
	if err := database.Create(&ak).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiGroup.Use(mw.ApiKeyAuth(database))
	sender := stubSender("test@example.com", "noreply@your-domain.com", "Test Subject", "Hello World")
	RegisterTestSendRoute(apiGroup, database, mw.ApiKeyAuth(database), sender)

	body := map[string]string{
		"to":      "test@example.com",
		"subject": "Test Subject",
		"body":    "Hello World",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/send-email", bytesFromString(string(payload)))
	req.Header.Set("X-Api-Key", fullKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var resp map[string]bool
	json.Unmarshal(w.Body.Bytes(), &resp)
	if !resp["sent"] {
		t.Fatal("expected sent=true")
	}
}

func TestSendEmailFailsWithoutAuth(t *testing.T) {
	setGinMode()
	database := testDB(t)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiGroup.Use(mw.ApiKeyAuth(database))
	sender := stubSender("test@example.com", "noreply@your-domain.com", "Test", "Body")
	RegisterTestSendRoute(apiGroup, database, mw.ApiKeyAuth(database), sender)

	body := map[string]string{
		"to":      "test@example.com",
		"subject": "Test",
		"body":    "Body",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/send-email", bytesFromString(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	// No X-Api-Key header
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without auth, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSendEmailFailsWithWrongScope(t *testing.T) {
	setGinMode()
	database := testDB(t)

	user := db.User{
		Email:        "wrongscope@test.local",
		Name:         "Wrong",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "readonly-key-abcdefghijklmnop"
	hash := sha256.Sum256([]byte(fullKey))
	ak := db.ApiKey{
		UserID:   user.ID,
		Name:     "Read Only",
		KeyHash:  hex.EncodeToString(hash[:]),
		Scopes:   "read_only",
		IsActive: true,
	}
	if err := database.Create(&ak).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiGroup.Use(mw.ApiKeyAuth(database))
	sender := stubSender("test@example.com", "noreply@your-domain.com", "Test", "Body")
	RegisterTestSendRoute(apiGroup, database, mw.ApiKeyAuth(database), sender)

	body := map[string]string{
		"to":      "test@example.com",
		"subject": "Test",
		"body":    "Body",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/send-email", bytesFromString(string(payload)))
	req.Header.Set("X-Api-Key", fullKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for read_only scope, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSendEmailSendsToStub(t *testing.T) {
	setGinMode()
	database := testDB(t)

	user := db.User{
		Email:        "sends-test@test.local",
		Name:         "SendTester",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "send-test-key-yyyy"
	hash := sha256.Sum256([]byte(fullKey))
	ak := db.ApiKey{
		UserID:   user.ID,
		Name:     "Send",
		KeyHash:  hex.EncodeToString(hash[:]),
		Scopes:   "send_email",
		IsActive: true,
	}
	if err := database.Create(&ak).Error; err != nil {
		t.Fatal(err)
	}

	// Verify sender returns error and handler propagates it
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiGroup.Use(mw.ApiKeyAuth(database))
	sender := failingStubSender()
	RegisterTestSendRoute(apiGroup, database, mw.ApiKeyAuth(database), sender)

	body := map[string]string{
		"to":      "test@example.com",
		"subject": "Test",
		"body":    "Body",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/send-email", bytesFromString(string(payload)))
	req.Header.Set("X-Api-Key", fullKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for sender failure, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSendEmailWithExpiredKey(t *testing.T) {
	setGinMode()
	database := testDB(t)

	user := db.User{
		Email:        "expired-send@test.local",
		Name:         "Expired",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "expired-send-key-zzzz"
	hash := sha256.Sum256([]byte(fullKey))
	past := time.Now().Add(-1 * time.Hour)
	ak := db.ApiKey{
		UserID:    user.ID,
		Name:      "Expired",
		KeyHash:   hex.EncodeToString(hash[:]),
		Scopes:    "send_email",
		IsActive:  true,
		ExpiresAt: &past,
	}
	if err := database.Create(&ak).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiGroup.Use(mw.ApiKeyAuth(database))
	sender := stubSender("test@example.com", "noreply@your-domain.com", "Test", "Body")
	RegisterTestSendRoute(apiGroup, database, mw.ApiKeyAuth(database), sender)

	body := map[string]string{
		"to":      "test@example.com",
		"subject": "Test",
		"body":    "Body",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/send-email", bytesFromString(string(payload)))
	req.Header.Set("X-Api-Key", fullKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for expired key, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestSendEmailValidationErrors(t *testing.T) {
	setGinMode()
	database := testDB(t)

	user := db.User{
		Email:        "validation@test.local",
		Name:         "Validator",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "validation-key-abcdef"
	hash := sha256.Sum256([]byte(fullKey))
	ak := db.ApiKey{
		UserID:   user.ID,
		Name:     "Validation Key",
		KeyHash:  hex.EncodeToString(hash[:]),
		Scopes:   "send_email",
		IsActive: true,
	}
	if err := database.Create(&ak).Error; err != nil {
		t.Fatal(err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	apiGroup.Use(mw.ApiKeyAuth(database))
	sender := stubSender("test@example.com", "noreply@your-domain.com", "Test", "Body")
	RegisterTestSendRoute(apiGroup, database, mw.ApiKeyAuth(database), sender)

	// Missing 'to' field
	body := map[string]string{
		"subject": "Test",
		"body":    "Body",
	}
	payload, _ := json.Marshal(body)

	req := httptest.NewRequest(http.MethodPost, "/api/send-email", bytesFromString(string(payload)))
	req.Header.Set("X-Api-Key", fullKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing 'to' field, got %d body=%s", w.Code, w.Body.String())
	}

	// Invalid email format
	body = map[string]string{
		"to":      "not-an-email",
		"subject": "Test",
		"body":    "Body",
	}
	payload, _ = json.Marshal(body)

	req = httptest.NewRequest(http.MethodPost, "/api/send-email", bytesFromString(string(payload)))
	req.Header.Set("X-Api-Key", fullKey)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid email, got %d body=%s", w.Code, w.Body.String())
	}
}

func bytesFromString(s string) *bytes.Reader {
	return bytes.NewReader([]byte(s))
}
