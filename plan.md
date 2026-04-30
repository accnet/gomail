# GoMail Implementation Plan

## 1. Product Goal

GoMail là hệ thống nhận email inbound cho nhiều user và nhiều domain, cho phép:

- User đăng ký tài khoản, thêm domain riêng, tạo inbox trên domain đó.
- Hệ thống nhận email qua SMTP, parse nội dung, lưu attachment, và hiển thị email trên web UI.
- Admin quản lý user quota và trạng thái hoạt động của tài khoản.

Mục tiêu bản đầu tiên là làm một hệ thống chạy ổn định trong môi trường dev/self-hosted, chưa ưu tiên anti-spam nâng cao hay outbound mail.

## 2. Scope V1

Trong phạm vi V1:

- Nhận email inbound qua SMTP.
- Quản lý user, domain, inbox.
- Lưu email, body text/html, attachment metadata.
- Tải attachment từ local storage.
- Realtime notify mail mới qua Redis Pub/Sub + SSE.
- UI cơ bản để đọc mail và quản lý domain/inbox.

Ngoài phạm vi V1:

- Outbound SMTP / gửi mail.
- DKIM signing.
- Spam filter nâng cao.
- Full-text search nâng cao.
- HA / horizontal scaling hoàn chỉnh.
- Object storage như S3.

## 3. Assumptions

- App backend viết bằng Go.
- API framework dùng Gin.
- ORM dùng GORM.
- SMTP server dùng `go-smtp`.
- MIME parser dùng `enmime`.
- Database dùng Postgres.
- Redis dùng cho Pub/Sub realtime.
- Attachment lưu local disk trong V1.
- Domain được coi là "verified" khi MX record trỏ đúng về mail host của hệ thống, không chỉ đơn giản là domain có MX record.
- Domain SaaS được cấu hình qua env, tối thiểu gồm `SAAS_DOMAIN`, `APP_BASE_URL`, `API_BASE_URL`, `SMTP_HOSTNAME`, `MX_TARGET`.
- Super admin mặc định được seed khi deploy lần đầu từ env `DEFAULT_ADMIN_*`; không hardcode credential trong source.

## 3.1 Deployment Configuration

Env tối thiểu cho deploy:

- `SAAS_DOMAIN`: domain chính của SaaS, ví dụ `example.com`.
- `APP_BASE_URL`: URL web app SPA, ví dụ `https://mail.example.com`.
- `API_BASE_URL`: URL API public, ví dụ `https://mail.example.com/api`.
- `SMTP_HOSTNAME`: hostname SMTP public, ví dụ `mx.example.com`.
- `MX_TARGET`: giá trị MX user cần trỏ tới; thường bằng `SMTP_HOSTNAME`.
- `SMTP_PORT`: mặc định `25` khi deploy VPS.
- `DEFAULT_ADMIN_EMAIL`: email super admin seed lần đầu.
- `DEFAULT_ADMIN_PASSWORD`: mật khẩu super admin seed lần đầu, bắt buộc đổi khỏi giá trị mặc định trước deploy.
- `DEFAULT_ADMIN_NAME`: tên hiển thị super admin.
- `DEFAULT_ADMIN_MAX_DOMAINS`, `DEFAULT_ADMIN_MAX_INBOXES`, `DEFAULT_ADMIN_MAX_MESSAGE_SIZE_MB`, `DEFAULT_ADMIN_MAX_ATTACHMENT_SIZE_MB`, `DEFAULT_ADMIN_MAX_STORAGE_GB`: quota mặc định của super admin.

Yêu cầu:

- Repo có `.env.example` làm mẫu.
- File `.env` thật không được commit.
- App validate env khi start và fail fast nếu thiếu domain SaaS, JWT secret, DB, Redis, hoặc default admin credential chưa đổi.
- Seed super admin phải idempotent: nếu `DEFAULT_ADMIN_EMAIL` đã tồn tại thì không reset password/quota ngoài ý muốn.

