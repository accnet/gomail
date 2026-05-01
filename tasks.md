# GoMail Execution Tasks

Tài liệu này chuyển `plan.md` thành backlog thực thi. Thứ tự ưu tiên đi từ nền tảng hạ tầng, schema, auth, domain/inbox, SMTP ingest, attachment, realtime, frontend, rồi admin.

## Nguyên tắc triển khai

- Làm backend/API ổn định trước frontend.
- Mỗi milestone phải có test hoặc kịch bản kiểm chứng tối thiểu.
- Không persist mail nếu SMTP `DATA` vượt quota hoặc parse thất bại.
- Mọi query tài nguyên user phải filter theo owner.
- Không expose path attachment thật ra client.

## Quyết định đã chốt

- [x] SMTP chạy trực tiếp port 25 khi deploy VPS. Cần mở firewall/security group cho port 25 ở môi trường deploy.
- [x] Lưu raw `.eml` cho từng email ngay từ V1.
- [x] Có quota tổng dung lượng storage theo user, cấu hình bởi super admin.
- [x] Frontend là SPA.
- [x] Có soft delete cho domain, inbox, email.
- [x] Domain `verified` bị recheck fail thì chuyển sang trạng thái cảnh báo, không dừng nhận mail ngay.
- [x] Attachment `flagged` bị block mặc định, không cho download trực tiếp. UI disable nút download hoặc yêu cầu admin override.

## Milestone 1: Project Bootstrap

- [x] Khởi tạo Go module.
- [x] Tạo cấu trúc thư mục:
  - `cmd/api`
  - `cmd/smtp`
  - `internal/auth`
  - `internal/config`
  - `internal/db`
  - `internal/dns`
  - `internal/http`
  - `internal/mail`
  - `internal/realtime`
  - `internal/smtp`
  - `internal/storage`
  - `pkg/logger`
  - `pkg/response`
  - `deploy/docker`
  - `web`
- [x] Thêm Docker Compose cho Postgres và Redis.
- [x] Viết config loader đọc env cho DB, Redis, JWT, SMTP, storage, MX target.
- [x] Load và validate cấu hình domain SaaS: `SAAS_DOMAIN`, `APP_BASE_URL`, `API_BASE_URL`, `SMTP_HOSTNAME`, `MX_TARGET`.
- [x] Dùng `MX_TARGET` để render hướng dẫn DNS MX cho user trong domain detail/API.
- [x] Dùng `SMTP_HOSTNAME` làm SMTP hostname/banner khi deploy.
- [x] Load và validate cấu hình default super admin: `DEFAULT_ADMIN_EMAIL`, `DEFAULT_ADMIN_PASSWORD`, `DEFAULT_ADMIN_NAME`.
- [x] Fail fast nếu deploy dùng default password/secret mẫu từ `.env.example`.
- [ ] Dùng `.env.example` làm mẫu cấu hình deploy và đảm bảo `.env` thật nằm trong `.gitignore`.
- [x] Viết structured logger.
- [x] Tạo API app Gin tối thiểu với `GET /healthz`.
- [x] Tạo SMTP binary tối thiểu start được.

Definition of done:

- [ ] `docker compose up` chạy được Postgres và Redis.
- [ ] API start được và `/healthz` trả success.
- [x] SMTP binary build được.
- [x] Config domain SaaS và default super admin được validate khi start.

## Milestone 2: Database and Models

- [ ] Chọn migration tool và wiring lệnh chạy migration.
- [ ] Tạo model/migration cho `users`.
- [ ] Tạo model/migration cho `domains`.
- [ ] Tạo model/migration cho `inboxes`.
- [ ] Tạo model/migration cho `emails`.
- [ ] Tạo model/migration cho `attachments`.
- [ ] Tạo model/migration cho `domain_events`.
- [ ] Tạo model/migration cho `audit_logs` hoặc `user_events`.
- [x] Tạo bảng refresh token/session để hỗ trợ rotation và revoke.
- [ ] Thêm soft delete fields cho `domains`, `inboxes`, `emails`.
- [ ] Thêm field lưu raw `.eml` path/metadata cho `emails`.
- [ ] Thêm quota tổng dung lượng storage theo user, ví dụ `max_storage_bytes` và `storage_used_bytes`.
- [ ] Thêm field trạng thái cảnh báo domain khi recheck fail, ví dụ `warning` hoặc `verified_warning`.
- [ ] Thêm field admin override cho attachment blocked/flagged nếu cần mở khóa có kiểm soát.
- [ ] Thêm indexes/unique constraints:
  - `users.email`
  - `domains.name`
  - `inboxes.address`
  - `inboxes(domain_id, local_part)`
  - `emails.message_id`
  - các FK owner/query phổ biến.
