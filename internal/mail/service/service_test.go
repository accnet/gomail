package service

import (
	"encoding/json"
	"strings"
	"testing"
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
	if parsed.MessageID != "<parse-1@example.net>" {
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