### 3.2 SaaS Domain and DNS

Ví dụ deploy với domain SaaS `example.com`:

- Web/API: `mail.example.com` trỏ về reverse proxy/API server.
- SMTP inbound: `mx.example.com` trỏ A/AAAA về VPS chạy SMTP port 25.
- User domain cần cấu hình MX trỏ về `MX_TARGET`, ví dụ `MX 10 mx.example.com`.

App dùng cấu hình domain như sau:

- `APP_BASE_URL` dùng cho link trong UI/email/system messages.
- `API_BASE_URL` dùng cho SPA gọi backend nếu API public tách path/subdomain.
- `SMTP_HOSTNAME` dùng làm hostname SMTP banner/identity.
- `MX_TARGET` dùng để verify domain user và render hướng dẫn cấu hình DNS trong UI.
- `SAAS_DOMAIN` là domain gốc của dịch vụ, dùng cho cấu hình mặc định, validation host public, và các URL/hostname sinh tự động nếu cần.

## 4. High-Level Architecture

Các thành phần chính:

1. `cmd/api`: HTTP API + SSE server.
2. `cmd/smtp`: SMTP listener nhận mail inbound.
3. `internal/db`: models, migrations, repositories.
4. `internal/smtp`: session, validation, parser, message pipeline.
5. `internal/storage`: local file storage cho attachment.
6. `internal/auth`: password hashing, JWT, middleware.
7. `internal/realtime`: publish Redis event, SSE fanout.
8. `web` hoặc `frontend`: UI 3 cột.

Luồng tổng quát:

1. User thêm domain.
2. Hệ thống sinh hướng dẫn cấu hình DNS MX.
3. User verify domain.
4. User tạo inbox trên domain đã verify.
5. SMTP server nhận mail đến `localpart@domain`.
6. Hệ thống validate domain, inbox, quota, attachment size.
7. Hệ thống parse MIME, lưu DB + file.
8. Hệ thống publish event để UI cập nhật realtime.

## 5. Suggested Directory Structure

```text
cmd/
  api/
  smtp/
internal/
  auth/
  config/
  db/
    migrations/
    models/
    repositories/
  dns/
  http/
    handlers/
    middleware/
  mail/
    parser/
    service/
  realtime/
  smtp/
    server/
    session/
    validator/
  storage/
pkg/
  logger/
  response/
deploy/
  docker/
web/
```

## 6. Data Model

### 6.1 users

Mục đích: chủ sở hữu domain và inbox.

Field đề xuất:

- `id` UUID / bigint
- `email` unique
- `password_hash`
- `is_admin` boolean
- `is_active` boolean
- `max_domains` integer
- `max_inboxes` integer
- `max_attachment_size_mb` integer
- `max_message_size_mb` integer
- `max_storage_bytes` integer/bigint
- `storage_used_bytes` integer/bigint
- `created_at`
- `updated_at`

### 6.2 domains

Mục đích: domain nhận mail thuộc về user.

Field đề xuất:

- `id`
- `user_id` FK -> users
- `name` unique toàn hệ thống
- `status` enum: `pending`, `verified`, `failed`, `disabled`
- `warning_status` nullable text
- `verification_method` text
- `mx_target` text
- `last_verified_at` nullable
- `verification_error` nullable text
- `created_at`
- `updated_at`
- `deleted_at` nullable

Ràng buộc:

- Một domain chỉ thuộc một user.
- Không cho hai user cùng claim một domain.

### 6.3 inboxes

Mục đích: địa chỉ email cụ thể như `hello@example.com`.

Field đề xuất:

- `id`
- `user_id` FK -> users
- `domain_id` FK -> domains
- `local_part`
- `address` unique toàn hệ thống
- `is_active` boolean
- `created_at`
- `updated_at`
- `deleted_at` nullable

Ràng buộc:

- Unique `(domain_id, local_part)`.
- Chỉ tạo inbox khi domain ở trạng thái `verified`.

### 6.4 emails

Mục đích: bản ghi mail đã nhận.

Field đề xuất:

- `id`
- `inbox_id` FK -> inboxes
- `message_id` nullable, indexed
- `from_address`
- `to_address`
- `subject`
- `received_at`
- `raw_size_bytes`
- `raw_storage_path` nullable
- `text_body` nullable
- `html_body` nullable
- `html_body_sanitized` nullable
- `headers_json` JSONB
- `auth_results_json` JSONB
- `is_read` boolean
- `created_at`
- `deleted_at` nullable

Ghi chú:

- `received_at` là thời điểm SMTP server accept mail.
- Có thể thêm `snippet` để tối ưu list view.
- `auth_results_json` lưu kết quả SPF, DKIM, DMARC parse để phục vụ UI và audit, chưa enforce ở V1.

### 6.5 attachments

Mục đích: metadata của file đính kèm.

Field đề xuất:

- `id`
- `email_id` FK -> emails
- `filename`
- `content_type`
- `size_bytes`
- `storage_path`
- `sha256` nullable
- `scan_status` enum: `pending`, `clean`, `flagged`, `infected`, `scan_failed`
- `scan_result` nullable text
- `is_blocked` boolean
- `admin_override_download` boolean
- `admin_override_by` nullable
- `admin_override_at` nullable
- `content_id` nullable
- `disposition` nullable
- `created_at`

Ghi chú:

- `scan_status` phục vụ content scanning tối thiểu và cho phép mở rộng sang ClamAV sau.

### 6.6 domain_events

Mục đích: audit nhẹ cho domain verification.

Field đề xuất:

- `id`
- `domain_id`
- `type`
- `payload_json`
- `created_at`

### 6.7 user_events hoặc audit_logs

Mục đích: log hành động admin quan trọng.

- toggle `is_active`
- đổi quota
- tạo / xóa domain

## 7. State Model

### 7.1 Domain State

- `pending`: vừa tạo, chưa verify.
- `verified`: MX trỏ đúng hệ thống và user hợp lệ.
- `verified_warning`: domain đang verified nhưng recheck gần nhất fail; hệ thống cảnh báo user/admin nhưng chưa dừng nhận mail ngay.
- `failed`: verify thất bại.
- `disabled`: admin hoặc hệ thống chặn domain.

### 7.2 Inbox State

- `is_active = true`: nhận mail bình thường.
- `is_active = false`: từ chối RCPT TO.

### 7.3 User State

- `is_active = true`: được phép login, tạo tài nguyên, nhận mail.
- `is_active = false`: chặn login mới và từ chối inbound mail cho toàn bộ domain của user.

## 8. SMTP Inbound Flow

### 8.1 Connection Handling

SMTP server nhận kết nối và xử lý:

1. `MAIL FROM`
2. `RCPT TO`
3. `DATA`

V1 chưa bắt buộc auth SMTP vì đây là inbound server nhận mail từ internet hoặc MTA trung gian.

Triển khai deploy V1 nghe trực tiếp SMTP port 25 trên VPS. Môi trường deploy cần mở firewall/security group port 25 và cấu hình DNS MX trỏ về host này.

### 8.2 Validation at RCPT TO

Khi nhận `RCPT TO`, hệ thống phải:

1. Parse địa chỉ nhận.
2. Tìm `domain` theo phần domain.
3. Kiểm tra `domain.status == verified`.
4. Kiểm tra owner user đang `is_active == true`.
5. Tìm `inbox` theo `(domain_id, local_part)`.
6. Kiểm tra `inbox.is_active == true`.

Nếu fail:

- Trả `550 mailbox unavailable` cho inbox/domain không hợp lệ.
- Trả `553` nếu địa chỉ malformed.
- Trả `552` nếu vượt message limit được xác định sớm.

