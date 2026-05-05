

---

# 📄 System Context: AI-Powered Adaptive Landing Page Generator

## 🎯 1. Triết lý hệ thống (Philosophy)
*   **AI-Driven Customization:** AI không chỉ lập kế hoạch mà còn trực tiếp tham gia vào việc định nghĩa phong cách (CSS) và hành vi (JS).
*   **Tĩnh hóa Logic (Static Logic Hardening):** Mọi mã nguồn do AI tạo ra (CSS/JS) phải được chốt cứng và kiểm tra tại thời điểm Build-time trước khi xuất bản bản tĩnh.
*   **Atomic & Utility First:** Sử dụng Tailwind CSS và Alpine.js làm nền tảng để AI có thể "lắp ghép" mã mà không cần viết lại từ đầu.

---

## 🏗️ 2. Cấu trúc dữ liệu mở rộng (Extended State JSON)

AI sẽ trả về cấu trúc bao gồm cả lớp giao diện và lớp hành vi:

```json
{
  "page_id": "uuid",
  "sections": [
    {
      "id": "hero_01",
      "type": "hero",
      "appearance": {
        "classes": "bg-gradient-to-br from-gray-900 to-black py-32",
        "custom_css": ".hero-title { letter-spacing: -0.05em; }"
      },
      "props": {
        "headline": "Sáng tạo không giới hạn"
      },
      "behavior": {
        "x_data": "{ scrolled: false }",
        "events": {
          "@scroll.window": "scrolled = (window.pageYOffset > 50)"
        },
        "custom_js": "console.log('Hero section initialized');"
      }
    }
  ]
}
```

---

## ⚙️ 3. Các thành phần thực thi (Execution Components)

### 🎨 A. CSS Stylist (AI + Tailwind JIT)
*   **AI Task:** Đề xuất danh sách các `Utility Classes` (Tailwind) và các đoạn `Custom CSS` nhỏ cho những hiệu ứng đặc biệt mà Tailwind chưa có sẵn.
*   **System Task:** Go sẽ thu thập tất cả các class này và chạy **Tailwind CLI** để tạo ra file CSS tối ưu nhất, loại bỏ code thừa.

### ⚡ B. JS Architect (AI + Alpine.js Sandbox)
*   **AI Task:** Định nghĩa các trạng thái (`x-data`) và các phản hồi sự kiện (`@click`, `@scroll`). Viết các hàm logic thuần (`custom_js`) cho các tác vụ đặc thù như tính toán hoặc animation.
*   **System Task:** 
    *   **Sanitizer:** Go quét `custom_js` để chặn các từ khóa nguy hiểm (`eval`, `fetch`, `XMLHttpRequest`, `document.cookie`).
    *   **Bundler:** Gộp các đoạn script nhỏ thành một file `main.js` tĩnh duy nhất.

### 🏗️ C. Go Renderer (The Compiler)
*   **Nhiệm vụ:** Ráp nối JSON vào hệ thống Template.
*   **Xử lý Logic:** 
    *   Nhúng `appearance.classes` vào thuộc tính `class` của HTML.
    *   Nhúng `behavior` vào các thuộc tính `x-` của Alpine.js.
    *   Inline hoặc Externalize các đoạn CSS/JS tùy biến vào kết quả cuối cùng.

---

## 🔄 4. Quy trình xử lý mã nguồn (Source Code Pipeline)

1.  **AI Generation:** AI tạo ra JSON chứa nội dung + Tailwind classes + Alpine logic + Custom Code.
2.  **Safety Gate (Go):** 
    *   Kiểm tra cú pháp (Syntax Check).
    *   Lọc bỏ mã độc (Sanitization).
3.  **Tailwind Processing:** Trích xuất classes để build CSS.
4.  **Static Export:** Xuất bản ra thư mục `/dist` gồm: `index.html`, `style.css`, `app.js`.

---

## 📏 5. Quy tắc vận hành (Guiding Rules)

*   **Rule 1: Utility First.** Ưu tiên dùng Tailwind class thay vì viết CSS thuần.
*   **Rule 2: Scoped Logic.** JS do AI tạo ra phải được cô lập trong phạm vi component (`x-data`) để tránh xung đột toàn cục.
*   **Rule 3: No Runtime AI.** Tuyệt đối không gọi API AI khi người dùng đang lướt web. Toàn bộ "trí thông minh" phải được biên dịch thành mã tĩnh trước đó.

---

## 🛠️ 6. Tech Stack (Updated)
*   **Core:** Golang (Fiber) & PostgreSQL.
*   **CSS Engine:** Tailwind CSS JIT.
*   **JS Engine:** Alpine.js (cho tương tác) & ESBuild (để nén JS tùy biến).
*   **Validation:** Go-Opa (Open Policy Agent) hoặc đơn giản là Regex-based Sanity Checker.

---
## 🧩 7. Deep Customization Protocol (Atomic Level)

### A. Layout Engine
- AI có quyền thay đổi: `padding`, `margin`, `alignment`, và `component-order`.
- Cấu trúc: Sử dụng hệ thống Grid 12 cột để AI chia tỷ lệ (ví dụ: 4/12 cho ảnh, 8/12 cho text).

### B. Typography & Colors
- AI định nghĩa `font-size`, `font-weight`, và `line-height` theo từng node.
- Palette: AI đề xuất Primary, Secondary, và Accent colors dựa trên Branding của người dùng.

### C. Iconography
- Thư viện mặc định: Lucide Icons.
- AI chọn icon theo `name` và có thể tùy chỉnh `stroke-width`, `color`.

### D. Content Logic
- Hỗ trợ Dynamic Content: AI có thể chèn các biến như `{{user_name}}` hoặc `{{current_date}}`.