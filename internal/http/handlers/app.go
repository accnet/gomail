package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gomail/internal/auth"
	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/dns"
	mw "gomail/internal/http/middleware"
	"gomail/internal/realtime"
	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type App struct {
	DB             *gorm.DB
	Auth           *auth.Service
	Config         config.Config
	Redis          *redis.Client
	Verifier       dns.Verifier
	StaticProjects *StaticProjectsHandler
	SendEmail      func(to, from, subject, body string) error
	RateLimiter    *mw.RateLimiter
}

func (a App) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), requestID())
	r.GET("/healthz", func(c *gin.Context) { response.OK(c, gin.H{"ok": true}) })
	r.GET("/", func(c *gin.Context) { c.Redirect(http.StatusFound, "/app/") })
	r.Static("/app", "./web")
	if a.StaticProjects != nil {
		r.GET("/static-thumbnails/:id/thumbnail.png", a.StaticProjects.Thumbnail)
	}

	api := r.Group("/api")

	// Apply rate limiter to public auth endpoints if configured
	authPublic := api.Group("")
	if a.RateLimiter != nil {
		authPublic.Use(a.RateLimiter.RateLimit())
	}
	authPublic.POST("/auth/register", a.register)
	authPublic.POST("/auth/login", a.login)
	api.POST("/auth/refresh", a.refresh)

	protected := api.Group("")
	protected.Use(mw.Auth(a.Auth, a.DB))
	protected.GET("/me", a.me)
	protected.POST("/auth/logout", a.logout)
	protected.POST("/auth/change-password", a.changePassword)
	protected.GET("/domains", a.listDomains)
	protected.POST("/domains", a.createDomain)
	protected.GET("/domains/:id", a.getDomain)
	protected.POST("/domains/:id/verify", a.verifyDomain)
	protected.GET("/domains/:id/email-auth", a.getDomainEmailAuth)
	protected.POST("/domains/:id/email-auth/dkim/generate", a.generateDomainDKIM)
	protected.POST("/domains/:id/email-auth/verify", a.verifyDomainEmailAuth)
	protected.DELETE("/domains/:id", a.deleteDomain)

	protected.GET("/inboxes", a.listInboxes)
	protected.POST("/inboxes", a.createInbox)
	protected.PATCH("/inboxes/:id", a.patchInbox)
	protected.DELETE("/inboxes/:id", a.deleteInbox)
	protected.GET("/emails", a.listEmails)
	protected.GET("/emails/:id", a.getEmail)
	protected.PATCH("/emails/:id/read", a.markRead)
	protected.DELETE("/emails/:id", a.deleteEmail)
	protected.GET("/emails/:id/attachments/:attachmentId/download", a.downloadAttachment)
	protected.GET("/dashboard", a.dashboard)

	// API key management (SMTP relay / submission)
	RegisterApiKeyRoutes(protected, a.DB, mw.Auth(a.Auth, a.DB), a.Config.SMTPAuthHostname, a.Config.SMTPAuthPort, a.Config.SMTPAuthTLSPort)
	// Test send endpoint (requires API key auth, not session)
	apiKeyGroup := api.Group("")
	apiKeyGroup.Use(mw.ApiKeyAuth(a.DB))
	RegisterTestSendRoute(apiKeyGroup, a.DB, mw.ApiKeyAuth(a.DB), a.SendEmail)

	api.GET("/events/stream", mw.AuthWithQueryToken(a.Auth, a.DB), a.events)

	admin := protected.Group("/admin")
	admin.Use(mw.Admin())
	admin.GET("/users", mw.SuperAdmin(), a.adminUsers)
	admin.PATCH("/users/:id/status", mw.SuperAdmin(), a.adminUserStatus)
	admin.PATCH("/users/:id/quotas", mw.SuperAdmin(), a.adminUserQuotas)
	admin.DELETE("/users/:id", mw.SuperAdmin(), a.adminDeleteUser)
	admin.PATCH("/attachments/:id/override", a.adminAttachmentOverride)
	if a.StaticProjects != nil {
		WireStaticProjectRoutes(protected, a.StaticProjects)
	}
	return r
}

