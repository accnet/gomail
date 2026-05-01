## Static Site Hosting SaaS Plan

Mục tiêu: cho user upload một file ZIP chứa site tĩnh, publish lên subdomain riêng, có thumbnail preview, có thể bật/tắt/xóa site an toàn.

Plan này được viết theo hướng có thể triển khai trực tiếp trong codebase hiện tại:
- dùng lại `users` và auth hiện có
- thêm module `static_projects`
- không cho chạy code server-side
- ưu tiên publish an toàn và rollback rõ ràng

### 1. Scope và giả định

In scope:
- Upload file ZIP của static site
- Extract vào storage server-side
- Parse source folder trong ZIP
- Publish lên subdomain ngẫu nhiên `{random}.{saas_domain}`
- Toggle active/inactive
- Generate thumbnail sau khi publish
- Delete project và cleanup file/config

Out of scope V1:
- Build framework app như Next/Nuxt/Astro
- SSR hoặc serverless functions
- CDN invalidation
- Malware scanning mức enterprise

Giả định:
- Hệ thống hiện có user/auth/admin trong cùng database
- Traefik chạy trong Docker và là entrypoint reverse proxy
- Có một static server viết bằng Go để phục vụ file tĩnh

### 2. UI/UX trong app

Menu sidebar cho chức năng này là: `Website`.

Màn `Website` hiển thị dạng grid thumbnail:
- mỗi website là một ô card
- card hiển thị thumbnail preview, subdomain, status, thời gian cập nhật
- action tối thiểu: open, edit website, disable/enable, delete

Card layout đề xuất:
- vùng ảnh thumbnail tỷ lệ cố định, ưu tiên `16:10`
- góc trên phải của card có menu action
- phần dưới thumbnail hiển thị:
  - `name` hoặc fallback là `subdomain`
  - `subdomain` đầy đủ
  - status badge
  - `updated_at` hoặc `published_at`
- nếu chưa có thumbnail:
  - hiển thị placeholder với trạng thái hiện tại, không để card trống

Từ card có nút `Edit Website`.

Khi bấm `Edit Website`, mở trang `Website Settings` của site đó.
Trang này dùng tab layout, ít nhất gồm 3 tab:

1. `Overview`
- hiển thị thumbnail, subdomain hiện tại, domain đang gán, status, thời gian publish
- hiển thị thông tin source gần nhất:
  - `upload_filename`
  - `archive_size_bytes`
  - `file_count`
  - `detected_root`
- action tại tab này:
  - `Open`
  - `Disable/Enable`
  - `Delete`

2. `Upload New Version`
- cho phép upload ZIP mới để redeploy
- flow parse/publish giống deploy ban đầu
- hiển thị progress, parse result, deploy error nếu có
- sau khi redeploy xong, quay lại `Overview` hoặc refresh dữ liệu tab

3. `Domains`
- cho phép gán domain cho website từ danh sách email domain hiện có của user
- chỉ hiển thị domain thuộc chính user
- domain chưa verified thì disable, không cho gán
- cho phép chọn:
  - chỉ dùng subdomain ngẫu nhiên mặc định
  - hoặc gán thêm một domain từ danh sách email domain
- nếu domain đã gán cho website khác thì phải reject rõ ràng

Tab `Domains` cần có thêm 2 action:
- `Check IP`
  - backend kiểm tra DNS A/AAAA của domain đang trỏ đúng về public IP của Traefik hay chưa
  - public IP này được đọc từ config `.env`
  - hiển thị kết quả pass/fail và giá trị IP thực tế đang resolve
- `Active SSL`
  - chỉ enable khi domain đã verified và `Check IP` pass
  - chỉ khi user bấm nút này, backend mới ghi file `.yaml` vào thư mục `dynamic_conf` của Traefik
  - file `.yaml` này dùng cho Traefik file provider để kích hoạt route và Let's Encrypt cho domain đó
  - sau khi `Active SSL` thành công, domain bắt đầu serve website qua HTTPS

Góc phải phía trên của màn có nút `Deploy New Website`.

Khi bấm nút này, mở popup:
- upload 1 file ZIP chứa mã nguồn
- hiển thị tên file, dung lượng, progress, lỗi parse nếu có
- không yêu cầu user nhập slug ở V1

Sau khi backend parse archive thành công:
- tự xác định thư mục publish root
- tự tạo `subdomain` ngẫu nhiên
- publish website
- quay lại grid và hiển thị card mới với trạng thái deploy

