## Plan: Email Reply Feature Handoff

Mục tiêu là bổ sung reply, reply-all, forward và thread view đầy đủ theo cách ít rủi ro nhất với kiến trúc hiện tại: chuẩn hoá threading ngay trên bảng emails, tái sử dụng SentEmailLog cho phía outbound ở pha đầu, thêm API session-auth cho reply/thread, rồi nối vào giao diện chi tiết email hiện có. Không triển khai trong pha này; đây là plan thực thi và handoff.

**Commit Checklist**
1. Commit 1: thêm migration cho các cột threading trên emails gồm conversation_id, root_email_id, parent_email_id, in_reply_to_message_id, references_message_ids và các index cần thiết.
2. Commit 1: cập nhật Email trong /home/accnet/gomail/internal/db/models.go để map các trường threading mới.
3. Commit 2: mở rộng parser trong /home/accnet/gomail/internal/mail/service/service.go để parse In-Reply-To, References, normalize message-id và suy ra conversation_id.
4. Commit 2: cập nhật Pipeline.Ingest để link parent/root cho inbound email và set conversation_id cho mọi email mới.
5. Commit 2: thêm chiến lược backfill dữ liệu cũ từ HeadersJSON theo batch.
6. Commit 3: tách logic gửi mail thành abstraction hỗ trợ to, cc, bcc, subject, text/html và custom headers.
7. Commit 3: bổ sung outbound builder để reply có Message-ID mới, In-Reply-To, References đúng chuẩn; forward mở conversation mới.
8. Commit 4: thêm endpoint session-auth mode-based cho reply, reply-all, forward trong /home/accnet/gomail/internal/http/handlers/app.go.
9. Commit 4: mở rộng SentEmailLog để lưu liên kết original_email_id, conversation_id, parent_email_id, mode và metadata threading tối thiểu.
10. Commit 5: thêm GET /api/emails/:id/thread để trả unified thread gồm inbound Email và outbound SentEmailLog.
11. Commit 6: thêm UI Reply, Reply all, Forward trong /home/accnet/gomail/web/main.js và refresh thread sau khi gửi.
12. Commit 6: dùng plain text quote ở pha đầu; không gửi lại nguyên HTML gốc.
13. Commit 7: thêm test cho parser, inbound linking, reply handler và thread API.
14. Commit 7: chạy kiểm tra thủ công end-to-end để xác nhận SMTP headers và thread hoạt động đúng.