func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-ID")
		if id == "" {
			id = uuid.NewString()
		}
		c.Header("X-Request-ID", id)
		c.Next()
	}
}

func (a App) register(c *gin.Context) {
	var req struct{ Email, Password, Name string }
	if !bind(c, &req) {
		return
	}
	if len(req.Password) < 8 {
		response.Error(c, http.StatusBadRequest, "invalid_input", "password length >= 8 required")
		return
	}
	if _, err := mail.ParseAddress(req.Email); err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_input", "valid email required")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "hash_failed", "could not hash password")
		return
	}
	user := db.User{Email: strings.ToLower(req.Email), Name: req.Name, PasswordHash: hash, IsActive: false, MaxDomains: 5, MaxInboxes: 50, MaxAttachmentSizeMB: 25, MaxMessageSizeMB: 25, MaxStorageBytes: 10 * 1024 * 1024 * 1024}
	if err := a.DB.Create(&user).Error; err != nil {
		if isDuplicateKeyError(err) {
			response.Error(c, http.StatusConflict, "email_exists", "email already exists")
		} else {
			response.Error(c, http.StatusInternalServerError, "registration_failed", "could not create account")
		}
		return
	}
	response.Created(c, user)
}

func (a App) login(c *gin.Context) {
	var req struct{ Email, Password string }
	if !bind(c, &req) {
		return
	}
	var user db.User
	if err := a.DB.Where("email = ?", strings.ToLower(req.Email)).First(&user).Error; err != nil || auth.CheckPassword(user.PasswordHash, req.Password) != nil {
		response.Error(c, http.StatusUnauthorized, "invalid_credentials", "invalid email or password")
		return
	}
	if !user.IsActive {
		response.Error(c, http.StatusForbidden, "account_inactive", "account is not active yet, please contact admin")
		return
	}
	access, err := a.Auth.AccessToken(user)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "token_failed", "could not issue access token")
		return
	}
	refresh, err := a.Auth.NewRefreshToken(user.ID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "token_failed", "could not issue refresh token")
		return
	}
	response.OK(c, gin.H{"access_token": access, "refresh_token": refresh, "user": user})
}

func (a App) me(c *gin.Context) {
	response.OK(c, mw.CurrentUser(c))
}

func (a App) refresh(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !bind(c, &req) {
		return
	}
	user, refresh, err := a.Auth.RotateRefreshToken(req.RefreshToken)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, "invalid_refresh", "refresh token invalid")
		return
	}
	access, _ := a.Auth.AccessToken(user)
	response.OK(c, gin.H{"access_token": access, "refresh_token": refresh})
}

func (a App) logout(c *gin.Context) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = c.ShouldBindJSON(&req)
	if req.RefreshToken != "" {
		_ = a.Auth.RevokeRefreshToken(req.RefreshToken)
	}
	response.OK(c, gin.H{"ok": true})
}

func (a App) changePassword(c *gin.Context) {
	var req struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !bind(c, &req) {
		return
	}
	user := mw.CurrentUser(c)
	if auth.CheckPassword(user.PasswordHash, req.OldPassword) != nil || len(req.NewPassword) < 8 {
		response.Error(c, http.StatusBadRequest, "invalid_password", "invalid old password or new password too short")
		return
	}
	hash, _ := auth.HashPassword(req.NewPassword)
	if err := a.DB.Model(&user).Update("password_hash", hash).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "password_failed", "could not update password")
		return
	}
	if err := a.Auth.RevokeUserRefreshTokens(user.ID); err != nil {
		response.Error(c, http.StatusInternalServerError, "session_revoke_failed", "could not revoke sessions")
		return
	}
	response.OK(c, gin.H{"ok": true})
}

func (a App) listDomains(c *gin.Context) {
	user := mw.CurrentUser(c)
	var rows []db.Domain
	a.DB.Where("user_id = ?", user.ID).Order("created_at desc").Find(&rows)
	response.OK(c, rows)
}