Grid nên hỗ trợ các state:
- loading: đang fetch danh sách website
- empty: chưa có website nào, vẫn giữ nút `Deploy New Website`
- success: render card grid
- deploying overlay: card mới hoặc card redeploy đang chạy background
- error: card có lỗi deploy, hiển thị rõ lỗi gần nhất

Trang `Website Settings` cũng nên có URL riêng để refresh không mất context, ví dụ:
- `/app/#/websites`
- `/app/#/websites/:id`
- `/app/#/websites/:id/upload`
- `/app/#/websites/:id/domains`

### 3. Trạng thái card UI

Frontend không nên hiển thị trực tiếp raw status từ DB mà nên map về các state dễ hiểu:

1. `deploying`
- điều kiện:
  - `status` là `draft`, `uploaded`, hoặc `extracting`
- hiển thị:
  - badge `Deploying`
  - thumbnail placeholder/skeleton
  - action cho phép `delete`
  - không cho `open`

2. `live`
- điều kiện:
  - `status = published`
  - `is_active = true`
- hiển thị:
  - badge `Live`
  - action `Open`, `Edit Website`, `Disable`, `Delete`
  - nếu `thumbnail_status = pending`, vẫn coi là live nhưng thumbnail có thể là placeholder

3. `failed`
- điều kiện:
  - `status = publish_failed`
- hiển thị:
  - badge `Failed`
  - text lỗi ngắn lấy từ `deploy_error`
  - action `Edit Website`, `Delete`
  - không cho `open`

4. `disabled`
- điều kiện:
  - `status = disabled` hoặc `is_active = false`
- hiển thị:
  - badge `Disabled`
  - có thể giữ thumbnail cũ để user nhận diện
  - action `Open` có thể ẩn hoặc disabled
  - action `Enable`, `Edit Website`, `Delete`

5. `thumbnail_failed`
- đây không phải lifecycle chính của website, mà là state phụ của card
- điều kiện:
  - website `live`
  - `thumbnail_status = failed`
- hiển thị:
  - website vẫn `Live`
  - thumbnail fallback placeholder
  - không coi là deploy lỗi

Luật mapping quan trọng:
- ưu tiên hiển thị `Failed` nếu `status = publish_failed`
- ưu tiên hiển thị `Disabled` nếu site đã published nhưng bị tắt
- `thumbnail_status` không được làm thay đổi lifecycle chính của website

Domain binding state trong tab `Domains` nên tách riêng khỏi lifecycle deploy:
- `unassigned`
- `dns_pending`
- `dns_ready`
- `activating`
- `active`
- `activation_failed`

### 4. Kiến trúc runtime

Thành phần:
- `API service` Go: auth, upload, metadata, publish orchestration
- `PostgreSQL`: metadata project và lifecycle
- `Traefik (Docker)`: route theo host
- `Static file server` Go: phục vụ nội dung site khi có request thực tế
- `Shared storage`: thư mục chứa site đã publish và thumbnail

Luồng request:
1. User upload ZIP qua API.
2. API validate archive, extract vào thư mục staging.
3. API kiểm tra manifest file cơ bản và giới hạn an toàn.
4. API publish site từ staging sang live bằng atomic rename.
5. API ghi metadata project vào DB.
6. Traefik trong Docker route host tới static server Go.
7. Static server Go nhận request, map `Host` sang `root_folder`, rồi trả file.
8. Worker nền tạo thumbnail.

### 5. Routing thực tế

Traefik không nên tạo một service riêng cho từng project nếu tất cả project đều được phục vụ bởi cùng một static server Go.

Thiết kế V1 đơn giản hơn:
- Traefik chạy trong Docker chỉ cần một router wildcard:
  - rule: `HostRegexp({subdomain:[a-z0-9-]+}.yourdomain.com)`
  - service: `static-sites`
- Traefik forward request tới container hoặc service nội bộ của static server Go.
- Static server Go đọc `Host` header, lookup project đang active trong DB hoặc in-memory cache, rồi map sang `root_folder`.

Kết luận:
- Không cần tạo file YAML riêng cho từng project ở Traefik trong V1.
- Chỉ cần 1 router wildcard trong Docker và 1 static server Go biết resolve host.

Nếu sau này cần custom domain hoặc isolation mạnh hơn, mới cân nhắc dynamic config per project.