- [ ] Seed super admin user từ env `DEFAULT_ADMIN_*`.
- [ ] Seed super admin idempotent: không reset password/quota nếu email đã tồn tại.
- [ ] Viết repository cơ bản cho user/domain/inbox/email/attachment.

Definition of done:

- [ ] Migration chạy sạch trên database trống.
- [ ] Schema có đủ constraint chống duplicate domain/inbox.
- [ ] Schema hỗ trợ soft delete mà vẫn giữ được audit và tránh leak dữ liệu đã xóa.
- [ ] Schema hỗ trợ quota storage tổng theo user.
- [ ] Seed super admin hoạt động, không lưu password plaintext, và bắt buộc đổi password mẫu trước deploy.

## Milestone 3: Auth and Basic API

- [x] Implement password hashing bằng bcrypt hoặc argon2id.
- [x] Implement JWT access token.
- [x] Implement refresh token hash, rotation, logout, reuse detection.
- [x] Implement auth middleware.
- [x] Chuẩn hóa JSON error `{ "code": "...", "message": "..." }`.
- [x] Implement `POST /api/auth/register`.
- [x] Implement `POST /api/auth/login`.
- [x] Implement `POST /api/auth/refresh`.
- [x] Implement `POST /api/auth/logout`.
- [x] Implement `POST /api/auth/change-password`.
- [x] Implement `GET /api/me`.
- [ ] Viết unit test cho auth và refresh token rotation.

Definition of done:

- [ ] User register/login được.
- [ ] Access token bảo vệ được route.
- [ ] Refresh token cũ bị rotate không dùng lại được.

## Milestone 4: Domain and Inbox API

- [ ] Implement domain service tạo domain trạng thái `pending`.
- [ ] Enforce domain unique toàn hệ thống.
- [ ] Enforce `max_domains` theo user.
- [ ] Implement `GET /api/domains`.
- [ ] Implement `POST /api/domains`.
- [ ] Implement `GET /api/domains/:id`.
- [ ] Implement `POST /api/domains/:id/verify` placeholder gọi verification service.
- [ ] Implement `DELETE /api/domains/:id`.
- [ ] `DELETE /api/domains/:id` thực hiện soft delete.
- [ ] Implement inbox service chỉ cho tạo trên domain `verified`.
- [ ] Enforce `max_inboxes` theo user.
- [ ] Normalize và validate `local_part`.
- [ ] Implement `GET /api/inboxes`.
- [ ] Implement `POST /api/inboxes`.
- [ ] Implement `PATCH /api/inboxes/:id`.
- [ ] Implement `DELETE /api/inboxes/:id`.
- [ ] `DELETE /api/inboxes/:id` thực hiện soft delete.
- [ ] Viết integration test cho domain/inbox ownership và quota.

Definition of done:

- [ ] User tạo/list/xóa domain của chính họ được.
- [ ] User tạo/list/update/xóa inbox của chính họ được.
- [ ] Không tạo được inbox trên domain chưa verified.

## Milestone 5: Domain Verification

- [ ] Implement DNS service dùng `net.LookupMX`.
- [ ] Verify domain chỉ thành công khi MX trỏ đúng `mx_target`.
- [ ] Persist `status`, `last_verified_at`, `verification_error`.
- [ ] Ghi `domain_events` cho mỗi lần verify.
- [ ] Implement background job recheck domain `pending` và `verified`.
- [ ] Nếu domain đang `verified` bị recheck fail, chuyển sang trạng thái cảnh báo và ghi lỗi, chưa dừng nhận mail ngay.
- [ ] Hiển thị/return trạng thái cảnh báo domain qua API để UI báo cho user.
- [ ] Viết unit test DNS verifier bằng mock resolver.
- [ ] Viết integration test API verify domain.

Definition of done:

- [ ] Domain chỉ chuyển `verified` khi có MX target đúng.
- [ ] Domain failed có lỗi rõ ràng để UI hiển thị.
- [ ] Domain verified bị cảnh báo có lỗi rõ ràng nhưng vẫn chưa bị chặn nhận mail.
- [ ] Background job retry được domain pending.

## Milestone 6: SMTP Ingest Pipeline

- [ ] Tích hợp `go-smtp` trong `cmd/smtp`.
- [ ] Implement SMTP session lifecycle logging.
- [ ] Parse `MAIL FROM` và `RCPT TO`.
- [ ] Validate RCPT:
  - domain tồn tại
  - domain `verified`
  - user active
  - inbox tồn tại
  - inbox active
