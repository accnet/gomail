# 📋 KẾ HOẠCH PHÁT TRIỂN - API KEY + SMTP AUTH SERVER

> Ngày: 01/05/2026
> Tính năng: API Key management + GoMail làm SMTP outbound relay cho external apps

---

## I. MỤC TIÊU

1. **Tạo API Key** để external apps xác thực với SMTP server của GoMail
2. **Quản lý API Key** (tạo, list, revoke, xem log sử dụng)
3. **Phân quyền scopes** cho từng API key (gửi email, đọc inbox, quản lý domain...)
4. **Rate limit riêng** cho từng API key
5. **GoMail làm SMTP outbound relay** - external apps kết nối trực tiếp qua SMTP protocol (port 587)
6. **SMTP AUTH** sử dụng API Key làm credentials (username = api_key_id, password = full api_key)
7. **Domain verification** - chỉ cho phép gửi email từ domain user đã verified
8. **Tự động lưu sent log** khi gửi thành công qua SMTP server

---

## II. PHÂN TÍCH HIỆN TRẠNG

### Đã có:
- ✅ SMTP inbound server (`internal/smtp/server/`) - nhận email qua `go-smtp`
- ✅ Mail parser (`internal/mail/service/`) - parse MIME, lưu email + attachment
- ✅ Auth system (JWT access + refresh token)
- ✅ Rate limiter middleware (`internal/http/middleware/ratelimit.go`)
- ✅ Soft-delete pattern với GORM
- ✅ Model `User` có sẵn các trường quota (`MaxMessageSizeMB`, `MaxAttachmentSizeMB`, `MaxStorageBytes`...)
- ✅ Storage layer (`internal/storage/`)
- ✅ Real-time events qua Redis pub/sub
- ✅ RefreshToken pattern với hashed token (tái sử dụng pattern cho API Key)

### Cần bổ sung:
- ❌ Model `ApiKey` - quản lý API key cho external apps
- ❌ Model `ApiKeyUsageLog` - theo dõi usage của API key
- ❌ Model `SentEmailLog` - log email đã gửi qua SMTP AUTH server
- ❌ Middleware Auth cho API Key (`internal/http/middleware/apikey_auth.go`)
- ❌ API endpoints cho API Key CRUD
- ❌ SMTP AUTH handler trong SMTP server - GoMail làm outbound relay
- ❌ Hash API key secret (SHA-256) + one-time reveal flow
- ❌ Rate limit / daily quota per API key
- ❌ Domain verification khi external app gửi qua SMTP AUTH
- ❌ Delivery queue + retry policy cho outbound SMTP
- ❌ Bounce processing + suppression list
- ❌ Delivery events / webhook callbacks cho external apps
- ❌ Domain-level outbound config (DKIM selector, return-path, relay mode)
- ❌ Deliverability readiness checks (SPF, DKIM, DMARC, PTR/rDNS)
- ❌ Test connection / send test email cho WordPress plugins
- ❌ Observability dashboard cho outbound relay

---

## III. THIẾT KẾ CHI TIẾT

### 3.1. Database Models

#### 3.1.1. ApiKey (bảng mới) - Quản lý API Key cho external apps

```go
type ApiKey struct {
    ID              uuid.UUID      `gorm:"type:uuid;primaryKey" json:"id"`
    UserID          uuid.UUID      `gorm:"type:uuid;index;not null" json:"user_id"`
    Name            string         `gorm:"not null" json:"name"`              // Tên hiển thị (vd: "My App", "WordPress Plugin")
    KeyPrefix       string         `gorm:"not null;index" json:"key_prefix"`  // 8 ký tự đầu của key (dùng để hiển thị, lookup)
    KeyHash         string         `gorm:"not null;uniqueIndex" json:"-"`     // SHA-256 hash của full key
    Scopes          string         `gorm:"not null;default:'send_email'" json:"scopes"` // Comma-separated
    AllowedIPs      string         `json:"allowed_ips"`                       // IP whitelist ("" = any)
    RateLimitRPM    int            `gorm:"not null;default:60" json:"rate_limit_rpm"`
    MaxDailyEmails  int            `gorm:"not null;default:500" json:"max_daily_emails"`
    DailySentCount  int            `gorm:"not null;default:0" json:"daily_sent_count"`
    LastUsedAt      *time.Time     `json:"last_used_at"`
    ExpiresAt       *time.Time     `json:"expires_at"`                        // NULL = không hết hạn
    IsActive        bool           `gorm:"not null;default:true" json:"is_active"`
    CreatedAt       time.Time      `json:"created_at"`
    UpdatedAt       time.Time      `json:"updated_at"`
    DeletedAt       gorm.DeletedAt `gorm:"index" json:"-"`
}
```

**Scopes:**
| Scope | Mô tả |
|-------|-------|
| `send_email` | Gửi email qua SMTP AUTH |
| `read_inbox` | Đọc danh sách inbox, email |
| `manage_domains` | CRUD domains |
| `manage_inboxes` | CRUD inboxes |
| `read_sent` | Xem lịch sử email đã gửi |
| `full_access` | Tất cả các quyền trên |

**Quy trình tạo API Key:**
- User gọi `POST /api/api-keys` với `name` và `scopes`
- Server tạo: `key = "gomail_" + crypto/rand(32 bytes hex encoded)` → `gomail_a1b2c3d4e5f6...`
- Trả về full key **một lần duy nhất** trong response
- Hash key với SHA-256, lưu `KeyHash` + `KeyPrefix` (8 ký tự đầu)
- Không thể xem lại full key sau này (chỉ có thể revoke + tạo mới)