Gợi ý vận hành:
- `traefik` container và `static-server` container cùng join một Docker network nội bộ
- Traefik chỉ public `80/443`
- static server Go chỉ expose cổng nội bộ cho Traefik gọi vào
- wildcard router hiện có dùng cho subdomain ngẫu nhiên của hệ thống
- khi user gán email domain riêng cho website, backend chưa ghi config Traefik ngay
- chỉ khi user bấm `Active SSL`, backend mới ghi file `.yaml` theo kiểu file provider để Traefik nhận host đó và xin Let's Encrypt certificate
- cần mount shared volume cho thư mục dynamic config giữa API service và `traefik` container

### 6. Database schema

Thêm bảng mới:

```sql
CREATE TABLE static_projects (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id),
    name VARCHAR(255),
    subdomain VARCHAR(255) NOT NULL UNIQUE,
    domain_id UUID REFERENCES domains(id),
    assigned_domain VARCHAR(255),
    domain_binding_status VARCHAR(32) NOT NULL DEFAULT 'unassigned',
    domain_last_dns_check_at TIMESTAMPTZ,
    domain_last_dns_result TEXT,
    domain_tls_enabled_at TIMESTAMPTZ,
    root_folder VARCHAR(512) NOT NULL,
    staging_folder VARCHAR(512),
    upload_filename VARCHAR(255),
    detected_root VARCHAR(512),
    archive_size_bytes BIGINT NOT NULL DEFAULT 0,
    file_count INT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    deploy_error TEXT,
    thumbnail_path VARCHAR(512),
    thumbnail_status VARCHAR(32) NOT NULL DEFAULT 'pending',
    is_active BOOLEAN NOT NULL DEFAULT true,
    published_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
```

Quota user cần bổ sung trong `users` hoặc bảng quota riêng:

```sql
ALTER TABLE users
ADD COLUMN max_websites INT NOT NULL DEFAULT 0;
```

Ý nghĩa:
- `max_websites` là số website deploy tối đa mà user được phép sở hữu
- `0` nghĩa là không được deploy website nào
- V1 không dùng `NULL = unlimited` để tránh semantics mơ hồ

Index/constraint:
- unique `subdomain`
- unique `domain_id` where `domain_id IS NOT NULL`
- index `user_id`
- index `status`
- index `deleted_at`

Status đề xuất:
- `draft`: vừa tạo record
- `uploaded`: đã nhận ZIP
- `extracting`: đang giải nén
- `published`: site live
- `publish_failed`: publish lỗi
- `disabled`: admin/user tắt site

Thumbnail status:
- `pending`
- `ready`
- `failed`

### 7. Storage layout

Thư mục:
- staging: `/data/static-sites/staging/{project_id}/`
- live: `/data/static-sites/live/{project_id}/`
- thumbnail: `/data/static-sites/live/{project_id}/thumbnail.png`

Nguyên tắc:
- Upload ZIP không extract thẳng vào thư mục live.
- Luôn extract vào `/data/static-sites/staging/{project_id}/` trước.
- Chỉ khi validate xong mới `rename(staging, live)` hoặc sync staging -> live.

### 8. Upload và publish pipeline

1. Upload ZIP:
- stream file vào disk tạm
- validate content-type và size
- check quota `max_websites` của user trước khi tạo project mới
- tạo trước một record `static_projects` ở trạng thái `draft`
- update `archive_size_bytes`

2. Extract an toàn:
- reject nếu tổng uncompressed size vượt quota
- reject nếu số file vượt ngưỡng
- reject nếu path chứa `../`, path tuyệt đối, hoặc symlink
- reject nếu filename null-byte hoặc quá dài

3. Detect publish root:
- hỗ trợ 2 case:
  - site nằm trực tiếp ở root của ZIP
  - site nằm trong đúng 1 thư mục con chứa toàn bộ source
- parser scan cây thư mục sau extract để tìm `index.html`
- nếu chỉ có 1 candidate rõ ràng, set `detected_root`
- nếu có nhiều candidate hoặc không có candidate, trả lỗi parse rõ ràng

Quy tắc V1:
- ưu tiên `index.html` ở root archive
- nếu root archive không có nhưng có đúng 1 thư mục con cấp 1 chứa `index.html`, chọn thư mục đó
- nếu có nhiều thư mục con đều có thể publish, reject để tránh deploy sai source

