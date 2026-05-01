package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"gomail/internal/db"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const contextKeyApiKey = "gomail_api_key"

func ApiKeyAuth(database *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-Api-Key")
		if key == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing X-Api-Key header"})
			c.Abort()
			return
		}

		hash := sha256.Sum256([]byte(key))
		keyHash := hex.EncodeToString(hash[:])

		var apiKey db.ApiKey
		if err := database.Where("key_hash = ?", keyHash).First(&apiKey).Error; err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
			c.Abort()
			return
		}

		if !apiKey.IsActive {
			c.JSON(http.StatusForbidden, gin.H{"error": "api key is inactive"})
			c.Abort()
			return
		}

		if apiKey.ExpiresAt != nil && time.Now().After(*apiKey.ExpiresAt) {
			c.JSON(http.StatusForbidden, gin.H{"error": "api key expired"})
			c.Abort()
			return
		}

		c.Set(contextKeyApiKey, &apiKey)
		c.Next()
	}
}

func GetApiKey(c *gin.Context) *db.ApiKey {
	if k, ok := c.Get(contextKeyApiKey); ok {
		return k.(*db.ApiKey)
	}
	return nil
}

func RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		k := GetApiKey(c)
		if k == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "no api key"})
			c.Abort()
			return
		}
		if k.Scopes != scope && k.Scopes != "all" {
			c.JSON(http.StatusForbidden, gin.H{"error": "api key scope insufficient"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func UserAndApiKey(c *gin.Context) (uuid.UUID, *db.ApiKey, bool) {
	ak := GetApiKey(c)
	if ak != nil {
		return ak.UserID, ak, true
	}
	user := CurrentUser(c)
	uid, exists := user.ID, true
	if exists {
		return uid, nil, false
	}
	return uuid.Nil, nil, false
}