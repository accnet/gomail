package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"strings"
	"time"

	"gomail/internal/config"
	"gomail/internal/db"
	"gomail/internal/realtime"
	"gomail/internal/storage"

	"github.com/google/uuid"
	"github.com/microcosm-cc/bluemonday"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type Pipeline struct {
	DB        *gorm.DB
	Config    config.Config
	Store     *storage.Local
	Publisher *realtime.Publisher
}

func (p Pipeline) Ingest(ctx context.Context, inbox db.Inbox, user db.User, from string, rcpt string, raw []byte) (db.Email, error) {
	if int64(len(raw)) > int64(user.MaxMessageSizeMB)*1024*1024 {
		return db.Email{}, errors.New("message size limit exceeded")
	}
	if user.StorageUsedBytes+int64(len(raw)) > user.MaxStorageBytes {
		return db.Email{}, errors.New("storage quota exceeded")
	}
	parsed, err := parse(raw)
	if err != nil {
		return db.Email{}, err
	}
	email := db.Email{
		InboxID:           inbox.ID,
		MessageID:         parsed.MessageID,
		FromAddress:       firstNonEmpty(parsed.From, from),
		ToAddress:         firstNonEmpty(parsed.To, rcpt),
		Subject:           parsed.Subject,
		ReceivedAt:        time.Now(),
		RawSizeBytes:      int64(len(raw)),
		Snippet:           snippet(parsed.Text),
		TextBody:          parsed.Text,
		HTMLBody:          parsed.HTML,
		HTMLBodySanitized: bluemonday.UGCPolicy().Sanitize(parsed.HTML),
		HeadersJSON:       mustJSON(parsed.Headers),
		AuthResultsJSON:   mustJSON(authResults(parsed.Headers)),
	}
	var saved []string
	err = p.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&email).Error; err != nil {
			return err
		}
		rawPath, err := p.Store.SaveRaw(user.ID, email.ID, raw)
		if err != nil {
			return err
		}
		saved = append(saved, rawPath)
		if err := tx.Model(&email).Update("raw_storage_path", rawPath).Error; err != nil {
			return err
		}
		var total int64 = int64(len(raw))
		for _, part := range parsed.Attachments {
			if total+part.Size > int64(user.MaxAttachmentSizeMB)*1024*1024+int64(len(raw)) {
				return errors.New("attachment size limit exceeded")
			}
			id := uuid.New()
			path, sha, size, sniffed, err := p.Store.SaveAttachment(user.ID, email.ID, id, part.Filename, bytes.NewReader(part.Data))
			if err != nil {
				return err
			}
			saved = append(saved, path)
			scan := storage.Scan(part.Filename, part.ContentType, sniffed, p.Config.BlockFlagged)
			att := db.Attachment{
				ID:          id,
				EmailID:     email.ID,
				Filename:    part.Filename,
				ContentType: part.ContentType,
				SizeBytes:   size,
				StoragePath: path,
				SHA256:      sha,
				ScanStatus:  scan.Status,
				ScanResult:  scan.Result,
				IsBlocked:   scan.IsBlocked,
				ContentID:   part.ContentID,
				Disposition: part.Disposition,
			}
			if err := tx.Create(&att).Error; err != nil {
				return err
			}
			total += size
		}
		result := tx.Model(&db.User{}).
			Where("id = ? AND is_active = true AND storage_used_bytes + ? <= max_storage_bytes", user.ID, total).
			Update("storage_used_bytes", gorm.Expr("storage_used_bytes + ?", total))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("storage quota exceeded")
		}
		return nil
	})
	if err != nil {
		for _, path := range saved {
			_ = remove(path)
		}
		return db.Email{}, err
	}
	if p.Publisher != nil {
		_ = p.Publisher.Publish(ctx, realtime.Event{Type: "mail.received", UserID: user.ID, Data: map[string]any{"email_id": email.ID, "inbox_id": inbox.ID}})
	}
	return email, nil
}

func remove(path string) error {
	return os.Remove(path)
}

type parsedMail struct {
	MessageID   string
	From        string
	To          string
	Subject     string
	Text        string
	HTML        string
	Headers     map[string][]string
	Attachments []parsedAttachment
}

type parsedAttachment struct {
	Filename    string
	ContentType string
	ContentID   string
	Disposition string
	Size        int64
	Data        []byte
}

func parse(raw []byte) (parsedMail, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return parsedMail{}, err
	}
	out := parsedMail{
		MessageID: strings.TrimSpace(msg.Header.Get("Message-ID")),
		From:      msg.Header.Get("From"),
		To:        msg.Header.Get("To"),
		Subject:   msg.Header.Get("Subject"),
		Headers:   map[string][]string(msg.Header),
	}
	ct := msg.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ct)
	body, _ := io.ReadAll(msg.Body)
	if strings.HasPrefix(mediaType, "multipart/") {
		parseMultipart(&out, params["boundary"], body)
	} else if strings.Contains(mediaType, "html") {
		out.HTML = string(body)
	} else {
		out.Text = string(body)
	}
	return out, nil
}

func parseMultipart(out *parsedMail, boundary string, body []byte) {
	if boundary == "" {
		return
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		data, _ := io.ReadAll(part)
		ct := part.Header.Get("Content-Type")
		mediaType, params, _ := mime.ParseMediaType(ct)
		if strings.HasPrefix(mediaType, "multipart/") {
			parseMultipart(out, params["boundary"], data)
			continue
		}
		disposition := part.Header.Get("Content-Disposition")
		_, dispParams, _ := mime.ParseMediaType(disposition)
		filename := part.FileName()
		if filename == "" {
			filename = dispParams["filename"]
		}
		if filename != "" || strings.Contains(strings.ToLower(disposition), "attachment") {
			out.Attachments = append(out.Attachments, parsedAttachment{
				Filename:    firstNonEmpty(filename, "attachment"),
				ContentType: ct,
				ContentID:   strings.Trim(part.Header.Get("Content-ID"), "<>"),
				Disposition: disposition,
				Size:        int64(len(data)),
				Data:        data,
			})
			continue
		}
		if strings.Contains(mediaType, "html") && out.HTML == "" {
			out.HTML = string(data)
		} else if strings.HasPrefix(mediaType, "text/") && out.Text == "" {
			out.Text = string(data)
		}
	}
}

func mustJSON(v any) datatypes.JSON {
	b, _ := json.Marshal(v)
	return b
}

func authResults(headers map[string][]string) map[string][]string {
	out := map[string][]string{}
	for k, v := range headers {
		lower := strings.ToLower(k)
		if strings.Contains(lower, "authentication-results") || lower == "received-spf" || strings.Contains(lower, "dkim") || strings.Contains(lower, "dmarc") {
			out[k] = v
		}
	}
	return out
}

func snippet(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		return s[:160]
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
