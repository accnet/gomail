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
	"sort"
	"strconv"
	"strings"
	"time"

	"gomail/internal/auth"
	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/dns"
	mw "gomail/internal/http/middleware"
	"gomail/internal/mail/outbound"
	"gomail/internal/realtime"
	"gomail/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type App struct {
	DB                   *gorm.DB
	Auth                 *auth.Service
	Config               config.Config
	Redis                *redis.Client
	Verifier             dns.Verifier
	StaticProjects       *StaticProjectsHandler
	StaticSiteMiddleware *StaticSiteMiddleware
	SendEmail            func(to, from, subject, body string) error
	SendOutbound         func(userID uuid.UUID, msg outbound.Message, log db.SentEmailLog) error
	RateLimiter          *mw.RateLimiter
}

func (a App) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery(), requestID())

	// Static site middleware: check if the SaaS domain has a static project bound.
	// Must run before the web app static handler so it can intercept and serve the
	// static site instead of the login page.
	if a.StaticSiteMiddleware != nil {
		r.Use(a.StaticSiteMiddleware.Handler())
	}

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
	protected.POST("/domains/:id/verify-a", a.verifyDomainA)
	protected.POST("/domains/:id/verify-mx", a.verifyDomainMX)
	protected.GET("/domains/:id/email-auth", a.getDomainEmailAuth)
	protected.POST("/domains/:id/email-auth/dkim/generate", a.generateDomainDKIM)
	protected.POST("/domains/:id/email-auth/verify", a.verifyDomainEmailAuth)
	protected.DELETE("/domains/:id", a.deleteDomain)

	protected.GET("/inboxes", a.listInboxes)
	protected.POST("/inboxes", a.createInbox)
	protected.PATCH("/inboxes/:id", a.patchInbox)
	protected.DELETE("/inboxes/:id", a.deleteInbox)
	protected.GET("/conversations", a.listConversations)
	protected.GET("/emails", a.listEmails)
	protected.GET("/emails/:id", a.getEmail)
	protected.GET("/emails/:id/thread", a.getEmailThread)
	protected.GET("/emails/:id/reply-status", a.getEmailReplyStatus)
	protected.POST("/emails/:id/reply", a.replyEmail)
	protected.PATCH("/emails/:id/read", a.markRead)
	protected.DELETE("/emails/:id", a.deleteEmail)
	protected.GET("/emails/:id/attachments/:attachmentId/download", a.downloadAttachment)
	protected.GET("/dashboard", a.dashboard)
	protected.GET("/outbound/status", a.outboundStatus)

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
	items := make([]domainListItem, 0, len(rows))
	for _, row := range rows {
		authRow, _ := loadOptionalDomainEmailAuth(a.DB, row.ID)
		items = append(items, buildDomainListItem(row, authRow))
	}
	response.OK(c, items)
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
	if ok {
		errMsg = ""
	}
	row = a.applyDomainMXVerification(row, ok, errMsg)
	aStatus, aResult, aCheckedAt := verifyDomainARecord(c.Request.Context(), a.Verifier, a.Config, row.Name)
	row.ARecordStatus = aStatus
	row.ARecordResult = aResult
	row.ARecordCheckAt = aCheckedAt
	authRow, _ := loadOptionalDomainEmailAuth(a.DB, row.ID)
	row.WarningStatus = deriveDomainWarningStatus(row, authRow)
	a.DB.Model(&row).Updates(map[string]any{
		"warning_status":    row.WarningStatus,
		"a_record_status":   row.ARecordStatus,
		"a_record_result":   row.ARecordResult,
		"a_record_check_at": row.ARecordCheckAt,
	})
	payload, _ := json.Marshal(gin.H{"ok": ok, "error": errMsg, "warning": row.WarningStatus, "a_record_status": row.ARecordStatus, "a_record_result": aResult, "mx_status": row.Status})
	a.DB.Create(&db.DomainEvent{DomainID: row.ID, Type: "domain.verify", Payload: payload})
	a.DB.First(&row, "id = ?", row.ID)
	if (ok || row.ARecordStatus == db.ARecordStatusVerified) && a.Redis != nil {
		_ = realtime.NewPublisher(a.Redis).Publish(c.Request.Context(), realtime.Event{Type: "domain.verified", UserID: user.ID, Data: gin.H{"domain_id": row.ID, "name": row.Name}})
	}
	response.OK(c, buildDomainListItem(row, mustLoadOptionalDomainEmailAuth(a.DB, row.ID)))
}