### 8.3 Validation at DATA

Trong lúc stream body:

1. Áp dụng giới hạn `max_message_size_mb`.
2. Reject nếu vượt ngưỡng bằng reader giới hạn.
3. Parse MIME.
4. Tính tổng dung lượng attachment.
5. Reject nếu tổng attachment vượt `max_attachment_size_mb`.
6. Reject nếu mail làm vượt quota tổng storage của user.

Quyết định V1:

- Nếu vượt limit trong giai đoạn `DATA`, trả lỗi SMTP và không persist mail.
- Không chấp nhận lưu partial mail.
- Raw `.eml` phải được lưu từ V1 cho mail persist thành công.

### 8.4 Parsing

Thông tin cần extract:

- Message-ID
- From
- To
- Subject
- Header raw
- Text body
- HTML body
- Danh sách attachments

Thông tin nên parse thêm ở V1:

- SPF result nếu có trong auth headers hoặc từ bước đánh giá inbound
- DKIM result nếu có
- DMARC result nếu có

Sau khi parse:

- Tạo `html_body_sanitized` từ `html_body` để phục vụ hiển thị an toàn trên UI.
- Lưu kết quả SPF, DKIM, DMARC vào `auth_results_json`.

### 8.5 Persistence

Sau khi parse thành công:

1. Insert `emails`.
2. Ghi raw `.eml` xuống local storage và lưu metadata/path nội bộ.
3. Ghi file attachment xuống disk.
4. Insert `attachments`.
5. Cập nhật quota storage đã dùng của user.
6. Publish event Redis: `mail.received`.

Lưu ý:

- Nên dùng transaction DB cho metadata.
- File write không transaction được, nên cần cleanup nếu insert DB thất bại sau khi file/raw `.eml` đã ghi.

## 9. Attachment Storage Design

V1 dùng local storage:

- Root path: `./data/attachments`
- Path pattern: `/{user_id}/{email_id}/{attachment_id}-{safe_filename}`

Yêu cầu:

- Sanitize filename.
- Không trust MIME type từ client hoàn toàn.
- Không cho path traversal.
- API download phải lookup metadata từ DB trước, không expose path thật.
- Attachment bị `flagged` hoặc `infected` bị block mặc định và không cho download trực tiếp.
- Download attachment blocked chỉ được mở bằng admin override nếu policy bật.

Attachment scanning tối thiểu:

- Chặn extension nguy hiểm như `.exe`, `.bat`, `.cmd`, `.js`, `.vbs`, `.scr`, `.ps1`.
- Gắn cờ file có MIME type hoặc magic bytes thể hiện executable / script.
- Nếu có ClamAV trong môi trường deploy, quét file sau khi ghi tạm và trước khi đánh dấu `clean`.
- File bị `flagged` hoặc `infected` vẫn lưu metadata để audit, nhưng mặc định `is_blocked = true`, UI disable download cho user thường.

## 10. Domain Verification Design

Flow verify domain:

1. User tạo domain.
2. Hệ thống trả về giá trị MX cần cấu hình, ví dụ `mx.gomail.local`.
3. User gọi API verify hoặc hệ thống cron verify định kỳ.
4. Service dùng `net.LookupMX`.
5. Domain chỉ được set `verified` khi ít nhất một MX record trỏ đúng mail host mong muốn.

Không dùng tiêu chí:

- Chỉ cần domain có bất kỳ MX record nào.

Nên lưu:

- Kết quả lookup gần nhất.
- Lỗi verify gần nhất.
- Thời điểm verify gần nhất.

Background job:

- Chạy định kỳ để verify lại tất cả domain `verified` và `pending`.
- Domain `verified` nếu không còn trỏ đúng MX target thì chuyển sang trạng thái cảnh báo `verified_warning`, lưu lỗi recheck gần nhất, nhưng chưa dừng nhận mail ngay.
- Domain `pending` được retry tự động để user không phải bấm verify thủ công liên tục.
- Mỗi lần job chạy nên ghi `domain_events` để audit và debug.

