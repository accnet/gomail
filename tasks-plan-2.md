# Static Website Hosting Execution Tasks

Tài liệu này chuyển [plan-2.md](/home/accnet/gomail/plan-2.md) thành backlog thực thi cho codebase hiện tại.

## Phân tích nhanh codebase hiện tại

Những phần có thể dùng lại ngay:

- [internal/db/models.go](/home/accnet/gomail/internal/db/models.go) đã có `users`, `domains`, `audit_logs`, soft delete, quota storage.
- [internal/http/handlers/app.go](/home/accnet/gomail/internal/http/handlers/app.go) đã có auth flow, domain API, admin API và quota update cho user.
- [web/main.js](/home/accnet/gomail/web/main.js) và [web/styles.css](/home/accnet/gomail/web/styles.css) đã là SPA shell có thể mở rộng thêm menu `Website`.
- Domain ownership và verify flow đã tồn tại, phù hợp để tái sử dụng cho tab `Domains` của static hosting.

Những phần chưa có và là đường găng:

- Chưa có schema `static_projects` và quota `max_websites`.
- Chưa có pipeline upload ZIP, extract an toàn, detect publish root, publish atomic.
- Chưa có static server resolve `Host -> root_folder`.
- Chưa có Traefik config/runtime cho wildcard subdomain và custom domain activation.
- Chưa có UI `Website`, `Website Settings`, thumbnail worker.

Nguyên tắc triển khai:

- Reuse auth, user, domain, admin quota sẵn có thay vì tạo module riêng song song.
- Tách backlog thành `Phase 1 -> Phase 2 -> Phase 3`; chỉ mở UI rộng sau khi backend publish path ổn định.
- Mỗi phase phải có test hoặc kịch bản verify được.

## Baseline có sẵn

- [x] Auth, user ownership, admin middleware.
- [x] Domain model và domain verification cơ bản.
- [x] Admin quota update endpoint pattern.
- [x] SPA frontend hiện hữu.
- [ ] Static hosting schema và runtime.

## Phase 0: Foundation and Decisions

Mục tiêu: chốt bề mặt tích hợp để implementation không phải sửa vòng lại.

- [ ] Chốt tên module backend mới: `internal/staticprojects`.
- [ ] Chốt storage root cho static sites, ví dụ `./data/static-sites`.
- [ ] Chốt env/config mới:
  - `STATIC_SITES_ROOT`
  - `STATIC_SITES_MAX_ARCHIVE_BYTES`
  - `STATIC_SITES_MAX_EXTRACTED_BYTES`
  - `STATIC_SITES_MAX_FILE_COUNT`
  - `STATIC_SITES_BASE_DOMAIN`
  - `TRAEFIK_DYNAMIC_CONF_DIR`
  - `TRAEFIK_PUBLIC_IP`
  - `STATIC_SERVER_ADDR`
- [ ] Chốt format subdomain random và retry strategy uniqueness.
- [ ] Chốt policy delete: soft delete DB + cleanup filesystem.
- [ ] Chốt policy disabled site: static server trả `404`.
- [ ] Chốt `ui_state` mapping ở backend thay vì để frontend tự suy luận.

Definition of done:

- [ ] Config contract được ghi rõ trong code/config và README.
- [ ] Naming và lifecycle state không còn mơ hồ giữa `status`, `is_active`, `thumbnail_status`, `domain_binding_status`.

## Phase 1: Schema, Config, and Quota

Mục tiêu: thêm schema và quota để mọi flow phía sau có persistence ổn định.

- [x] Thêm field `max_websites` vào model `users` trong [internal/db/models.go](/home/accnet/gomail/internal/db/models.go).
- [x] Thêm seed/default cho `max_websites` trong config admin mặc định ở [internal/config/config.go](/home/accnet/gomail/internal/config/config.go).
- [x] Mở rộng admin quota API ở [internal/http/handlers/app.go](/home/accnet/gomail/internal/http/handlers/app.go) để patch được `max_websites`.
- [x] Thêm model `StaticProject` vào [internal/db/models.go](/home/accnet/gomail/internal/db/models.go) với các field từ plan:
  - `user_id`
  - `name`
  - `subdomain`
  - `domain_id`
  - `assigned_domain`
  - `domain_binding_status`
  - `domain_last_dns_check_at`
  - `domain_last_dns_result`
  - `domain_tls_enabled_at`
  - `root_folder`
  - `staging_folder`
  - `upload_filename`
  - `detected_root`
  - `archive_size_bytes`
  - `file_count`
  - `status`
  - `deploy_error`
  - `thumbnail_path`
  - `thumbnail_status`
  - `is_active`
  - `published_at`
  - `deleted_at`
- [x] Thêm index/constraint:
  - unique `subdomain`
  - unique `domain_id` khi khác `NULL`
  - index `user_id`
  - index `status`
  - index `deleted_at`
- [ ] Nếu cần audit chuẩn hơn, thêm `static_project_events` hoặc reuse `audit_logs` với type prefix `static_project.*`.
- [x] Cập nhật migrate/AutoMigrate wiring để tạo schema mới.
- [ ] Cập nhật fixture/test helpers để tạo user có `max_websites`.

Definition of done:

- [x] User có quota `max_websites`.
- [x] `static_projects` được tạo sạch trên DB trống.
- [x] Admin chỉnh được `max_websites` mà không phá API quota cũ.

## Phase 2: Storage and Safe Publish Pipeline

Mục tiêu: hoàn thành lõi kỹ thuật quan trọng nhất là upload, extract, validate và publish atomic.