func (a App) verifyDomainA(c *gin.Context) {
	user := mw.CurrentUser(c)
	var row db.Domain
	if ownerFirst(a.DB, c.Param("id"), user.ID, &row) != nil {
		response.Error(c, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	aStatus, aResult, aCheckedAt := verifyDomainARecord(c.Request.Context(), a.Verifier, a.Config, row.Name)
	row.ARecordStatus = aStatus
	row.ARecordResult = aResult
	row.ARecordCheckAt = aCheckedAt
	authRow, _ := loadOptionalDomainEmailAuth(a.DB, row.ID)
	row.WarningStatus = deriveDomainWarningStatus(row, authRow)
	a.DB.Model(&row).Updates(map[string]any{
		"warning_status":    row.WarningStatus,
		"a_record_status":   row.ARecordStatus,
		"a_record_result":   row.ARecordResult,
		"a_record_check_at": row.ARecordCheckAt,
	})
	a.DB.First(&row, "id = ?", row.ID)
	response.OK(c, buildDomainListItem(row, mustLoadOptionalDomainEmailAuth(a.DB, row.ID)))
}

func (a App) verifyDomainMX(c *gin.Context) {
	user := mw.CurrentUser(c)
	var row db.Domain
	if ownerFirst(a.DB, c.Param("id"), user.ID, &row) != nil {
		response.Error(c, http.StatusNotFound, "not_found", "domain not found")
		return
	}
	ok, errMsg := a.Verifier.Verify(c.Request.Context(), row.Name)
	if ok {
		errMsg = ""
	}
	row = a.applyDomainMXVerification(row, ok, errMsg)
	response.OK(c, buildDomainListItem(row, mustLoadOptionalDomainEmailAuth(a.DB, row.ID)))
}

func (a App) applyDomainMXVerification(row db.Domain, ok bool, errMsg string) db.Domain {
	status := db.DomainStatusFailed
	if ok {
		status = db.DomainStatusVerified
		errMsg = ""
	} else if row.Status == db.DomainStatusVerified {
		status = db.DomainStatusVerified
	}
	now := time.Now()
	row.Status = status
	row.VerificationError = errMsg
	row.LastVerifiedAt = &now
	authRow, _ := loadOptionalDomainEmailAuth(a.DB, row.ID)
	row.WarningStatus = deriveDomainWarningStatus(row, authRow)
	a.DB.Model(&row).Updates(map[string]any{
		"status":             row.Status,
		"warning_status":     row.WarningStatus,
		"last_verified_at":   row.LastVerifiedAt,
		"verification_error": row.VerificationError,
	})
	a.DB.First(&row, "id = ?", row.ID)
	return row
}

type domainListItem struct {
	db.Domain
	MXStatus   string `json:"mx_status"`
	SPFStatus  string `json:"spf_status"`
	DKIMStatus string `json:"dkim_status"`
}

func buildDomainListItem(row db.Domain, auth *db.DomainEmailAuth) domainListItem {
	item := domainListItem{
		Domain:     row,
		MXStatus:   row.Status,
		SPFStatus:  db.DomainAuthStatusPending,
		DKIMStatus: db.DomainAuthStatusPending,
	}
	if auth != nil {
		if auth.SPFStatus != "" {
			item.SPFStatus = auth.SPFStatus
		}
		if auth.DKIMStatus != "" {
			item.DKIMStatus = auth.DKIMStatus
		}
	}
	return item
}

func mustLoadOptionalDomainEmailAuth(database *gorm.DB, domainID uuid.UUID) *db.DomainEmailAuth {
	authRow, _ := loadOptionalDomainEmailAuth(database, domainID)
	return authRow
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

type conversationListItem struct {
	ConversationID string    `json:"conversation_id"`
	PrimaryEmailID uuid.UUID `json:"primary_email_id"`
	InboxID        uuid.UUID `json:"inbox_id"`
	Subject        string    `json:"subject"`
	FromAddress    string    `json:"from_address"`
	ToAddress      string    `json:"to_address"`
	Snippet        string    `json:"snippet"`
	LatestAt       time.Time `json:"latest_at"`
	Count          int       `json:"count"`
	UnreadCount    int       `json:"unread_count"`
	IsRead         bool      `json:"is_read"`
}

func (a App) listConversations(c *gin.Context) {
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

	var emails []db.Email
	if err := q.Omit("TextBody", "HTMLBody", "HTMLBodySanitized", "HeadersJSON").
		Order("emails.received_at desc").
		Find(&emails).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "conversation_list_failed", "could not load conversations")
		return
	}

	byConversation := map[string]*conversationListItem{}
	order := make([]string, 0)
	for _, row := range emails {
		key := conversationKey(row)
		item := byConversation[key]
		if item == nil {
			item = &conversationListItem{
				ConversationID: key,
				PrimaryEmailID: row.ID,
				InboxID:        row.InboxID,
				Subject:        row.Subject,
				FromAddress:    row.FromAddress,
				ToAddress:      row.ToAddress,
				Snippet:        row.Snippet,
				LatestAt:       row.ReceivedAt,
			}
			byConversation[key] = item
			order = append(order, key)
		}
		item.Count++
		if !row.IsRead {
			item.UnreadCount++
		}
		if row.ReceivedAt.After(item.LatestAt) {
			item.PrimaryEmailID = row.ID
			item.InboxID = row.InboxID
			item.Subject = row.Subject
			item.FromAddress = row.FromAddress
			item.ToAddress = row.ToAddress
			item.Snippet = row.Snippet
			item.LatestAt = row.ReceivedAt
		}
	}

	if len(order) > 0 {
		var sent []db.SentEmailLog
		if err := a.DB.Where("user_id = ? AND conversation_id IN ?", user.ID, order).Find(&sent).Error; err != nil {
			response.Error(c, http.StatusInternalServerError, "conversation_list_failed", "could not load sent conversations")
			return
		}
		for _, row := range sent {
			item := byConversation[row.ConversationID]
			if item == nil {
				continue
			}
			item.Count++
			at := row.CreatedAt
			if row.SentAt != nil {
				at = *row.SentAt
			}
			if at.After(item.LatestAt) {
				item.Subject = row.Subject
				item.FromAddress = row.FromAddress
				item.ToAddress = row.ToAddress
				item.Snippet = firstNonEmpty(row.BodyText, row.BodyHTML)
				item.LatestAt = at
			}
		}
	}

	items := make([]conversationListItem, 0, len(byConversation))
	for _, item := range byConversation {
		item.IsRead = item.UnreadCount == 0
		items = append(items, *item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].LatestAt.After(items[j].LatestAt)
	})
	total := len(items)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	totalPages := 0
	if total > 0 {
		totalPages = (total + pageSize - 1) / pageSize
	}
	response.OK(c, gin.H{
		"items": items[start:end],
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

func conversationKey(row db.Email) string {
	return firstNonEmpty(cleanHeaderMessageID(row.ConversationID), cleanHeaderMessageID(row.MessageID), row.ID.String())
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

type replyEmailRequest struct {
	Mode     string   `json:"mode"`
	To       []string `json:"to"`
	Cc       []string `json:"cc"`
	Bcc      []string `json:"bcc"`
	Subject  string   `json:"subject"`
	BodyText string   `json:"body_text"`
}

func (a App) replyEmail(c *gin.Context) {
	if a.SendOutbound == nil {
		response.Error(c, http.StatusServiceUnavailable, "sender_unavailable", "email sender is not configured")
		return
	}
	user := mw.CurrentUser(c)
	original, inbox, ok := a.loadOwnedEmailWithInbox(c, user.ID, c.Param("id"))
	if !ok {
		return
	}
	domainReady, reason := a.senderDomainReady(inbox)
	if !domainReady {
		response.Error(c, http.StatusForbidden, "sender_domain_not_ready", reason)
		return
	}
	var req replyEmailRequest
	if !bind(c, &req) {
		return
	}
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.Mode == "" {
		req.Mode = "reply"
	}
	if req.Mode != "reply" && req.Mode != "reply_all" && req.Mode != "forward" {
		response.Error(c, http.StatusBadRequest, "invalid_mode", "mode must be reply, reply_all, or forward")
		return
	}
	to, cc, bcc := replyRecipients(req, original, inbox)
	if len(to)+len(cc)+len(bcc) == 0 {
		response.Error(c, http.StatusBadRequest, "invalid_recipients", "at least one recipient is required")
		return
	}
	subject := replySubject(req.Mode, firstNonEmpty(req.Subject, original.Subject))
	messageID := newOutboundMessageID(inbox.Address)
	refs := append([]string{}, decodeStringSlice([]byte(original.ReferencesMessageIDs))...)
	originalMessageID := cleanHeaderMessageID(original.MessageID)
	if req.Mode != "forward" {
		if originalMessageID != "" && !containsString(refs, originalMessageID) {
			refs = append(refs, originalMessageID)
		}
	}
	headers := map[string]string{"Message-ID": "<" + messageID + ">"}
	inReplyTo := ""
	conversationID := messageID
	if req.Mode != "forward" {
		inReplyTo = originalMessageID
		conversationID = firstNonEmpty(cleanHeaderMessageID(original.ConversationID), originalMessageID)
		if inReplyTo != "" {
			headers["In-Reply-To"] = "<" + inReplyTo + ">"
		}
		if len(refs) > 0 {
			headers["References"] = formatReferences(refs)
		}
	}
	bodyText := strings.TrimSpace(req.BodyText)
	if bodyText == "" {
		bodyText = quotedBody(original)
	}
	log := db.SentEmailLog{
		OriginalEmailID:      &original.ID,
		ParentEmailID:        &original.ID,
		ConversationID:       conversationID,
		Mode:                 req.Mode,
		MessageID:            messageID,
		InReplyToMessageID:   inReplyTo,
		ReferencesMessageIDs: encodeJSON(refs),
	}
	msg := outbound.Message{
		From:     inbox.Address,
		To:       to,
		Cc:       cc,
		Bcc:      bcc,
		Subject:  subject,
		TextBody: bodyText,
		Headers:  headers,
	}
	if err := a.SendOutbound(user.ID, msg, log); err != nil {
		response.Error(c, http.StatusInternalServerError, "send_failed", "failed to send email: "+err.Error())
		return
	}
	response.OK(c, gin.H{"sent": true, "message_id": messageID, "conversation_id": conversationID})
}

func (a App) getEmailReplyStatus(c *gin.Context) {
	user := mw.CurrentUser(c)
	_, inbox, ok := a.loadOwnedEmailWithInbox(c, user.ID, c.Param("id"))
	if !ok {
		return
	}
	domainReady, reason := a.senderDomainReady(inbox)
	response.OK(c, gin.H{
		"configured":           a.SendOutbound != nil,
		"sender_domain_ready":  domainReady,
		"sender_domain_reason": reason,
		"from_address":         inbox.Address,
	})
}

func (a App) getEmailThread(c *gin.Context) {
	user := mw.CurrentUser(c)
	original, _, ok := a.loadOwnedEmailWithInbox(c, user.ID, c.Param("id"))
	if !ok {
		return
	}
	conversationID := firstNonEmpty(original.ConversationID, original.MessageID)
	var inbound []db.Email
	if err := a.DB.Joins("JOIN inboxes ON inboxes.id = emails.inbox_id").
		Where("inboxes.user_id = ? AND emails.conversation_id = ?", user.ID, conversationID).
		Order("emails.received_at ASC").
		Find(&inbound).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "thread_failed", "could not load thread")
		return
	}
	var sent []db.SentEmailLog
	if err := a.DB.Where("user_id = ? AND conversation_id = ?", user.ID, conversationID).
		Order("COALESCE(sent_at, created_at) ASC").
		Find(&sent).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, "thread_failed", "could not load thread")
		return
	}
	items := make([]threadItem, 0, len(inbound)+len(sent))
	for _, row := range inbound {
		items = append(items, inboundThreadItem(row))
	}
	for _, row := range sent {
		items = append(items, sentThreadItem(row))
	}
	sortThreadItems(items)
	response.OK(c, gin.H{"conversation_id": conversationID, "root_email_id": original.RootEmailID, "items": items})
}