## 11. Auth and Authorization

### 11.1 Auth

- Register
- Login
- Password hash bằng bcrypt hoặc argon2id
- Access token JWT
- Refresh token
- Refresh token rotation
- Token revoke khi logout hoặc khi phát hiện reuse

Thiết kế đề xuất:

- Access token sống ngắn.
- Refresh token sống dài hơn và lưu dạng hash trong DB.
- Mỗi lần refresh phải rotate token cũ sang token mới.
- Nếu phát hiện refresh token cũ bị reuse, revoke toàn bộ session chain của user đó.

### 11.2 Authorization

User thường:

- Quản lý domain/inbox của chính họ.
- Xem email thuộc inbox của họ.

Admin:

- Xem danh sách user.
- Toggle `is_active`.
- Update quota.

Mọi query phải filter theo owner, không tin client-provided `user_id`.

## 12. API Surface

### 12.1 Auth

- `POST /api/auth/register`
- `POST /api/auth/login`
- `POST /api/auth/refresh`
- `POST /api/auth/logout`
- `POST /api/auth/change-password`
- `GET /api/me`

### 12.2 Domains

- `GET /api/domains`
- `POST /api/domains`
- `GET /api/domains/:id`
- `POST /api/domains/:id/verify`
- `DELETE /api/domains/:id`

### 12.3 Inboxes

- `GET /api/inboxes`
- `POST /api/inboxes`
- `PATCH /api/inboxes/:id`
- `DELETE /api/inboxes/:id`

### 12.4 Emails

- `GET /api/emails`
- `GET /api/emails/:id`
- `PATCH /api/emails/:id/read`
- `GET /api/emails/:id/attachments/:attachmentId/download`

Filter tối thiểu cho list email:

- inbox id
- unread/read
- pagination

### 12.5 Admin

- `GET /api/admin/users`
- `PATCH /api/admin/users/:id/status`
- `PATCH /api/admin/users/:id/quotas`

### 12.6 SSE

- `GET /api/events/stream`

Event tối thiểu:

- `mail.received`
- `domain.verified`

## 13. Frontend Plan

UI 3 cột:

### 13.1 Column 1

- Dashboard
- Email
- Domains
- Settings

Dashboard widgets tối thiểu:

- số mail nhận hôm nay
- tổng dung lượng đã dùng
- số inbox đang hoạt động

Top-right header controls:

- Icon account ở góc phải trên cùng.
- Nút chuyển theme sáng/tối ở góc phải trên cùng, đứng cạnh account icon.
- Theme mặc định là `light`.
- Nút đóng/mở sidebar để thu gọn hoặc mở rộng menu trái.

Account menu items:

- `Settings`
- `Change Password`
- `Logout`

### 13.2 Domains View

Khi chọn menu `Domains`:

- Khu vực nội dung chính hiển thị danh sách domain theo dạng hàng/bảng.
- Nút thêm domain nằm bên phải phía trên danh sách.

Danh sách domain nên có các cột tối thiểu:

- `Domain`
- `MX`
- `Status`
- `Last Verified`
- `Actions`

Hành vi chính:

- Click một dòng domain để xem chi tiết hoặc mở panel chi tiết.
- Cho phép verify lại domain từ cột `Actions`.
- Hiển thị hướng dẫn cấu hình MX trong phần chi tiết domain.

### 13.3 Emails View

Khi chọn menu `Email`:

- Layout hiển thị thêm cột phụ dành cho danh sách địa chỉ email đã tạo.
- Cột phụ này là entry point để chọn inbox/address trước khi xem danh sách mail nhận được.

Column 2:

- Danh sách địa chỉ email / inbox
- Nút `Create New Email` ở trên cùng
- `Create New Email` chỉ cho phép tạo địa chỉ trên các domain user đã thêm
- Bộ lọc unread

Column 3:

- Danh sách email
- Click vào email để xem detail
- Nội dung text/html
- Attachment download
- Hiển thị trạng thái SPF, DKIM, DMARC nếu có
- Cảnh báo attachment bị gắn cờ hoặc bị block

### 13.4 Realtime

- UI mở SSE stream sau khi login.
- Khi nhận `mail.received`, refresh mailbox đang xem hoặc prepend email mới vào list.

### 13.5 HTML Email Safety

- UI chỉ render `html_body_sanitized`.
- Không render trực tiếp `html_body` gốc.
- Không tự động load remote images trong mail viewer ở V1 nếu chưa có proxy hoặc cơ chế allowlist.

### 13.6 Theme and Account UX

- App khởi động với light theme mặc định.
- Theme toggle phải đổi được giữa light và dark mà không reload toàn trang.
- Nên persist lựa chọn theme ở local storage hoặc user preference nếu sau này cần sync đa thiết bị.
- Account menu mở từ avatar/icon ở góc phải, chứa các tác vụ cá nhân thay vì đặt rải rác trong navigation trái.
- Sidebar cần có trạng thái collapse/expand để tối ưu không gian đọc mail.

## 14. Error Handling Rules

Nên chuẩn hóa một số rule:

- API trả JSON error nhất quán: `code`, `message`.
- SMTP log đầy đủ theo message lifecycle.
- Không expose internal file path, stack trace, hay SQL error thô ra client.

Case cần định nghĩa rõ:

- Domain chưa verify nhưng user cố tạo inbox.
- User bị disable khi vẫn còn domain/inbox cũ.
- Verify domain timeout hoặc DNS tạm thời lỗi.
- DB insert thành công nhưng file attachment fail.

## 15. Logging and Observability

V1 tối thiểu cần:

- Structured logs.
- Request ID cho HTTP.
- Message ID nội bộ cho SMTP pipeline.
- Error logs cho parse fail, storage fail, DNS verify fail.

Metrics tốt nếu có:

- số mail nhận thành công
- số mail nhận hôm nay
- số mail bị reject
- số verify domain thành công/thất bại
- dung lượng attachment lưu trữ
- số inbox đang hoạt động

## 16. Security Notes

V1 chưa cần giải bài toán mail security đầy đủ, nhưng vẫn cần:

- Hash password đúng chuẩn.
- JWT secret cấu hình qua env.
- Giới hạn upload/stream size.
- Sanitize attachment filename.
- Render HTML email chỉ từ bản đã sanitize.
- Dùng HTML sanitization trước khi hiển thị.
- Không tự động load remote image/script/resource từ email HTML.
- Parse và lưu kết quả SPF, DKIM, DMARC vào metadata để hiển thị và audit.
- Quét attachment tối thiểu theo extension, MIME, magic bytes; hỗ trợ ClamAV nếu có.
- Refresh token phải được rotate và revoke đúng cách, không lưu plaintext token trong DB.

## 17. Testing Strategy

### 17.1 Unit Tests

- auth service
- domain verification service
- inbox validation
- attachment limit logic
- parser service
- HTML sanitization service
- refresh token rotation logic
- attachment scan classifier

### 17.2 Integration Tests

- API auth/domain/inbox/email
- DB migration
- SMTP receive -> DB persist -> Redis publish
- refresh token refresh/reuse/revoke flow
- domain verification background job
- attachment scan pipeline

### 17.3 Manual End-to-End

1. Tạo user.
2. Thêm domain.
3. Verify domain.
4. Tạo inbox.
5. Gửi mail test qua SMTP.
6. Kiểm tra email xuất hiện ở UI.
7. Download attachment.

## 18. Milestones

### Milestone 1: Project Bootstrap

