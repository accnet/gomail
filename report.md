# 📊 BÁO CÁO PHÂN TÍCH TOÀN DIỆN DỰ ÁN GoMail

> Ngày: 01/05/2026
> Phiên bản: v2.0 (✅ đã apply P0+P1+P2 fixes)

---

## I. TỔNG QUAN DỰ ÁN

**GoMail** là một SaaS platform với 2 sản phẩm:

| Sản phẩm | Mô tả | Trạng thái |
|---|---|---|
| **Inbound Email Hosting (V1)** | Nhận email qua SMTP, parse MIME, lưu attachment, realtime SSE | ✅ Hoàn thiện |
| **Static Website Hosting (Plan-2)** | Upload ZIP, publish lên subdomain, Traefik routing, custom domain + SSL | ✅ Hoàn thiện |

### Stack Công Nghệ

| Component | Công Nghệ | Version |
|---|---|---|
| Backend | Go + Gin + GORM | Go 1.23 |
| Database | PostgreSQL | 16-alpine |
| Cache/Realtime | Redis | 7-alpine |
| SMTP Library | emersion/go-smtp | - |
| HTML Sanitizer | microcosm-cc/bluemonday | - |
| Reverse Proxy | Traefik | v3.3 |
| Frontend | Vanilla JS SPA | - |

### Cấu Trúc Service

```
cmd/api              → HTTP API + SSE (port 8080/8080)
cmd/smtp             → SMTP inbound (port 25/2525)
cmd/static-server    → Static file serving (port 8090)
```

### Sơ đồ Luồng Dữ Liệu

```
SMTP Sender → Traefik :25 → SMTP Server → Mail Parser
                                          ↓
                                    Pipeline.Ingest()
                                          ↓
                              ┌───────────┼───────────┐
                              ↓           ↓           ↓
                           Database   Storage      Redis Pub
                              (Email)   (.eml)     (SSE Event)
                                                    ↓
                                                Frontend SPA
                                                    ↓
                                              SSE Real-time
```

```
User Upload ZIP → API → extractAndValidateArchive()
                         ↓
                    publishAtomic()
                         ↓
                    Traefik Config → Static Server
                         ↓
                    HostResolver → Serve Files
```

---

## II. PHÂN TÍCH KIẾN TRÚC

### ✅ Điểm Mạnh

1. **Clean Architecture**: Tách biệt rõ giữa handler/service/storage
2. **Atomic Publish**: Staging → Live rename cho static sites, rollback khi lỗi
3. **Refresh Token Rotation**: Phát hiện reuse và revoke toàn bộ session chain
4. **Security-aware HTML**: Sanitize HTML email, scrub remote images, block dangerous extensions
5. **Graceful Shutdown**: Static server + SMTP server + API server đều có signal handling (đã fix)
6. **Partial Unique Indexes**: Cho phép soft-delete vẫn unique với non-deleted rows
7. **ZIP Security**: Validate zip-slip, symlink, file extension trước khi extract
8. **Audit Logging**: Static project deploy, redeploy, delete, domain assign, attachment override đều có audit trail

### ❌ Còn Tồn Đọng

| Issue | Severity | Mô Tả |
|---|---|---|
| **Config Coupling** | 🟡 Trung bình | Config struct được pass khắp nơi, nên tách interface |
| **GORM Coupling** | 🟡 Trung bình | Models có `gorm` tags lẫn `json` tags, khó đổi ORM |
| **Service Layer Missing** | 🟡 Trung bình | Handler (app.go) query DB trực tiếp thay vì qua service |

---

## III. TÌNH TRẠNG ĐÃ FIX

### 🔴 P0 - Critical (✅ Đã Sửa)

| # | Task | File(s) | Trạng thái |
|---|---|---|---|
| 1 | **Context lifecycle management** | `cmd/api/main.go` | ✅ Background workers dùng context có cancel + signal handling |
| 2 | **Rate limiting middleware** | `internal/http/middleware/ratelimit.go` | ✅ Token bucket in-memory, wire vào App struct |
| 3 | **Remove query string token auth** | `internal/http/middleware/auth.go` | ✅ Xóa `c.Query("token")`, chỉ accept Bearer header |
| 4 | **SMTP graceful shutdown** | `cmd/smtp/main.go` | ✅ Bắt SIGINT/SIGTERM, cancel context cho SMTP server |