func (a App) createDomain(c *gin.Context) {
	user := mw.CurrentUser(c)
	var req struct{ Name string }
	if !bind(c, &req) {
		return
	}
	var count int64
	a.DB.Model(&db.Domain{}).Where("user_id = ?", user.ID).Count(&count)
	if int(count) >= user.MaxDomains {
		response.Error(c, http.StatusForbidden, "quota_exceeded", "domain quota exceeded")
		return
	}
	name := strings.ToLower(strings.TrimSpace(req.Name))
	row := db.Domain{UserID: user.ID, Name: name, Status: "pending", VerificationMethod: "mx", MXTarget: a.Config.MXTarget}
	if err := a.DB.Create(&row).Error; err != nil {
		response.Error(c, http.StatusConflict, "domain_exists", "domain already claimed")
		return
	}
	response.Created(c, row)
}

func (a App) getDomain(c *gin.Context) {
	user := mw.CurrentUser(c)
	var row db.Domain
	if ownerFirst(a.DB, c.Param("id"), user.ID, &row) != nil {
		response.Error(c, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	response.OK(c, gin.H{"domain": row, "mx_instruction": fmt.Sprintf("Create MX 10 %s for %s", a.Config.MXTarget, row.Name)})
}

func (a App) verifyDomain(c *gin.Context) {
	user := mw.CurrentUser(c)
	var row db.Domain
	if ownerFirst(a.DB, c.Param("id"), user.ID, &row) != nil {
		response.Error(c, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	ok, errMsg := a.Verifier.Verify(c.Request.Context(), row.Name)
	now := time.Now()
	status := "failed"
	warning := ""
	if ok {
		status = "verified"
		errMsg = ""
	} else if row.Status == "verified" {
		status = "verified"
		warning = "verified_warning"
	}
	a.DB.Model(&row).Updates(map[string]any{"status": status, "warning_status": warning, "last_verified_at": &now, "verification_error": errMsg})
	payload, _ := json.Marshal(gin.H{"ok": ok, "error": errMsg, "warning": warning})
	a.DB.Create(&db.DomainEvent{DomainID: row.ID, Type: "domain.verify", Payload: payload})
	a.DB.First(&row, "id = ?", row.ID)
	if ok && a.Redis != nil {
		_ = realtime.NewPublisher(a.Redis).Publish(c.Request.Context(), realtime.Event{Type: "domain.verified", UserID: user.ID, Data: gin.H{"domain_id": row.ID, "name": row.Name}})
	}
	response.OK(c, row)
}

func (a App) deleteDomain(c *gin.Context) {
	user := mw.CurrentUser(c)
	a.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).Delete(&db.Domain{})
	response.OK(c, gin.H{"ok": true})
}

func (a App) listInboxes(c *gin.Context) {
	user := mw.CurrentUser(c)
	var rows []db.Inbox
	a.DB.Where("user_id = ?", user.ID).Order("created_at desc").Find(&rows)
	response.OK(c, rows)
}

func (a App) createInbox(c *gin.Context) {
	user := mw.CurrentUser(c)
	var req struct {
		DomainID  uuid.UUID `json:"domain_id"`
		LocalPart string    `json:"local_part"`
	}
	if !bind(c, &req) {
		return
	}
	var count int64
	a.DB.Model(&db.Inbox{}).Where("user_id = ?", user.ID).Count(&count)
	if int(count) >= user.MaxInboxes {
		response.Error(c, http.StatusForbidden, "quota_exceeded", "inbox quota exceeded")
		return
	}
	var domain db.Domain
	if err := a.DB.Where("id = ? AND user_id = ? AND status = ?", req.DomainID, user.ID, "verified").First(&domain).Error; err != nil {
		response.Error(c, http.StatusBadRequest, "domain_not_verified", "domain must be verified")
		return
	}
	local := strings.ToLower(strings.TrimSpace(req.LocalPart))
	if local == "" || strings.ContainsAny(local, " @<>") {
		response.Error(c, http.StatusBadRequest, "invalid_local_part", "invalid local part")
		return
	}
	row := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: local, Address: local + "@" + domain.Name, IsActive: true}
	if err := a.DB.Create(&row).Error; err != nil {
		response.Error(c, http.StatusConflict, "inbox_exists", "inbox already exists")
		return
	}
	response.Created(c, row)
}

func (a App) patchInbox(c *gin.Context) {
	user := mw.CurrentUser(c)
	var req struct {
		IsActive *bool `json:"is_active"`
	}
	if !bind(c, &req) {
		return
	}
	updates := map[string]any{}
	if req.IsActive != nil {
		updates["is_active"] = *req.IsActive
	}
	a.DB.Model(&db.Inbox{}).Where("id = ? AND user_id = ?", c.Param("id"), user.ID).Updates(updates)
	response.OK(c, gin.H{"ok": true})
}

func (a App) deleteInbox(c *gin.Context) {
	user := mw.CurrentUser(c)
	a.DB.Where("id = ? AND user_id = ?", c.Param("id"), user.ID).Delete(&db.Inbox{})
	response.OK(c, gin.H{"ok": true})
}

func (a App) listEmails(c *gin.Context) {
	user := mw.CurrentUser(c)
	q := a.DB.Joins("JOIN inboxes ON inboxes.id = emails.inbox_id").Where("inboxes.user_id = ?", user.ID)
	if inbox := c.Query("inbox_id"); inbox != "" {
		q = q.Where("emails.inbox_id = ?", inbox)
	}
	if unread := c.Query("unread"); unread != "" {
		switch unread {
		case "true":
			q = q.Where("emails.is_read = false")
		case "false":
			q = q.Where("emails.is_read = true")
		default:
			response.Error(c, http.StatusBadRequest, "invalid_query", "unread must be true or false")
			return
		}
	}
	pageSize := 25
	if raw := c.DefaultQuery("page_size", "25"); raw != "" {
		var err error
		pageSize, err = parsePositiveInt(raw, 1, 100)
		if err != nil {
			response.Error(c, http.StatusBadRequest, "invalid_query", "page_size must be between 1 and 100")
			return
		}
	}
	page := 1
	if raw := c.DefaultQuery("page", "1"); raw != "" {
		var err error
		page, err = parsePositiveInt(raw, 1, 1000000)
		if err != nil {
			response.Error(c, http.StatusBadRequest, "invalid_query", "page must be >= 1")
			return
		}
	}
	var total int64
	if err := q.Model(&db.Email{}).Count(&total).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "email_list_failed", "could not load emails")
		return
	}
	var rows []db.Email
	if err := q.Omit("TextBody", "HTMLBody", "HTMLBodySanitized", "HeadersJSON").
		Order("emails.received_at desc").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&rows).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "email_list_failed", "could not load emails")
		return
	}
	totalPages := 0
	if total > 0 {
		totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	response.OK(c, gin.H{
		"items": rows,
		"pagination": gin.H{
			"page":        page,
			"page_size":   pageSize,
			"total":       total,
			"total_pages": totalPages,
			"has_prev":    page > 1,
			"has_next":    page < totalPages,
		},
	})
}