func (a App) loadOwnedEmailWithInbox(c *gin.Context, userID uuid.UUID, id string) (db.Email, db.Inbox, bool) {
	var row db.Email
	err := a.DB.Preload("Attachments").Joins("JOIN inboxes ON inboxes.id = emails.inbox_id").Where("emails.id = ? AND inboxes.user_id = ?", id, userID).First(&row).Error
	if err != nil {
		response.Error(c, http.StatusNotFound, "not_found", "email not found")
		return db.Email{}, db.Inbox{}, false
	}
	var inbox db.Inbox
	if err := a.DB.First(&inbox, "id = ?", row.InboxID).Error; err != nil {
		response.Error(c, http.StatusNotFound, "not_found", "inbox not found")
		return db.Email{}, db.Inbox{}, false
	}
	return row, inbox, true
}

func (a App) senderDomainReady(inbox db.Inbox) (bool, string) {
	var domain db.Domain
	if err := a.DB.First(&domain, "id = ? AND deleted_at IS NULL", inbox.DomainID).Error; err != nil {
		return false, "sender domain was not found"
	}
	if domain.Status != db.DomainStatusVerified {
		return false, "sender domain MX is not verified"
	}
	var auth db.DomainEmailAuth
	if err := a.DB.Where("domain_id = ?", domain.ID).First(&auth).Error; err != nil {
		return false, "sender domain SPF and DKIM are not verified"
	}
	if auth.SPFStatus != db.DomainAuthStatusVerified || auth.DKIMStatus != db.DomainAuthStatusVerified {
		return false, "sender domain SPF and DKIM are not verified"
	}
	return true, ""
}