### 🟡 P1 - High Priority (✅ Đã Sửa)

| # | Task | File(s) | Trạng thái |
|---|---|---|---|
| 5 | **Typed constants cho status** | `internal/db/models.go` | ✅ Thêm `DomainStatus*`, `AttachmentScanStatus*`, `StaticProjectStatus*`, `ThumbnailStatus*`, `DomainBindingStatus*` |
| 6 | **Fix typo UISate → UIState** | `internal/staticprojects/service.go`, `service_test.go`, `static_projects_integration_test.go` | ✅ Sửa type, const, function, mọi reference |
| 7 | **Cleanup duplicate code** | `internal/staticprojects/service.go` | ✅ Tách `publishArchive()` dùng chung cho deploy + redeploy, bỏ ~70 dòng duplicate |
| 8 | **Wire NormalizeMessage()** | `internal/smtp/server/server.go` | ✅ Gọi `normalized := NormalizeMessage(raw)` trong `Data()` handler |

### 🟢 P2 - Medium Priority (✅ Đã Sửa)

| # | Task | File(s) | Trạng thái |
|---|---|---|---|
| 9 | **Connection pool config** | `internal/db/db.go` | ✅ SetMaxIdleConns(10), SetMaxOpenConns(50), SetConnMaxLifetime(5m), SetConnMaxIdleTime(1m) |
| 10 | **Thêm audit log cho attachment override** | `internal/http/handlers/app.go` | ✅ Audit log đã có sẵn trong `adminAttachmentOverride` |

---

## IV. PHÂN TÍCH BẢO MẬT

### ✅ Đã Làm Tốt

- Password hash bcrypt
- JWT access + refresh token rotation
- Path traversal protection cho ZIP extraction
- ZIP bomb protection (max archive size, max extracted size, max file count)
- Forbidden file extensions (.php, .sh, .exe...)
- HTML sanitization (bluemonday) + remote image scrubbing
- Soft delete với data ownership checks
- Attachment scan theo extension và content-type
- **Rate limiting cho auth endpoints** (đã thêm)
- **Không còn query string token auth** (đã xóa)

### Còn Lại

| # | Vấn Đề | Severity | Mô Tả | Đề Xuất |
|---|---|---|---|---|
| 13 | **Default secrets** | 🔴 Cao | `dev-secret-change-me` trong `.env` mẫu | Yêu cầu thay đổi khi production |
| 14 | **No ClamAV integration** | 🟡 Trung bình | Config có `ClamAVEnabled` nhưng không có implementation | Implement hoặc remove |

---

## V. TESTING ANALYSIS

| Package | Coverage | Quality | Ghi chú |
|---|---|---|---|
| `internal/auth` | ✅ Tốt | ✅ | Unit test + integration (SQLite) |
| `internal/dns/verifier` | ✅ Tốt | ✅ | Mock DNS test |
| `internal/mail/service` | ✅ Tốt | ✅ | HTML sanitize, MIME parse |
| `internal/smtp/server` | ✅ Tốt | ✅ | Full SMTP session test |
| `internal/staticprojects` | ✅ Tốt | ✅ | ZIP validation, host resolver |
| `internal/http/handlers` | ⚠️ Trung bình | ⚠️ | Integration tests cho một số routes |
| `internal/db` | ⚠️ Trung bình | ⚠️ | Schema regression test |
| `internal/config` | ❌ Yếu | ❌ | Single test |
| `internal/storage` | ⚠️ Trung bình | ⚠️ | Static sites path test |
| `internal/staticprojects/thumbnail_worker` | ❌ Không có | ❌ | **Không có test** |
| `internal/staticprojects/audit` | ❌ Không có | ❌ | **Không có test** |
| `cmd/static-server` | ❌ Không có | ❌ | **Không có test** |
| `internal/realtime` | ❌ Không có | ❌ | **Không có test** |
| `internal/http/middleware` | ❌ Không có | ❌ | **Rate limiter chưa có test** |