func (a App) getEmail(c *gin.Context) {
	user := mw.CurrentUser(c)
	var row db.Email
	err := a.DB.Preload("Attachments").Joins("JOIN inboxes ON inboxes.id = emails.inbox_id").Where("emails.id = ? AND inboxes.user_id = ?", c.Param("id"), user.ID).First(&row).Error
	if err != nil {
		response.Error(c, http.StatusNotFound, "not_found", "email not found")
		return
	}
	response.OK(c, row)
}

func (a App) markRead(c *gin.Context) {
	user := mw.CurrentUser(c)
	a.DB.Exec("UPDATE emails SET is_read = true FROM inboxes WHERE inboxes.id = emails.inbox_id AND emails.id = ? AND inboxes.user_id = ?", c.Param("id"), user.ID)
	response.OK(c, gin.H{"ok": true})
}

func (a App) deleteEmail(c *gin.Context) {
	user := mw.CurrentUser(c)
	a.DB.Exec("UPDATE emails SET deleted_at = NOW() FROM inboxes WHERE inboxes.id = emails.inbox_id AND emails.id = ? AND inboxes.user_id = ?", c.Param("id"), user.ID)
	response.OK(c, gin.H{"ok": true})
}

func (a App) downloadAttachment(c *gin.Context) {
	user := mw.CurrentUser(c)
	var att db.Attachment
	err := a.DB.Joins("JOIN emails ON emails.id = attachments.email_id JOIN inboxes ON inboxes.id = emails.inbox_id").
		Where("emails.id = ? AND attachments.id = ? AND inboxes.user_id = ?", c.Param("id"), c.Param("attachmentId"), user.ID).First(&att).Error
	if err != nil {
		response.Error(c, http.StatusNotFound, "not_found", "attachment not found")
		return
	}
	if att.IsBlocked && !att.AdminOverrideDownload {
		response.Error(c, http.StatusForbidden, "attachment_blocked", "attachment is blocked")
		return
	}
	c.FileAttachment(att.StoragePath, filepath.Base(att.Filename))
}