- [x] Tạo module [internal/storage/static_sites.go](/home/accnet/gomail/internal/storage/static_sites.go) để quản lý path staging/live/thumbnail.
- [x] Implement helper tạo thư mục:
  - staging `/data/static-sites/staging/{project_id}`
  - live `/data/static-sites/live/{project_id}`
  - thumbnail `/data/static-sites/live/{project_id}/thumbnail.png`
- [x] Implement stream upload ZIP vào file tạm.
- [x] Implement validate archive size theo env.
- [x] Implement validate extracted total size.
- [x] Implement validate max file count.
- [x] Implement zip-slip protection:
  - reject `../`
  - reject path tuyệt đối
  - reject entry ra ngoài staging root
- [x] Reject symlink và file name bất thường.
- [x] Implement detect publish root rule:
  - ưu tiên `index.html` tại root archive
  - fallback nếu đúng 1 thư mục con cấp 1 chứa `index.html`
  - reject nếu nhiều candidate
- [x] Implement static file validator:
  - whitelist extension V1
  - blacklist executable/server-side extension
- [x] Implement file count thống kê và metadata archive.
- [x] Implement publish atomic từ staging sang live.
- [x] Implement rollback/cleanup nếu publish fail nửa chừng.
- [x] Giữ thumbnail cũ trong redeploy cho tới khi thumbnail mới sẵn sàng.

Definition of done:

- [x] ZIP hợp lệ publish được vào live folder.
- [x] ZIP độc hại hoặc ambiguous root bị reject rõ lý do.
- [x] Publish không để lại live folder ở trạng thái nửa vời.

## Phase 3: Static Project Service and User API

Mục tiêu: expose publish lifecycle qua API dùng lại auth/ownership hiện tại.

- [x] Tạo service [internal/staticprojects/service.go](/home/accnet/gomail/internal/staticprojects/service.go).
- [x] Implement quota checker dùng `max_websites`.
- [x] Implement random subdomain generator + uniqueness check.
- [x] Implement mapper `status + is_active + thumbnail_status -> ui_state`.
- [x] Implement deploy flow:
  - tạo record `draft`
  - upload ZIP
  - extract/validate
  - generate subdomain
  - publish
  - update `published` hoặc `publish_failed`
- [x] Implement redeploy flow dùng lại project record hiện có.
- [x] Implement list/detail service theo owner.
- [x] Implement delete flow:
  - soft delete DB
  - cleanup live/staging/thumbnail
  - clear domain binding metadata nếu có
- [x] Implement enable/disable flow bằng `is_active`.
- [x] Implement response quota block:
  - `max_websites`
  - `websites_used`
  - `websites_remaining`
- [x] Thêm handler file [internal/http/handlers/static_projects.go](/home/accnet/gomail/internal/http/handlers/static_projects.go).
- [x] Wire routes vào [internal/http/handlers/app.go](/home/accnet/gomail/internal/http/handlers/app.go):
  - `GET /api/static-projects`
  - `GET /api/static-projects/:id`
  - `POST /api/static-projects/deploy`
  - `POST /api/static-projects/:id/redeploy`
  - `PATCH /api/static-projects/:id/status`
  - `DELETE /api/static-projects/:id`
- [x] Chuẩn hóa JSON error cho các case:
  - `website_quota_exceeded`
  - `invalid_archive`
  - `publish_root_not_found`
  - `multiple_publish_roots`
  - `forbidden_file_type`
  - `publish_failed`

Definition of done:

- [x] User deploy website đầu tiên qua API được.
- [x] User redeploy được mà không tốn thêm quota.
- [x] List API trả đủ dữ liệu card và `ui_state`.

## Phase 4: Domain Binding and Traefik Activation

Mục tiêu: cho phép gán verified email domain của chính user vào website.

- [x] Tạo module [internal/staticprojects/domain_binding.go](/home/accnet/gomail/internal/staticprojects/domain_binding.go).
- [x] Implement `available domains` query chỉ lấy domain verified thuộc owner.
- [x] Implement assign/unassign domain validator:
  - domain thuộc chính user
  - domain đã verified
  - domain chưa bị website khác giữ
- [x] Implement `PATCH /api/static-projects/:id/domain`.
- [x] Implement `GET /api/static-projects/:id/available-domains`.
- [x] Implement DNS A/AAAA checker so với `TRAEFIK_PUBLIC_IP`.
- [x] Implement `POST /api/static-projects/:id/domain/check-ip`.
- [x] Chỉ cho `Active SSL` khi:
  - website đang có assigned domain
  - domain verified
  - check IP pass
- [x] Implement writer tạo file `.yaml` trong `dynamic_conf` cho Traefik file provider.
- [x] Implement `POST /api/static-projects/:id/domain/active-ssl`.
- [x] Khi unassign/delete project, cleanup file config Traefik tương ứng.
- [x] Persist `domain_binding_status`, `domain_last_dns_result`, `domain_tls_enabled_at`.

Definition of done:

- [x] User chỉ gán được domain verified của chính mình.
- [x] `Check IP` trả rõ IP thực tế đang resolve.
- [x] `Active SSL` chỉ chạy khi precondition hợp lệ và tạo được config file.

## Phase 5: Static Server Runtime

Mục tiêu: phục vụ static site theo `Host` qua một binary chung.

- [x] Tạo binary [cmd/static-server/main.go](/home/accnet/gomail/cmd/static-server/main.go).
- [x] Tạo package [internal/staticprojects/host_resolver.go](/home/accnet/gomail/internal/staticprojects/host_resolver.go).
- [x] Implement resolve `Host -> active project` theo:
  - subdomain mặc định
  - assigned custom domain đã active
