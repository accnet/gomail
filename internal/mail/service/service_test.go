package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/storage"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestSanitizeHTMLBlocksRemoteImages(t *testing.T) {
	raw := `<div><img src="https://cdn.example.com/a.png" srcset="https://cdn.example.com/a.png 1x, cid:local 2x"><img src="cid:logo"><img src="/local.png"></div>`
	out := sanitizeHTML(raw)
	if strings.Contains(out, "https://cdn.example.com") {
		t.Fatalf("expected remote image source removed, got %q", out)
	}
	if !strings.Contains(out, `src="cid:logo"`) {
		t.Fatalf("expected cid image preserved, got %q", out)
	}
	if !strings.Contains(out, `src="/local.png"`) {
		t.Fatalf("expected local image preserved, got %q", out)
	}
	if strings.Contains(out, "srcset=") {
		t.Fatalf("expected srcset removed after sanitization, got %q", out)
	}
}

func TestSanitizeHTMLBlocksRemoteBackgroundImages(t *testing.T) {
	raw := `<div style="background-image:url(https://cdn.example.com/bg.png)">x</div><img src="//cdn.example.com/a.png"><img src="data:image/png;base64,AAAA">`
	out := sanitizeHTML(raw)
	if strings.Contains(out, "cdn.example.com") {
		t.Fatalf("expected remote image references removed, got %q", out)
	}
	if !strings.Contains(out, `src="data:image/png;base64,AAAA"`) {
		t.Fatalf("expected data image preserved, got %q", out)
	}
}

func TestParseMultipartExtractsBodiesAndAttachments(t *testing.T) {
	raw := strings.Join([]string{
		"From: sender@example.net",
		"To: hello@example.test",
		"Subject: Multipart Test",
		"Message-ID: <parse-1@example.net>",
		"MIME-Version: 1.0",
		"Content-Type: multipart/mixed; boundary=abc123",
		"",
		"--abc123",
		"Content-Type: multipart/alternative; boundary=alt456",
		"",
		"--alt456",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"plain body",
		"--alt456",
		"Content-Type: text/html; charset=utf-8",
		"",
		"<p>html body</p>",
		"--alt456--",
		"--abc123",
		"Content-Type: text/plain",
		"Content-Disposition: attachment; filename=\"notes.txt\"",
		"Content-ID: <att-1>",
		"",
		"attachment body",
		"--abc123--",
		"",
	}, "\r\n")

	parsed, err := parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.MessageID != "parse-1@example.net" {
		t.Fatalf("unexpected message id: %q", parsed.MessageID)
	}
	if parsed.Text != "plain body" {
		t.Fatalf("unexpected text body: %q", parsed.Text)
	}
	if parsed.HTML != "<p>html body</p>" {
		t.Fatalf("unexpected html body: %q", parsed.HTML)
	}
	if len(parsed.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(parsed.Attachments))
	}
	att := parsed.Attachments[0]
	if att.Filename != "notes.txt" || att.ContentID != "att-1" || string(att.Data) != "attachment body" {
		t.Fatalf("unexpected attachment: %+v", att)
	}
}

func TestParseThreadHeaders(t *testing.T) {
	raw := strings.Join([]string{
		"From: sender@example.net",
		"To: hello@example.test",
		"Subject: Re: Thread",
		"Message-ID: <child@example.net>",
		"In-Reply-To: <parent@example.net>",
		"References: <root@example.net> <parent@example.net>",
		"",
		"body",
	}, "\r\n")
	parsed, err := parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.MessageID != "child@example.net" {
		t.Fatalf("message id = %q", parsed.MessageID)
	}
	if parsed.InReplyToMessageID != "parent@example.net" {
		t.Fatalf("in reply to = %q", parsed.InReplyToMessageID)
	}
	if got := strings.Join(parsed.ReferencesMessageIDs, ","); got != "root@example.net,parent@example.net" {
		t.Fatalf("references = %q", got)
	}
	if conv := inferConversationID(parsed.MessageID, parsed.InReplyToMessageID, parsed.ReferencesMessageIDs, parsed.Subject); conv != "root@example.net" {
		t.Fatalf("conversation id = %q", conv)
	}
}

func TestIngestLinksInboundThreadAcrossUserInboxes(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(database); err != nil {
		t.Fatal(err)
	}
	user := db.User{
		Email:               "thread@test.local",
		PasswordHash:        "x",
		IsActive:            true,
		MaxMessageSizeMB:    25,
		MaxAttachmentSizeMB: 25,
		MaxStorageBytes:     1 << 30,
	}
	if err := database.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	domain := db.Domain{UserID: user.ID, Name: "test.local", Status: db.DomainStatusVerified}
	if err := database.Create(&domain).Error; err != nil {
		t.Fatal(err)
	}
	first := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "one", Address: "one@test.local", IsActive: true}
	second := db.Inbox{UserID: user.ID, DomainID: domain.ID, LocalPart: "two", Address: "two@test.local", IsActive: true}
	if err := database.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	pipeline := Pipeline{
		DB:     database,
		Config: config.Config{},
		Store:  storage.NewLocal(t.TempDir()),
	}
	parentRaw := "From: a@example.net\r\nTo: one@test.local\r\nSubject: Thread\r\nMessage-ID: <parent@example.net>\r\n\r\nparent"
	parent, err := pipeline.Ingest(context.Background(), first, user, "a@example.net", first.Address, []byte(parentRaw))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	childRaw := "From: a@example.net\r\nTo: two@test.local\r\nSubject: Re: Thread\r\nMessage-ID: <child@example.net>\r\nIn-Reply-To: <parent@example.net>\r\nReferences: <parent@example.net>\r\n\r\nchild"
	child, err := pipeline.Ingest(context.Background(), second, user, "a@example.net", second.Address, []byte(childRaw))
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentEmailID == nil || *child.ParentEmailID != parent.ID {
		t.Fatalf("parent link = %v want %s", child.ParentEmailID, parent.ID)
	}
	if child.RootEmailID == nil || *child.RootEmailID != parent.ID {
		t.Fatalf("root link = %v want %s", child.RootEmailID, parent.ID)
	}
	if child.ConversationID != parent.ConversationID {
		t.Fatalf("conversation = %q want %q", child.ConversationID, parent.ConversationID)
	}
}

func TestAuthResultsExtractsRelevantHeaders(t *testing.T) {
	headers := map[string][]string{
		"Authentication-Results": {"mx; spf=pass"},
		"Received-SPF":           {"pass"},
		"X-Test":                 {"ignore"},
		"DKIM-Signature":         {"v=1; a=rsa-sha256"},
	}
	got := authResults(headers)
	if len(got) != 3 {
		b, _ := json.Marshal(got)
		t.Fatalf("expected 3 auth headers, got %s", b)
	}
	if _, ok := got["X-Test"]; ok {
		t.Fatalf("unexpected header included: %+v", got)
	}
}