type threadItem struct {
	ID                uuid.UUID `json:"id"`
	IsOutbound        bool      `json:"is_outbound"`
	Mode              string    `json:"mode,omitempty"`
	FromAddress       string    `json:"from_address"`
	ToAddress         string    `json:"to_address"`
	CcAddress         string    `json:"cc_address,omitempty"`
	Subject           string    `json:"subject"`
	TextBody          string    `json:"text_body,omitempty"`
	HTMLBodySanitized string    `json:"html_body_sanitized,omitempty"`
	MessageID         string    `json:"message_id"`
	ConversationID    string    `json:"conversation_id"`
	At                time.Time `json:"at"`
	Status            string    `json:"status,omitempty"`
}

func inboundThreadItem(row db.Email) threadItem {
	return threadItem{
		ID:                row.ID,
		IsOutbound:        false,
		FromAddress:       row.FromAddress,
		ToAddress:         row.ToAddress,
		Subject:           row.Subject,
		TextBody:          row.TextBody,
		HTMLBodySanitized: row.HTMLBodySanitized,
		MessageID:         row.MessageID,
		ConversationID:    row.ConversationID,
		At:                row.ReceivedAt,
	}
}

func sentThreadItem(row db.SentEmailLog) threadItem {
	at := row.CreatedAt
	if row.SentAt != nil {
		at = *row.SentAt
	}
	return threadItem{
		ID:             row.ID,
		IsOutbound:     true,
		Mode:           row.Mode,
		FromAddress:    row.FromAddress,
		ToAddress:      row.ToAddress,
		CcAddress:      row.CcAddress,
		Subject:        row.Subject,
		TextBody:       row.BodyText,
		MessageID:      row.MessageID,
		ConversationID: row.ConversationID,
		At:             at,
		Status:         row.Status,
	}
}