func (a App) dashboard(c *gin.Context) {
	user := mw.CurrentUser(c)
	var inboxes, today int64
	a.DB.Model(&db.Inbox{}).Where("user_id = ? AND is_active = true", user.ID).Count(&inboxes)
	a.DB.Model(&db.Email{}).Joins("JOIN inboxes ON inboxes.id = emails.inbox_id").Where("inboxes.user_id = ? AND emails.received_at >= ?", user.ID, time.Now().Truncate(24*time.Hour)).Count(&today)
	response.OK(c, gin.H{"mail_today": today, "storage_used_bytes": user.StorageUsedBytes, "active_inboxes": inboxes})
}

func (a App) events(c *gin.Context) {
	user := mw.CurrentUser(c)
	if a.Redis == nil {
		response.Error(c, http.StatusServiceUnavailable, "redis_unavailable", "realtime events not available")
		return
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)
	c.Writer.Flush()
	pubsub := a.Redis.Subscribe(c.Request.Context(), realtime.Channel)
	defer pubsub.Close()
	writer := bufio.NewWriter(c.Writer)
	fmt.Fprint(writer, ": connected\n\n")
	writer.Flush()
	c.Writer.Flush()
	ch := pubsub.Channel()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case msg := <-ch:
			var ev realtime.Event
			if json.Unmarshal([]byte(msg.Payload), &ev) == nil && ev.UserID == user.ID {
				fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", ev.Type, msg.Payload)
				writer.Flush()
				c.Writer.Flush()
			}
		}
	}
}

func (a App) adminUsers(c *gin.Context) {
	var users []db.User
	if err := a.DB.Order("created_at desc").Find(&users).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "users_failed", "could not load users")
		return
	}
	response.OK(c, users)
}

func (a App) adminUserStatus(c *gin.Context) {
	var req struct {
		IsActive bool `json:"is_active"`
	}
	if !bind(c, &req) {
		return
	}
	current := mw.CurrentUser(c)
	if !req.IsActive && c.Param("id") == current.ID.String() {
		response.Error(c, http.StatusBadRequest, "invalid_status", "cannot disable your own account")
		return
	}
	result := a.DB.Model(&db.User{}).Where("id = ?", c.Param("id")).Update("is_active", req.IsActive)
	if result.Error != nil {
		response.Error(c, http.StatusInternalServerError, "status_failed", "could not update user status")
		return
	}
	if result.RowsAffected == 0 {
		response.Error(c, http.StatusNotFound, "not_found", "user not found")
		return
	}
	payload, _ := json.Marshal(gin.H{"user_id": c.Param("id"), "is_active": req.IsActive})
	_ = a.DB.Create(&db.AuditLog{ActorID: &current.ID, Type: "user.status", Payload: payload}).Error
	response.OK(c, gin.H{"ok": true})
}