4. Validate static site:
- phải có `index.html` trong `detected_root`
- whitelist extension V1:
  - `.html`, `.css`, `.js`, `.json`, `.txt`, `.xml`, `.svg`
  - image/font/media phổ biến: `.png`, `.jpg`, `.jpeg`, `.gif`, `.webp`, `.ico`, `.woff`, `.woff2`, `.ttf`, `.eot`, `.mp4`
- blacklist cứng:
  - `.php`, `.phtml`, `.cgi`, `.pl`, `.py`, `.rb`, `.sh`, `.exe`, `.dll`, `.so`, `.bat`, `.cmd`

5. Tạo subdomain:
- sau khi parse và validate thành công, backend sinh `subdomain` ngẫu nhiên
- format đề xuất: `{adjective-or-shortid}-{suffix}.{saas_domain}` hoặc random hex/base32 ngắn
- phải kiểm tra uniqueness trước khi ghi DB
- user không tự chọn subdomain ở V1

6. Publish:
- set status `extracting`
- cleanup live folder cũ nếu có
- publish từ `detected_root`
- atomic rename từ staging sang live nếu cùng filesystem
- nếu không rename được atomically, copy sang thư mục live mới rồi swap symlink hoặc move folder
- set status `published`, `published_at = now()`

7. Thumbnail:
- enqueue job nền
- browser headless mở `https://{subdomain}`
- chụp màn hình `thumbnail.png`
- update `thumbnail_status`

8. Domain activation:
- nếu user chọn gán email domain cho website:
  - backend lưu `domain_id`, `assigned_domain`
  - user bấm `Check IP` để kiểm tra domain đã trỏ đúng về Traefik public IP chưa
  - Traefik public IP được đọc từ biến môi trường trong `.env`, ví dụ `TRAEFIK_PUBLIC_IP`
  - nếu DNS đúng, backend cho phép `Active SSL`
  - `Active SSL` sẽ tạo hoặc cập nhật file `.yaml` trong thư mục `dynamic_conf` của Traefik file provider
  - file này chứa host rule, service mapping tới static server, và TLS config để Traefik xin Let's Encrypt cho domain đó
  - khi xong, set `domain_binding_status = active`

### 9. Security

Yêu cầu bắt buộc:
- chống zip bomb:
  - max archive size
  - max uncompressed total size
  - max file count
- chống zip slip:
  - clean path
  - path phải luôn nằm dưới staging root
- không cho symlink/hardlink
- không cho executable/server-side file
- không trust MIME từ client

Về “scan mã độc”:
- V1 không coi regex scan HTML/JS là cơ chế bảo mật chính
- có thể thêm rule nhẹ để flag nội dung nghi vấn, nhưng không tuyên bố “site sạch”
- nếu cần sâu hơn, thêm `ClamAV` như optional scanner cho archive upload

Về domain activation:
- vì dùng lại `email domains`, verify domain ownership đã có thể tận dụng lại từ module domain hiện tại
- nhưng website publish cần thêm bước riêng:
  - `Check IP` xác nhận DNS web đang trỏ đúng Traefik
  - `Active SSL` ghi file `.yaml` vào `dynamic_conf` và xác nhận Traefik/TLS đã được bật cho host đó qua file provider

### 10. Static server

Khuyến nghị cho codebase này:
- thêm một service Go riêng để phục vụ static files
- flow:
  - đọc `Host`
  - resolve project active theo `subdomain`
  - map tới `root_folder`
  - serve file
  - hỗ trợ SPA fallback có điều kiện về `index.html`

Mô hình runtime:
- static server Go chỉ serve khi có HTTP request đi vào từ Traefik
- không cần spawn process riêng cho từng website
- mọi website dùng chung một binary, phân biệt bằng `Host`

Routing cần hỗ trợ 2 nhóm host:
- subdomain ngẫu nhiên của hệ thống qua wildcard router
- email domain của user sau khi đã `Active SSL` thành công trên Traefik/TLS

Khuyến nghị V1:
- nên hỗ trợ SPA fallback cho route như `/about`, `/pricing`
- rule fallback:
  - nếu file tồn tại thì serve file bình thường
  - nếu file không tồn tại thì chỉ fallback về `index.html` khi đồng thời thỏa:
    - method là `GET`
    - header `Accept` chứa `text/html`
    - path request không có file extension rõ ràng như `.js`, `.css`, `.png`, `.jpg`, `.svg`, `.json`
  - các request asset không tồn tại vẫn phải trả `404`
  - các request không phải HTML navigation vẫn phải trả `404`

### 11. API design