func sortThreadItems(items []threadItem) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].At.Before(items[j].At)
	})
}

func replyRecipients(req replyEmailRequest, original db.Email, inbox db.Inbox) ([]string, []string, []string) {
	if len(req.To)+len(req.Cc)+len(req.Bcc) > 0 {
		return cleanAddressList(req.To, inbox.Address), cleanAddressList(req.Cc, inbox.Address), cleanAddressList(req.Bcc, inbox.Address)
	}
	switch req.Mode {
	case "forward":
		return nil, nil, nil
	case "reply_all":
		to := cleanAddressList([]string{original.FromAddress}, inbox.Address)
		cc := cleanAddressList([]string{original.ToAddress}, inbox.Address)
		return to, cc, nil
	default:
		return cleanAddressList([]string{original.FromAddress}, inbox.Address), nil, nil
	}
}

func cleanAddressList(values []string, exclude string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	exclude = strings.ToLower(strings.TrimSpace(exclude))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			addr, err := mail.ParseAddress(strings.TrimSpace(part))
			if err != nil || addr.Address == "" {
				continue
			}
			key := strings.ToLower(addr.Address)
			if key == exclude {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, addr.Address)
		}
	}
	return out
}

func replySubject(mode, subject string) string {
	subject = strings.TrimSpace(subject)
	lower := strings.ToLower(subject)
	if mode == "forward" {
		if strings.HasPrefix(lower, "fwd:") {
			return subject
		}
		return "Fwd: " + subject
	}
	if strings.HasPrefix(lower, "re:") {
		return subject
	}
	return "Re: " + subject
}

