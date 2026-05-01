# SMTP Relay Execution Tasks

Tài liệu này chuyển [plan-smtp-relay.md](./plan-smtp-relay.md) thành backlog thực thi cho codebase hiện tại.

## Phân tích nhanh codebase hiện tại

Những phần có thể dùng lại ngay:

- [internal/smtp/server/server.go](./internal/smtp/server/server.go) đã có SMTP server lifecycle cơ bản bằng `go-smtp`.
- [internal/http/handlers/app.go](./internal/http/handlers/app.go) đã có auth flow JWT, protected routes, admin routes và pattern wiring route rõ ràng.
- [internal/http/middleware/auth.go](./internal/http/middleware/auth.go) đã có middleware auth theo user để tái sử dụng pattern context principal.
- [internal/config/config.go](./internal/config/config.go) đã có config loader/validator và seed defaults cho app hiện tại.
- [internal/db/models.go](./internal/db/models.go) và [internal/db/db.go](./internal/db/db.go) đã có soft delete, auto-migrate, quota fields, audit log.
- [README.md](./README.md) đã mô tả rõ luồng inbound hiện tại, phù hợp để mở rộng thêm phần outbound relay.

Những phần chưa có và là đường găng:

- Chưa có schema `api_keys`, `api_key_usage_logs`, `sent_email_logs`.
- Chưa có SMTP submission server cho AUTH/STARTTLS/TLS port `587/465`.
- Chưa có outbound sender riêng để DKIM sign và relay mail đi.
- Chưa có middleware/API key auth cho HTTP routes.
- Chưa có WordPress onboarding flow, SMTP presets và test send.

Nguyên tắc triển khai:

- Làm nền tảng dữ liệu và auth trước SMTP submission.
- Tách `MVP` khỏi `production-ready`; không trộn queue/bounce vào vòng đầu nếu chưa cần.
- Mỗi milestone phải có test hoặc kịch bản verify tối thiểu.
- API key chỉ reveal đúng một lần, sau đó chỉ lưu hash.
- SMTP relay chỉ được gửi từ verified domain thuộc chính owner.

## Baseline có sẵn

- [x] JWT auth, refresh token rotation, admin middleware.
- [x] SMTP inbound server bằng `go-smtp`.
- [x] Domain verification flow và verified-domain ownership.
- [x] Quota fields cơ bản ở user model.
- [ ] API key management cho external apps.
- [ ] SMTP submission relay cho WordPress/external apps.
- [ ] DKIM signing và outbound delivery pipeline.

## Phase 0: Scope and Decisions

Mục tiêu: chốt boundary để implementation không phải sửa vòng lại.

- [ ] Chốt `MVP` gồm: API key CRUD, SMTP AUTH submission, DKIM sign, sent log, WordPress presets.
- [ ] Chốt `production-ready` để tách riêng queue, retry, bounce, suppression list, webhook.
- [ ] Chốt security model: hash API key bằng SHA-256, không mã hóa giải ngược.
- [ ] Chốt auth contract cho SMTP: `username = api_key_id`, `password = full_api_key`.
- [ ] Chốt profile submission support:
  - port `587` + STARTTLS
  - port `465` + implicit TLS
  - optional plain port chỉ cho môi trường private nếu cần
- [ ] Chốt outbound strategy V1:
  - direct MX delivery
  - hoặc fixed upstream relay
- [ ] Chốt DKIM mode V1:
  - global key
  - hoặc per-domain key
- [ ] Chốt deliverability requirements V1:
  - SPF required trước production send
  - DKIM required
  - DMARC khuyến nghị

Definition of done:

- [ ] Scope MVP được chốt rõ, không lẫn production-ready items.
- [ ] Không còn mơ hồ giữa submission server và inbound SMTP server.

## Phase 1: Schema, Constants, and Config

Mục tiêu: có persistence và config contract ổn định cho relay.

- [ ] Thêm constants `ApiKeyScope*` vào [internal/db/models.go](./internal/db/models.go).
- [ ] Thêm constants `SentEmailStatus*` vào [internal/db/models.go](./internal/db/models.go).
- [ ] Thêm model `ApiKey` vào [internal/db/models.go](./internal/db/models.go).
- [ ] Thêm model `ApiKeyUsageLog` vào [internal/db/models.go](./internal/db/models.go).
- [ ] Thêm model `SentEmailLog` vào [internal/db/models.go](./internal/db/models.go).
- [ ] Nếu cần tracking mở rộng, thêm field `delivery_status`, `error_message`, `sent_at`, `message_id` rõ ràng cho `SentEmailLog`.
- [ ] Cập nhật [internal/db/db.go](./internal/db/db.go) để AutoMigrate bảng mới.
- [ ] Thêm partial/secondary indexes cho:
  - `api_keys.key_hash`
  - `api_keys.user_id`
  - `api_keys.is_active`
  - `api_key_usage_logs.api_key_id`
  - `sent_email_logs.user_id`
  - `sent_email_logs.api_key_id`
  - `sent_email_logs.status`
  - `sent_email_logs.sent_at`