User API:
- `GET /api/static-projects`
  - list project của user
  - response nên trả sẵn field `ui_state` để frontend không phải tự suy luận hoàn toàn
- `GET /api/static-projects/:id`
  - detail cho trang settings
- `POST /api/static-projects/deploy`
  - upload ZIP
  - parse source
  - tạo project
  - sinh subdomain ngẫu nhiên
  - publish
- `POST /api/static-projects/:id/redeploy`
  - upload ZIP mới, parse lại, publish lại
- `PATCH /api/static-projects/:id/status`
  - active/inactive
- `PATCH /api/static-projects/:id/domain`
  - gán hoặc bỏ gán domain
  - chỉ nhận domain thuộc user trong danh sách email domain và đã verified
- `GET /api/static-projects/:id/available-domains`
  - trả danh sách email domain của user để render tab `Domains`
- `POST /api/static-projects/:id/domain/check-ip`
  - kiểm tra DNS A/AAAA của domain đang gán có trỏ về Traefik public IP không
- `POST /api/static-projects/:id/domain/active-ssl`
  - ghi file `.yaml` vào thư mục `dynamic_conf` của Traefik file provider
  - kích hoạt Traefik route và Let's Encrypt TLS cho domain đang gán
- `DELETE /api/static-projects/:id`
  - soft delete metadata, cleanup filesystem, unpublish

Admin API:
- `GET /api/admin/static-projects`
- `PATCH /api/admin/static-projects/:id/status`
- `DELETE /api/admin/static-projects/:id`
- `PATCH /api/admin/users/:id/website-quota`
  - super admin set `max_websites` cho user

Quota response cần trả về cho frontend:
- `max_websites`
- `websites_used`
- `websites_remaining`

### 12. Lifecycle và cleanup

Delete project:
- mark deleted trong DB hoặc hard delete tùy policy
- remove live folder
- remove staging folder
- remove thumbnail

Disable project:
- `is_active = false`
- static server trả `404` hoặc `410` cho host đó

Republish:
- upload ZIP mới
- extract staging mới
- detect publish root mới
- swap live folder
- giữ thumbnail cũ tới khi thumbnail mới sẵn sàng

Assign domain:
- website luôn có `subdomain` ngẫu nhiên mặc định
- user có thể gán thêm 1 domain từ danh sách email domain của chính họ
- chỉ email domain `verified` mới được gán
- sau khi gán, user phải chạy `Check IP`
- chỉ khi `Check IP` pass mới được `Active SSL`
- `Active SSL` là bước ghi file `.yaml` vào `dynamic_conf` để Traefik file provider bật route và Let's Encrypt TLS cho domain website
- nếu bỏ gán, website quay về chỉ dùng subdomain mặc định

### 13. Thumbnail worker

Workflow:
- chỉ chạy khi project `published` và `is_active = true`
- retry giới hạn
- timeout ngắn
- viewport cố định
- ghi lỗi vào `thumbnail_status = failed` và `deploy_error` hoặc trường riêng

Không nên block request publish vì thumbnail.

### 14. Quota

Quota tối thiểu cần có:
- max project per user
- max archive upload size
- max extracted size per project
- tổng dung lượng static hosting per user nếu muốn gom theo account

Trong scope hiện tại:
- `max project per user` chính là `max_websites`
- quota này phải do `super admin` chỉnh được cho từng user
- quota được hiểu là tổng số website record chưa bị delete của user
- `redeploy` không tiêu tốn thêm quota vì dùng lại cùng website record
- frontend user nên thấy `used/max` ở màn `Website`
- khi vượt quota, API `deploy` trả lỗi rõ ràng kiểu `website quota exceeded`

Admin UI cần có:
- trong màn `Users`, super admin chỉnh `max_websites`
- validation:
  - không âm
  - nếu hạ quota thấp hơn số website đang dùng, không xóa bớt site cũ
  - chỉ chặn tạo mới cho tới khi usage <= quota

### 15. Audit log

Cần log các action:
- create project
- upload archive
- publish success/fail
- assign/unassign domain
- check domain IP
- active SSL cho domain
- disable/enable
- delete
- admin website quota update

Payload nên có:
- `project_id`
- `user_id`
- `subdomain`
- `assigned_domain`
- `archive_size`
- `file_count`
- `status_before/status_after`

### 16. Test plan

Unit:
- zip path validation
- zip size/file-count validation
- extension whitelist/blacklist
- publish root detection:
  - root zip
  - single nested folder
  - multiple nested candidates
