package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"gomail/internal/db"
	mw "gomail/internal/http/middleware"
	"gomail/internal/teams"
	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type smtpSettings struct {
	Host           string `json:"host"`
	Port587        string `json:"port_587"`
	Port465        string `json:"port_465"`
	Security       string `json:"recommended_security"`
	UsernameFormat string `json:"username_format"`
	PasswordFormat string `json:"password_format"`
}

func smtpSettingsFromConfig(authHostname, port587, port465 string) smtpSettings {
	return smtpSettings{
		Host:           authHostname,
		Port587:        port587,
		Port465:        port465,
		Security:       "STARTTLS on 587, implicit TLS on 465",
		UsernameFormat: "api_key",
		PasswordFormat: "secret_key",
	}
}

type createApiKeyBody struct {
	Name     string  `json:"name" binding:"required,min=1,max=255"`
	Scope    string  `json:"scope" binding:"required,oneof=send_email full_access"`
	ExpireAt *string `json:"expires_at,omitempty"`
}

type apiKeyResponse struct {
	db.ApiKey
	SmtpSettings smtpSettings `json:"smtp_settings"`
}

type apiKeyCreateResponse struct {
	db.ApiKey
	APIKey       string       `json:"api_key"`
	SecretKey    string       `json:"secret_key"`
	FullApiKey   string       `json:"full_api_key"`
	SmtpSettings smtpSettings `json:"smtp_settings"`
}

func generateApiKeyValue() (string, string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", err
	}
	full := hex.EncodeToString(buf)
	hash := sha256.Sum256([]byte(full))
	return full, full[:8], hex.EncodeToString(hash[:]), nil
}

func RegisterApiKeyRoutes(r gin.IRouter, database *gorm.DB, authMiddleware gin.HandlerFunc, smtpAuthHostname, smtpAuthPort, smtpAuthTLSPort string) {
	api := r.Group("/api-keys")
	api.Use(authMiddleware)
	settings := smtpSettingsFromConfig(smtpAuthHostname, smtpAuthPort, smtpAuthTLSPort)
	api.POST("", createApiKey(database, settings))
	api.GET("", listApiKeys(database, settings))
	api.GET("/settings", getApiKeySettings(settings))
	api.GET("/:id", getApiKey(database, settings))
	api.PATCH("/:id", patchApiKey(database, settings))
	api.POST("/:id/revoke", revokeApiKey(database, settings))
	api.DELETE("/:id", deleteApiKey(database, settings))
	api.GET("/:id/usage", usageApiKey(database))
}

func createApiKey(database *gorm.DB, s smtpSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, _, _ := mw.UserAndApiKey(c)
		ctx := teams.FromGin(c)
		var body createApiKeyBody
		if err := c.ShouldBindJSON(&body); err != nil {
			response.BadRequest(c, err.Error())
			return
		}

		full, prefix, hash, err := generateApiKeyValue()
		if err != nil {
			response.ServerError(c, "failed to generate api key")
			return
		}

		ak := db.ApiKey{
			UserID:    uid,
			TeamID:    ShouldSetTeamID(ctx),
			Name:      body.Name,
			KeyPrefix: prefix,
			KeyHash:   hash,
			Scopes:    body.Scope,
		}
		if body.ExpireAt != nil && *body.ExpireAt != "" {
			t, err := time.Parse(time.RFC3339, *body.ExpireAt)
			if err != nil {
				response.BadRequest(c, "invalid expires_at format, use RFC3339")
				return
			}
			ak.ExpiresAt = &t
		}

		if err := database.Create(&ak).Error; err != nil {
			response.ServerError(c, "failed to create api key")
			return
		}

		resp := apiKeyCreateResponse{
			ApiKey:       ak,
			APIKey:       ak.ID.String(),
			SecretKey:    full,
			FullApiKey:   full,
			SmtpSettings: s,
		}
		response.Created(c, resp)
	}
}

func listApiKeys(database *gorm.DB, s smtpSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := teams.FromGin(c)
		var keys []db.ApiKey
		q := ScopeApiKeys(database.Model(&db.ApiKey{}), ctx)
		if err := q.Order("created_at DESC").Find(&keys).Error; err != nil {
			response.ServerError(c, "failed to list api keys")
			return
		}
		resp := make([]apiKeyResponse, 0, len(keys))
		for _, k := range keys {
			resp = append(resp, apiKeyResponse{ApiKey: k, SmtpSettings: s})
		}
		response.OK(c, resp)
	}
}

func getApiKeySettings(s smtpSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		response.OK(c, s)
	}
}

func getApiKey(database *gorm.DB, s smtpSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := teams.FromGin(c)
		id := c.Param("id")
		var ak db.ApiKey
		q := ScopeApiKeys(database.Model(&db.ApiKey{}), ctx)
		if err := q.Where("id = ?", id).First(&ak).Error; err != nil {
			response.NotFound(c, "api key not found")
			return
		}
		response.OK(c, apiKeyResponse{ApiKey: ak, SmtpSettings: s})
	}
}