- [x] Cache lookup nếu cần, nhưng phase đầu cho phép query DB trực tiếp.
- [x] Implement file serving từ `root_folder`.
- [x] Implement rule disabled/deleted project trả `404`.
- [x] Implement SPA fallback:
  - chỉ `GET`
  - `Accept` chứa `text/html`
  - path không có extension asset
  - nếu không thỏa thì trả `404`
- [x] Implement header/caching cơ bản cho static assets.
- [x] Bổ sung health endpoint cho static server.

Definition of done:

- [x] Truy cập host hợp lệ trả file từ đúng project.
- [x] Asset thiếu trả `404`, không fallback sai.
- [x] Route SPA kiểu `/about` fallback về `index.html` đúng điều kiện.

## Phase 6: Docker and Traefik Wiring

Mục tiêu: nối runtime mới vào môi trường deploy hiện tại.

- [x] Bổ sung service `static-server` vào [docker-compose.yaml](/home/accnet/gomail/docker-compose.yaml).
- [x] Bổ sung build/runtime cho static server vào [deploy/docker/Dockerfile](/home/accnet/gomail/deploy/docker/Dockerfile).
- [x] Tạo thư mục deploy cho Traefik config, ví dụ [deploy/docker/traefik](/home/accnet/gomail/deploy/docker).
- [x] Thêm wildcard router cho `{subdomain}.{saas_domain}` trỏ vào `static-server`.
- [x] Mount shared volume cho:
  - live static sites
  - dynamic conf
- [x] Đảm bảo API service ghi được file `dynamic_conf` mà Traefik đọc được.
- [x] Cập nhật prod compose ở [deploy/docker/docker-compose.prod.yaml](/home/accnet/gomail/deploy/docker/docker-compose.prod.yaml).
- [x] Viết tài liệu env/deploy cho Traefik public IP, wildcard DNS, volume mount.

Definition of done:

- [x] Wildcard subdomain route chạy qua Traefik tới static server.
- [x] API và Traefik cùng thấy shared volume cần thiết.

## Phase 7: Frontend Website Management

Mục tiêu: tạo UX đúng với `plan-2.md` nhưng chỉ sau khi backend đã usable.

- [x] Thêm menu `Website` vào SPA trong [web/main.js](/home/accnet/gomail/web/main.js).
- [x] Tạo route hash:
  - `/app/#/websites`
  - `/app/#/websites/:id`
  - `/app/#/websites/:id/upload`
  - `/app/#/websites/:id/domains`
- [x] Implement website grid view:
  - loading
  - empty
  - success
  - failed/deploying overlay
- [x] Implement website card:
  - thumbnail/placeholder
  - subdomain
  - status badge
  - updated/published time
  - action menu
- [x] Implement popup `Deploy New Website` upload ZIP.
- [x] Implement `Website Settings` page.
- [x] Implement tab `Overview`.
- [x] Implement tab `Upload New Version`.
- [x] Implement tab `Domains`.
- [x] Render quota `used/max` ở màn `Website`.
- [x] Render state theo `ui_state` backend, không tự đoán từ raw fields.
- [x] Disable action `Open` với card `deploying`, `failed`, `disabled` theo policy.
- [x] Hiển thị lỗi deploy ngắn từ `deploy_error`.
- [x] Giữ responsive desktop/mobile cơ bản.
- [x] Bổ sung style card, badge, tab, overlay trong [web/styles.css](/home/accnet/gomail/web/styles.css).

Definition of done:

- [ ] User có thể deploy, xem card, mở settings, redeploy, disable/enable, delete từ UI.
- [x] UI không cần reload cứng để chuyển giữa grid và settings page.

## Phase 8: Thumbnail Worker and Background Jobs

Mục tiêu: hoàn thiện phần preview nhưng không block publish.

- [x] Chốt execution model cho thumbnail worker:
  - goroutine nội bộ
  - queue đơn giản
  - hoặc command riêng nếu cần
- [x] Implement enqueue sau publish thành công.
- [x] Chỉ generate thumbnail khi `published` và `is_active = true`.
- [x] Implement timeout, retry giới hạn, viewport cố định.
- [x] Update `thumbnail_status` thành `ready` hoặc `failed`.
- [x] Nếu fail, không làm website rơi khỏi state `live`.
- [x] Nếu redeploy, giữ thumbnail cũ tới khi ảnh mới sẵn sàng.

Definition of done:

- [x] Publish không bị block bởi thumbnail.
- [x] Card live hiển thị preview khi worker chạy xong.

## Phase 9: Audit, Observability, and Hardening

Mục tiêu: bổ sung dấu vết vận hành và tránh blind spots khi lỗi production.

- [x] Ghi audit log cho các action:
  - `static_project.create`
  - `static_project.upload`
  - `static_project.publish_success`
  - `static_project.publish_failed`
  - `static_project.domain_assign`
  - `static_project.domain_unassign`
  - `static_project.domain_check_ip`
  - `static_project.ssl_activate`
  - `static_project.disable`
  - `static_project.enable`
  - `static_project.delete`
  - `user.website_quota`
- [ ] Bổ sung structured logs cho upload, extract, publish, host resolve, Traefik config write.
- [ ] Thêm metric/counter cơ bản nếu repo đã có chỗ gắn monitoring.
- [ ] Chuẩn hóa cleanup khi deploy fail giữa chừng.
- [ ] Review permission filesystem cho thư mục static sites và `dynamic_conf`.

Definition of done:

- [ ] Có log/audit đủ để lần ra deploy fail, domain activation fail, host serve sai.

## Phase 10: Test Backlog

Unit tests:

- [x] Zip path validation.
- [x] Archive size/file-count validation.
- [x] Publish root detection.
- [x] Extension whitelist/blacklist.
- [ ] Subdomain generator uniqueness retry.
- [x] `ui_state` mapper.
- [x] Website quota checker.
- [x] Domain assignment validator.
- [x] Domain IP checker.
- [x] SSL activation precondition checker.
- [x] Host resolver.
- [x] SPA fallback logic.

Integration tests:

- [x] Deploy ZIP hợp lệ -> publish -> DB update.
- [x] ZIP ở root publish thành công.
- [x] ZIP trong đúng 1 thư mục con publish thành công.
- [x] ZIP có nhiều candidate root bị reject.
- [x] ZIP có path traversal bị reject.
- [x] ZIP có file cấm bị reject.
- [x] Vượt `max_websites` bị reject.
- [x] Assign verified domain của chính user thành công.
- [x] Assign pending domain hoặc domain user khác bị reject.
- [x] `Check IP` fail khi domain chưa trỏ đúng.
- [x] `Active SSL` fail khi chưa pass `Check IP`.
- [x] `Active SSL` thành công thì host custom domain serve đúng website.
- [x] Disabled project không serve.
- [x] Delete project cleanup file và metadata.
- [x] List API trả đúng `ui_state`.

Manual E2E:

- [ ] Mở `Website` grid.
- [ ] Deploy ZIP đơn giản.
- [ ] Mở `Website Settings`.
- [ ] Redeploy bản mới.
- [ ] Gán domain từ danh sách domain verified.
- [ ] `Check IP`.
- [ ] `Active SSL`.
- [ ] Mở subdomain mặc định.
- [ ] Mở custom domain qua HTTPS.
- [x] Thumbnail xuất hiện.
- [ ] Disable rồi enable lại.
- [ ] Delete project.

## Critical Path đề xuất

Thứ tự nên làm để giảm rework:

1. [ ] Phase 1: schema + `max_websites` + admin quota.
2. [ ] Phase 2: safe ZIP pipeline + publish atomic.
3. [ ] Phase 3: deploy/list/detail/delete/redeploy API.
4. [ ] Phase 5: static server runtime.
5. [ ] Phase 6: Docker + Traefik wildcard route.
6. [ ] Phase 7: UI `Website` + `Website Settings`.
7. [ ] Phase 4: domain binding + `Check IP` + `Active SSL`.
8. [ ] Phase 8: thumbnail worker.
9. [ ] Phase 9 và Phase 10: hardening + test closure.

Lý do:

- Không nên làm `Check IP` hay `Active SSL` trước khi subdomain mặc định serve ổn định.
- Không nên làm thumbnail trước khi publish lifecycle cố định.
- UI nên bám response `ui_state` của backend để tránh duplicate state machine.

## Task Breakdown theo ticket/PR

Mục tiêu của phần này là chia backlog lớn thành các task đủ nhỏ để giao việc hoặc làm tuần tự mà không bị chồng chéo quá nhiều.

Quy ước:

- Mỗi task nên gói trong 1 PR nhỏ.
- Nếu task có `Depends on`, không nên làm trước task đó.
- `Verify` là bước kiểm tra tối thiểu để đóng task.

### Stream A: Schema và quota

- [x] T01. Thêm `max_websites` vào `User` model. Depends on: none. Output: field mới trong [internal/db/models.go](/home/accnet/gomail/internal/db/models.go). Verify: user record load/save được với field mới.
- [x] T02. Thêm config default/seed cho `max_websites`. Depends on: T01. Output: config/admin seed ở [internal/config/config.go](/home/accnet/gomail/internal/config/config.go). Verify: app boot không lỗi khi có env mặc định.
- [x] T03. Mở rộng admin quota API để patch `max_websites`. Depends on: T01, T02. Output: handler ở [internal/http/handlers/app.go](/home/accnet/gomail/internal/http/handlers/app.go). Verify: `PATCH /api/admin/users/:id/quotas` nhận `max_websites`.
- [x] T04. Thêm model `StaticProject`. Depends on: T01. Output: struct mới trong [internal/db/models.go](/home/accnet/gomail/internal/db/models.go). Verify: schema migrate được.
- [x] T05. Thêm index/constraint cho `static_projects`. Depends on: T04. Output: unique/index đúng cho `subdomain`, `domain_id`, `user_id`, `status`, `deleted_at`. Verify: DB tạo index sạch trên database trống.
- [x] T06. Wire migration/AutoMigrate cho `StaticProject`. Depends on: T04, T05. Output: bootstrap DB biết tạo bảng mới. Verify: test migration hoặc app start tạo đủ bảng.
- [ ] T07. Cập nhật test helper/fixture có `max_websites`. Depends on: T01, T06. Output: helper tạo user/project dùng được trong test mới. Verify: test integration không phải hardcode quota thủ công.

### Stream B: Config và storage groundwork

- [x] T08. Khai báo config cho static hosting. Depends on: none. Output: các env `STATIC_SITES_*`, `TRAEFIK_*`, `STATIC_SERVER_ADDR`. Verify: config load và validate được.
- [x] T09. Tạo storage helper sinh path staging/live/thumbnail. Depends on: T08. Output: file [internal/storage/static_sites.go](/home/accnet/gomail/internal/storage/static_sites.go). Verify: unit test path output đúng theo `project_id`.
- [x] T10. Implement tạo và cleanup thư mục project. Depends on: T09. Output: helper `EnsureProjectDirs`, `CleanupProjectDirs` hoặc tương đương. Verify: tạo/xóa staging-live chạy đúng trên disk tạm.

### Stream C: ZIP safety

