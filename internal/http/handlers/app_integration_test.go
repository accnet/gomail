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
	"gomail/internal/mail/outbound"
	teamservice "gomail/internal/teams"

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
		CanCreateWorkspaces: true,
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

func createVerifiedDomainEmailAuth(t *testing.T, database *gorm.DB, domain db.Domain) {
	t.Helper()
	authRow := db.DomainEmailAuth{
		DomainID:        domain.ID,
		SPFStatus:       db.DomainAuthStatusVerified,
		SPFRecord:       "v=spf1 a mx -all",
		DKIMSelector:    "gomail",
		DKIMPublicKey:   "test-public-key",
		DKIMStatus:      db.DomainAuthStatusVerified,
		DKIMRecordName:  "gomail._domainkey." + domain.Name,
		DKIMRecordValue: "v=DKIM1; k=rsa; p=test",
	}
	if err := database.Create(&authRow).Error; err != nil {
		t.Fatal(err)
	}
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

func TestInviteMemberSendsEmailFromSaaSDomain(t *testing.T) {
	app, database := newTestApp(t)
	app.Config.SaaSDomain = "saas.test"
	app.Config.AppBaseURL = "https://mail.saas.test"
	app.Teams = teamservice.NewService(database)

	owner := createUser(t, database, "owner@saas.test", true, true, false)
	team, err := app.Teams.CreateTeam(context.Background(), owner.ID, "Product Team")
	if err != nil {
		t.Fatal(err)
	}

	var captured outbound.Message
	app.SendOutbound = func(userID uuid.UUID, msg outbound.Message, sentLog db.SentEmailLog) error {
		if userID != owner.ID {
			t.Fatalf("sender user id = %s want %s", userID, owner.ID)
		}
		if sentLog.TeamID == nil || *sentLog.TeamID != team.ID {
			t.Fatalf("sent log team id = %v want %s", sentLog.TeamID, team.ID)
		}
		captured = msg
		return nil
	}

	router := app.Router()
	token := bearerToken(t, app, owner)
	resp := doJSON(t, router, http.MethodPost, "/api/teams/"+team.ID.String()+"/invites", map[string]any{
		"email": "member@example.net",
		"role":  db.TeamRoleMember,
	}, token)
	if resp.Code != http.StatusCreated {
		t.Fatalf("invite status = %d body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Token     string `json:"token"`
		EmailSent bool   `json:"email_sent"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Token == "" || !body.EmailSent {
		t.Fatalf("invite response token=%q email_sent=%v", body.Token, body.EmailSent)
	}
	if captured.From != "no-reply@saas.test" {
		t.Fatalf("from = %q want no-reply@saas.test", captured.From)
	}
	if len(captured.To) != 1 || captured.To[0] != "member@example.net" {
		t.Fatalf("to = %#v", captured.To)
	}
	if !strings.Contains(captured.TextBody, "https://mail.saas.test/app/join.html?token="+body.Token) {
		t.Fatalf("invite body missing join link: %s", captured.TextBody)
	}
}

func TestInviteRegisterCannotCreateWorkspace(t *testing.T) {
	app, database := newTestApp(t)
	app.Teams = teamservice.NewService(database)

	owner := createUser(t, database, "workspace-owner@test.local", true, true, false)
	team, err := app.Teams.CreateTeam(context.Background(), owner.ID, "Locked Team")
	if err != nil {
		t.Fatal(err)
	}
	invite, err := app.Teams.InviteMember(context.Background(), owner.ID, team.ID, "invited@test.local", db.TeamRoleMember, []string{teamservice.ScopeEmailAccess})
	if err != nil {
		t.Fatal(err)
	}

	router := app.Router()
	registerResp := doJSON(t, router, http.MethodPost, "/api/team-invites/"+invite.Token+"/register", map[string]any{
		"email":    "invited@test.local",
		"password": "password123",
	}, "")
	if registerResp.Code != http.StatusCreated {
		t.Fatalf("invite register status = %d body=%s", registerResp.Code, registerResp.Body.String())
	}

	var registerBody struct {
		AccessToken string  `json:"access_token"`
		User        db.User `json:"user"`
	}
	if err := json.Unmarshal(registerResp.Body.Bytes(), &registerBody); err != nil {
		t.Fatal(err)
	}
	if registerBody.User.CanCreateWorkspaces {
		t.Fatal("invited user should not be allowed to create workspaces")
	}

	var ownedTeams int64
	if err := database.Model(&db.Team{}).Where("owner_id = ? AND deleted_at IS NULL", registerBody.User.ID).Count(&ownedTeams).Error; err != nil {
		t.Fatal(err)
	}
	if ownedTeams != 0 {
		t.Fatalf("invited user owns %d workspaces, want 0", ownedTeams)
	}

	createResp := doJSON(t, router, http.MethodPost, "/api/teams", map[string]any{"name": "Should Fail"}, registerBody.AccessToken)
	if createResp.Code != http.StatusForbidden {
		t.Fatalf("create workspace status = %d body=%s", createResp.Code, createResp.Body.String())
	}
}

func TestTeamScopeMiddlewareRestrictsGrantedPermissions(t *testing.T) {
	app, database := newTestApp(t)
	app.Teams = teamservice.NewService(database)

	owner := createUser(t, database, "scope-owner@test.local", true, true, false)
	member := createUser(t, database, "scope-member@test.local", true, false, false)
	team, err := app.Teams.CreateTeam(context.Background(), owner.ID, "Scoped Team")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&db.TeamMember{
		TeamID:      team.ID,
		UserID:      member.ID,
		Role:        db.TeamRoleMember,
		Permissions: teamservice.MarshalScopes([]string{teamservice.ScopeEmailAccess}),
	}).Error; err != nil {
		t.Fatal(err)
	}

	router := app.Router()
	token := bearerToken(t, app, member)
	forbidden := doJSONWithTeam(t, router, http.MethodPost, "/api/domains", map[string]any{"name": "blocked.test"}, token, team.ID.String())
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("domain create status = %d body=%s", forbidden.Code, forbidden.Body.String())
	}
	allowed := doJSONWithTeam(t, router, http.MethodGet, "/api/inboxes", nil, token, team.ID.String())
	if allowed.Code != http.StatusOK {
		t.Fatalf("inbox list status = %d body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestReplyEmailPersistsSentLogAndThread(t *testing.T) {
	app, database := newTestApp(t)
	user := createUser(t, database, "reply@test.local", true, false, false)
	domain := db.Domain{UserID: user.ID, Name: "reply.test", Status: db.DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	createVerifiedDomainEmailAuth(t, database, domain)
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@reply.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	email := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "original@example.net",
		ConversationID: "original@example.net",
		FromAddress:    "sender@example.net",
		ToAddress:      inbox.Address,
		Subject:        "Question",
		TextBody:       "Original body",
		ReceivedAt:     time.Now().Add(-time.Minute),
	}
	if err := database.Create(&email).Error; err != nil {
		t.Fatal(err)
	}
	var captured outbound.Message
	app.SendOutbound = func(userID uuid.UUID, msg outbound.Message, sentLog db.SentEmailLog) error {
		captured = msg
		sentLog.UserID = userID
		sentLog.FromAddress = msg.From
		sentLog.ToAddress = strings.Join(msg.To, ",")
		sentLog.Subject = msg.Subject
		sentLog.BodyText = msg.TextBody
		sentLog.Status = db.SentEmailStatusSent
		now := time.Now()
		sentLog.SentAt = &now
		return database.Create(&sentLog).Error
	}
	router := app.Router()
	token := bearerToken(t, app, user)
	replyResp := doJSON(t, router, http.MethodPost, "/api/emails/"+email.ID.String()+"/reply", map[string]any{
		"mode":      "reply",
		"body_text": "Thanks",
	}, token)
	if replyResp.Code != http.StatusOK {
		t.Fatalf("reply status = %d body=%s", replyResp.Code, replyResp.Body.String())
	}
	if captured.From != inbox.Address || strings.Join(captured.To, ",") != "sender@example.net" {
		t.Fatalf("unexpected captured message: %+v", captured)
	}
	if captured.Headers["In-Reply-To"] != "<original@example.net>" {
		t.Fatalf("missing in-reply-to header: %+v", captured.Headers)
	}

	threadResp := doJSON(t, router, http.MethodGet, "/api/emails/"+email.ID.String()+"/thread", nil, token)
	if threadResp.Code != http.StatusOK {
		t.Fatalf("thread status = %d body=%s", threadResp.Code, threadResp.Body.String())
	}
	var threadBody struct {
		ConversationID string `json:"conversation_id"`
		Items          []struct {
			IsOutbound bool   `json:"is_outbound"`
			Subject    string `json:"subject"`
		} `json:"items"`
	}
	if err := json.Unmarshal(threadResp.Body.Bytes(), &threadBody); err != nil {
		t.Fatal(err)
	}
	if threadBody.ConversationID != email.ConversationID || len(threadBody.Items) != 2 || !threadBody.Items[1].IsOutbound {
		t.Fatalf("unexpected thread body: %+v", threadBody)
	}
}

func TestReplyEmailNormalizesBracketedOriginalMessageID(t *testing.T) {
	app, database := newTestApp(t)
	user := createUser(t, database, "reply-bracket@test.local", true, false, false)
	domain := db.Domain{UserID: user.ID, Name: "replybracket.test", Status: db.DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	createVerifiedDomainEmailAuth(t, database, domain)
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@replybracket.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	email := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "<original-bracket@example.net>",
		ConversationID: "<original-bracket@example.net>",
		FromAddress:    "sender@example.net",
		ToAddress:      inbox.Address,
		Subject:        "Question",
		ReceivedAt:     time.Now(),
	}
	if err := database.Create(&email).Error; err != nil {
		t.Fatal(err)
	}
	var captured outbound.Message
	app.SendOutbound = func(userID uuid.UUID, msg outbound.Message, sentLog db.SentEmailLog) error {
		captured = msg
		sentLog.UserID = userID
		sentLog.Status = db.SentEmailStatusSent
		return database.Create(&sentLog).Error
	}
	router := app.Router()
	token := bearerToken(t, app, user)
	resp := doJSON(t, router, http.MethodPost, "/api/emails/"+email.ID.String()+"/reply", map[string]any{
		"mode":      "reply",
		"body_text": "Thanks",
	}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("reply status = %d body=%s", resp.Code, resp.Body.String())
	}
	if captured.Headers["In-Reply-To"] != "<original-bracket@example.net>" {
		t.Fatalf("in-reply-to = %q", captured.Headers["In-Reply-To"])
	}
	if captured.Headers["References"] != "<original-bracket@example.net>" {
		t.Fatalf("references = %q", captured.Headers["References"])
	}
}

func TestReplyEmailFailsWhenSenderNotConfigured(t *testing.T) {
	app, database := newTestApp(t)
	user := createUser(t, database, "no-reply@test.local", true, false, false)
	domain := db.Domain{UserID: user.ID, Name: "nosmtp.test", Status: db.DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@nosmtp.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	email := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "no-smtp@example.net",
		ConversationID: "no-smtp@example.net",
		FromAddress:    "sender@example.net",
		ToAddress:      inbox.Address,
		Subject:        "Question",
		ReceivedAt:     time.Now(),
	}
	if err := database.Create(&email).Error; err != nil {
		t.Fatal(err)
	}
	router := app.Router()
	token := bearerToken(t, app, user)
	resp := doJSON(t, router, http.MethodPost, "/api/emails/"+email.ID.String()+"/reply", map[string]any{
		"mode":      "reply",
		"body_text": "Thanks",
	}, token)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("reply status = %d body=%s", resp.Code, resp.Body.String())
	}
}

func TestReplyEmailWarnsWhenSenderDomainEmailAuthNotVerified(t *testing.T) {
	app, database := newTestApp(t)
	app.SendOutbound = func(userID uuid.UUID, msg outbound.Message, sentLog db.SentEmailLog) error {
		t.Fatal("sender should not be called")
		return nil
	}
	user := createUser(t, database, "domain-not-ready@test.local", true, false, false)
	domain := db.Domain{UserID: user.ID, Name: "notready.test", Status: db.DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	authRow := db.DomainEmailAuth{
		DomainID:     domain.ID,
		SPFStatus:    db.DomainAuthStatusPending,
		DKIMSelector: "gomail",
		DKIMStatus:   db.DomainAuthStatusPending,
	}
	if err := database.Create(&authRow).Error; err != nil {
		t.Fatal(err)
	}
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@notready.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	email := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "notready@example.net",
		ConversationID: "notready@example.net",
		FromAddress:    "sender@example.net",
		ToAddress:      inbox.Address,
		Subject:        "Question",
		ReceivedAt:     time.Now(),
	}
	if err := database.Create(&email).Error; err != nil {
		t.Fatal(err)
	}
	router := app.Router()
	token := bearerToken(t, app, user)
	statusResp := doJSON(t, router, http.MethodGet, "/api/emails/"+email.ID.String()+"/reply-status", nil, token)
	if statusResp.Code != http.StatusOK {
		t.Fatalf("reply-status = %d body=%s", statusResp.Code, statusResp.Body.String())
	}
	var statusBody struct {
		Configured         bool   `json:"configured"`
		SenderDomainReady  bool   `json:"sender_domain_ready"`
		SenderDomainReason string `json:"sender_domain_reason"`
	}
	if err := json.Unmarshal(statusResp.Body.Bytes(), &statusBody); err != nil {
		t.Fatal(err)
	}
	if !statusBody.Configured || statusBody.SenderDomainReady || !strings.Contains(statusBody.SenderDomainReason, "SPF and DKIM") {
		t.Fatalf("unexpected reply status: %+v", statusBody)
	}
	replyResp := doJSON(t, router, http.MethodPost, "/api/emails/"+email.ID.String()+"/reply", map[string]any{
		"mode":      "reply",
		"body_text": "Thanks",
	}, token)
	if replyResp.Code != http.StatusForbidden {
		t.Fatalf("reply status = %d body=%s", replyResp.Code, replyResp.Body.String())
	}
}

func TestOutboundStatusReflectsSenderConfiguration(t *testing.T) {
	app, _ := newTestApp(t)
	user := createUser(t, app.DB, "outbound-status@test.local", true, false, false)
	router := app.Router()
	token := bearerToken(t, app, user)
	resp := doJSON(t, router, http.MethodGet, "/api/outbound/status", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Configured {
		t.Fatal("expected outbound to be unconfigured")
	}

	app.SendOutbound = func(userID uuid.UUID, msg outbound.Message, sentLog db.SentEmailLog) error {
		return nil
	}
	router = app.Router()
	resp = doJSON(t, router, http.MethodGet, "/api/outbound/status", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("configured status = %d body=%s", resp.Code, resp.Body.String())
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Configured {
		t.Fatal("expected outbound to be configured")
	}
}

func TestListConversationsGroupsInboundAndSentMessages(t *testing.T) {
	app, database := newTestApp(t)
	user := createUser(t, database, "conversations@test.local", true, false, false)
	domain := db.Domain{UserID: user.ID, Name: "conversations.test", Status: db.DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@conversations.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	conversationID := "thread@example.net"
	first := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "first@example.net",
		ConversationID: conversationID,
		FromAddress:    "sender@example.net",
		ToAddress:      inbox.Address,
		Subject:        "Question",
		Snippet:        "first",
		ReceivedAt:     time.Now().Add(-3 * time.Minute),
		IsRead:         false,
	}
	second := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "second@example.net",
		ConversationID: conversationID,
		FromAddress:    "sender@example.net",
		ToAddress:      inbox.Address,
		Subject:        "Re: Question",
		Snippet:        "second",
		ReceivedAt:     time.Now().Add(-2 * time.Minute),
		IsRead:         true,
	}
	if err := database.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	sentAt := time.Now().Add(-time.Minute)
	sent := db.SentEmailLog{
		UserID:         user.ID,
		ConversationID: conversationID,
		Mode:           "reply",
		FromAddress:    inbox.Address,
		ToAddress:      "sender@example.net",
		Subject:        "Re: Question",
		BodyText:       "latest sent",
		Status:         db.SentEmailStatusSent,
		SentAt:         &sentAt,
	}
	if err := database.Create(&sent).Error; err != nil {
		t.Fatal(err)
	}

	router := app.Router()
	token := bearerToken(t, app, user)
	resp := doJSON(t, router, http.MethodGet, "/api/conversations?page=1&page_size=25", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("conversations status = %d body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Items []struct {
			ConversationID string `json:"conversation_id"`
			PrimaryEmailID string `json:"primary_email_id"`
			Count          int    `json:"count"`
			UnreadCount    int    `json:"unread_count"`
			Snippet        string `json:"snippet"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %+v", body.Items)
	}
	got := body.Items[0]
	if got.ConversationID != conversationID || got.PrimaryEmailID != second.ID.String() || got.Count != 3 || got.UnreadCount != 1 || got.Snippet != "latest sent" {
		t.Fatalf("unexpected conversation: %+v", got)
	}
}

func TestListConversationsIgnoresSoftDeletedEmails(t *testing.T) {
	app, database := newTestApp(t)
	user := createUser(t, database, "softdelete-conversations@test.local", true, false, false)
	domain := db.Domain{UserID: user.ID, Name: "softdelete-conversations.test", Status: db.DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@softdelete-conversations.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	conversationID := "softdelete-thread@example.net"
	active := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "active@example.net",
		ConversationID: conversationID,
		FromAddress:    "sender@example.net",
		ToAddress:      inbox.Address,
		Subject:        "Question",
		Snippet:        "active",
		ReceivedAt:     time.Now().Add(-2 * time.Minute),
		IsRead:         false,
	}
	deleted := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "deleted@example.net",
		ConversationID: conversationID,
		FromAddress:    "sender@example.net",
		ToAddress:      inbox.Address,
		Subject:        "Question",
		Snippet:        "deleted",
		ReceivedAt:     time.Now().Add(-time.Minute),
		IsRead:         false,
	}
	if err := database.Create(&active).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&deleted).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Delete(&deleted).Error; err != nil {
		t.Fatal(err)
	}

	sentAt := time.Now()
	sent := db.SentEmailLog{
		UserID:         user.ID,
		ConversationID: conversationID,
		Mode:           "reply",
		FromAddress:    inbox.Address,
		ToAddress:      "sender@example.net",
		Subject:        "Re: Question",
		BodyText:       "reply",
		Status:         db.SentEmailStatusSent,
		SentAt:         &sentAt,
	}
	if err := database.Create(&sent).Error; err != nil {
		t.Fatal(err)
	}

	router := app.Router()
	token := bearerToken(t, app, user)
	resp := doJSON(t, router, http.MethodGet, "/api/conversations?page=1&page_size=25", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("conversations status = %d body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Items []struct {
			ConversationID string `json:"conversation_id"`
			PrimaryEmailID string `json:"primary_email_id"`
			Count          int    `json:"count"`
			UnreadCount    int    `json:"unread_count"`
		} `json:"items"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %+v", body.Items)
	}
	got := body.Items[0]
	if got.ConversationID != conversationID || got.PrimaryEmailID != active.ID.String() || got.Count != 2 || got.UnreadCount != 1 {
		t.Fatalf("unexpected conversation with soft delete: %+v", got)
	}
}

func TestReplyAllNormalizesRecipients(t *testing.T) {
	app, database := newTestApp(t)
	user := createUser(t, database, "reply-all@test.local", true, false, false)
	domain := db.Domain{UserID: user.ID, Name: "replyall.test", Status: db.DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	createVerifiedDomainEmailAuth(t, database, domain)
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@replyall.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	email := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "reply-all@example.net",
		ConversationID: "reply-all@example.net",
		FromAddress:    "Sender <sender@example.net>",
		ToAddress:      "hello@replyall.test, peer@example.net",
		Subject:        "Team question",
		ReceivedAt:     time.Now(),
	}
	if err := database.Create(&email).Error; err != nil {
		t.Fatal(err)
	}
	var captured outbound.Message
	app.SendOutbound = func(userID uuid.UUID, msg outbound.Message, sentLog db.SentEmailLog) error {
		captured = msg
		sentLog.UserID = userID
		sentLog.FromAddress = msg.From
		sentLog.ToAddress = strings.Join(msg.To, ",")
		sentLog.CcAddress = strings.Join(msg.Cc, ",")
		sentLog.Subject = msg.Subject
		sentLog.Status = db.SentEmailStatusSent
		now := time.Now()
		sentLog.SentAt = &now
		return database.Create(&sentLog).Error
	}
	router := app.Router()
	token := bearerToken(t, app, user)
	resp := doJSON(t, router, http.MethodPost, "/api/emails/"+email.ID.String()+"/reply", map[string]any{
		"mode":      "reply_all",
		"body_text": "Thanks all",
	}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("reply-all status = %d body=%s", resp.Code, resp.Body.String())
	}
	if strings.Join(captured.To, ",") != "sender@example.net" {
		t.Fatalf("to = %v", captured.To)
	}
	if strings.Join(captured.Cc, ",") != "peer@example.net" {
		t.Fatalf("cc = %v", captured.Cc)
	}
}

func TestForwardStartsNewConversationWithoutReplyHeaders(t *testing.T) {
	app, database := newTestApp(t)
	user := createUser(t, database, "forward@test.local", true, false, false)
	domain := db.Domain{UserID: user.ID, Name: "forward.test", Status: db.DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	createVerifiedDomainEmailAuth(t, database, domain)
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@forward.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	email := db.Email{
		InboxID:        inbox.ID,
		MessageID:      "forward-source@example.net",
		ConversationID: "forward-source@example.net",
		FromAddress:    "sender@example.net",
		ToAddress:      inbox.Address,
		Subject:        "Original",
		TextBody:       "Original body",
		ReceivedAt:     time.Now(),
	}
	if err := database.Create(&email).Error; err != nil {
		t.Fatal(err)
	}
	var captured outbound.Message
	var saved db.SentEmailLog
	app.SendOutbound = func(userID uuid.UUID, msg outbound.Message, sentLog db.SentEmailLog) error {
		captured = msg
		sentLog.UserID = userID
		sentLog.FromAddress = msg.From
		sentLog.ToAddress = strings.Join(msg.To, ",")
		sentLog.Subject = msg.Subject
		sentLog.BodyText = msg.TextBody
		sentLog.Status = db.SentEmailStatusSent
		now := time.Now()
		sentLog.SentAt = &now
		if err := database.Create(&sentLog).Error; err != nil {
			return err
		}
		saved = sentLog
		return nil
	}
	router := app.Router()
	token := bearerToken(t, app, user)
	resp := doJSON(t, router, http.MethodPost, "/api/emails/"+email.ID.String()+"/reply", map[string]any{
		"mode":      "forward",
		"to":        []string{"friend@example.net"},
		"body_text": "FYI",
	}, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("forward status = %d body=%s", resp.Code, resp.Body.String())
	}
	if captured.Headers["In-Reply-To"] != "" || captured.Headers["References"] != "" {
		t.Fatalf("forward should not carry reply headers: %+v", captured.Headers)
	}
	if saved.ConversationID == "" || saved.ConversationID == email.ConversationID {
		t.Fatalf("forward conversation id = %q", saved.ConversationID)
	}
	if saved.InReplyToMessageID != "" {
		t.Fatalf("forward in-reply-to = %q", saved.InReplyToMessageID)
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
	if !bytes.Contains(getResp.Body.Bytes(), []byte(`"name":"_dmarc.auth-example.test"`)) {
		t.Fatalf("expected DMARC record name, body=%s", getResp.Body.String())
	}
	if !bytes.Contains(getResp.Body.Bytes(), []byte("v=DMARC1; p=none; adkim=s; aspf=s")) {
		t.Fatalf("expected DMARC instruction, body=%s", getResp.Body.String())
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

func TestVerifyDomainEmailAuthUpdatesDomainWarningStatus(t *testing.T) {
	app, database := newTestApp(t)
	app.Config.SMTPRelayPublicIP = "203.0.113.10"
	app.Config.TraefikPublicIP = "203.0.113.10"
	app.Verifier = dns.Verifier{
		MXTarget: app.Config.MXTarget,
		Timeout:  time.Second,
		Resolver: stubResolver{
			target: app.Config.MXTarget,
			txtRecords: map[string][]string{
				"warn-auth.test": {`v=spf1 +ip4:203.0.113.10/32 mx -all`},
			},
			ipRecords: map[string][]net.IPAddr{
				"warn-auth.test": {{IP: net.ParseIP("203.0.113.10")}},
			},
		},
	}
	router := app.Router()

	user := createUser(t, database, "warn-auth@test.local", true, false, false)
	token := bearerToken(t, app, user)
	domain := db.Domain{
		UserID:             user.ID,
		Name:               "warn-auth.test",
		Status:             db.DomainStatusVerified,
		VerificationMethod: "mx",
		MXTarget:           app.Config.MXTarget,
		ARecordStatus:      db.ARecordStatusVerified,
		ARecordResult:      "203.0.113.10",
	}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	authRow := db.DomainEmailAuth{
		DomainID:        domain.ID,
		SPFStatus:       db.DomainAuthStatusPending,
		SPFRecord:       "v=spf1 ip4:203.0.113.10 mx -all",
		DKIMSelector:    "gomail1",
		DKIMStatus:      db.DomainAuthStatusPending,
		DKIMPublicKey:   "abc123",
		DKIMRecordName:  "gomail1._domainkey.warn-auth.test",
		DKIMRecordValue: "v=DKIM1; k=rsa; p=abc123",
	}
	if err := database.Create(&authRow).Error; err != nil {
		t.Fatal(err)
	}

	verifyResp := doJSON(t, router, http.MethodPost, "/api/domains/"+domain.ID.String()+"/email-auth/verify", nil, token)
	if verifyResp.Code != http.StatusOK {
		t.Fatalf("verify email auth status = %d body=%s", verifyResp.Code, verifyResp.Body.String())
	}

	var gotDomain db.Domain
	var gotAuth db.DomainEmailAuth
	if err := database.First(&gotDomain, "id = ?", domain.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.First(&gotAuth, "domain_id = ?", domain.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotAuth.SPFStatus != db.DomainAuthStatusVerified {
		t.Fatalf("expected SPF verified, got %+v", gotAuth)
	}
	if gotAuth.DKIMStatus != db.DomainAuthStatusFailed {
		t.Fatalf("expected DKIM failed, got %+v", gotAuth)
	}
	if gotDomain.WarningStatus != domainVerifiedWarningStatus {
		t.Fatalf("expected domain warning status %q, got %+v", domainVerifiedWarningStatus, gotDomain)
	}
}

func TestVerifyDomainUpdatesARecordStatus(t *testing.T) {
	app, database := newTestApp(t)
	app.Config.TraefikPublicIP = "203.0.113.10"
	app.Verifier = dns.Verifier{
		MXTarget: app.Config.MXTarget,
		Timeout:  time.Second,
		Resolver: stubResolver{
			target: app.Config.MXTarget,
			ipRecords: map[string][]net.IPAddr{
				"a-record.test": {{IP: net.ParseIP("203.0.113.10")}},
			},
		},
	}
	router := app.Router()

	user := createUser(t, database, "a-record@test.local", true, false, false)
	token := bearerToken(t, app, user)
	createResp := doJSON(t, router, http.MethodPost, "/api/domains", map[string]any{"name": "a-record.test"}, token)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create domain status = %d body=%s", createResp.Code, createResp.Body.String())
	}

	var domain db.Domain
	if err := database.Where("name = ?", "a-record.test").First(&domain).Error; err != nil {
		t.Fatal(err)
	}
	verifyResp := doJSON(t, router, http.MethodPost, "/api/domains/"+domain.ID.String()+"/verify", nil, token)
	if verifyResp.Code != http.StatusOK {
		t.Fatalf("verify domain status = %d body=%s", verifyResp.Code, verifyResp.Body.String())
	}
	if err := database.First(&domain, "id = ?", domain.ID).Error; err != nil {
		t.Fatal(err)
	}
	if domain.ARecordStatus != db.ARecordStatusVerified || domain.ARecordResult != "203.0.113.10" {
		t.Fatalf("expected verified A record, got %+v", domain)
	}
	if domain.WarningStatus != "" {
		t.Fatalf("expected no warning status, got %+v", domain)
	}
}

func TestListDomainsIncludesVerificationColumns(t *testing.T) {
	app, database := newTestApp(t)
	router := app.Router()

	user := createUser(t, database, "domains-columns@test.local", true, false, false)
	token := bearerToken(t, app, user)
	domain := db.Domain{
		UserID:             user.ID,
		Name:               "columns-example.test",
		Status:             db.DomainStatusVerified,
		VerificationMethod: "mx",
		MXTarget:           app.Config.MXTarget,
		ARecordStatus:      db.ARecordStatusFailed,
		ARecordResult:      "domain resolves to 203.0.113.11",
	}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	authRow := db.DomainEmailAuth{
		DomainID:   domain.ID,
		SPFStatus:  db.DomainAuthStatusVerified,
		DKIMStatus: db.DomainAuthStatusFailed,
	}
	if err := database.Create(&authRow).Error; err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, router, http.MethodGet, "/api/domains", nil, token)
	if resp.Code != http.StatusOK {
		t.Fatalf("list domains status = %d body=%s", resp.Code, resp.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 {
		t.Fatalf("expected 1 domain, got %d", len(body))
	}
	if got := body[0]["mx_status"]; got != db.DomainStatusVerified {
		t.Fatalf("mx_status = %v, want %q", got, db.DomainStatusVerified)
	}
	if got := body[0]["spf_status"]; got != db.DomainAuthStatusVerified {
		t.Fatalf("spf_status = %v, want %q", got, db.DomainAuthStatusVerified)
	}
	if got := body[0]["dkim_status"]; got != db.DomainAuthStatusFailed {
		t.Fatalf("dkim_status = %v, want %q", got, db.DomainAuthStatusFailed)
	}
	if got := body[0]["a_record_status"]; got != db.ARecordStatusFailed {
		t.Fatalf("a_record_status = %v, want %q", got, db.ARecordStatusFailed)
	}
}

func TestVerifyDomainAAndMXIndependently(t *testing.T) {
	app, database := newTestApp(t)
	app.Config.TraefikPublicIP = "203.0.113.10"
	app.Verifier = dns.Verifier{
		MXTarget: app.Config.MXTarget,
		Timeout:  time.Second,
		Resolver: stubResolver{
			target: app.Config.MXTarget,
			ipRecords: map[string][]net.IPAddr{
				"split-verify.test": {{IP: net.ParseIP("203.0.113.10")}},
			},
		},
	}
	router := app.Router()

	user := createUser(t, database, "split-verify@test.local", true, false, false)
	token := bearerToken(t, app, user)
	domain := db.Domain{
		UserID:             user.ID,
		Name:               "split-verify.test",
		Status:             db.DomainStatusPending,
		VerificationMethod: "mx",
		MXTarget:           app.Config.MXTarget,
		ARecordStatus:      db.ARecordStatusPending,
	}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}

	aResp := doJSON(t, router, http.MethodPost, "/api/domains/"+domain.ID.String()+"/verify-a", nil, token)
	if aResp.Code != http.StatusOK {
		t.Fatalf("verify a status = %d body=%s", aResp.Code, aResp.Body.String())
	}
	var afterA db.Domain
	if err := database.First(&afterA, "id = ?", domain.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterA.ARecordStatus != db.ARecordStatusVerified {
		t.Fatalf("a_record_status after verify-a = %q", afterA.ARecordStatus)
	}
	if afterA.Status != db.DomainStatusPending {
		t.Fatalf("mx/domain status after verify-a = %q, want pending", afterA.Status)
	}

	mxResp := doJSON(t, router, http.MethodPost, "/api/domains/"+domain.ID.String()+"/verify-mx", nil, token)
	if mxResp.Code != http.StatusOK {
		t.Fatalf("verify mx status = %d body=%s", mxResp.Code, mxResp.Body.String())
	}
	var afterMX db.Domain
	if err := database.First(&afterMX, "id = ?", domain.ID).Error; err != nil {
		t.Fatal(err)
	}
	if afterMX.Status != db.DomainStatusVerified {
		t.Fatalf("mx/domain status after verify-mx = %q, want verified", afterMX.Status)
	}
	if afterMX.ARecordStatus != db.ARecordStatusVerified {
		t.Fatalf("a_record_status after verify-mx = %q, want preserved verified", afterMX.ARecordStatus)
	}
}

type stubResolver struct {
	target     string
	txtRecords map[string][]string
	ipRecords  map[string][]net.IPAddr
}

func (s stubResolver) LookupMX(_ context.Context, _ string) ([]*net.MX, error) {
	return []*net.MX{{Host: s.target + ".", Pref: 10}}, nil
}

func (s stubResolver) LookupTXT(_ context.Context, name string) ([]string, error) {
	if s.txtRecords == nil {
		return nil, nil
	}
	return s.txtRecords[name], nil
}

func (s stubResolver) LookupIPAddr(_ context.Context, name string) ([]net.IPAddr, error) {
	if s.ipRecords == nil {
		return nil, nil
	}
	return s.ipRecords[name], nil
}