#### 3.1.2. ApiKeyUsageLog (bảng mới) - Theo dõi usage

```go
type ApiKeyUsageLog struct {
    ID          uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
    ApiKeyID    uuid.UUID  `gorm:"type:uuid;index;not null" json:"api_key_id"`
    UserID      uuid.UUID  `gorm:"type:uuid;index;not null" json:"user_id"`
    Endpoint    string     `json:"endpoint"`              // "/api/emails/send" hoặc "smtp://mx.gomail:587"
    Method      string     `json:"method"`                // "POST" hoặc "SMTP"
    StatusCode  int        `json:"status_code"`           // 200, 235 (SMTP auth ok), 401, 429...
    IPAddress   string     `json:"ip_address"`
    UserAgent   string     `json:"user_agent"`
    CreatedAt   time.Time  `json:"created_at"`
}
```

#### 3.1.3. SentEmailLog (bảng mới) - Log email đã gửi qua SMTP AUTH

```go
type SentEmailLog struct {
    ID            uuid.UUID  `gorm:"type:uuid;primaryKey" json:"id"`
    UserID        uuid.UUID  `gorm:"type:uuid;index;not null" json:"user_id"`
    ApiKeyID      *uuid.UUID `gorm:"type:uuid;index" json:"api_key_id"`      // NULL nếu gửi qua JWT
    Channel       string     `gorm:"not null;default:'smtp_auth'" json:"channel"` // "smtp_auth" hoặc "api"
    FromAddress   string     `json:"from_address"`
    ToAddress     string     `json:"to_address"`
    CcAddress     string     `json:"cc_address"`
    BccAddress    string     `json:"bcc_address"`
    Subject       string     `json:"subject"`
    BodyText      string     `json:"body_text,omitempty"`
    BodyHTML      string     `json:"body_html,omitempty"`
    Status        string     `gorm:"index;not null" json:"status"`            // "sent", "failed"
    ErrorMessage  string     `json:"error_message"`
    MessageID     string     `json:"message_id"`                              // SMTP message ID
    SentAt        *time.Time `json:"sent_at"`
    CreatedAt     time.Time  `json:"created_at"`
}
```

### 3.2. Config Bổ Sung

Thêm vào `internal/config/config.go`:

```go
// API Key
MaxApiKeysPerUser        int    // Default: 10
ApiKeyDefaultRPM         int    // Default: 60  (rate limit per minute per API key)
ApiKeyMaxDailyEmails     int    // Default: 500 (daily email limit per API key)

// SMTP AUTH Server (GoMail làm outbound SMTP relay)
SMTPAuthEnabled          bool   // Default: false
SMTPAuthHostname         string // Default: "smtp.gomail.local" - hostname cho SMTP submission
SMTPAuthPort             string // Default: "587" - STARTTLS submission port
SMTPAuthTLSPort          string // Default: "465" - implicit TLS port cho plugins hỗ trợ SMTPS
SMTPAuthAllowPlainPort   bool   // Default: false - chỉ bật nếu cần legacy port 25/2525 nội bộ
SMTPAuthPlainPort        string // Default: "2526" - optional legacy/plain submission port
SMTPAuthTLSEnabled       bool   // Default: true  (required STARTTLS trên port 587)
SMTPAuthTLSMode          string // Default: "starttls_or_tls" - "starttls_only", "tls_only", hoặc cả hai
SMTPAuthCertFile         string // TLS certificate path
SMTPAuthKeyFile          string // TLS key path

// SPF / DKIM cho outbound relay
SMTPRelayPublicIP        string // Public IP dùng để publish SPF cho hostname relay
SMTPRelayHostname        string // Default: "smtp.gomail.local" - HELO/EHLO hostname cho outbound delivery
DKIMEnabled              bool   // Default: true
DKIMSelector             string // Default: "gomail1"
DKIMPrivateKeyPath       string // Path private key PEM để ký DKIM
DKIMHeaderCanonicalization string // Default: "relaxed"
DKIMBodyCanonicalization   string // Default: "relaxed"
DKIMSignHeaders          string // Default: "from:to:subject:date:message-id:mime-version:content-type"
```

**SMTP submission profiles cho external apps / WordPress plugins:**

| Use case | Host | Port | Encryption | Ghi chú |
|----------|------|------|------------|---------|
| Khuyến nghị cho WordPress / WP Mail SMTP / FluentSMTP | `smtp.gomail.tld` | `587` | STARTTLS | Mặc định nên dùng |
| Plugin chỉ hỗ trợ SMTPS / SSL/TLS | `smtp.gomail.tld` | `465` | Implicit TLS | Bật thêm listener TLS riêng |
| Legacy nội bộ, không khuyến nghị public | `smtp.gomail.tld` | `2526` | None | Chỉ cho môi trường private/VPN |

**DNS requirements cho SMTP relay:**

| Record | Ví dụ | Mục đích |
|--------|-------|----------|
| `A/AAAA smtp.gomail.tld` | `203.0.113.10` | Host để WordPress/plugin kết nối |
| `MX example.com` | `10 mx.gomail.tld` | Inbound mail như hiện tại |
| `TXT example.com` | `v=spf1 a:smtp.gomail.tld ip4:203.0.113.10 -all` | Cho phép relay host gửi thay domain user |
| `TXT gomail1._domainkey.example.com` | `v=DKIM1; k=rsa; p=...` | Public key DKIM cho domain user |
| `TXT _dmarc.example.com` | `v=DMARC1; p=quarantine; rua=mailto:dmarc@example.com` | Khuyến nghị để deliverability tốt hơn |