- [x] T11. Implement stream upload ZIP vào file tạm. Depends on: T08, T09. Output: helper nhận multipart/file stream và ghi tạm. Verify: file upload mẫu được ghi đầy đủ và đo được size.
- [x] T12. Implement archive size guard. Depends on: T11. Output: reject khi ZIP vượt `STATIC_SITES_MAX_ARCHIVE_BYTES`. Verify: test upload oversized trả lỗi đúng.
- [x] T13. Implement zip-slip protection. Depends on: T11. Output: reject `../`, path tuyệt đối, entry thoát staging root. Verify: test archive độc hại bị reject.
- [x] T14. Reject symlink và tên file bất thường. Depends on: T11. Output: validator archive entry. Verify: test symlink/null-byte bị reject.
- [x] T15. Implement extracted size và file-count guard. Depends on: T11. Output: theo dõi total uncompressed bytes và file count khi extract. Verify: zip bomb giả lập bị reject.
- [x] T16. Implement extension whitelist/blacklist validator. Depends on: T11. Output: validator file static hợp lệ. Verify: file `.php` hoặc `.sh` bị reject, `.html/.css/.js` pass.

### Stream D: Detect root và publish atomic

- [x] T17. Implement detect publish root tại root archive. Depends on: T13, T14, T15, T16. Output: detect `index.html` tại root. Verify: ZIP có `index.html` ở root được nhận diện đúng.
- [x] T18. Implement detect publish root trong đúng 1 thư mục con cấp 1. Depends on: T17. Output: fallback nested-root hợp lệ. Verify: ZIP một-folder deploy được.
- [x] T19. Reject nhiều candidate publish root. Depends on: T17, T18. Output: lỗi rõ cho archive ambiguous. Verify: ZIP có 2 folder đều chứa `index.html` bị reject.
- [x] T20. Implement publish atomic staging -> live. Depends on: T09, T10, T17, T18, T19. Output: live folder được swap an toàn. Verify: publish thành công không để trạng thái nửa vời.
- [x] T21. Implement rollback/cleanup khi publish fail. Depends on: T20. Output: cleanup staging/live tạm khi lỗi. Verify: simulate publish fail không để file rác.

### Stream E: Service layer

- [x] T22. Tạo package service `internal/staticprojects`. Depends on: T04, T08, T20. Output: skeleton service/repository methods. Verify: compile pass.
- [x] T23. Implement website quota checker. Depends on: T01, T22. Output: check số project chưa delete của user so với `max_websites`. Verify: user vượt quota bị block.
- [x] T24. Implement subdomain generator + uniqueness retry. Depends on: T04, T22. Output: helper sinh subdomain random. Verify: collision giả lập vẫn sinh được giá trị mới.
- [x] T25. Implement `ui_state` mapper. Depends on: T04, T22. Output: mapping `deploying/live/failed/disabled`. Verify: unit test đủ các state chính.
- [x] T26. Implement deploy use case end-to-end trong service. Depends on: T11 đến T25. Output: method deploy tạo record, extract, validate, publish, update DB. Verify: test service deploy ZIP hợp lệ thành công.
- [x] T27. Implement list/detail use case. Depends on: T25, T26. Output: query theo owner kèm quota block. Verify: response có `ui_state` và metadata card.
- [x] T28. Implement redeploy use case. Depends on: T26, T27. Output: dùng lại project record, giữ quota usage cũ. Verify: redeploy không tăng usage.
- [x] T29. Implement delete use case. Depends on: T10, T21, T27. Output: soft delete + cleanup filesystem. Verify: project biến mất khỏi list và file được xóa.
- [x] T30. Implement enable/disable use case. Depends on: T27. Output: toggle `is_active`. Verify: status đổi đúng và phản ánh sang `ui_state`.

### Stream F: User API

- [x] T31. Tạo handler file cho static projects. Depends on: T22. Output: file [internal/http/handlers/static_projects.go](/home/accnet/gomail/internal/http/handlers/static_projects.go). Verify: compile pass.
- [x] T32. Wire route `GET /api/static-projects`. Depends on: T27, T31. Output: list endpoint. Verify: authenticated user chỉ thấy project của mình.
- [x] T33. Wire route `GET /api/static-projects/:id`. Depends on: T27, T31. Output: detail endpoint. Verify: owner access pass, non-owner bị chặn.
- [x] T34. Wire route `POST /api/static-projects/deploy`. Depends on: T26, T31. Output: deploy endpoint multipart/upload. Verify: API deploy được ZIP hợp lệ.
- [x] T35. Wire route `POST /api/static-projects/:id/redeploy`. Depends on: T28, T31. Output: redeploy endpoint. Verify: API redeploy thành công.
- [x] T36. Wire route `PATCH /api/static-projects/:id/status`. Depends on: T30, T31. Output: enable/disable endpoint. Verify: toggle được project của owner.
- [x] T37. Wire route `DELETE /api/static-projects/:id`. Depends on: T29, T31. Output: delete endpoint. Verify: delete cleanup đúng.
- [x] T38. Chuẩn hóa error code cho static hosting API. Depends on: T32 đến T37. Output: error JSON ổn định. Verify: các case quota/archive/root lỗi trả code đúng.

### Stream G: Static server