- [ ] Trả mã lỗi SMTP phù hợp: `553`, `550`, `552`.
- [ ] Implement DATA reader giới hạn theo `max_message_size_mb`.
- [ ] Enforce quota tổng dung lượng storage theo user trước khi accept/persist mail nếu tính được sớm, và sau parse trước khi commit.
- [ ] Tích hợp MIME parser `enmime`.
- [ ] Extract Message-ID, From, To, Subject, headers, text body, HTML body, attachments.
- [ ] Sanitize HTML body thành `html_body_sanitized`.
- [ ] Parse/lưu SPF, DKIM, DMARC từ auth headers nếu có.
- [ ] Persist email record trong DB transaction.
- [ ] Lưu raw `.eml` vào local storage cho từng email V1.
- [ ] Cleanup raw `.eml` nếu persist DB hoặc attachment pipeline thất bại.
- [ ] Publish domain/mail lifecycle log với internal message ID.
- [x] Viết integration test gửi mail mẫu qua SMTP vào DB.

Definition of done:

- [ ] Gửi mail test qua SMTP tạo được record `emails`.
- [ ] Mail vượt message size bị reject và không persist.
- [ ] Mail làm vượt quota storage user bị reject và không persist.
- [ ] Mail persist thành công có raw `.eml` lưu được và lookup được qua metadata nội bộ.
- [ ] RCPT invalid bị reject trước DATA.

## Milestone 7: Attachment Storage

- [ ] Implement local storage root `./data/attachments`.
- [ ] Implement safe filename sanitizer.
- [ ] Implement path pattern `/{user_id}/{email_id}/{attachment_id}-{safe_filename}`.
- [ ] Ghi attachment file sau parse và trước/đồng bộ với metadata.
- [ ] Cleanup file nếu DB insert hoặc transaction thất bại.
- [ ] Implement attachment size quota theo `max_attachment_size_mb`.
- [ ] Tính attachment vào quota tổng dung lượng storage của user.
- [ ] Implement scanner tối thiểu theo extension nguy hiểm.
- [ ] Implement MIME/magic bytes classifier cho executable/script.
- [ ] Thiết kế hook ClamAV optional nếu env có cấu hình.
- [ ] Persist `scan_status`, `scan_result`, `is_blocked`.
- [ ] Attachment `flagged` hoặc `infected` phải set `is_blocked = true` mặc định.
- [ ] Implement `GET /api/emails/:id/attachments/:attachmentId/download`.
- [ ] Download API phải lookup DB, check owner, check block policy, và từ chối attachment blocked nếu không có admin override.
- [ ] Implement admin override flow cho attachment blocked nếu policy UI/API cần mở khóa có kiểm soát.
- [ ] Viết unit test filename sanitizer và scanner.
- [x] Viết integration test mail có attachment.

Definition of done:

- [ ] Attachment hợp lệ lưu và tải lại được.
- [ ] Path traversal không thể xảy ra qua filename.
- [x] Attachment bị block không download công khai được.
- [x] Attachment flagged mặc định không download trực tiếp được.

## Milestone 8: Email Read API and Metrics

- [x] Implement `GET /api/emails` với filter inbox, unread/read, pagination.
- [ ] Implement `GET /api/emails/:id`.
- [ ] Implement `PATCH /api/emails/:id/read`.
- [ ] Implement soft delete email endpoint hoặc behavior cho delete email nếu UI cần.
- [ ] Chỉ trả `html_body_sanitized` cho UI mặc định.
- [ ] Không trả internal storage path.
- [ ] Không trả raw `.eml` path cho client thường.
- [ ] Implement dashboard metrics:
  - mail hôm nay
  - dung lượng đã dùng
  - inbox active
  - mail reject nếu đã có log/metric nguồn.
- [ ] Viết integration test ownership cho email/attachment.

Definition of done:

- [ ] User xem được danh sách mail và chi tiết mail của chính họ.
- [ ] User không truy cập được mail/attachment của user khác.

## Milestone 9: Realtime

- [x] Implement Redis publisher cho `mail.received`.
- [x] Implement Redis publisher cho `domain.verified`.
- [ ] Implement SSE endpoint `GET /api/events/stream`.
- [ ] Auth SSE bằng access token hoặc cookie theo frontend strategy.
- [x] Fanout event theo user, không leak event giữa users.
- [ ] Reconnect handling cho SSE.
- [x] Viết integration test publish event tới SSE client.

Definition of done:

- [ ] Khi SMTP persist mail thành công, UI/client nhận được `mail.received`.
- [x] SSE không gửi event của user khác.

## Milestone 10: Frontend V1

