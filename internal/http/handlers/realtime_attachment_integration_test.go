package handlers

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	mailservice "gomail/internal/mail/service"
	"gomail/internal/realtime"
	"gomail/internal/storage"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestEventsStreamFanoutByUser(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	app, database := newTestApp(t)
	app.Redis = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	router := httptest.NewServer(app.Router())
	defer router.Close()

	user := createUser(t, database, "stream@test.local", true, false, false)
	other := createUser(t, database, "other@test.local", true, false, false)
	token := bearerToken(t, app, user)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, router.URL+"/api/events/stream?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	done := make(chan string, 1)
	ready := make(chan struct{}, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		var buf strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				done <- buf.String()
				return
			}
			buf.WriteString(line)
			if strings.Contains(buf.String(), ": connected") {
				select {
				case ready <- struct{}{}:
				default:
				}
			}
			if strings.Contains(buf.String(), "event: mail.received") && strings.Contains(buf.String(), "data: ") {
				done <- buf.String()
				return
			}
		}
	}()

	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SSE ready frame")
	}

	pub := realtime.NewPublisher(app.Redis)
	_ = pub.Publish(context.Background(), realtime.Event{Type: "mail.received", UserID: other.ID, Data: map[string]any{"skip": true}})
	_ = pub.Publish(context.Background(), realtime.Event{Type: "mail.received", UserID: user.ID, Data: map[string]any{"ok": true}})

	select {
	case body := <-done:
		if !strings.Contains(body, "mail.received") || !strings.Contains(body, `"ok":true`) {
			t.Fatalf("unexpected SSE body: %q", body)
		}
		if strings.Contains(body, `"skip":true`) {
			t.Fatalf("unexpected foreign event leaked: %q", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SSE event")
	}
}

func TestAttachmentPipelineBlockedAndOverrideFlow(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()

	app, database := newTestApp(t)
	app.Config.AllowAdminOverride = true
	app.Redis = redis.NewClient(&redis.Options{Addr: mr.Addr()})
	router := app.Router()

	store := storage.NewLocal(t.TempDir()+"/attachments", t.TempDir()+"/raw")
	if err := store.Ensure(); err != nil {
		t.Fatal(err)
	}

	user := createUser(t, database, "mailbox@test.local", true, false, false)
	admin := createUser(t, database, "admin2@test.local", true, true, false)
	domain := db.Domain{UserID: user.ID, Name: "attach.test", Status: "verified", MXTarget: app.Config.MXTarget, VerificationMethod: "mx", LastVerifiedAt: timePtr(time.Now())}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	inbox := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "hello", Address: "hello@attach.test", IsActive: true}
	if err := database.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}

	pipeline := mailservice.Pipeline{
		DB:        database,
		Config:    config.Config{BlockFlagged: true},
		Store:     store,
		Publisher: realtime.NewPublisher(app.Redis),
	}
	raw := "From: sender@test.local\r\nTo: hello@attach.test\r\nSubject: Attachment\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=abc123\r\n\r\n--abc123\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello\r\n--abc123\r\nContent-Type: text/plain\r\nContent-Disposition: attachment; filename=\"run.ps1\"\r\n\r\nWrite-Host hacked\r\n--abc123--\r\n"
	email, err := pipeline.Ingest(context.Background(), inbox, user, "sender@test.local", inbox.Address, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	var attachment db.Attachment
	if err := database.Where("email_id = ?", email.ID).First(&attachment).Error; err != nil {
		t.Fatal(err)
	}
	if !attachment.IsBlocked {
		t.Fatalf("expected blocked attachment, got %+v", attachment)
	}

	userToken := bearerToken(t, app, user)
	downloadResp := doJSON(t, router, http.MethodGet, fmt.Sprintf("/api/emails/%s/attachments/%s/download", email.ID, attachment.ID), nil, userToken)
	if downloadResp.Code != http.StatusForbidden {
		t.Fatalf("expected blocked download 403, got %d body=%s", downloadResp.Code, downloadResp.Body.String())
	}

	adminToken := bearerToken(t, app, admin)
	overrideResp := doJSON(t, router, http.MethodPatch, "/api/admin/attachments/"+attachment.ID.String()+"/override", nil, adminToken)
	if overrideResp.Code != http.StatusOK {
		t.Fatalf("override status = %d body=%s", overrideResp.Code, overrideResp.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/emails/%s/attachments/%s/download", email.ID, attachment.ID), nil)
	req.Header.Set("Authorization", "Bearer "+userToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected download ok, got %d body=%s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, "Write-Host hacked") {
		t.Fatalf("expected attachment body, got %q", body)
	}

	var auditCount int64
	if err := database.Model(&db.AuditLog{}).Where("type = ?", "attachment.override").Count(&auditCount).Error; err != nil {
		t.Fatal(err)
	}
	if auditCount == 0 {
		t.Fatal("expected attachment.override audit log")
	}
}

func timePtr(v time.Time) *time.Time {
	return &v
}