func (a App) adminUserQuotas(c *gin.Context) {
	current := mw.CurrentUser(c)
	var req struct {
		MaxDomains          *int   `json:"max_domains"`
		MaxInboxes          *int   `json:"max_inboxes"`
		MaxAttachmentSizeMB *int   `json:"max_attachment_size_mb"`
		MaxMessageSizeMB    *int   `json:"max_message_size_mb"`
		MaxStorageBytes     *int64 `json:"max_storage_bytes"`
		MaxWebsites         *int   `json:"max_websites"`
	}
	if !bind(c, &req) {
		return
	}
	updates := map[string]any{}
	if req.MaxDomains != nil {
		if *req.MaxDomains < 0 {
			response.Error(c, http.StatusBadRequest, "invalid_quota", "max_domains must be >= 0")
			return
		}
		updates["max_domains"] = *req.MaxDomains
	}
	if req.MaxInboxes != nil {
		if *req.MaxInboxes < 0 {
			response.Error(c, http.StatusBadRequest, "invalid_quota", "max_inboxes must be >= 0")
			return
		}
		updates["max_inboxes"] = *req.MaxInboxes
	}
	if req.MaxAttachmentSizeMB != nil {
		if *req.MaxAttachmentSizeMB < 1 {
			response.Error(c, http.StatusBadRequest, "invalid_quota", "max_attachment_size_mb must be >= 1")
			return
		}
		updates["max_attachment_size_mb"] = *req.MaxAttachmentSizeMB
	}
	if req.MaxMessageSizeMB != nil {
		if *req.MaxMessageSizeMB < 1 {
			response.Error(c, http.StatusBadRequest, "invalid_quota", "max_message_size_mb must be >= 1")
			return
		}
		updates["max_message_size_mb"] = *req.MaxMessageSizeMB
	}
	if req.MaxStorageBytes != nil {
		if *req.MaxStorageBytes < 1 {
			response.Error(c, http.StatusBadRequest, "invalid_quota", "max_storage_bytes must be >= 1")
			return
		}
		updates["max_storage_bytes"] = *req.MaxStorageBytes
	}
	if req.MaxWebsites != nil {
		if *req.MaxWebsites < 0 {
			response.Error(c, http.StatusBadRequest, "invalid_quota", "max_websites must be >= 0")
			return
		}
		updates["max_websites"] = *req.MaxWebsites
	}
	if len(updates) == 0 {
		response.Error(c, http.StatusBadRequest, "invalid_quota", "no quota fields provided")
		return
	}
	result := a.DB.Model(&db.User{}).Where("id = ?", c.Param("id")).Updates(updates)
	if result.Error != nil {
		response.Error(c, http.StatusInternalServerError, "quota_failed", "could not update user quotas")
		return
	}
	if result.RowsAffected == 0 {
		response.Error(c, http.StatusNotFound, "not_found", "user not found")
		return
	}
	payload, _ := json.Marshal(gin.H{"user_id": c.Param("id"), "updates": updates})
	_ = a.DB.Create(&db.AuditLog{ActorID: &current.ID, Type: "user.quota", Payload: payload}).Error
	response.OK(c, gin.H{"ok": true})
}

