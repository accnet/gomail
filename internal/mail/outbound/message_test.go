package outbound

import (
	"strings"
	"testing"
)

func TestBuildRFC5322IncludesThreadHeadersAndOmitsBcc(t *testing.T) {
	raw, err := BuildRFC5322(Message{
		From:     "hello@example.test",
		To:       []string{"recipient@example.net"},
		Cc:       []string{"copy@example.net"},
		Bcc:      []string{"hidden@example.net"},
		Subject:  "Re: Question",
		TextBody: "Thanks",
		Headers: map[string]string{
			"Message-ID":  "<reply@example.test>",
			"In-Reply-To": "<original@example.net>",
			"References":  "<root@example.net> <original@example.net>",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := string(raw)
	for _, want := range []string{
		"Message-ID: <reply@example.test>\r\n",
		"In-Reply-To: <original@example.net>\r\n",
		"References: <root@example.net> <original@example.net>\r\n",
		"To: recipient@example.net\r\n",
		"Cc: copy@example.net\r\n",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("message missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(strings.ToLower(msg), "bcc:") || strings.Contains(msg, "hidden@example.net") {
		t.Fatalf("message leaked bcc:\n%s", msg)
	}
}