- Init Go module
- Tạo cấu trúc thư mục
- Docker Compose cho Postgres + Redis
- Config loader
- `.env.example` cho domain SaaS, SMTP, storage, JWT, DB/Redis, default super admin
- Logger

Definition of done:

- `docker compose up` chạy được.
- API service start được với healthcheck.
- App validate được config deploy tối thiểu và seed super admin từ env lần đầu.

### Milestone 2: Database and Models

- Thiết kế models
- Viết migration đầu tiên
- Seed super admin user từ env `DEFAULT_ADMIN_*`

Definition of done:

- Có schema ổn định cho `users`, `domains`, `inboxes`, `emails`, `attachments`.

### Milestone 3: Auth and Basic API

- Register/login
- JWT middleware
- Refresh token + rotation
- Change password
- CRUD domain
- CRUD inbox

Definition of done:

- User tạo được domain và inbox qua API.

### Milestone 4: Domain Verification

- Verify MX target
- Persist status và error
- Background recheck job định kỳ

Definition of done:

- Chỉ domain verified mới tạo inbox và nhận mail.

### Milestone 5: SMTP Ingest Pipeline

- SMTP listener
- RCPT validation
- DATA streaming limit
- MIME parse
- SPF/DKIM/DMARC metadata parse
- DB persist

Definition of done:

- Gửi một mail mẫu và thấy record xuất hiện trong DB.

### Milestone 6: Attachment Storage

- Local file storage
- Attachment metadata
- Attachment content scanning
- Download API

Definition of done:

- Mail có attachment được lưu và tải lại được.

### Milestone 7: Realtime

- Redis Pub/Sub
- SSE endpoint
- UI nhận event mail mới

Definition of done:

- Mail mới xuất hiện trên UI mà không cần reload trang.

### Milestone 8: Frontend V1

- Layout 3 cột
- Dashboard metrics
- Domain view
- Inbox/email list
- Email detail
- HTML sanitized mail viewer
- Auth results badges
- Top-right account menu
- Light/dark theme toggle, mặc định light

Definition of done:

- User có thể dùng UI để quản lý domain/inbox và đọc mail.

### Milestone 9: Admin Controls

- User list
- Toggle active
- Update quota

Definition of done:

- Admin chặn được user và thay đổi quota qua UI/API.

## 19. Recommended Build Order

Thứ tự triển khai thực tế nên là:

1. Bootstrap project.
2. Chốt schema + migration.
3. Auth.
4. Domain + inbox API.
5. Domain verification.
6. SMTP ingest pipeline.
7. Attachment storage.
8. Email read API + dashboard metrics.
9. SSE realtime.
10. Frontend.
11. Admin panel.

Lý do:

- Schema và SMTP pipeline là phần ảnh hưởng kiến trúc mạnh nhất.
- Frontend nên bám theo API đã ổn định.
- Admin là phần phụ trợ, không nên chặn core mail flow.

## 20. Decisions and Remaining Questions

Đã chốt:

1. SMTP deploy trực tiếp trên port 25 ở VPS.
2. Lưu raw `.eml` cho mỗi email từ V1.
3. Có quota tổng dung lượng lưu trữ theo user, cấu hình bởi super admin.
4. Frontend dùng SPA.
5. Có soft delete cho domain, inbox, email.
6. Domain verified bị recheck fail thì chuyển trạng thái cảnh báo, chưa dừng nhận mail ngay.
7. Attachment `flagged` bị block mặc định, không cho download trực tiếp; UI disable download hoặc yêu cầu admin override.

Còn cần chốt:

1. Có cho phép catch-all inbox ở V1 không?

## 21. Immediate Next Step

Việc nên làm ngay sau tài liệu này:

1. Tạo `go.mod`.
2. Dựng `docker-compose.yaml`.
3. Viết migration đầu tiên theo schema ở mục 6.
4. Khởi tạo `cmd/api` và `cmd/smtp`.