func newOutboundMessageID(from string) string {
	domain := "gomail.local"
	if parts := strings.Split(from, "@"); len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		domain = strings.ToLower(strings.TrimSpace(parts[1]))
	}
	return uuid.NewString() + "@" + domain
}

func quotedBody(email db.Email) string {
	source := strings.TrimSpace(email.TextBody)
	if source == "" {
		source = htmlToText(email.HTMLBodySanitized)
	}
	if source == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")
	for i, line := range lines {
		lines[i] = "> " + line
	}
	return "\n\n" + strings.Join(lines, "\n")
}

func htmlToText(value string) string {
	value = strings.ReplaceAll(value, "<br>", "\n")
	value = strings.ReplaceAll(value, "<br/>", "\n")
	value = strings.ReplaceAll(value, "<br />", "\n")
	var out strings.Builder
	inTag := false
	for _, r := range value {
		switch r {
		case '<':
			inTag = true
		case '>':
			inTag = false
		default:
			if !inTag {
				out.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(out.String())
}

func decodeStringSlice(raw json.RawMessage) []string {
	var out []string
	if len(raw) == 0 {
		return nil
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func encodeJSON(value any) []byte {
	b, _ := json.Marshal(value)
	return b
}

func formatReferences(refs []string) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = cleanHeaderMessageID(ref)
		if ref != "" {
			parts = append(parts, "<"+ref+">")
		}
	}
	return strings.Join(parts, " ")
}

func containsString(values []string, needle string) bool {
	needle = cleanHeaderMessageID(needle)
	for _, value := range values {
		if strings.EqualFold(cleanHeaderMessageID(value), needle) {
			return true
		}
	}
	return false
}

func cleanHeaderMessageID(value string) string {
	return strings.Trim(strings.TrimSpace(value), "<>")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func (a App) outboundStatus(c *gin.Context) {
	response.OK(c, gin.H{
		"configured": a.SendOutbound != nil,
		"mode":       a.Config.OutboundMode,
	})
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

func BackgroundDomainRecheck(ctx context.Context, database *gorm.DB, verifier dns.Verifier, cfg config.Config, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			recheckDomainsOnce(ctx, database, verifier, cfg)
		}
	}
}

func recheckDomainsOnce(ctx context.Context, database *gorm.DB, verifier dns.Verifier, cfg config.Config) {
	var domains []db.Domain
	database.Where("status IN ?", []string{"pending", "verified"}).Find(&domains)
	for _, domain := range domains {
		ok, msg := verifier.Verify(ctx, domain.Name)
		aStatus, aResult, aCheckedAt := verifyDomainARecord(ctx, verifier, cfg, domain.Name)
		now := time.Now()
		updates := map[string]any{"last_verified_at": &now, "verification_error": msg, "a_record_status": aStatus, "a_record_result": aResult, "a_record_check_at": aCheckedAt}
		if ok {
			updates["status"] = "verified"
		} else if domain.Status == "verified" {
			updates["status"] = "verified"
		} else {
			updates["status"] = "failed"
		}
		candidate := domain
		candidate.Status = updates["status"].(string)
		candidate.VerificationError = msg
		candidate.LastVerifiedAt = &now
		candidate.ARecordStatus = aStatus
		candidate.ARecordResult = aResult
		candidate.ARecordCheckAt = aCheckedAt
		authRow, _ := loadOptionalDomainEmailAuth(database, domain.ID)
		updates["warning_status"] = deriveDomainWarningStatus(candidate, authRow)
		database.Model(&domain).Updates(updates)
	}
}