- [ ] Thêm config relay vào [internal/config/config.go](./internal/config/config.go):
  - `SMTPAuthEnabled`
  - `SMTPAuthHostname`
  - `SMTPAuthPort`
  - `SMTPAuthTLSPort`
  - `SMTPAuthTLSMode`
  - `SMTPAuthCertFile`
  - `SMTPAuthKeyFile`
  - `SMTPRelayHostname`
  - `SMTPRelayPublicIP`
  - `DKIMEnabled`
  - `DKIMSelector`
  - `DKIMPrivateKeyPath`
- [ ] Validate env/config mới khi start app.

Definition of done:

- [ ] DB trống migrate được các bảng mới.
- [ ] Config relay/TLS/DKIM load được và fail fast khi thiếu field bắt buộc.

## Phase 2: API Key HTTP Surface

Mục tiêu: external app có thể nhận credential relay hợp lệ qua API.

- [ ] Tạo [internal/http/middleware/apikey_auth.go](./internal/http/middleware/apikey_auth.go).
- [ ] Middleware phải:
  - đọc `X-Api-Key`
  - hash SHA-256
  - lookup key theo hash
  - check active/expiry/IP allowlist
  - attach principal vào context
- [ ] Tạo [internal/http/handlers/apikey.go](./internal/http/handlers/apikey.go).
- [ ] Implement `POST /api/api-keys`.
- [ ] Implement `GET /api/api-keys`.
- [ ] Implement `GET /api/api-keys/:id`.
- [ ] Implement `PATCH /api/api-keys/:id`.
- [ ] Implement `POST /api/api-keys/:id/revoke`.
- [ ] Implement `DELETE /api/api-keys/:id`.
- [ ] Implement `GET /api/api-keys/:id/usage`.
- [ ] API create key phải trả `full_api_key` đúng một lần.
- [ ] Response create/list key nên trả thêm `smtp_settings`:
  - host
  - port `587`
  - port `465`
  - recommended security
  - username format
- [ ] Wire routes vào [internal/http/handlers/app.go](./internal/http/handlers/app.go).
- [ ] Nếu hỗ trợ onboarding tốt hơn, thêm endpoint `send test email` hoặc `test smtp settings`.

Definition of done:

- [ ] User tạo/list/revoke API key được.
- [ ] Full key chỉ hiện đúng một lần lúc create.
- [ ] API trả đủ host/port/security info để cấu hình WordPress plugin.

## Phase 3: SMTP Submission Server

Mục tiêu: external app authenticate và submit mail qua SMTP relay.

- [ ] Tạo [internal/smtp/server/smtp_auth.go](./internal/smtp/server/smtp_auth.go).
- [ ] Implement backend/session tương thích `go-smtp` auth session API hiện đang dùng trong repo.
- [ ] Support `AUTH PLAIN`.
- [ ] Support `AUTH LOGIN` nếu cần cho plugin compatibility.
- [ ] Bật `STARTTLS` listener trên port `587`.
- [ ] Bật implicit TLS listener trên port `465`.
- [ ] Banner/hostname dùng `SMTPAuthHostname`.
- [ ] Auth flow phải:
  - lookup `ApiKey` theo `api_key_id`
  - verify `password` bằng `key_hash`
  - check `is_active`
  - check `expires_at`
  - check `allowed_ips`
  - check scope `send_email`
- [ ] `MAIL FROM` phải thuộc verified domain của owner.
- [ ] Enforce max message size.
- [ ] Enforce recipient validation tối thiểu.
- [ ] Log usage vào `ApiKeyUsageLog`.
- [ ] Wire startup vào [cmd/smtp/main.go](./cmd/smtp/main.go).

Definition of done:

- [ ] WordPress client auth được qua port `587` STARTTLS.
- [ ] Plugin chỉ hỗ trợ SMTPS auth được qua port `465`.
- [ ] Gửi từ domain không thuộc owner hoặc chưa verified bị reject.

## Phase 4: Outbound Sender and DKIM

Mục tiêu: SMTP submission nhận mail xong có thể relay outbound đúng chuẩn tối thiểu.

- [ ] Tạo [internal/mail/sender/sender.go](./internal/mail/sender/sender.go).
- [ ] Tạo [internal/mail/sender/dkim.go](./internal/mail/sender/dkim.go).
- [ ] Tạo [internal/mail/sender/ratelimit.go](./internal/mail/sender/ratelimit.go).
- [ ] Parse raw MIME từ SMTP session.
- [ ] Build envelope outbound từ `MAIL FROM` + `RCPT TO`.
- [ ] DKIM sign message trước khi relay.
- [ ] Chọn strategy outbound:
  - direct MX lookup
  - hoặc upstream relay cố định
