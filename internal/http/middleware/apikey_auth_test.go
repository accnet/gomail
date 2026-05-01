package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gomail/internal/db"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testMiddlewareDB(t *testing.T) *gorm.DB {
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

func createApiKeyForTest(t *testing.T, database *gorm.DB, userID uuid.UUID, fullKey string, active bool, expiresAt *time.Time, scope string) db.ApiKey {
	t.Helper()
	hash := sha256.Sum256([]byte(fullKey))
	ak := db.ApiKey{
		UserID:    userID,
		Name:      "test-key",
		KeyPrefix: "",
		KeyHash:   hex.EncodeToString(hash[:]),
		Scopes:    scope,
		IsActive:  active,
		ExpiresAt: expiresAt,
	}
	if err := database.Create(&ak).Error; err != nil {
		t.Fatal(err)
	}
	return ak
}

func setupTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	return r
}

func TestApiKeyAuthMissingHeader(t *testing.T) {
	database := testMiddlewareDB(t)
	r := setupTestRouter()
	r.GET("/test", ApiKeyAuth(database), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestApiKeyAuthInvalidKey(t *testing.T) {
	database := testMiddlewareDB(t)
	r := setupTestRouter()
	r.GET("/test", ApiKeyAuth(database), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Api-Key", "invalid-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestApiKeyAuthValidKey(t *testing.T) {
	database := testMiddlewareDB(t)

	user := db.User{
		Email:        "user@test.local",
		Name:         "User",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "test-api-key-1234567890abcdef"
	ak := createApiKeyForTest(t, database, user.ID, fullKey, true, nil, "send_email")

	r := setupTestRouter()
	r.GET("/test", ApiKeyAuth(database), func(c *gin.Context) {
		k := GetApiKey(c)
		if k == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "no key"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"key_id": k.ID.String()})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Api-Key", fullKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["key_id"] != ak.ID.String() {
		t.Fatalf("expected key_id=%s, got %s", ak.ID.String(), body["key_id"])
	}
}

func TestApiKeyAuthInactiveKey(t *testing.T) {
	database := testMiddlewareDB(t)

	user := db.User{
		Email:        "inactive@test.local",
		Name:         "User",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "test-inactive-key-12345678"
	ak := createApiKeyForTest(t, database, user.ID, fullKey, true, nil, "send_email")

	// Set inactive via direct DB update (GORM zero-value issue with bool)
	if err := database.Model(&ak).Update("is_active", false).Error; err != nil {
		t.Fatal(err)
	}

	r := setupTestRouter()
	r.GET("/test", ApiKeyAuth(database), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Api-Key", fullKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for inactive key, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestApiKeyAuthExpiredKey(t *testing.T) {
	database := testMiddlewareDB(t)

	user := db.User{
		Email:        "expired@test.local",
		Name:         "User",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	past := time.Now().Add(-1 * time.Hour)
	fullKey := "test-expired-key-12345678"
	createApiKeyForTest(t, database, user.ID, fullKey, true, &past, "send_email")

	r := setupTestRouter()
	r.GET("/test", ApiKeyAuth(database), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Api-Key", fullKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for expired key, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRequireScope(t *testing.T) {
	database := testMiddlewareDB(t)

	user := db.User{
		Email:        "scope@test.local",
		Name:         "User",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "test-scope-key-12345678"
	createApiKeyForTest(t, database, user.ID, fullKey, true, nil, "read_only")

	r := setupTestRouter()
	r.GET("/test", ApiKeyAuth(database), RequireScope("send_email"), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Api-Key", fullKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for insufficient scope, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetApiKey(t *testing.T) {
	database := testMiddlewareDB(t)

	user := db.User{
		Email:        "get@test.local",
		Name:         "User",
		PasswordHash: "$2a$10$placeholder",
		IsActive:     true,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	fullKey := "test-get-key-abcdefghij"
	ak := createApiKeyForTest(t, database, user.ID, fullKey, true, nil, "send_email")

	r := setupTestRouter()
	r.GET("/test", ApiKeyAuth(database), func(c *gin.Context) {
		k := GetApiKey(c)
		if k == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "no key"})
			return
		}
		if k.ID != ak.ID {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "wrong key"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"name": k.Name, "scopes": k.Scopes})
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Api-Key", fullKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "test-key" {
		t.Fatalf("expected name 'test-key', got %s", body["name"])
	}
}