- backend `status` -> `ui_state` mapper
- website quota checker
- domain assignment validator:
  - ownership
  - verified status
  - conflict với website khác
- domain IP checker:
  - resolve A/AAAA
  - compare với Traefik public IP từ `.env`
- domain activation precondition checker
- host -> project resolver
- SPA fallback logic

Integration:
- deploy ZIP hợp lệ -> parse -> publish -> DB update
- ZIP với source ở root -> publish thành công
- ZIP với source trong 1 thư mục con -> publish thành công
- ZIP có nhiều thư mục con đều chứa `index.html` -> reject
- ZIP có path traversal -> reject
- ZIP có file cấm -> reject
- user vượt `max_websites` -> deploy bị reject
- assign domain verified của chính user -> thành công
- assign domain pending hoặc domain của user khác -> reject
- check IP fail khi domain chưa trỏ về Traefik
- active SSL fail khi chưa check IP pass
- active SSL thành công -> host domain user serve đúng website
- disabled project -> static server không serve
- delete project -> file và metadata bị cleanup
- list API trả đúng `ui_state` cho `deploying/live/failed/disabled`

Manual E2E:
- user mở menu `Website`
- thấy grid website
- bấm `Deploy New Website`
- upload ZIP đơn giản
- bấm `Edit Website`
- mở tab `Overview`
- mở tab `Upload New Version` và redeploy
- mở tab `Domains` và chọn domain từ danh sách email domain
- bấm `Check IP`
- bấm `Active SSL`
- mở subdomain
- mở domain đã gán qua HTTPS
- thumbnail xuất hiện
- redeploy website và thấy card chuyển `Deploying` rồi về `Live`
- force lỗi deploy và thấy card ở trạng thái `Failed`
- disable project
- enable lại
- delete project

### 17. Triển khai trong codebase hiện tại

Đề xuất module mới:
- `internal/staticprojects`
  - service upload/publish
- `internal/http/handlers/static_projects.go`
  - API handler
- `internal/storage/static_sites.go`
  - extract, publish, cleanup
- `internal/staticprojects/domain_binding.go`
  - validate, check IP, active SSL cho domain website
- `cmd/static-server`
  - binary Go phục vụ website theo `Host`
- `deploy/docker/traefik`
  - cấu hình Traefik chạy trong Docker, gồm wildcard route và file provider cho assigned domains
- `web/main.js`
  - grid website, popup deploy, settings tabs, state mapping/render
- `web/styles.css`
  - card grid, badge, placeholder, overlay states, settings tabs

Nếu chưa muốn thêm binary mới:
- có thể mount handler static vào API app hiện tại
- nhưng với yêu cầu Traefik trong Docker, về dài hạn vẫn nên tách `static-server` riêng để route rõ ràng và giảm blast radius

### 18. Ưu tiên triển khai

Phase 1:
- DB schema
- quota `max_websites` cho user + admin API chỉnh quota
- API list/detail/delete/deploy/redeploy/domain-assign
- ZIP extract an toàn
- detect publish root trong root hoặc 1 thư mục con
- random subdomain generator
- static server Go host resolver
- Traefik Docker wildcard route -> static server
- publish live basic
- UI `Website` grid + popup deploy
- UI `Website Settings` với tabs `Overview`, `Upload New Version`, `Domains`
- UI state mapping `deploying/live/failed/disabled`

Phase 2:
- `Check IP`
- `Active SSL` cho assigned domain
- thumbnail worker
- active/inactive
- audit log
- quota

Phase 3:
- custom domain
- ClamAV optional
- CDN/cache tuning

### 19. Kết luận

Thiết kế V1 nên đơn giản:
- 1 wildcard router ở Traefik Docker
- 1 static server Go resolve `Host -> root_folder` khi có request
- Go API xử lý upload/extract/parse/publish
- staging -> live publish atomic

Điểm quan trọng nhất không phải là “quét mã độc bằng regex”, mà là:
- unzip an toàn
- detect đúng publish root
- sinh subdomain ngẫu nhiên sau parse thành công
- gán domain chỉ từ danh sách email domain verified của chính user
- `Check IP` dùng public IP đọc từ `.env`
- domain website phải qua `Check IP` rồi mới `Active SSL` qua Traefik file provider
- quota `max_websites` do super admin quản lý
- không cho server-side executable
- publish atomic
- cleanup và lifecycle rõ ràng