- [x] T39. Tạo binary [cmd/static-server/main.go](/home/accnet/gomail/cmd/static-server/main.go). Depends on: T08. Output: server boot với config riêng. Verify: binary build/start được.
- [x] T40. Implement host resolver theo subdomain mặc định. Depends on: T04, T39. Output: resolve `Host -> project`. Verify: host hợp lệ trả đúng project active.
- [x] T41. Implement file serving theo `root_folder`. Depends on: T20, T40. Output: serve file index và asset. Verify: request vào host trả nội dung đúng.
- [x] T42. Implement `404` cho project disabled/deleted. Depends on: T30, T40, T41. Output: guard trước khi serve. Verify: project disabled không truy cập được.
- [x] T43. Implement SPA fallback có điều kiện. Depends on: T41. Output: fallback chỉ cho HTML navigation. Verify: `/about` fallback được nhưng asset thiếu vẫn `404`.
- [x] T44. Thêm health endpoint và cache header cơ bản. Depends on: T39, T41. Output: `/healthz` và header static assets. Verify: health pass, asset có cache-control.

### Stream H: Docker và routing

- [x] T45. Bổ sung service `static-server` vào [docker-compose.yaml](/home/accnet/gomail/docker-compose.yaml). Depends on: T39. Output: local compose chạy thêm static server. Verify: compose up start được service mới.
- [x] T46. Cập nhật [deploy/docker/Dockerfile](/home/accnet/gomail/deploy/docker/Dockerfile) để build static server. Depends on: T39. Output: image build được binary mới. Verify: docker build pass.
- [x] T47. Thêm wildcard route cho subdomain hệ thống. Depends on: T40, T45, T46. Output: Traefik/reverse-proxy config route tới static server. Verify: subdomain dev/local mở được site.
- [x] T48. Mount shared volume cho live sites và dynamic conf. Depends on: T45. Output: API, Traefik, static server cùng thấy volume cần thiết. Verify: file publish và config có thể đọc chéo.

### Stream I: Domain binding

- [x] T49. Implement query available domains của owner. Depends on: T04, domain API hiện có. Output: list verified domain thuộc user. Verify: domain user khác không xuất hiện.
- [x] T50. Implement assign/unassign validator. Depends on: T49. Output: check owner, verified, conflict. Verify: domain pending hoặc đang bị giữ bị reject.
- [x] T51. Wire `PATCH /api/static-projects/:id/domain`. Depends on: T50, T31. Output: assign/unassign endpoint. Verify: gán domain verified thành công.
- [x] T52. Wire `GET /api/static-projects/:id/available-domains`. Depends on: T49, T31. Output: endpoint cho tab Domains. Verify: frontend lấy được danh sách.
- [x] T53. Implement `Check IP` so với `TRAEFIK_PUBLIC_IP`. Depends on: T08, T50. Output: resolver A/AAAA + compare result. Verify: trả pass/fail và IP resolve thực tế.
- [x] T54. Wire `POST /api/static-projects/:id/domain/check-ip`. Depends on: T53, T31. Output: check IP endpoint. Verify: API phản hồi đúng trạng thái.
- [x] T55. Implement writer tạo file Traefik dynamic config. Depends on: T08, T48, T50. Output: `.yaml` writer cho custom domain. Verify: file được tạo đúng thư mục.
- [x] T56. Wire `POST /api/static-projects/:id/domain/active-ssl`. Depends on: T53, T55, T31. Output: active SSL endpoint. Verify: chưa pass `Check IP` thì bị chặn; pass rồi thì tạo config file.

### Stream J: Frontend

- [x] T57. Thêm menu và route `Website`. Depends on: T32, T33. Output: route hash mới trong [web/main.js](/home/accnet/gomail/web/main.js). Verify: điều hướng tới màn website được.
- [x] T58. Tạo grid view + fetch list API. Depends on: T32, T57. Output: loading/empty/success states. Verify: grid render danh sách project.
- [x] T59. Tạo website card render theo `ui_state`. Depends on: T25, T58. Output: card có badge, subdomain, thumbnail placeholder. Verify: các state chính hiển thị đúng.
- [x] T60. Tạo modal `Deploy New Website`. Depends on: T34, T58. Output: upload ZIP từ UI. Verify: deploy từ UI tạo project mới.
- [x] T61. Tạo trang `Website Settings` + tab `Overview`. Depends on: T33, T57. Output: detail view cơ bản. Verify: mở từng project xem metadata được.
- [x] T62. Tạo tab `Upload New Version`. Depends on: T35, T61. Output: redeploy UI. Verify: upload bản mới từ settings thành công.
- [x] T63. Tạo tab `Domains`. Depends on: T52, T54, T56, T61. Output: assign domain, check IP, active SSL từ UI. Verify: thao tác domain binding chạy end-to-end.
- [x] T64. Bổ sung CSS cho grid/card/tab/overlay. Depends on: T58 đến T63. Output: style ở [web/styles.css](/home/accnet/gomail/web/styles.css). Verify: layout usable trên desktop/mobile cơ bản.

### Stream K: Thumbnail, audit, hardening

- [x] T65. Chốt execution model thumbnail worker. Depends on: T26, T39. Output: chọn goroutine nội bộ hoặc worker riêng. Verify: tài liệu/contract rõ.
- [x] T66. Implement enqueue và generate thumbnail. Depends on: T65. Output: worker tạo `thumbnail.png` cho project live. Verify: publish xong sinh thumbnail thành công.
- [x] T67. Persist `thumbnail_status` và giữ state `live` khi thumbnail fail. Depends on: T66, T25. Output: logic state phụ cho thumbnail. Verify: fail thumbnail không biến thành deploy fail.
- [x] T68. Ghi audit log cho static project actions. Depends on: T26 đến T30, T51, T54, T56. Output: log type `static_project.*`. Verify: DB có audit record cho action chính.
- [ ] T69. Bổ sung structured logs cho upload/publish/resolve host. Depends on: T26, T40, T55. Output: log phục vụ vận hành. Verify: log có project id, host, status before/after.

### Stream L: Test closure

