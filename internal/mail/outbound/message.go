package outbound

import (
	"bytes"
	"fmt"
	"mime"
	"net/mail"
	"strings"
	"time"
)

type Message struct {
	From     string
	To       []string
	Cc       []string
	Bcc      []string
	Subject  string
	TextBody string
	HTMLBody string
	Headers  map[string]string
}

func (m Message) Recipients() []string {
	out := make([]string, 0, len(m.To)+len(m.Cc)+len(m.Bcc))
	out = append(out, m.To...)
	out = append(out, m.Cc...)
	out = append(out, m.Bcc...)
	return normalizeAddresses(out)
}

func BuildRFC5322(m Message) ([]byte, error) {
	from, err := parseSingleAddress(m.From)
	if err != nil {
		return nil, fmt.Errorf("from: %w", err)
	}
	to := normalizeAddresses(m.To)
	cc := normalizeAddresses(m.Cc)
	if len(to) == 0 && len(cc) == 0 && len(m.Bcc) == 0 {
		return nil, fmt.Errorf("at least one recipient required")
	}
	var buf bytes.Buffer
	writeHeader(&buf, "From", from)
	writeHeader(&buf, "To", strings.Join(to, ", "))
	if len(cc) > 0 {
		writeHeader(&buf, "Cc", strings.Join(cc, ", "))
	}
	writeHeader(&buf, "Subject", mime.QEncoding.Encode("utf-8", strings.TrimSpace(m.Subject)))
	writeHeader(&buf, "Date", time.Now().Format(time.RFC1123Z))
	writeHeader(&buf, "MIME-Version", "1.0")
	for key, value := range m.Headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" || strings.ContainsAny(key, "\r\n:") || strings.ContainsAny(value, "\r\n") {
			continue
		}
		if strings.EqualFold(key, "From") || strings.EqualFold(key, "To") || strings.EqualFold(key, "Cc") || strings.EqualFold(key, "Bcc") || strings.EqualFold(key, "Subject") || strings.EqualFold(key, "Date") || strings.EqualFold(key, "MIME-Version") {
			continue
		}
		writeHeader(&buf, key, value)
	}
	writeHeader(&buf, "Content-Type", `text/plain; charset="utf-8"`)
	writeHeader(&buf, "Content-Transfer-Encoding", "8bit")
	buf.WriteString("\r\n")
	buf.WriteString(normalizeBody(m.TextBody))
	return buf.Bytes(), nil
}

func parseSingleAddress(value string) (string, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return addr.Address, nil
}

func normalizeAddresses(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			addr, err := mail.ParseAddress(strings.TrimSpace(part))
			if err != nil || addr.Address == "" {
				continue
			}
			clean := strings.ToLower(addr.Address)
			if _, ok := seen[clean]; ok {
				continue
			}
			seen[clean] = struct{}{}
			out = append(out, addr.Address)
		}
	}
	return out
}

func writeHeader(buf *bytes.Buffer, key, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	buf.WriteString(key)
	buf.WriteString(": ")
	buf.WriteString(value)
	buf.WriteString("\r\n")
}

func normalizeBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	return strings.ReplaceAll(body, "\n", "\r\n")
}