**Steps**
1. Commit 1 - Threading schema foundation: thêm migration mới trong /home/accnet/gomail/internal/db/migrations để mở rộng bảng emails với conversation_id, root_email_id, parent_email_id, in_reply_to_message_id, references_message_ids. Tất cả để nullable trong migration đầu để tránh phá dữ liệu cũ. Thêm index cho conversation_id, root_email_id, parent_email_id và cân nhắc unique index theo inbox_id + message_id nếu dữ liệu hiện tại cho phép.
2. Commit 1 - Model updates: cập nhật Email trong /home/accnet/gomail/internal/db/models.go để phản ánh các trường mới. Không thêm is_outbound hoặc is_draft vào Email ở pha đầu vì inbound email nên giữ semantics rõ ràng; outbound sẽ đi qua SentEmailLog. Step này phụ thuộc trực tiếp vào bước 1.
3. Commit 2 - Parsing and resolution primitives: mở rộng parsedMail và parse trong /home/accnet/gomail/internal/mail/service/service.go để trích xuất rõ In-Reply-To và References. Thêm helper chuẩn hoá message-id, tách chuỗi references và suy ra conversation_id. Quy tắc khuyến nghị: nếu có References thì lấy phần tử đầu tiên đã normalize làm conversation_id; nếu không có In-Reply-To hoặc References thì dùng Message-ID hiện tại; chỉ fallback sang subject-normalization nếu mail thiếu toàn bộ message-id chain.
4. Commit 2 - Inbound thread linking: trong Pipeline.Ingest của /home/accnet/gomail/internal/mail/service/service.go, resolve parent_email_id bằng cách match in_reply_to_message_id với Email.message_id trên tập inbox thuộc cùng user, không chỉ một inbox đơn lẻ. Sau đó gán root_email_id từ parent nếu có, ngược lại root_email_id để null cho root message đầu cuộc hội thoại. conversation_id phải được set cho mọi email mới để thread query không cần tree walk.
5. Commit 2 - Backfill strategy: thêm logic backfill một lần trong migration hoặc một command nội bộ nhẹ để đọc HeadersJSON của email cũ, trích In-Reply-To/References, rồi điền conversation_id, parent_email_id, root_email_id theo batch. Khuyến nghị không nhồi toàn bộ backfill phức tạp vào SQL migration; nên dùng đoạn Go chạy hậu migration hoặc startup-safe backfill để dễ test và rollback.
6. Commit 3 - Outbound send abstraction: tách logic gửi mail hiện có khỏi /home/accnet/gomail/internal/http/handlers/apikey.go thành một abstraction/service nhận được from, to, cc, bcc, subject, body text/html và custom headers. Bước này chặn toàn bộ reply feature nếu relay sender trong /home/accnet/gomail/internal/smtp/relay/sender.go hiện chỉ chấp nhận to/from/subject/body và không dựng MIME/header tuỳ ý.
7. Commit 3 - Thread-safe outbound headers: bổ sung builder ở tầng gửi để mail reply phát ra có Message-ID mới, In-Reply-To trỏ đến email gốc được reply, References là chuỗi references cũ cộng thêm Message-ID của parent. Với forward, không mang In-Reply-To và không reuse conversation cũ; forward bắt đầu conversation mới nhưng có quoted content từ mail gốc.
8. Commit 4 - Reply API design: thêm endpoint session-auth trong /home/accnet/gomail/internal/http/handlers/app.go. Khuyến nghị dùng một endpoint mode-based như POST /api/emails/:id/reply với body chứa mode=reply|reply_all|forward thay vì ba endpoint tách rời, để giảm lặp validation. Handler cần kiểm tra ownership của email gốc, chọn inbox gửi hợp lệ của user, chuẩn hoá recipient list, chuẩn hoá subject và gọi outbound send abstraction.
9. Commit 4 - Sent-side persistence: mở rộng SentEmailLog trong /home/accnet/gomail/internal/db/models.go với các trường tối thiểu gồm original_email_id, conversation_id, parent_email_id, mode và có thể references_message_ids hoặc in_reply_to_message_id. Khuyến nghị chưa tạo bảng outbound_emails riêng ở pha đầu vì sẽ làm nặng migration, query và test matrix; SentEmailLog hiện đã gần đủ để đại diện cho outbound item trong thread API.
10. Commit 5 - Thread API: thêm GET /api/emails/:id/thread trong /home/accnet/gomail/internal/http/handlers/app.go để trả về unified conversation gồm inbound Email và outbound SentEmailLog đã được map về cùng response shape với cờ is_outbound. Dùng conversation_id làm khoá nhóm chính, sort theo received_at hoặc sent_at tăng dần, và trả root/context để UI render conversation rõ ràng.
11. Commit 5 - Conversation listing decision: không thêm GET /api/conversations ở pha đầu trừ khi UI thật sự chuyển từ email-list sang thread-list. Với màn hình hiện tại ở /home/accnet/gomail/web/main.js, chỉ cần thread endpoint cho email detail là đủ để giao được tính năng reply mà không phải đổi toàn bộ cột danh sách.
12. Commit 6 - Web reply UX: mở rộng renderEmailDetail trong /home/accnet/gomail/web/main.js để thêm nút Reply, Reply all, Forward. Dùng modal/form pattern có sẵn trong file này để prefill recipient, Re:/Fwd: subject và quoted body. Sau khi gửi thành công, gọi lại GET /api/emails/:id/thread hoặc render detail để cập nhật conversation mà không full reload toàn app.
13. Commit 6 - Quoted content rules: pha đầu nên quote từ text_body nếu có; nếu không có thì convert HTMLBodySanitized thành plain text quote tối thiểu. Không gửi lại nguyên HTML gốc ở pha đầu để tránh XSS, layout lỗi và tăng độ phức tạp MIME. Đây là giới hạn chủ động, không phải thiếu sót.
14. Commit 7 - Tests: thêm integration tests trong /home/accnet/gomail/internal/http/handlers/send_email_integration_test.go hoặc file mới cùng package để kiểm tra mode reply, reply-all, forward, ownership, subject normalization và sender failure. Thêm tests trong /home/accnet/gomail/internal/mail/service/service_test.go cho parse In-Reply-To/References và inbound thread linking. Thêm tests ở /home/accnet/gomail/internal/http/handlers/app_integration_test.go cho GET thread trả đúng inbound và outbound items theo conversation_id.
15. Commit 7 - Manual verification: nhận một email vào inbox test, reply từ UI, xác minh mail phát ra có header đúng; sau đó gửi mail phản hồi ngược lại để kiểm tra thread được nối đúng ở cả inbound lẫn outbound. Đây là bước bắt buộc vì rủi ro chính nằm ở RFC header và integration SMTP chứ không chỉ ở logic handler.