- [ ] Chốt framework frontend.
- [ ] Build frontend theo SPA.
- [ ] Tạo auth screens login/register/change password.
- [ ] Tạo app shell 3 cột.
- [ ] Sidebar trái có Dashboard, Email, Domains, Settings.
- [ ] Sidebar collapse/expand.
- [ ] Header góc phải có theme toggle và account menu.
- [ ] Theme mặc định light, toggle light/dark không reload, persist local storage.
- [ ] Dashboard hiển thị metrics tối thiểu.
- [ ] Domains view dạng bảng với cột Domain, MX, Status, Last Verified, Actions.
- [ ] Domain status hiển thị được trạng thái cảnh báo khi recheck fail.
- [ ] Domain detail/panel hiển thị hướng dẫn MX.
- [ ] Verify lại domain từ Actions.
- [ ] Emails view có cột inbox/address.
- [ ] Tạo inbox từ các domain đã verified.
- [x] Filter unread.
- [ ] Email list và email detail.
- [ ] Render HTML chỉ từ `html_body_sanitized`.
- [x] Không tự động load remote images trong email viewer.
- [ ] Attachment download và cảnh báo blocked/flagged.
- [ ] Disable nút download cho attachment blocked/flagged nếu user thường không có quyền override.
- [ ] Hiển thị badge SPF, DKIM, DMARC.
- [ ] Kết nối SSE sau login và refresh/prepend mail mới.
- [ ] Kiểm tra responsive desktop/mobile cơ bản.

Definition of done:

- [ ] User quản lý domain/inbox và đọc mail qua UI.
- [ ] Mail mới xuất hiện không cần reload.
- [ ] UI không render HTML gốc hoặc remote resource không kiểm soát.

## Milestone 11: Admin Controls

- [x] Implement admin middleware.
- [x] Implement `GET /api/admin/users`.
- [x] Implement `PATCH /api/admin/users/:id/status`.
- [x] Implement `PATCH /api/admin/users/:id/quotas`.
- [ ] Quota API phải cho super admin cấu hình tổng dung lượng storage theo user.
- [ ] Phân quyền super admin cho các thao tác quota nhạy cảm nếu khác admin thường.
- [ ] Implement admin override cho attachment blocked/flagged nếu được bật trong policy.
- [x] Ghi audit log khi toggle active hoặc đổi quota.
- [x] Admin UI list user.
- [x] Admin UI toggle active.
- [x] Admin UI update quota, bao gồm quota tổng storage.
- [x] Admin UI override attachment blocked/flagged nếu policy bật.
- [x] SMTP RCPT phải từ chối mail cho user bị disable.
- [x] Login phải chặn user bị disable.
- [x] Viết integration test admin authorization.

Definition of done:

- [ ] Admin quản lý được user/quota qua API và UI.
- [ ] Super admin cấu hình được quota tổng storage theo user.
- [ ] User thường không gọi được admin API.
- [ ] Disable user chặn login mới và inbound mail.

## Cross-Cutting Tasks

- [x] Request ID middleware cho HTTP.
- [ ] Structured logs cho HTTP, SMTP, DNS verify, storage.
- [ ] Không trả stack trace/SQL error thô ra client.
- [ ] Centralize config validation khi app start.
- [x] Add Makefile hoặc task runner cho build/test/migrate.
- [ ] Add CI workflow nếu repo dùng GitHub.
- [x] Add README dev setup.
- [x] Add manual E2E script:
  - tạo user
  - thêm domain
  - verify domain
  - tạo inbox
  - gửi mail test
  - đọc mail UI/API
  - download attachment

## Test Backlog

- [ ] Unit: auth service.
- [ ] Unit: refresh token rotation/reuse/revoke.
- [ ] Unit: domain verification service.
- [ ] Unit: inbox validation.
- [ ] Unit: attachment limit logic.
- [ ] Unit: parser service.
- [ ] Unit: HTML sanitization service.
- [ ] Unit: attachment scan classifier.
- [x] Integration: DB migration.
- [x] Integration: API auth/domain/inbox/email.
- [ ] Integration: SMTP receive -> DB persist -> Redis publish.
- [x] Integration: domain verification background job.
- [x] Integration: attachment scan pipeline.
- [x] Manual E2E: full user flow from register to reading inbound mail.

## First Implementation Sprint

Sprint đầu tiên nên giới hạn trong nền tảng để các milestone sau không phải sửa nhiều.

- [ ] Init Go module.
- [x] Dựng Docker Compose Postgres + Redis.
- [x] Tạo config loader.
- [x] Thêm `.env.example` cho domain SaaS và default super admin.
- [ ] Tạo logger.
- [ ] Tạo Gin API với `/healthz`.
- [ ] Tạo SMTP command build được.
- [ ] Chọn migration tool.
- [ ] Viết migration đầu tiên cho `users`, `domains`, `inboxes`, `emails`, `attachments`.
- [x] Thêm README dev setup tối thiểu.