- [x] T70. Viết unit test cho ZIP validators. Depends on: T12 đến T19. Output: test path, size, file-count, extension, root detection. Verify: suite pass.
- [x] T71. Viết unit test cho service helpers. Depends on: T23, T24, T25, T53. Output: quota, subdomain, ui_state, check IP tests. Verify: suite pass.
- [x] T72. Viết integration test cho deploy/list/redeploy/delete API. Depends on: T32 đến T38. Output: API lifecycle tests. Verify: suite pass.
- [x] T73. Viết integration test cho static server host resolve và SPA fallback. Depends on: T40 đến T44. Output: host serve tests. Verify: suite pass.
- [x] T74. Viết integration test cho domain binding và active SSL. Depends on: T51 đến T56. Output: assign/check-ip/ssl tests. Verify: suite pass.

## Sprint 1 breakdown chi tiết

Nếu chỉ làm sprint đầu tiên để có bản chạy được end-to-end, nên khóa phạm vi vào 14 task sau:

- [x] S1-01 = T01
- [x] S1-02 = T02
- [x] S1-03 = T03
- [x] S1-04 = T04
- [x] S1-05 = T05
- [x] S1-06 = T06
- [x] S1-07 = T08
- [x] S1-08 = T09
- [x] S1-09 = T11
- [x] S1-10 = T13
- [x] S1-11 = T15
- [x] S1-12 = T17
- [x] S1-13 = T20
- [x] S1-14 = T22
- [x] S1-15 = T23
- [x] S1-16 = T24
- [x] S1-17 = T25
- [x] S1-18 = T26
- [x] S1-19 = T31
- [x] S1-20 = T32
- [x] S1-21 = T34
- [x] S1-22 = T39
- [x] S1-23 = T40
- [x] S1-24 = T41
- [x] S1-25 = T45
- [x] S1-26 = T47
- [x] S1-27 = T57
- [x] S1-28 = T58
- [x] S1-29 = T59
- [x] S1-30 = T60

Điểm dừng của Sprint 1:

- [x] Upload một ZIP đơn giản qua UI hoặc API.
- [x] Backend publish thành công sang live folder.
- [x] Hệ thống sinh subdomain unique.
- [x] Static server serve được site qua subdomain mặc định.
- [x] Grid `Website` hiển thị project vừa deploy.

## PR Plan cho T01-T30

Phần này gom `T01-T30` thành các PR backend có thể triển khai tuần tự.

Nguyên tắc chia PR:

- Mỗi PR chỉ giải một lát cắt kỹ thuật rõ ràng.
- Không trộn schema, file IO, service orchestration vào cùng một PR nếu chưa cần.
- Mỗi PR phải để lại hệ thống ở trạng thái compile được và có điểm verify cụ thể.

### PR-01: User quota groundwork

Bao gồm:

- [x] T01
- [x] T02
- [x] T03
- [ ] T07

Phạm vi:

- Thêm `max_websites` vào user model.
- Wire config mặc định/seed cho `max_websites`.
- Mở rộng admin quota API hiện có để patch field mới.
- Cập nhật fixture/test helper liên quan tới user quota.

Không làm trong PR này:

- Chưa thêm `static_projects`.
- Chưa có upload/publish flow.

Verify:

- [x] App boot với config mới không lỗi.
- [x] `PATCH /api/admin/users/:id/quotas` nhận `max_websites`.
- [ ] Test/helper tạo user có quota mới chạy được.

### PR-02: Static project schema

Bao gồm:

- [x] T04
- [x] T05
- [x] T06

Phạm vi:

- Thêm model `StaticProject`.
- Thêm unique/index/constraint cần thiết.
- Wire migrate/AutoMigrate cho bảng mới.

Không làm trong PR này:

- Chưa có business logic deploy.
- Chưa có API static projects.

Verify:

- [x] App tạo được bảng `static_projects` trên DB trống.
- [x] Constraint `subdomain` và `domain_id` hoạt động đúng.

### PR-03: Static hosting config và path helpers

Bao gồm:

- [x] T08
- [x] T09
- [x] T10

Phạm vi:

- Thêm env/config cho static hosting.
- Tạo helper sinh path staging/live/thumbnail.
- Tạo helper tạo và cleanup thư mục project.

Không làm trong PR này:

- Chưa parse ZIP.
- Chưa publish sang live.

Verify:

- [x] Config load/validate được.
- [x] Unit test path helper pass.
- [x] Tạo/xóa thư mục project chạy đúng trên disk tạm.

### PR-04: ZIP ingest guardrails

Bao gồm:

- [x] T11
- [x] T12
- [x] T13
- [x] T14
- [x] T15
- [x] T16

Phạm vi:

- Stream upload ZIP vào file tạm.
- Validate archive size, extracted size, file count.
- Chặn zip-slip, symlink, tên file bất thường.
- Validate whitelist/blacklist extension.

Không làm trong PR này:

- Chưa detect publish root.
- Chưa publish sang live.

Verify:

- [x] Upload ZIP mẫu được ghi ra file tạm.
- [x] ZIP oversized, zip-slip, symlink, file cấm đều bị reject.

### PR-05: Publish root detection

Bao gồm:

- [x] T17
- [x] T18
- [x] T19

Phạm vi:

- Detect `index.html` ở root archive.
- Fallback cho đúng 1 thư mục con cấp 1.
- Reject archive có nhiều candidate root.

Không làm trong PR này:

- Chưa publish atomic.
- Chưa ghi DB lifecycle.

Verify:

- [x] ZIP root-site pass.
- [x] ZIP single-folder pass.
- [x] ZIP ambiguous bị reject với lỗi rõ ràng.

### PR-06: Atomic publish primitives

Bao gồm:

- [x] T20
- [x] T21

Phạm vi:

- Publish từ staging sang live.
- Cleanup/rollback khi publish fail.

Không làm trong PR này:

- Chưa có orchestration cấp service.
- Chưa có API deploy.

Verify:

- [x] Publish thành công tạo live folder usable.
- [x] Simulate lỗi giữa chừng không để file rác hoặc live state nửa vời.

### PR-07: Static project service skeleton

Bao gồm:

- [x] T22
- [x] T23
- [x] T24
- [x] T25

Phạm vi:

- Tạo package `internal/staticprojects`.
- Implement quota checker.
- Implement subdomain generator.
- Implement `ui_state` mapper.

Không làm trong PR này:

- Chưa có deploy end-to-end.
- Chưa wire HTTP routes.

Verify:

- [x] Package compile pass.
- [x] Unit test quota/subdomain/ui_state pass.

### PR-08: Deploy orchestration service

Bao gồm:

- [x] T26

Phạm vi:

- Orchestrate create record -> upload -> extract -> validate -> generate subdomain -> publish -> update DB.

Không làm trong PR này:

- Chưa có list/detail/delete/redeploy.
- Chưa có HTTP endpoint.

Verify:

- [x] Service deploy ZIP hợp lệ thành công.
- [x] Deploy fail cập nhật `publish_failed` và `deploy_error` đúng.

### PR-09: Read/delete/toggle service flows

Bao gồm:

- [x] T27
- [x] T29
- [x] T30

Phạm vi:

- List/detail theo owner.
- Delete flow với soft delete + cleanup filesystem.
- Enable/disable flow bằng `is_active`.

Không làm trong PR này:

- Chưa có redeploy.
- Chưa có HTTP endpoints.

Verify:

- [x] Query trả đủ card metadata và `ui_state`.
- [x] Delete cleanup đúng DB và filesystem.
- [x] Toggle active đổi state đúng.

### PR-10: Redeploy service flow

Bao gồm:

- [x] T28

Phạm vi:

- Upload phiên bản mới cho project hiện có.
- Publish lại nhưng giữ nguyên quota usage.

Không làm trong PR này:

- Chưa wire HTTP endpoint.

Verify:

- [x] Redeploy thành công không tạo project record mới.
- [x] Thumbnail/path cũ không bị xử lý sai khi publish lại.

### PR-11: Static project HTTP skeleton

Bao gồm:

- [x] T31
- [x] T38

Phạm vi:

- Tạo handler file cho static projects.
- Chuẩn hóa error code cho module này.

Không làm trong PR này:

- Chưa wire đủ tất cả endpoints.

Verify:

- [x] Handler compile pass.
- [x] Error mapping cho quota/archive/root/publish được định nghĩa rõ.

### PR-12: Read APIs cho static projects

Bao gồm:

- [x] T32
- [x] T33

Phạm vi:

- `GET /api/static-projects`
- `GET /api/static-projects/:id`

Không làm trong PR này:

- Chưa có deploy/redeploy/delete endpoints.

Verify:

- [x] Owner chỉ thấy project của mình.
- [x] Detail endpoint block non-owner.

### PR-13: Deploy và mutate APIs

Bao gồm:

- [x] T34
- [x] T36
- [x] T37

Phạm vi:

- Deploy endpoint.
- Enable/disable endpoint.
- Delete endpoint.

Không làm trong PR này:

- Chưa có redeploy API.

Verify:

- [x] API deploy ZIP hợp lệ thành công.
- [x] Toggle/delete endpoint chạy đúng theo owner.

### PR-14: Redeploy API

Bao gồm:

- [x] T35

Phạm vi:

- `POST /api/static-projects/:id/redeploy`.

Không làm trong PR này:

- Chưa có domain binding.

Verify:

- [x] API redeploy thành công và không tăng usage/quota.

## Merge order đề xuất cho backend T01-T30

1. [ ] PR-01
2. [ ] PR-02
3. [ ] PR-03
4. [ ] PR-04
5. [ ] PR-05
6. [ ] PR-06
7. [ ] PR-07
8. [ ] PR-08
9. [ ] PR-09
10. [ ] PR-10
11. [ ] PR-11
12. [ ] PR-12
13. [ ] PR-13
14. [ ] PR-14

Lý do:

- Tách phần schema/config ra trước để service layer không phải sửa type giữa chừng.
- Tách ZIP safety, root detection, atomic publish thành 3 PR khác nhau để review dễ hơn.
- Tách service orchestration khỏi HTTP wiring để test logic độc lập trước.
- Tách redeploy riêng vì đây là flow dễ phát sinh bug rollback/path reuse.

## First Execution Sprint

Sprint đầu tiên nên chỉ tập trung vào xương sống có thể chạy được end-to-end:

- [x] Thêm `max_websites` vào user model, config, admin quota API.
- [x] Thêm schema `static_projects`.
- [x] Tạo storage helper cho staging/live.
- [x] Implement safe ZIP extract + detect publish root + publish atomic.
- [x] Implement deploy API `POST /api/static-projects/deploy`.
- [x] Implement list API `GET /api/static-projects` với `ui_state`.
- [x] Tạo `cmd/static-server` serve theo subdomain mặc định.
- [x] Wire wildcard route local/dev qua Docker hoặc reverse proxy hiện có.
- [x] Tạo UI grid tối thiểu để deploy và mở subdomain.

Sprint 1 definition of done:

- [x] User deploy được một ZIP hợp lệ.
- [x] Hệ thống sinh subdomain random unique.
- [x] Truy cập subdomain thấy site live.
- [x] Grid `Website` hiển thị card của project vừa deploy.