func patchApiKey(database *gorm.DB, s smtpSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := teams.FromGin(c)
		id := c.Param("id")
		var ak db.ApiKey
		q := ScopeApiKeys(database.Model(&db.ApiKey{}), ctx)
		if err := q.Where("id = ?", id).First(&ak).Error; err != nil {
			response.NotFound(c, "api key not found")
			return
		}
		var body map[string]any
		if err := c.ShouldBindJSON(&body); err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		if n, ok := body["name"]; ok {
			name, valid := n.(string)
			if !valid || len(name) < 1 || len(name) > 255 {
				response.BadRequest(c, "invalid name")
				return
			}
			ak.Name = name
		}
		if s, ok := body["scope"]; ok {
			scope, valid := s.(string)
			if !valid || (scope != "send_email" && scope != "full_access") {
				response.BadRequest(c, "invalid scope")
				return
			}
			ak.Scopes = scope
		}
		if a, ok := body["is_active"]; ok {
			active, valid := a.(bool)
			if valid {
				ak.IsActive = active
			}
		}
		if e, ok := body["expires_at"]; ok {
			if e == nil {
				ak.ExpiresAt = nil
			} else if expStr, valid := e.(string); valid {
				t, err := time.Parse(time.RFC3339, expStr)
				if err != nil {
					response.BadRequest(c, "invalid expires_at format")
					return
				}
				ak.ExpiresAt = &t
			}
		}
		if err := database.Save(&ak).Error; err != nil {
			response.ServerError(c, "failed to update api key")
			return
		}
		response.OK(c, apiKeyResponse{ApiKey: ak, SmtpSettings: s})
	}
}

func revokeApiKey(database *gorm.DB, s smtpSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := teams.FromGin(c)
		id := c.Param("id")
		var ak db.ApiKey
		q := ScopeApiKeys(database.Model(&db.ApiKey{}), ctx)
		if err := q.Where("id = ?", id).First(&ak).Error; err != nil {
			response.NotFound(c, "api key not found")
			return
		}
		ak.IsActive = false
		if err := database.Save(&ak).Error; err != nil {
			response.ServerError(c, "failed to revoke api key")
			return
		}
		response.OK(c, apiKeyResponse{ApiKey: ak, SmtpSettings: s})
	}
}

func deleteApiKey(database *gorm.DB, s smtpSettings) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := teams.FromGin(c)
		id := c.Param("id")
		q := ScopeApiKeys(database.Model(&db.ApiKey{}), ctx)
		result := q.Where("id = ?", id).Delete(&db.ApiKey{})
		if result.Error != nil {
			response.ServerError(c, "failed to delete api key")
			return
		}
		if result.RowsAffected == 0 {
			response.NotFound(c, "api key not found")
			return
		}
		response.OK(c, gin.H{"deleted": true})
	}
}

func usageApiKey(database *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := teams.FromGin(c)
		id := c.Param("id")

		// Verify ownership
		var ak db.ApiKey
		q := ScopeApiKeys(database.Model(&db.ApiKey{}), ctx)
		if err := q.Where("id = ?", id).First(&ak).Error; err != nil {
			response.NotFound(c, "api key not found")
			return
		}

		var logs []db.ApiKeyUsageLog
		if err := database.Where("api_key_id = ?", id).Order("created_at DESC").Limit(100).Find(&logs).Error; err != nil {
			response.ServerError(c, "failed to fetch usage logs")
			return
		}

		var count int64
		database.Model(&db.ApiKeyUsageLog{}).Where("api_key_id = ?", id).Count(&count)

		response.OK(c, gin.H{
			"api_key_id": id,
			"total":      count,
			"logs":       logs,
		})
	}
}

type testSendBody struct {
	To      string `json:"to" binding:"required,email"`
	Subject string `json:"subject" binding:"required"`
	Body    string `json:"body" binding:"required"`
}

func RegisterTestSendRoute(r gin.IRouter, database *gorm.DB, apiKeyAuth gin.HandlerFunc, sender func(to, from, subject, body string) error) {
	r.POST("/send-email", apiKeyAuth, func(c *gin.Context) {
		ak := mw.GetApiKey(c)
		if ak == nil {
			response.Unauthorized(c, "no api key")
			return
		}
		if ak.Scopes != "send_email" && ak.Scopes != "full_access" {
			response.Forbidden(c, "api key scope insufficient")
			return
		}

		var body testSendBody
		if err := c.ShouldBindJSON(&body); err != nil {
			response.BadRequest(c, err.Error())
			return
		}

		if sender == nil {
			response.ServerError(c, "email sender is not configured")
			return
		}
		if err := sender(body.To, "noreply@your-domain.com", body.Subject, body.Body); err != nil {
			response.ServerError(c, "failed to send email: "+err.Error())
			return
		}

		response.OK(c, gin.H{"sent": true})
	})
}