func (a App) adminDeleteUser(c *gin.Context) {
	current := mw.CurrentUser(c)
	targetID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_user", "invalid user id")
		return
	}
	if targetID == current.ID {
		response.Error(c, http.StatusBadRequest, "invalid_user", "cannot delete your own account")
		return
	}

	var target db.User
	if err := a.DB.First(&target, "id = ?", targetID).Error; err != nil {
		response.Error(c, http.StatusNotFound, "not_found", "user not found")
		return
	}
	if target.IsSuperAdmin {
		response.Error(c, http.StatusBadRequest, "invalid_user", "cannot delete a super admin account")
		return
	}

	var files []string
	err = a.DB.Transaction(func(tx *gorm.DB) error {
		var inboxIDs []uuid.UUID
		if err := tx.Model(&db.Inbox{}).Unscoped().Where("user_id = ?", targetID).Pluck("id", &inboxIDs).Error; err != nil {
			return err
		}

		var emailIDs []uuid.UUID
		if len(inboxIDs) > 0 {
			if err := tx.Model(&db.Email{}).Unscoped().Where("inbox_id IN ?", inboxIDs).Pluck("id", &emailIDs).Error; err != nil {
				return err
			}
			var rawPaths []string
			if err := tx.Model(&db.Email{}).Unscoped().Where("inbox_id IN ? AND raw_storage_path <> ''", inboxIDs).Pluck("raw_storage_path", &rawPaths).Error; err != nil {
				return err
			}
			files = append(files, rawPaths...)
		}

		if len(emailIDs) > 0 {
			var attachmentPaths []string
			if err := tx.Model(&db.Attachment{}).Where("email_id IN ? AND storage_path <> ''", emailIDs).Pluck("storage_path", &attachmentPaths).Error; err != nil {
				return err
			}
			files = append(files, attachmentPaths...)
			if err := tx.Unscoped().Where("email_id IN ?", emailIDs).Delete(&db.Attachment{}).Error; err != nil {
				return err
			}
			if err := tx.Unscoped().Where("id IN ?", emailIDs).Delete(&db.Email{}).Error; err != nil {
				return err
			}
		}

		var domainIDs []uuid.UUID
		if err := tx.Model(&db.Domain{}).Unscoped().Where("user_id = ?", targetID).Pluck("id", &domainIDs).Error; err != nil {
			return err
		}
		if len(domainIDs) > 0 {
			if err := tx.Unscoped().Where("domain_id IN ?", domainIDs).Delete(&db.DomainEvent{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Unscoped().Where("user_id = ?", targetID).Delete(&db.Inbox{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("user_id = ?", targetID).Delete(&db.Domain{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("user_id = ?", targetID).Delete(&db.RefreshToken{}).Error; err != nil {
			return err
		}

		payload, _ := json.Marshal(gin.H{"user_id": target.ID, "email": target.Email})
		if err := tx.Create(&db.AuditLog{ActorID: &current.ID, Type: "user.delete", Payload: payload}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(&target).Error
	})
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "delete_failed", "could not delete user")
		return
	}

	for _, file := range files {
		if file != "" {
			_ = os.Remove(file)
		}
	}
	response.OK(c, gin.H{"ok": true})
}

func (a App) adminAttachmentOverride(c *gin.Context) {
	if !a.Config.AllowAdminOverride {
		response.Error(c, http.StatusForbidden, "override_disabled", "admin attachment override is disabled")
		return
	}
	user := mw.CurrentUser(c)
	now := time.Now()
	result := a.DB.Model(&db.Attachment{}).Where("id = ?", c.Param("id")).Updates(map[string]any{"admin_override_download": true, "admin_override_by": user.ID, "admin_override_at": &now})
	if result.Error != nil {
		response.Error(c, http.StatusInternalServerError, "override_failed", "could not override attachment")
		return
	}
	if result.RowsAffected == 0 {
		response.Error(c, http.StatusNotFound, "not_found", "attachment not found")
		return
	}
	payload, _ := json.Marshal(gin.H{"attachment_id": c.Param("id")})
	_ = a.DB.Create(&db.AuditLog{ActorID: &user.ID, Type: "attachment.override", Payload: payload}).Error
	response.OK(c, gin.H{"ok": true})
}

// isDuplicateKeyError checks if a GORM/PostgreSQL error is a unique constraint violation.
func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "duplicate key") ||
		strings.Contains(err.Error(), "SQLSTATE 23505") ||
		strings.Contains(err.Error(), "UNIQUE constraint")
}

func bind(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		response.Error(c, http.StatusBadRequest, "invalid_json", "invalid request body")
		return false
	}
	return true
}

func ownerFirst(database *gorm.DB, id string, userID uuid.UUID, dest any) error {
	err := database.Where("id = ? AND user_id = ?", id, userID).First(dest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	return err
}

func parsePositiveInt(raw string, min int, max int) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, errors.New("invalid int")
	}
	return n, nil
}

func BackgroundDomainRecheck(ctx context.Context, database *gorm.DB, verifier dns.Verifier, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recheckDomainsOnce(ctx, database, verifier)
		}
	}
}

func recheckDomainsOnce(ctx context.Context, database *gorm.DB, verifier dns.Verifier) {
	var domains []db.Domain
	database.Where("status IN ?", []string{"pending", "verified"}).Find(&domains)
	for _, domain := range domains {
		ok, msg := verifier.Verify(ctx, domain.Name)
		now := time.Now()
		updates := map[string]any{"last_verified_at": &now, "verification_error": msg}
		if ok {
			updates["status"] = "verified"
			updates["warning_status"] = ""
		} else if domain.Status == "verified" {
			updates["warning_status"] = "verified_warning"
		} else {
			updates["status"] = "failed"
		}
		database.Model(&domain).Updates(updates)
	}
}