**Ghi chú:**
- SPF là DNS config của domain gửi, không phải secret lưu trong app. App chỉ cần expose đúng relay hostname/public IP để user publish record.
- DKIM nên được ký ở outbound relay trước khi gửi tới recipient MX. Nếu hỗ trợ multi-tenant đúng nghĩa, nên lưu selector/public key theo từng domain verified thay vì chỉ dùng 1 cặp key global.

### 3.3. Middleware: ApiKeyAuth

File: `internal/http/middleware/apikey_auth.go`

```go
// ApiKeyAuth - xác thực qua header "X-Api-Key: gomail_xxx"
// Flow:
// 1. Đọc X-Api-Key header
// 2. Hash key = SHA-256(key)
// 3. Lookup ApiKey bằng KeyHash
// 4. Check IsActive, ExpiresAt, AllowedIPs
// 5. Check RateLimitRPM (Redis token bucket)
// 6. Set CurrentUser + ApiKey vào context
// 7. Log usage vào ApiKeyUsageLog (async)

func ApiKeyAuth(db *gorm.DB, redis *redis.Client) gin.HandlerFunc
```

### 3.4. SMTP AUTH Server - GoMail làm Outbound SMTP Relay

**Cách hoạt động:**
1. GoMail chạy thêm SMTP submission server riêng, tối thiểu trên port 587 và tùy chọn thêm port 465
2. External app (WordPress, Laravel, nodemailer...) kết nối tới SMTP server này như một SMTP relay
3. SMTP AUTH: `username = api_key_id (UUID)`, `password = api_key_secret (full key gomail_xxx...)`
4. From address **PHẢI** thuộc domain đã được user verify trong GoMail
5. Trước khi relay outbound, GoMail ký DKIM và yêu cầu domain gửi đã publish SPF trỏ về relay host/IP

**SMTP AUTH Session Flow:**
```
  username = api_key_id (UUID)
  password = gomail_xxx (full key)
    ├── EHLO myapp.com ───────────►│
    │◄── 250-STARTTLS ───────────┤
    │◄── 250 AUTH PLAIN LOGIN ───┤
  1. Lookup ApiKey by ID
  2. Verify SHA-256(password) == ApiKey.KeyHash
  3. Check IsActive
  4. Check ExpiresAt
  5. Check AllowedIPs
  6. Check scope "send_email"
  7. Check daily quota
  8. Update LastUsedAt
    │       │  │ 7. Check AllowedIP│
    │       │  │ 8. Check Scopes   │
    │       │  └───────────────────┤
  - Lookup domain trong DB
  - Must BELONG TO user
  - Must be VERIFIED
  - Must be ACTIVE
    │◄── 250 OK ──────────────────┤
    ├── RCPT TO: to@example.com ──►│
    │◄── 250 OK ──────────────────┤
  - Validate email address format
  - Check rate limit per API key
    │       ├──┤ 11. Parse MIME    │
    │       │  │ 12. Build envelope│
    │       │  │ 13. Send via MX   │
  1. Receive raw MIME bytes
  2. Parse MIME (headers + body)
  3. Build envelope
  4. DKIM sign message
  5. MX lookup recipient domain
  6. Connect to MX server
  7. Send email
    │◄── 221 Bye ─────────────────┤
```

**Implementation:** `internal/smtp/server/smtp_auth.go`

```go
// SmtpAuthBackend - implement go-smtp AuthBackend interface
type SmtpAuthBackend struct {
    DB          *gorm.DB
    Redis       *redis.Client
    Sender      *sender.Sender
    Logger      *slog.Logger
    RateLimiter *sender.RateLimiter
}

func (b *SmtpAuthBackend) Login(state *smtp.ConnectionState, username, password string) (smtp.Session, error)

// SmtpAuthSession - implement go-smtp Session interface
type SmtpAuthSession struct {
    ApiKey    *db.ApiKey
    User      *db.User
    From      string
    To        []string
    Data      []byte
    Sender    *sender.Sender
    Logger    *slog.Logger
}

func (s *SmtpAuthSession) Mail(from string, opts *smtp.MailOptions) error
func (s *SmtpAuthSession) Rcpt(to string, opts *smtp.RcptOptions) error
func (s *SmtpAuthSession) Data(r io.Reader) error
```

### 3.5. MIME Parser + MX Sender

Khi SMTP AUTH server nhận DATA từ external app, email cần được:
1. Parse MIME content
2. Build envelope với From đã được verify
3. Gửi qua MX lookup tới recipient domain

```go
// internal/mail/sender/sender.go
type Sender struct {
    DB          *gorm.DB
    Config      config.Config
    Redis       *redis.Client
    Logger      *slog.Logger
    RateLimiter *RateLimiter
}

// SendEmail - Gửi email qua MX lookup (dùng trong SMTP AUTH handler)
// fromAddress phải thuộc domain user đã verified
func (s *Sender) SendEmail(ctx context.Context, userID uuid.UUID, apiKeyID *uuid.UUID, fromAddress string, rawMIME []byte) (*SentEmailLog, error)
```