**Relevant files**
- /home/accnet/gomail/internal/db/models.go — thêm trường threading cho Email và SentEmailLog.
- /home/accnet/gomail/internal/db/migrations — migration mới cho threading schema và sent-side linkage.
- /home/accnet/gomail/internal/mail/service/service.go — parse In-Reply-To/References, suy ra conversation_id, link parent/root cho inbound.
- /home/accnet/gomail/internal/http/handlers/app.go — route session-auth mới cho reply/thread.
- /home/accnet/gomail/internal/http/handlers/apikey.go — nguồn logic gửi hiện tại cần được tách/reuse.
- /home/accnet/gomail/internal/smtp/relay/sender.go — xác minh hoặc mở rộng khả năng gửi custom headers/MIME.
- /home/accnet/gomail/web/main.js — thêm action reply/reply-all/forward và UI hiển thị thread trong detail view.
- /home/accnet/gomail/internal/http/handlers/send_email_integration_test.go — mẫu test sender/auth hiện có.
- /home/accnet/gomail/internal/http/handlers/app_integration_test.go — nơi hợp lý cho thread API tests.
- /home/accnet/gomail/internal/mail/service/service_test.go — test parser và inbound thread linkage.

**Verification**
1. Chạy go test ./internal/mail/service -run 'Reply|Thread|Parse' để xác nhận parser và inbound linking.
2. Chạy go test ./internal/http/handlers -run 'Reply|Thread|SendEmail' để xác nhận handler/session-auth/sender integration.
3. Chạy go test ./internal/db ./internal/http/handlers ./internal/mail/service sau migration/backfill.
4. Kiểm tra thủ công UI ở /home/accnet/gomail/web/main.js với Reply, Reply all, Forward và thread refresh.
5. Kiểm tra header thực tế của mail gửi đi để xác nhận Message-ID, In-Reply-To và References đúng chuẩn.

**Decisions**
- Chọn schema hybrid: conversation_id + root_email_id + parent_email_id.
- Không tạo outbound_emails table ở pha đầu; tái sử dụng SentEmailLog để giảm độ phức tạp và rủi ro migration.
- conversation_id ưu tiên lấy từ root/reference message-id chain, không ưu tiên subject hashing nếu message headers đã đủ.
- Thread scope khuyến nghị theo toàn bộ inbox user sở hữu, không bó hẹp vào một inbox duy nhất.
- Forward là conversation mới; reply và reply-all mới nối vào thread cũ.
- Không gồm draft/auto-save trong đợt đầu.
- Không chuyển toàn bộ danh sách inbox/email sang conversation-list ở pha đầu.

**Further Considerations**
1. Nếu /home/accnet/gomail/internal/smtp/relay/sender.go không hỗ trợ custom header injection, cần thêm sub-step refactor riêng trước mọi API reply; đây là blocker kỹ thuật rõ nhất.
2. Nếu dữ liệu cũ có email thiếu Message-ID, nên để conversation_id fallback từ subject-normalized key nhưng chỉ dùng như giải pháp cứu hộ, không phải mặc định.
3. Nếu sau này cần full mail client, lúc đó mới cân nhắc tách outbound thành bảng riêng hoặc hợp nhất inbound/outbound vào một conversation_messages model.