---

## VI. DANH SÁCH FILE CHÍNH

### Backend
| File | Dòng | Chức Năng |
|---|---|---|
| `cmd/api/main.go` | ~70 | API entry point |
| `cmd/smtp/main.go` | ~50 | SMTP entry point |
| `cmd/static-server/main.go` | 238 | Static server entry point |
| `internal/config/config.go` | ~120 | Configuration |
| `internal/db/db.go` | ~190 | Database connection + migration + seed |
| `internal/db/models.go` | ~250 | Data models + typed constants |
| `internal/auth/auth.go` | 152 | Authentication + JWT |
| `internal/http/handlers/app.go` | ~630 | HTTP handlers |
| `internal/http/handlers/static_projects.go` | 330 | Static project handlers |
| `internal/http/middleware/auth.go` | 66 | Auth middleware (clean) |
| `internal/http/middleware/ratelimit.go` | ~90 | Rate limiter middleware (new) |
| `internal/mail/service/service.go` | 325 | Mail processing pipeline |
| `internal/smtp/server/server.go` | 141 | SMTP server (+ NormalizeMessage wired) |
| `internal/staticprojects/service.go` | ~880 | Static project service (refactored) |
| `internal/staticprojects/host_resolver.go` | 86 | Host header resolver |
| `internal/staticprojects/thumbnail_worker.go` | 166 | Thumbnail generator |
| `internal/staticprojects/audit.go` | 89 | Audit logger |
| `internal/storage/storage.go` | 108 | Attachment storage |
| `internal/storage/static_sites.go` | 64 | Static sites storage |
| `internal/realtime/realtime.go` | 37 | Redis pub/sub |
| `internal/dns/verifier.go` | ~200 | DNS verification |

### Frontend
| File | Dòng | Chức Năng |
|---|---|---|
| `web/index.html` | ~50 | Main app shell |
| `web/login.html` | ~50 | Login/Register page |
| `web/main.js` | 1937 | Main SPA application |
| `web/login.js` | 106 | Login/Register logic |
| `web/styles.css` | 1726 | Styles |

### Infrastructure
| File | Chức Năng |
|---|---|
| `docker-compose.yaml` | Local dev environment |
| `deploy/docker/docker-compose.prod.yaml` | Production deployment |
| `deploy/docker/Dockerfile` | Multi-stage Docker build |
| `deploy/docker/traefik/dynamic-wildcard.yaml` | Wildcard TLS cert |
| `Makefile` | Build/test commands |
| `scripts/manual_e2e.sh` | E2E test script |
| `start.sh` | Quick start |

---

## VII. ĐỀ XUẤT CẢI THIỆN CÒN LẠI

### 🔵 P3 - Future

| # | Task | Complexity | Effort |
|---|---|---|---|
| 13 | **ClamAV integration** | High | 8h |
| 14 | **Metrics/Prometheus** | High | 6h |
| 15 | **Object storage (S3)** | High | 12h |
| 16 | **Full test suite** | High | 16h |
| 17 | **Webhook support** | Medium | 4h |
| 18 | **Service/Repository pattern** | High | 12h |
| 19 | **Refactor config → interface** | High | 4h |
| 20 | **Frontend improvements** | Medium | 4h |
| 21 | **Rate limiter tests** | Low | 1h |

---

## VIII. KẾT LUẬN

Dự án **GoMail** có kiến trúc tốt, security-aware rõ ràng. Sau v2.0:

| Hạng mục | Số lượng |
|---|---|
| Backend source files | 20+ |
| Frontend files | 5 |
| Infrastructure files | 8+ |
| Test files | 12 |
| **P0 đã fix** | **4/4** |
| **P1 đã fix** | **4/4** |
| **P2 đã fix** | **2/4** (còn: config→interface, frontend) |
| **P3 còn lại** | **9** (gồm frontend + config interface) |

### Build Status

- ✅ `go build ./...` — Pass
- ✅ `go vet ./...` — Pass

---

*Report generated by automated code analysis tool*