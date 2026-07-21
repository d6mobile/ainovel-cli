# Thiết kế lớp văn phong (Voice Layer)

> Trạng thái: bản thiết kế chốt v2 (2026-07-12, tiếp thu review bên ngoài: bổ sung ngữ nghĩa bao phủ, ngữ nghĩa đường dẫn, thứ tự ghép, entry eval, giao thức thống kê đánh giá), **có thể triển khai**.
> Ưu tiên: đi trước tiến hóa control plane (docs/engine-arbiter.md) — “AI flavor” là pain point đang hoạt động của người dùng.

## 1. Bối cảnh và định nghĩa vấn đề

Người dùng phản hồi rằng nội dung sinh ra “mang nặng AI flavor”. Sau khi rà soát, kết luận là: **vấn đề không phải do tri thức văn phong và quy trình bị ràng buộc quá chặt, mà là vòng lặp cải tiến bị đứt ở hai điểm**:

1. **Sửa một lần là phải biên dịch lại** — các tài sản ngữ nghĩa về văn phong (anti-ai-tone.md, writer.md tiêu chuẩn viết, styles/*.md) đều `go:embed`; chỉ cần chỉnh một cách diễn đạt là phải build và phát hành lại.
2. **Không có vòng đo lường chuyên cho văn phong** — sửa xong chỉ có thể dựa vào cảm giác đọc của con người, không có đối chiếu trước/sau khách quan; tối ưu hóa trở thành huyền học.

## 2. Kiểm kê hiện trạng (tài sản liên quan đến văn phong có năm lớp)

| Lớp | Vị trí | Hiện trạng | Người dùng có thể chỉnh |
|----|------|------|---------|
| Tiêu chí ngữ nghĩa | `assets/references/anti-ai-tone.md` | writer né tránh + editor chứng minh dùng chung; gồm 5 nhóm: cấu trúc/ngữ từ/mô tả/đối thoại/nhịp điệu | ❌ nhúng sẵn |
| Tiêu chuẩn viết | `assets/prompts/writer.md` §tiêu chuẩn viết | trộn với protocol thực thi trong cùng một file nhúng | ❌ |
| Preset phong cách | `assets/styles/*.md` (4 file) | chọn đơn lẻ qua `cfg.Style`, nối thêm vào writer prompt | ❌ và không thể thêm mới |
| Quy tắc cơ học | `internal/rules` | từ khóa mệt mỏi / câu cấm / số lượng từ, kiểm tra bắt buộc lúc commit | ✅ đã có ba lớp bao phủ (lưu ý: ràng buộc “cấp dự án” của nó là **cwd**, xem 3.4) |
| Sở thích runtime | hành động `rules` của Arbiter | ngôn ngữ tự nhiên → cấu trúc hóa, có hiệu lực qua restart | ✅ |

Ngoài ra còn có hai nền tảng quan trọng khác: **stylestat** (thống kê tic câu cấp toàn sách, phản hồi ngược về writer như “gương câu cửa miệng”, hoàn toàn bằng code, không ảo giác) và **`OverridePrompt` của eval** (nền tảng prompt A/B đã sẵn có).

Kết luận: tính chỉnh được của lớp cơ học và nguyên liệu đo lường đã sẵn sàng; khoảng trống tập trung ở **không thể ghi đè lớp ngữ nghĩa** và **vòng đo lường chưa bám đúng văn phong**.

## 3. Thiết kế

### 3.1 Nguyên tắc cốt lõi

**Tách “viết như thế nào” (văn phong) ra khỏi “phối hợp như thế nào” (protocol): phần trước được dữ liệu hóa, có thể ghi đè; phần sau giữ dạng nhúng khi biên dịch.**

### 3.2 Tách writer.md: hồi tiếp placeholder tại chỗ

Phần tiêu chuẩn viết của writer.md nằm ở **giữa** file (sau protocol thực thi, trước phần liên tục của nhân vật phụ), không thể chỉ nối thêm ở cuối. Cần dùng phương án placeholder:

- `writer.md` (protocol, nhúng): giữ lại protocol thực thi / chạy tiếp từ điểm ngắt / viết lại và mài giũa / hợp đồng chương / mô tả cơ chế sở thích người dùng / **toàn bộ phần số lượng từ (bao gồm cả gợi ý cách viết)** / liên tục của nhân vật phụ / tham số commit; vị trí phần tiêu chuẩn viết cũ được thay bằng **duy nhất** placeholder `{{VOICE}}`
- `voice.md` (văn phong, có thể ghi đè): toàn bộ phần tiêu chuẩn viết (tránh AI flavor / đa dạng câu / không nhắc lại tiền văn)

Gợi ý cách viết cho số lượng từ được giữ lại trong file protocol (được review chấp nhận vào 2026-07-12): nó gắn chặt với việc thực thi hợp đồng số lượng từ; tách ra sẽ cần placeholder thứ hai, biến Voice thành định dạng nhiều mảnh — không đáng để làm chỉ vì một đoạn kỹ thuật hiếm khi ai muốn ghi đè. Sở thích về số lượng từ của người dùng đi qua `user_rules`. Tên file giữ nguyên là `writer.md` (eval `OverridePrompt` dùng tên file làm key; đổi tên chỉ làm tăng chi phí nối dây).

**Thứ tự ghép phải tương thích từng byte với hiện trạng.** Hiện trạng là `writer.md → simulationGuidance → style` (assets/load.go:84 + agents/build.go:247), vì vậy hàm ghép duy nhất sẽ là:

```go
// Entry duy nhất cho production, eval, và test; hồi tiếp {{VOICE}} tại chỗ để tách file mà không mất dữ liệu
func BuildWriterPrompt(protocolTemplate, voice, simulationGuidance, style string) string
// = replace(protocolTemplate, "{{VOICE}}", voice) + simulationGuidance + style
```

Bài học tiền lệ: chú thích `WithSimulationGuidance` từng ghi lại lỗi “baseline có wrapper, variant không có → A/B không tương đương”; việc tách nhánh ở đường ghép là nơi sinh lỗi cùng loại, vì vậy cần thu về một hàm duy nhất.

### 3.3 Mô hình bao phủ: ngữ nghĩa theo từng tài sản (không mập mờ)

| Tài sản | Ngữ nghĩa bao phủ | Lý do |
|------|---------|------|
| `voice.md` | **append**: giữ nội dung nhúng sẵn, global/toàn sách được nối thêm như các đoạn đánh dấu | thay thế toàn file sẽ khiến người dùng mãi mắc kẹt ở phiên bản nhúng cũ; nhu cầu phổ biến là tinh chỉnh chứ không viết lại |
| `anti-ai-tone.md` | **append** (như trên) | nhu cầu phổ biến là bổ sung tiêu chí; người muốn lật ngược tiêu chí nhúng là thiểu số rất nhỏ, không thiết kế riêng cho họ |
| `styles/<name>.md` | **thay thế toàn file cùng tên**; tên file mới đồng nghĩa phong cách mới | phong cách là “giọng nói” tổng thể, trộn hai phong cách lại là vô nghĩa |
| `genres/<name>/style-references.md` | thay thế toàn file cùng tên; nếu style tùy biến không có reference thì **được phép thiếu, không fallback default** (tham chiếu sai còn tệ hơn là không có) | như trên |
| `user_rules` | ưu tiên cao nhất ở runtime (hiện trạng không đổi) | — |

Việc ghép theo ngữ nghĩa append có ranh giới đánh dấu rõ ràng:

```
## Văn phong mặc định của dự án
...
## Ghi đè văn phong toàn cục của người dùng (các yêu cầu dưới đây ưu tiên hơn mặc định của dự án)
...
## Ghi đè văn phong riêng của sách này (các yêu cầu dưới đây ưu tiên hơn tất cả phần trên)
...
```

**Ranh giới trung thực**: trong ngữ nghĩa append, “cái sau thắng” chỉ là chỉ dẫn ưu tiên cho LLM, không phải bảo đảm cơ học — văn phong là nội dung mang tính khuyến nghị, và điều đó là chấp nhận được; các ràng buộc cần bảo đảm cơ học thì đi qua lớp rules (ở đó mới là ghi đè thật). Ranh giới này sẽ được viết vào tài liệu người dùng.

`arc-templates.md` thuộc mặt phẳng quy hoạch (định hình cấu trúc câu chuyện chứ không phải giọng điệu), **không nằm trong whitelist v1**, ghi lại để bàn sau.

### 3.4 Ngữ nghĩa đường dẫn: gắn theo outputDir ở cấp sách, không gắn theo cwd

```
Cấp sách   <outputDir>/style/     >   toàn cục   ~/.ainovel/style/   >   mặc định nhúng (fallback)
```

- Gắn theo `outputDir` khiến Voice **đi theo cuốn sách**: đổi thư mục vẫn đọc được cùng một bộ văn phong; Docker/headless/TUI có cách giải quyết đường dẫn thống nhất; nhiều sách dùng chung `cwd` sẽ không lẫn nhau
- `assets.Load` có chữ ký nhận rõ root giải quyết (outputDir), **bên trong không đọc cwd**
- Lưu ý khác với lớp rules: rules gắn `./.ainovel/rules` theo cwd (quy ước sẵn có trong `internal/rules/loader.go`, thiết kế này không đụng vào); tài liệu người dùng phải nói rõ hai ngữ nghĩa khác nhau — rules là “cấp dự án”, voice là “cấp sách”

Cấu trúc đầy đủ trong thư mục người dùng:

```
<outputDir>/style/            (đồng cấu với ~/.ainovel/style/)
  voice.md                    đoạn append
  anti-ai-tone.md             đoạn append
  styles/
    xianxia.md                thêm mới hoặc thay thế cùng tên
  genres/
    xianxia/
      style-references.md     tùy chọn
```

Tên style chính là tên file, xác thực bằng `[a-z0-9-]+`, từ chối các ký tự đường dẫn.

### 3.5 Vì sao mở cho người dùng là an toàn

Toàn bộ bất biến protocol đều nằm ở **lớp sự thật**: `draft` trước `check`, commit bắt buộc kiểm tra rules cơ học, chặn vượt giới hạn số lượng từ, checkpoint idempotent — không nằm trong prompt. Dù người dùng sửa `voice.md` kỳ quặc đến đâu, guard và tiền điều kiện của tool vẫn hoạt động như cũ; tệ nhất là văn phong xấu, còn state machine thì không hỏng.

### 3.6 Thời điểm có hiệu lực và entry eval

- v1 parse lúc khởi động, **có hiệu lực sau restart** (phục hồi checkpoint chính xác đến từng bước, chi phí restart gần như bằng 0; không làm hot reload)
- eval thêm **entry variant riêng cho voice** (ví dụ `Bundle.OverrideVoice(raw)`), bên trong đi cùng một đường `BuildWriterPrompt` — cấm ghi đè nguyên writer.md để làm A/B văn phong (vì sẽ kéo theo cả protocol, và protocol giữa baseline/variant có thể không tương đương)

## 4. Vòng đo lường: bộ đánh giá văn phong

```
Sửa voice/anti-ai-tone
  → bộ đánh giá văn phong (case cố định, eval voice-variant A/B)      ← phần bổ sung duy nhất
  → so sánh chỉ số stylestat (chỉ số cứng xác định được)
  + LLM judge chấm điểm theo từng tiêu chí của anti-ai-tone bằng chứng cụ thể (giai đoạn đầu chỉ báo cáo, chưa hard gate)
```

Giao thức thống kê (đầu vào cố định chỉ bảo đảm **so sánh được**, không bảo đảm **tái lập được**):

- baseline/variant khóa cùng một model và cùng tham số suy luận
- mỗi case lặp N≥3 lần, báo cáo trung bình, phương sai và mẫu thô
- judge chấm mù (không lộ danh tính baseline/variant)
- case bao phủ theo thể loại × kiểu chương (mở đầu / triển khai thường ngày / cao trào / kết)

## 5. Những việc rõ ràng không làm (để tránh thiết kế quá tay)

- Không mở prompt protocol cho người dùng cuối (`OverridePrompt` giữ vai trò năng lực nội bộ của eval)
- Không làm hot reload khi đang chạy
- Không mở pattern regex của stylestat cho người dùng cấu hình (điểm mở rộng của lớp cơ học đã có: fatigue_words / forbidden_phrases trong rules)
- Không làm marketplace / cơ chế chia sẻ phong cách (copy cả thư mục style là đã đủ để chia sẻ)
- `arc-templates` không vào whitelist v1

## 6. Các bước triển khai và tiêu chí nghiệm thu

1. Tách `writer.md` (`{{VOICE}}` placeholder) + hàm ghép duy nhất `BuildWriterPrompt`
2. Parser ba lớp: `assets.Load(outputDir, style)` + ngữ nghĩa theo từng tài sản (bảng 3.3) + gộp các style enum; test đơn bao phủ ưu tiên / fallback khi thiếu / ranh giới append
3. Entry `OverrideVoice` trong eval
4. Tài liệu người dùng: cấu trúc thư mục, ngữ nghĩa theo từng tài sản, khác biệt ngữ nghĩa đường dẫn giữa rules và voice, ví dụ minh họa
5. Bộ đánh giá văn phong (có thể để sau như một task độc lập)

**Tiêu chí nghiệm thu**: ① khi không có file ghi đè nào, `BuildWriterPrompt` cho ra kết quả **y nguyên từng byte** so với trước khi tách; ② có test table-driven cho ưu tiên ba lớp và ngữ nghĩa append / replace; ③ thêm `styles/xianxia.md` xong thì `style: xianxia` dùng ngay; ④ eval voice A/B và production dùng chung đường ghép (có test chứng minh); ⑤ toàn bộ test và sim regression đều xanh.

## 7. Quan hệ với tiến hóa control plane

Hoàn toàn trực giao (mặt phẳng nội dung vs. mặt phẳng điều khiển), không có phụ thuộc triển khai. Thứ tự thống nhất là: **lớp văn phong → bộ đánh giá văn phong → Engine/Arbiter (tiếp tục theo quyết nghị ở §8 của tài liệu của nó)**.