- [ ] Enforce daily quota theo API key.
- [ ] Ghi `SentEmailLog` khi send thành công/thất bại.
- [ ] Update `last_used_at` cho API key.
- [ ] Persist `message_id`, `status`, `error_message`, `sent_at`.

Definition of done:

- [ ] Mail submit thành công được relay outbound.
- [ ] Message outbound có DKIM signature.
- [ ] Có sent log tra cứu lại được theo user/API key.

## Phase 5: Tests and Verification

Mục tiêu: có tối thiểu bộ kiểm chứng cho luồng relay MVP.

- [ ] Tạo [internal/http/middleware/apikey_auth_test.go](./internal/http/middleware/apikey_auth_test.go).
- [ ] Tạo [internal/http/handlers/apikey_integration_test.go](./internal/http/handlers/apikey_integration_test.go).
- [ ] Tạo [internal/http/handlers/send_email_integration_test.go](./internal/http/handlers/send_email_integration_test.go).
- [ ] Tạo [internal/smtp/server/smtp_auth_integration_test.go](./internal/smtp/server/smtp_auth_integration_test.go).
- [ ] Tạo [internal/mail/sender/sender_test.go](./internal/mail/sender/sender_test.go).
- [ ] Test create key one-time reveal flow.
- [ ] Test expired/revoked API key bị reject.
- [ ] Test `587` STARTTLS happy path.
- [ ] Test `465` implicit TLS happy path.
- [ ] Test invalid `MAIL FROM` domain bị reject.
- [ ] Test quota exceeded bị reject và không ghi sent success.
- [ ] Test DKIM signing helper hoạt động.

Definition of done:

- [ ] Có test cho API key CRUD, API key auth middleware, SMTP auth flow, outbound sender basics.

## Phase 6: Docs and WordPress Onboarding

Mục tiêu: user dùng được relay mà không phải đoán cấu hình.

- [ ] Cập nhật [README.md](./README.md) với phần SMTP relay.
- [ ] Thêm hướng dẫn cấu hình cho:
  - WP Mail SMTP
  - FluentSMTP
  - Post SMTP
- [ ] Ghi rõ mapping host/port/security:
  - `smtp.gomail.tld:587` + STARTTLS
  - `smtp.gomail.tld:465` + TLS/SSL
- [ ] Thêm checklist DNS onboarding:
  - A/AAAA record cho relay host
  - SPF record
  - DKIM selector record
  - DMARC khuyến nghị
- [ ] Thêm troubleshooting cơ bản:
  - sai username/password
  - TLS handshake fail
  - domain chưa verified
  - SPF/DKIM chưa đúng

Definition of done:

- [ ] User WordPress có thể tự cấu hình plugin từ docs mà không cần đọc source code.

## Phase 7: Production-Ready Follow-up

Mục tiêu: mở rộng relay khi MVP đã chạy ổn định.

- [ ] Queue outbound jobs thay vì gửi đồng bộ hoàn toàn trong SMTP session.
- [ ] Retry policy cho `4xx` với exponential backoff.
- [ ] Dead-letter queue cho mail retry fail nhiều lần.
- [ ] Bounce/DSN processing.
- [ ] Suppression list cho hard bounce/complaint.
- [ ] Delivery webhooks cho external apps.
- [ ] Deliverability checker cho SPF/DKIM/DMARC/PTR.
- [ ] Outbound metrics dashboard.
- [ ] Domain-level outbound profile.

Definition of done:

- [ ] Relay không còn phụ thuộc hoàn toàn vào synchronous submit path.
- [ ] Có tín hiệu delivery đầy đủ hơn `sent` / `failed`.

## Thứ Tự Coding Đề Xuất

1. Schema + config.
2. API key CRUD + middleware.
3. SMTP submission auth flow.
4. Outbound sender + DKIM.
5. Integration tests.
6. Docs WordPress.
7. Production-ready backlog.

## Definition Of Done Cho MVP

- [ ] User tạo được API key và chỉ thấy full key đúng một lần.
- [ ] API trả đủ SMTP settings cho WordPress plugin.
- [ ] SMTP submission hoạt động với `587` STARTTLS và `465` implicit TLS.
- [ ] Chỉ gửi được từ verified domain của chính owner.
- [ ] Mail outbound được DKIM sign.
- [ ] Có `SentEmailLog` và `ApiKeyUsageLog`.
- [ ] Có test tối thiểu cho CRUD, auth middleware, SMTP auth, quota/domain validation.