**Thư viện:** Sử dụng [gomail](https://github.com/go-gomail/gomail) để build MIME, thư viện `net/smtp` để gửi qua MX, và thêm DKIM signing trước khi chuyển tiếp outbound.

### 3.5.1. WordPress Plugin Config Presets

Các plugin WordPress phổ biến thường cần cấu hình rõ host/port/encryption. Plan nên cung cấp sẵn preset trong UI/docs:

| Plugin | SMTP Host | Port | Encryption | Username | Password |
|--------|-----------|------|------------|----------|----------|
| WP Mail SMTP | `smtp.gomail.tld` | `587` | `TLS / STARTTLS` | `api_key_id` | `full_api_key` |
| FluentSMTP | `smtp.gomail.tld` | `587` | `STARTTLS` | `api_key_id` | `full_api_key` |
| Post SMTP | `smtp.gomail.tld` | `465` hoặc `587` | `SSL/TLS` hoặc `STARTTLS` | `api_key_id` | `full_api_key` |

Nên thêm endpoint hoặc response metadata để UI hiển thị nhanh:

```json
{
      "smtp_settings": {
            "host": "smtp.gomail.tld",
            "ports": {
                  "starttls": 587,
                  "tls": 465
            },
            "username": "api_key_id",
            "password_hint": "full_api_key_shown_once",
            "recommended_security": "starttls"
      }
}
```

### 3.6. API Endpoints

#### 3.6.1. API Key Management (JWT Auth required)

| Method | Path | Mô tả |
|--------|------|-------|
| `POST` | `/api/api-keys` | Tạo API key mới (trả về full key **1 lần duy nhất**) |
| `GET` | `/api/api-keys` | List tất cả API keys của user (chỉ hiện KeyPrefix) |
| `GET` | `/api/api-keys/:id` | Xem chi tiết API key |
| `GET` | `/api/api-keys/:id/usage` | Xem usage log của API key |
| `PATCH` | `/api/api-keys/:id` | Cập nhật (name, scopes, allowed_ips, rate_limit_rpm, max_daily_emails, is_active) |
| `POST` | `/api/api-keys/:id/revoke` | Revoke API key (set IsActive = false) |
| `DELETE` | `/api/api-keys/:id` | Xóa API key (soft delete) |

#### 3.6.2. Sent Email History (JWT hoặc API Key)

| Method | Path | Auth | Mô tả |
|--------|------|------|-------|
| `GET` | `/api/emails/sent` | JWT / API Key (scope: `read_sent`) | Lịch sử email đã gửi qua SMTP AUTH |

#### 3.6.3. Admin Endpoints (JWT Admin)

| Method | Path | Mô tả |
|--------|------|-------|
| `GET` | `/api/admin/api-keys` | Admin xem tất cả API keys |
| `GET` | `/api/admin/emails/sent` | Admin xem tất cả email đã gửi |

### 3.7. Request/Response Schemas

#### POST /api/api-keys

```json
// Request
{
  "name": "My WordPress Site",
  "scopes": "send_email,read_inbox,read_sent",
  "allowed_ips": "203.0.113.1,203.0.113.2",
  "rate_limit_rpm": 120,
  "max_daily_emails": 1000,
  "expires_at": "2027-01-01T00:00:00Z"
}

// Response 201 - FULL KEY CHỈ HIỆN 1 LẦN
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "My WordPress Site",
  "key_prefix": "gomail_a1",
  "api_key": "gomail_a1b2c3d4e5f67890abcdef1234567890abcdef1234567890abcdef123456",
  "scopes": "send_email,read_inbox,read_sent",
  "allowed_ips": "203.0.113.1,203.0.113.2",
  "rate_limit_rpm": 120,
  "max_daily_emails": 1000,
  "expires_at": "2027-01-01T00:00:00Z",
  "created_at": "2026-05-01T13:00:00Z"
}
```

#### GET /api/emails/sent

```json
// Response 200
{
  "data": [
    {
      "id": "uuid",
      "api_key_name": "My WordPress Site",
      "channel": "smtp_auth",
      "from_address": "noreply@mydomain.com",
      "to_address": "recipient@example.com",
      "subject": "Hello from GoMail",
      "status": "sent",
      "message_id": "<abc123@mx.gomail>",
      "sent_at": "2026-05-01T13:00:01Z"
    }
  ],
  "pagination": {
    "page": 1,
    "page_size": 50,
    "total": 150
  }
}
```

### 3.8. Bảo Mật

| Vấn đề | Giải pháp |
|--------|-----------|
| API Key secret | SHA-256 hash lưu trong DB, chỉ trả về 1 lần khi tạo |
| Credentials truyền qua mạng | SMTP AUTH bắt buộc STARTTLS (587) hoặc implicit TLS (465) |
| Rate limit | Token bucket per API key (Redis) |
| IP whitelist | AllowedIPs trên ApiKey - kiểm tra trong SMTP AUTH backend |
| Domain verification | Chỉ cho phép MAIL FROM từ domain user đã verified trong GoMail |
| SPF alignment | Domain user phải publish SPF chứa relay host/IP trước khi production send |
| DKIM signing | Outbound relay ký DKIM trước khi gửi tới recipient MX |
| Spam prevention | Daily quota per API key, admin có thể disable |
| Input validation | Validate email addresses, sanitize content |
| Audit trail | AuditLog cho mọi hành động tạo/sửa/xóa API key |

### 3.9. Quy Trình Gửi Email Qua SMTP AUTH

```
External App (WordPress, nodemailer...)
        │
        │  SMTP connect to smtp.gomail:587 hoặc 465
        ▼
  TLS Handshake (STARTTLS hoặc implicit TLS)
        │
        ▼
  SMTP AUTH PLAIN
  username = api_key_id (UUID)
  password = gomail_xxx (full key)
        │
        ▼
  Auth Backend:
  1. Lookup ApiKey by ID
  2. Verify SHA-256(password) == ApiKey.KeyHash
  3. Check IsActive
  4. Check ExpiresAt
  5. Check AllowedIPs
  6. Check scope "send_email"
  7. Check daily quota
  8. Update LastUsedAt
        │
        ▼
  MAIL FROM: user@verified-domain.com
  - Lookup domain trong DB
  - Must BELONG TO user
  - Must be VERIFIED
  - Must be ACTIVE
        │
        ▼
  RCPT TO: recipient@example.com
  - Validate email address format
  - Check rate limit per API key
        │
        ▼
  DATA (MIME content)
  1. Receive raw MIME bytes
  2. Parse MIME (headers + body)
  3. Build envelope
  4. DKIM sign message
  5. MX lookup recipient domain
  6. Connect to MX server
  7. Send email
        │
        ▼
  Lưu SentEmailLog (status = "sent")
  Cập nhật daily counter (Redis)
        │
        ▼
  250 OK <message-id>
```

---

## IV. CÁC BƯỚC TRIỂN KHAI

### Phase 1: Foundation (Database + Models + Config)

| Step | Task | File(s) | Effort |
|------|------|---------|--------|
| 1.1 | Thêm constants (`ApiKeyScope*`, `SentEmailStatus*`) | `internal/db/models.go` | 15m |
| 1.2 | Thêm models: `ApiKey`, `ApiKeyUsageLog`, `SentEmailLog` | `internal/db/models.go` | 30m |
| 1.3 | AutoMigrate 3 bảng mới | `internal/db/db.go` | 10m |
| 1.4 | Thêm config fields (API key limits, SMTP AUTH, SPF/DKIM, TLS ports 587/465) | `internal/config/config.go` | 30m |
| 1.5 | Implement API Key generator + SHA-256 hash helper | `internal/http/handlers/apikey.go` | 30m |

### Phase 2: API Key Management Handlers

| Step | Task | File(s) | Effort |
|------|------|---------|--------|
| 2.1 | Implement ApiKeyAuth middleware (X-Api-Key header) | `internal/http/middleware/apikey_auth.go` | 1h |
| 2.2 | API Key CRUD handlers (create, list, get, update, revoke, delete) | `internal/http/handlers/apikey.go` | 2h |
| 2.3 | API Key usage log handler | `internal/http/handlers/apikey.go` | 30m |
| 2.4 | Sent email history handler | `internal/http/handlers/send_email.go` | 30m |
| 2.5 | Wire ApiKeyAuth middleware + routes vào `App.Router()` | `internal/http/handlers/app.go` | 30m |

### Phase 3: SMTP Sender Service

| Step | Task | File(s) | Effort |
|------|------|---------|--------|
| 3.1 | Implement `SendEmail()` - build MIME, DKIM sign, gửi qua MX lookup | `internal/mail/sender/sender.go` | 2h |
| 3.2 | Implement DKIM signer + domain config lookup | `internal/mail/sender/dkim.go` | 1h |
| 3.3 | Implement RateLimiter (Redis token bucket: per-api-key daily) | `internal/mail/sender/ratelimit.go` | 1h |
| 3.4 | Unit test cho Sender + DKIM signer + RateLimiter | `internal/mail/sender/sender_test.go` | 1.5h |

### Phase 4: SMTP AUTH Server

| Step | Task | File(s) | Effort |
|------|------|---------|--------|
| 4.1 | Implement SmtpAuthBackend (API Key auth + domain verify) | `internal/smtp/server/smtp_auth.go` | 2h |
| 4.2 | Implement SmtpAuthSession (Mail, Rcpt, Data callbacks) | `internal/smtp/server/smtp_auth.go` | 1.5h |
| 4.3 | Implement SmtpAuthServer startup + dual TLS listeners (587 STARTTLS, 465 implicit TLS) | `internal/smtp/server/smtp_auth.go` | 1.5h |
| 4.4 | Wire SMTP AUTH server vào `cmd/smtp/main.go` | `cmd/smtp/main.go` | 45m |
| 4.5 | Document WordPress SMTP presets trong API/UI docs | `README.md`, `plan-smtp-relay.md` | 30m |

### Phase 5: Admin Endpoints

| Step | Task | File(s) | Effort |
|------|------|---------|--------|
| 5.1 | Admin: view all API keys, all sent emails | `internal/http/handlers/apikey.go`, `send_email.go` | 1h |
| 5.2 | Admin user quotas (thêm `max_api_keys`, `max_daily_emails` vào User model + admin handler) | `internal/db/models.go`, `internal/http/handlers/app.go` | 1h |
| 5.3 | Audit logging cho API key actions | `internal/http/handlers/apikey.go` | 30m |

### Phase 6: Testing & Documentation

| Step | Task | File(s) | Effort |
|------|------|---------|--------|
| 6.1 | Integration tests cho API Key CRUD | `internal/http/handlers/apikey_integration_test.go` | 1.5h |
| 6.2 | Integration tests cho ApiKeyAuth middleware | `internal/http/middleware/apikey_auth_test.go` | 1h |
| 6.3 | Integration tests cho SMTP AUTH server | `internal/smtp/server/smtp_auth_integration_test.go` | 1.5h |
| 6.4 | Update `report.md` và các file plan | `plan-smtp-relay.md`, `report.md` | 20m |

---

## V. FILE TREE MỚI

```
gomail/
├── cmd/
│   └── smtp/
│       └── main.go                           # + Khởi động SMTP AUTH server (port 587)
├── internal/
│   ├── config/
│   │   └── config.go                         # + ApiKey + SMTP AUTH config fields
│   ├── db/
│   │   ├── db.go                             # + AutoMigrate ApiKey, ApiKeyUsageLog, SentEmailLog
│   │   └── models.go                         # + ApiKey, ApiKeyUsageLog, SentEmailLog models + constants
│   ├── http/
│   │   ├── handlers/
│   │   │   ├── app.go                        # + Wire Sender, ApiKeyAuth, routes cho API keys + sent emails
│   │   │   ├── apikey.go                     # NEW: API Key CRUD handlers + usage log
│   │   │   ├── send_email.go                 # NEW: Sent email history handler
│   │   │   ├── apikey_integration_test.go    # NEW
│   │   │   └── send_email_integration_test.go # NEW
│   │   └── middleware/
│   │       ├── apikey_auth.go                # NEW: API Key authentication middleware
│   │       └── apikey_auth_test.go           # NEW
│   ├── mail/
│   │   └── sender/
│   │       ├── sender.go                     # NEW: Email sender (MX lookup + gomail)
│   │       ├── dkim.go                       # NEW: DKIM signing helper
│   │       ├── ratelimit.go                  # NEW: Redis daily rate limiter (per API key)
│   │       └── sender_test.go                # NEW
│   └── smtp/
│       └── server/
│           ├── server.go                     # Existing: Inbound SMTP server (port 25)
│           ├── smtp_auth.go                  # NEW: SMTP AUTH server (port 587 - outbound relay)
│           └── smtp_auth_integration_test.go # NEW
├── plan-smtp-relay.md                        # File này
├── go.mod                                    # + gopkg.in/gomail.v2
└── go.sum
```

---

## VI. DEPENDENCIES MỚI

```bash
go get gopkg.in/gomail.v2         # SMTP client library (MIME, HTML, attachment)
```

---

## VII. TỔNG EFFORT ƯỚC TÍNH

**Lưu ý:** tổng effort dưới đây đang tính cho core relay scope hiện tại. Các tính năng production-ready và advanced ở phần roadmap phía dưới sẽ làm effort tăng thêm.

| Phase | Effort |
|-------|--------|
| Phase 1: Foundation | 1h 55m |
| Phase 2: API Key Management | 4h 30m |
| Phase 3: SMTP Sender Service | 4h 30m |
| Phase 4: SMTP AUTH Server | 6h |
| Phase 5: Admin Endpoints | 2h 30m |
| Phase 6: Testing & Docs | 4h 50m |
| **TOTAL** | **~24h 15m** |

---

## VIII. PHÂN TẦNG PHẠM VI

### 8.1. MVP

Mục tiêu: external app có thể authenticate và gửi mail qua relay cơ bản, phù hợp để build bản đầu tiên.

| Nhóm | Bao gồm |
|------|---------|
| API Key | Create/list/revoke API key, scope `send_email`, IP allowlist cơ bản |
| SMTP Submission | Port `587` STARTTLS, port `465` implicit TLS, WordPress presets |
| Domain Controls | Chỉ cho gửi từ verified domain, SPF/DKIM checklist cơ bản |
| Logging | `SentEmailLog`, `ApiKeyUsageLog`, audit log cơ bản |
| Quota | Rate limit RPM + daily quota theo API key |

**Phù hợp khi:** cần release nhanh cho use case WordPress, Laravel, nodemailer với mức độ vận hành cơ bản.

### 8.2. Production-Ready

Mục tiêu: relay vận hành ổn định ngoài môi trường thật, chịu được lỗi MX và có tín hiệu delivery rõ ràng.

| Nhóm | Bao gồm |
|------|---------|
| Queueing | Hàng đợi outbound, retry backoff cho lỗi `4xx`, dead-letter handling |
| Delivery Tracking | Status `queued`, `sent`, `deferred`, `bounced`, `failed`, `delivered` |
| Bounce Handling | Parse DSN/bounce, suppression list, block hard-bounced recipients |
| Deliverability | DKIM per-domain, SPF/DMARC/PTR readiness checks, return-path strategy |
| Webhooks | Callback khi sent/deferred/bounced/failed |
| Admin Ops | Dashboard error rate, queue depth, top failing MX, top API keys |

**Phù hợp khi:** có user thật, cần giảm support cost và bảo vệ reputation gửi mail.

### 8.3. Advanced

Mục tiêu: biến relay thành outbound email platform hoàn chỉnh.

| Nhóm | Bao gồm |
|------|---------|
| Multi-tenant Controls | Domain-level outbound profile, per-domain key rotation, relay mode riêng |
| Product API | REST send API, templates, batch send controls |
| Security | Suspicious login detection, geo/IP policy, forced TLS policy per key |
| Analytics | Delivery dashboard, engagement events, per-domain trend reports |
| Integrations | Test connection wizard, onboarding checker, webhook secret rotation |

**Phù hợp khi:** muốn đi từ relay service sang transactional email product.

---

## IX. API SUMMARY

| Method | Endpoint | Auth | Mô tả |
|--------|----------|------|-------|
| `POST` | `/api/api-keys` | JWT User | Tạo API key mới (trả về full key **1 lần**) |
| `GET` | `/api/api-keys` | JWT User | List API keys (chỉ hiện key_prefix) |
| `GET` | `/api/api-keys/:id` | JWT User | Get API key detail |
| `PATCH` | `/api/api-keys/:id` | JWT User | Update API key |
| `POST` | `/api/api-keys/:id/revoke` | JWT User | Revoke API key |
| `DELETE` | `/api/api-keys/:id` | JWT User | Delete API key |
| `GET` | `/api/api-keys/:id/usage` | JWT User | Get API key usage log |
| `GET` | `/api/emails/sent` | JWT / ApiKey (`read_sent`) | List sent emails |
| `GET` | `/api/admin/api-keys` | JWT Admin | Admin view all API keys |
| `GET` | `/api/admin/emails/sent` | JWT Admin | Admin view all sent emails |
| _(SMTP)_ | `smtp://smtp.gomail:587` | SMTP AUTH (ApiKey) | STARTTLS relay cho WordPress/external apps |
| _(SMTPS)_ | `smtps://smtp.gomail:465` | SMTP AUTH (ApiKey) | Implicit TLS relay cho plugins chỉ hỗ trợ SSL/TLS |

---

## X. BACKLOG TÍNH NĂNG ĐỀ XUẤT

### 10.1. Production-Ready Backlog

| Feature | Mô tả | Giá trị | Effort rough |
|---------|------|---------|--------------|
| Delivery queue | Đưa outbound mail vào queue thay vì gửi hoàn toàn đồng bộ trong SMTP session | Giảm timeout, tăng độ ổn định khi recipient MX chậm | 4h - 6h |
| Retry policy | Retry lỗi `4xx` với exponential backoff, cap retry, dead-letter queue | Tránh mất mail do lỗi tạm thời | 3h - 5h |
| Bounce processing | Nhận và parse DSN/bounce, đánh dấu hard/soft bounce | Bảo vệ sender reputation | 4h - 6h |
| Suppression list | Chặn gửi tới recipient đã hard bounce/complaint/unsubscribe | Giảm spam risk và block rate | 2h - 4h |
| Delivery webhooks | Gửi callback tới external app khi `sent`, `deferred`, `bounced`, `failed` | Giúp app tích hợp theo dõi delivery | 3h - 5h |
| Deliverability checker | Kiểm tra SPF, DKIM, DMARC, PTR/rDNS, TLS readiness | Giảm lỗi onboarding | 3h - 4h |
| Outbound metrics | Dashboard cho queue depth, MX failures, retry count, top domains | Hỗ trợ vận hành | 3h - 5h |

### 10.2. Advanced Backlog

| Feature | Mô tả | Giá trị | Effort rough |
|---------|------|---------|--------------|
| Domain outbound profile | DKIM selector/key, return-path, HELO hostname, relay mode theo domain | Chuẩn multi-tenant | 4h - 6h |
| REST send API | `POST /api/emails/send` song song với SMTP AUTH | Dễ tích hợp cho app không muốn dùng SMTP | 3h - 5h |
| Email templates | Template + merge variables + preview/test send | Hỗ trợ productization | 4h - 6h |
| Plugin test connection | API/UI flow để test SMTP settings và gửi test email | Giảm support cho WordPress users | 2h - 3h |
| Suspicious auth detection | Detect login bất thường theo IP/geo/frequency | Tăng bảo mật | 3h - 5h |
| Batch controls | Max recipients per message, per-key batch guard, abuse heuristics | Giảm lạm dụng bulk mail | 2h - 4h |

### 10.3. Gợi ý thứ tự ưu tiên sau MVP

1. Delivery queue + retry policy
2. Bounce processing + suppression list
3. Delivery webhooks
4. Deliverability checker
5. Domain outbound profile
6. REST send API

---

## XI. CÂU HỎI MỞ

1. **DKIM sẽ ký bằng key global hay per-domain?** (khuyến nghị: per-domain cho đúng alignment và deliverability)
2. **SPF onboarding có cần wizard/checker trong UI không?** (nên có để user WordPress tự cấu hình đúng DNS)
3. **Có cần queue email gửi không đồng bộ không?** (Redis/SQS/NSQ...) - hiện tại plan là gửi đồng bộ trong SMTP session
4. **Có cần webhook callback khi email gửi thành công/thất bại không?** (delivery status, bounce handling)
5. **MX lookup cho recipient: gửi trực tiếp qua MX hay qua một SMTP relay cố định?** (khuyến nghị: MX lookup gửi trực tiếp để tận dụng deliverability tốt nhất)
6. **Có cần expose thêm REST API endpoint để gửi email không?** (vd: `POST /api/emails/send` với API Key auth, song song với SMTP AUTH)

---

## XII. IMPLEMENTATION CHECKLIST THEO FILE

### 12.1. Core Config / Bootstrap

| File | Checklist |
|------|-----------|
| `internal/config/config.go` | Thêm config cho API key limits, SMTP submission hostname, port `587`, port `465`, TLS mode, cert/key paths, relay hostname/public IP, DKIM selector/private key path |
| `cmd/smtp/main.go` | Khởi tạo thêm SMTP submission server song song với inbound server hoặc tách bootstrap rõ ràng cho inbound/submission |
| `README.md` | Thêm hướng dẫn SMTP relay host/port, WordPress plugin presets, SPF/DKIM/DMARC setup |

### 12.2. Database / Models

| File | Checklist |
|------|-----------|
| `internal/db/models.go` | Thêm constants cho `ApiKeyScope*`, `SentEmailStatus*`, `DeliveryStatus*` nếu mở rộng production-ready |
| `internal/db/models.go` | Thêm model `ApiKey` |
| `internal/db/models.go` | Thêm model `ApiKeyUsageLog` |
| `internal/db/models.go` | Thêm model `SentEmailLog` |
| `internal/db/models.go` | Nếu đi production-ready: thêm model `SuppressionEntry`, `OutboundEvent`, `DeliveryAttempt` |
| `internal/db/db.go` | AutoMigrate các bảng mới và thêm index cần cho lookup theo `key_hash`, `user_id`, `status`, `sent_at` |

### 12.3. HTTP Layer

| File | Checklist |
|------|-----------|
| `internal/http/middleware/apikey_auth.go` | Middleware đọc `X-Api-Key`, hash SHA-256, lookup key, check active/expiry/IP allowlist, set context principal |
| `internal/http/middleware/apikey_auth_test.go` | Test missing key, invalid key, revoked key, expired key, allowed IP, scope checks |
| `internal/http/handlers/apikey.go` | Create/list/get/update/revoke/delete API key |
| `internal/http/handlers/apikey.go` | Response trả `smtp_settings` cho WordPress/plugin config |
| `internal/http/handlers/apikey.go` | Endpoint usage log + validate scopes/rate limit fields |
| `internal/http/handlers/send_email.go` | List sent emails theo JWT hoặc API key scope `read_sent` |
| `internal/http/handlers/send_email.go` | Nếu cần: endpoint `send test email` cho plugin onboarding |
| `internal/http/handlers/app.go` | Wire routes cho API key CRUD, sent email history, optional test connection/test send route |
| `internal/http/handlers/apikey_integration_test.go` | Integration tests cho CRUD + one-time reveal flow |
| `internal/http/handlers/send_email_integration_test.go` | Integration tests cho history filtering, auth mode, pagination |

### 12.4. SMTP Submission / Relay

| File | Checklist |
|------|-----------|
| `internal/smtp/server/smtp_auth.go` | Implement submission backend tương thích `go-smtp` auth session API |
| `internal/smtp/server/smtp_auth.go` | Support AUTH PLAIN/LOGIN, STARTTLS trên `587`, implicit TLS trên `465` |
| `internal/smtp/server/smtp_auth.go` | Validate `MAIL FROM` thuộc verified domain của user |
| `internal/smtp/server/smtp_auth.go` | Enforce quota, size limit, recipient validation, logging usage |
| `internal/smtp/server/smtp_auth_integration_test.go` | Test auth success/failure, STARTTLS, implicit TLS, invalid domain, quota exceeded |

### 12.5. Outbound Sender

| File | Checklist |
|------|-----------|
| `internal/mail/sender/sender.go` | Parse raw MIME, build envelope, attach metadata user/api key, send outbound qua MX hoặc relay strategy |
| `internal/mail/sender/sender.go` | Tạo `SentEmailLog`, update quota counters, persist delivery result |
| `internal/mail/sender/dkim.go` | Load private key, sign message theo selector/domain, expose helper cho sender |
| `internal/mail/sender/ratelimit.go` | Rate limit / daily quota per API key qua Redis |
| `internal/mail/sender/sender_test.go` | Unit tests cho MIME handling, DKIM signing, logging, quota failure |

### 12.6. Production-Ready Extensions

| File | Checklist |
|------|-----------|
| `internal/mail/sender/queue.go` | Queue outbound jobs, retry backoff, dead-letter handling |
| `internal/mail/sender/queue_test.go` | Test retry/dead-letter behavior |
| `internal/mail/sender/bounce.go` | Parse bounce/DSN và map sang recipient suppression |
| `internal/http/handlers/webhooks.go` | Webhook delivery events cho external apps |
| `internal/http/handlers/admin_outbound.go` | Dashboard metrics: queue depth, failure rate, top MX errors |

### 12.7. Thứ Tự Coding Đề Xuất

1. `internal/db/models.go` + `internal/db/db.go`
2. `internal/config/config.go`
3. `internal/http/handlers/apikey.go` + `internal/http/middleware/apikey_auth.go`
4. `internal/mail/sender/dkim.go` + `internal/mail/sender/sender.go`
5. `internal/smtp/server/smtp_auth.go`
6. `cmd/smtp/main.go`
7. Integration tests
8. `README.md`

### 12.8. Definition Of Done Cho MVP

- User tạo được API key và chỉ thấy full key đúng một lần.
- WordPress plugin dùng được với host `smtp.gomail.tld`, port `587` STARTTLS hoặc `465` TLS.
- SMTP AUTH chỉ chấp nhận gửi từ verified domain của chính user.
- Mail outbound được DKIM sign trước khi relay.
- Có log gửi mail và usage log cho API key.
- Có test tối thiểu cho API key CRUD, middleware auth, SMTP auth flow.

---

*Plan created for review. Switch to ACT MODE to implement.*