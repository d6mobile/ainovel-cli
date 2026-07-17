# Thiết kế thống nhất cho user rules

## Một câu ngắn gọn

Mọi quy tắc viết dài hạn đều được chuẩn hóa vào cùng một snapshot rules của toàn sách; runtime chỉ tiêm snapshot này qua `novel_context`, không còn liên tục nhét lại nguyên văn rules vào prompt.

```text
Startup prompt / file user rules / yêu cầu dài hạn trong lúc chạy
        ↓
LLM chuẩn hóa ngữ nghĩa (theo nguồn)
        ↓
Go gộp xác định (theo độ ưu tiên)  ←  system default rules (được code nhúng sẵn, đi thẳng vào gộp, không qua LLM)
        ↓
output/novel/meta/user_rules.json
        ↓
novel_context tiêm vào
        ↓
Architect / Writer / Editor / kiểm tra commit dùng chung
```

## Trạng thái triển khai (2026-06-28, đã có + đã sửa theo review)

Thiết kế này đã được triển khai, toàn bộ `go build` / `go vet` / `go test` của package 24 đều xanh. Sau một vòng code review đã sửa 4 lỗ hổng (đều đã khắc phục): ① rules của startup prompt chỉ gắn vào method chết `Host.Start`, còn entry thật đi qua `StartPrepared` nên bị thiếu snapshot — đã truyền nguyên prompt qua `Plan.RawPrompt` vào hai nhánh quick/cocreate, rồi thống nhất gọi `Host.PrepareUserRules`; ② lỗi ghi snapshot xuống đĩa bị nuốt — `PrepareUserRules` đổi thành hễ ghi đĩa thất bại là trả error và dừng mở sách (đường resume vẫn best-effort, để tránh tạo failure mode mới cho sách cũ); ③ lỗi đọc file rules bị bỏ qua im lặng — `raw.go` nay log với lỗi không phải “không tồn tại” (quyền, v.v.); ④ README vẫn dạy YAML/front matter cũ và còn trỏ tới file đã xóa — đã viết lại.

Cách triển khai và mô tả trong tài liệu này về cơ bản khớp nhau; có hai lựa chọn thực thi khác với cách diễn đạt theo chữ, được ghi lại ở đây:

1. **Chuẩn hóa không phải là “gọi theo schema của provider”, mà là “chỉ thị JSON trong prompt + kiểm tra phía Go”.**
   Lý do: structured output của litellm phụ thuộc từng provider (OpenAI/Gemini hỗ trợ JSONSchema, Anthropic không hỗ trợ),
   để nhất quán xuyên provider thì chuẩn hóa dùng system prompt quy định hình dạng JSON + Go parse / kiểm tra kiểu / sanitize miền giá trị để làm lớp đệm,
   không đẩy JSONSchema xuống model. Mọi chỗ phía dưới nhắc đến “schema constraints” đều hiểu theo cách này (kiểm tra schema ở phía Go, không phải ràng buộc ở provider).
2. **Khi một giá trị của một field là không hợp lệ thì hạ cấp riêng field đó, không hạ cấp cả nguồn.**
   Ví dụ nếu một field là placeholder rỗng hoặc sai kiểu, sanitize sẽ loại field đó đi (coi như chưa khai báo), nhưng vẫn giữ các field hợp lệ khác của cùng nguồn;
   chỉ khi “toàn bộ lần chuẩn hóa thất bại” (mạng / model / JSON không hợp lệ / parse thất bại) thì mới hạ cấp cả nguồn về raw preferences,
   đặt `status=degraded`. Như vậy một field xấu sẽ không kéo theo các rules hợp lệ khác của cùng nguồn. Lỗi kỹ thuật được ghi vào log,
   thử lại có giới hạn 1 lần rồi hạ cấp (`normalizeMaxAttempts=2`).

Điểm đặt code: `internal/rules` (dữ liệu thuần + gộp xác định: snapshot.go / raw.go / types.go), `internal/userrules`
(LLM chuẩn hóa + điều phối + ghi đĩa: normalize.go / service.go), `internal/store/user_rules.go` (lưu snapshot),
`internal/userrules/service.go` (ghi rules trong lúc chạy), `assets/prompts/arbiter-intervention.md` (phân luồng ba loại).
Baseline cơ học mặc định của hệ thống đã được chuyển từ `assets/rules/default.md` vào `rules.SystemDefaults()` nhúng trong code; đường parse YAML và dependency `yaml.v3` đã bị xóa. **Chưa kiểm chứng**: toàn bộ luồng thật của mở sách bằng LLM / action `rules` của Arbiter trong runtime (prototype offline của normalizer đã xác minh 10/10).

## Vì sao cần thiết

Writer ở mỗi chương không phải lúc nào cũng nhận ổn định toàn bộ prompt gốc của người dùng. Nó chủ yếu dựa vào nhiệm vụ của chương hiện tại và `novel_context(chapter=N)`.

Vì vậy, các rules dài hạn không thể trông chờ vào trí nhớ của lịch sử hội thoại, và cũng không nên âm thầm đoán bằng regex từ ngôn ngữ tự nhiên. Cách đúng là: biến rules dài hạn thành một trạng thái đã được chuẩn hóa một cách tường minh, rồi phân phát đồng bộ qua `novel_context`.

“Chuẩn hóa” ở đây phải tận dụng năng lực hiểu ngôn ngữ tự nhiên của mô hình lớn, chứ không phải liệt kê các cách diễn đạt trong Go. Chương trình chỉ định nghĩa một số field có thể kiểm tra cơ học, chịu trách nhiệm schema, gộp xác định, kiểm tra, ghi đĩa và kiểm tra commit; các câu như “mỗi chương khoảng một nghìn rưỡi”, “một chương đừng vượt quá hai nghìn”, “đừng viết kiểu bánh xe số phận nữa” sẽ do LLM hiểu ngữ nghĩa.

## Trạng thái thống nhất

Runtime của toàn sách chỉ giữ một nguồn sự thật duy nhất cho user rules:

```text
output/novel/meta/user_rules.json
```

Hình dạng giữ đơn giản:

```json
{
  "version": 1,
  "status": "ready",
  "structured": {
    "genre": "修仙",
    "forbidden_chars": [],
    "forbidden_phrases": ["某种程度上"],
    "fatigue_words": {}
  },
  "preferences": "主角冷静克制；少解释，多用行动和对话。",
  "sources": [
    "startup_prompt",
    ".ainovel/rules/style.md"
  ],
  "uncertain": [
    "少用比喻：没有明确阈值，按风格偏好处理"
  ]
}
```

Ranh giới field:

- `version`: phiên bản schema của snapshot, để tiện di chuyển về sau.
- `status`: `ready` / `degraded`, đánh dấu việc chuẩn hóa có thành công đầy đủ hay không; chỉ dùng cho phản hồi và chẩn đoán, không đi vào quyết định sáng tác.
- `structured`: các rules mà code có thể kiểm tra cơ học hoặc tiêu thụ ổn định.
- `preferences`: sở thích ngôn ngữ tự nhiên không thể kiểm tra cơ học nhưng có tác dụng lâu dài lên sáng tác.
- `sources`: phục vụ audit nguồn, không đi vào quyết định sáng tác.
- `uncertain`: chẩn đoán của quá trình chuẩn hóa, chỉ dùng để phản hồi và truy vết, không đi vào quyết định sáng tác.

Chỉ `structured` và `preferences` được tiêm vào model; `version` / `status` / `sources` / `uncertain` là metadata vận hành và chẩn đoán, không đi vào `working_memory.user_rules`. Lỗi kỹ thuật không vào snapshot, chỉ vào log (xem §thất bại và hạ cấp).

## Nguồn đầu vào

Rules dài hạn có bốn nguồn đầu vào:

1. **Startup prompt**: yêu cầu dài hạn người dùng viết khi mở sách.
2. **File user rules**: sở thích dài hạn ở mức global hoặc dự án, được đọc như ngôn ngữ tự nhiên bình thường.
3. **System default rules**: baseline cơ học nhúng sẵn trong code.
4. **Yêu cầu dài hạn trong lúc chạy**: người dùng nói giữa chừng “về sau cứ như vậy”, Arbiter trích xuất action `rules`, Host gọi `AddRuntimeRule`.

Các nguồn này không đi thẳng vào Writer prompt, cũng không bị đọc lặp lại trong runtime. Chúng chỉ tham gia chuẩn hóa khi tạo hoặc cập nhật snapshot, rồi kết quả được gộp vào `meta/user_rules.json`.

## File rules

File rules là prompt dài hạn bình thường, không phải prompt runtime, cũng không phải file cấu hình. Nó chỉ là đầu vào cho quá trình chuẩn hóa, không hỗ trợ YAML:

```md
# Sở thích viết

Mỗi chương 1200-1600 chữ.
Nhân vật chính điềm tĩnh, kiềm chế, đừng quá thánh mẫu.
Ít giải thích, đẩy tiến độ bằng hành động và đối thoại.
Không được xuất hiện “某种程度上”.
```

Sau khi hệ thống đọc xong, nó được chuẩn hóa thành:

```json
{
  "structured": {
    "forbidden_phrases": ["某种程度上"]
  },
  "preferences": "Mỗi chương 1200-1600 chữ; nhân vật chính điềm tĩnh, kiềm chế, đừng quá thánh mẫu; ít giải thích, đẩy tiến độ bằng hành động và đối thoại."
}
```

Nếu trong file có YAML front matter, hệ thống cũng xử lý như văn bản bình thường, không coi đó là khai báo cấu trúc. Kết quả cấu trúc chỉ đến từ luồng chuẩn hóa thống nhất.

Sau khi khởi động, nếu người dùng sửa file rules, cuốn sách hiện tại sẽ không tự đổi; cần tạo lại snapshot. Như vậy sách cũ sẽ không bị trôi hành vi chỉ vì file rules global thay đổi.

## Chuẩn hóa ngữ nghĩa

Chuẩn hóa là một lời gọi LLM độc lập, có schema constraints — mỗi nguồn được chuẩn hóa riêng một lần, không trộn vào việc sinh sáng tác, và cũng không dựa vào regex hay bảng từ khóa để parse cứng.

Đầu vào:

- Văn bản gốc của một nguồn đơn lẻ (startup prompt / một file rules / một yêu cầu trong lúc chạy)
- Mô tả các field `structured` mà hệ thống hiện đang hỗ trợ

System default rules không nằm trong nhóm này — chúng là các rules có cấu trúc đã được biên dịch sẵn trong code, đi thẳng vào §quy tắc gộp, không qua normalizer.

Đầu ra:

- `structured` ứng viên của nguồn đó
- `preferences` ứng viên của nguồn đó
- `sources`
- `uncertain`

Trách nhiệm phía Go:

- Cung cấp schema.
- Kiểm tra kiểu và miền giá trị của field.
- Gộp xác định các nguồn theo độ ưu tiên ở §quy tắc gộp (LLM không quyết định thứ tự ưu tiên giữa các nguồn).
- Lưu snapshot.
- Tiêm snapshot vào `novel_context`.
- Dùng cùng một snapshot để kiểm tra cơ học trong `commit_chapter`.

Trách nhiệm phía LLM:

- Hiểu rules ngôn ngữ tự nhiên của một nguồn đơn lẻ.
- Nâng các rule rõ ràng, có thể kiểm tra cơ học lên `structured`.
- Giữ các ý kiến thẩm mỹ, phong cách, sở thích nhân vật ở `preferences`.
- Với nội dung không chắc chắn, phải thận trọng, không tự bịa ngưỡng.

### Nâng cấp thận trọng

`structured` là hard rule hoặc tham số ổn định, không phải “vùng đoán của model”. Quy tắc nâng cấp phải thận trọng:

- Chỉ khi người dùng diễn đạt rõ ràng, không mơ hồ, mới ghi vào `structured`.
- `forbidden_chars` / `forbidden_phrases` là field cấp error, phải đặc biệt thận trọng; chỉ khi có dạng cấm rõ ràng như “đừng xuất hiện X”, “cấm X”, “đừng viết X” mới nâng cấp.
- `fatigue_words` chỉ nâng cấp khi người dùng đưa ra từ rõ ràng và ngưỡng rõ ràng; các kiểu như “ít dùng phép so sánh”, “đừng quá văn vẻ”, “giảm từ cửa miệng” mà không có ngưỡng thì vào `preferences`.
- Mọi yêu cầu liên quan số lượng từ / độ dài (“mỗi chương 3000 chữ”, “ngắn lại”) đều đi vào `preferences`: độ dài chương là một quyết định nhịp kể chuyện, không làm kiểm tra cơ học — biến số thành hard line sẽ khuyến khích model bơm nước để vượt line.
- Mọi yêu cầu không thể cơ giới hóa, không có ngưỡng rõ ràng, hoặc phụ thuộc ngữ cảnh đều đi vào `preferences`.

Nguyên tắc:

```text
Thà bỏ sót trong structured, hạ cấp thành soft preference;
Không được đưa nhầm vào structured, tạo ra false positive hard mỗi chương.
```

Cái giá của việc không trích xuất hết là sở thích phong cách sẽ yếu hơn một chút; cái giá của việc trích xuất sai là mỗi chương sinh ra một sự thật rules sai.

## Thất bại và hạ cấp

Chuẩn hóa là một đường tăng cường, không phải điều kiện tiên quyết trước sáng tác chính. Model hiểu sai tuyệt đối không được chặn việc viết sách.

- **Hạ cấp theo nguồn**: nếu một nguồn chuẩn hóa thất bại (mạng / model / JSON không hợp lệ / schema check thất bại), nguồn đó sẽ hạ xuống raw preferences, không sinh `structured`; các nguồn thành công khác vẫn đóng góp `structured` như bình thường.
- **Thử lại có giới hạn**: thất bại có thể thử lại hữu hạn (ví dụ 1 lần), vẫn thất bại thì hạ cấp, không retry vô hạn.
- **Lỗi kỹ thuật vào log**: lỗi JSON / schema / mạng được ghi vào log, không đi vào `working_memory.user_rules`, không trở thành input sáng tác.
- **Đánh dấu snapshot**: khi bất kỳ nguồn nào bị hạ cấp, snapshot đặt `status=degraded`.
- **Còn ghi được thì cứ chạy tiếp**: chỉ cần `meta/user_rules.json` vẫn ghi được thì sáng tác chính phải tiếp tục.
- **Chỉ dừng khi không ghi được**: chỉ khi snapshot không thể ghi xuống đĩa mới dừng, vì khi đó về sau không còn nguồn sự thật ổn định.

Hợp đồng `AddRuntimeRule` (trong lúc chạy): khi normalizer thất bại thì lưu degraded snapshot,
không đưa lỗi JSON / schema / mạng vào luồng sáng tác; chỉ khi ghi đĩa thất bại mới trả error.

## System default rules

`System defaults` là baseline cơ học nhúng trong code, không phải file rules của người dùng, và cũng không dùng YAML.

Nó không qua chuẩn hóa LLM — vì đã là dạng có cấu trúc, nên được đưa thẳng làm nguồn ưu tiên thấp nhất vào phần gộp Go ở §quy tắc gộp. Nhờ vậy default rules không có vấn đề thất bại LLM, drift, hay chi phí.

Baseline cơ học mặc định của hệ thống trước đây được lưu tạm trong `assets/rules/default.md` (chi tiết triển khai cũ, không phải YAML người dùng buộc phải tương thích); khi đưa thiết kế này vào thực tế, nó đã được chuyển vào `rules.SystemDefaults()` nhúng trong code, còn đường parse YAML đã bị xóa (xem §trạng thái triển khai).

Khi di chuyển, vẫn giữ các chú thích cần thiết để giải thích nguồn gốc ngưỡng, chẳng hạn một số ngưỡng từ khóa mệt mỏi đến từ thực chứng của các sản phẩm chạy đường dài. Điều này không phải để tương thích YAML cũ, mà để người bảo trì sau này biết vì sao ngưỡng mặc định tồn tại và khi nào nên điều chỉnh.

## Quy tắc gộp

Thứ tự gộp theo nguyên tắc “càng cụ thể càng ưu tiên”:

```text
System defaults
→ Kết quả biên dịch của Global rules
→ Kết quả biên dịch của Project rules
→ Kết quả biên dịch của Startup prompt
→ Runtime user update
```

Nguồn ưu tiên cao sẽ ghi đè nguồn ưu tiên thấp.

Việc gộp do Go thực hiện một cách xác định: LLM chỉ chuẩn hóa ngôn ngữ tự nhiên của một nguồn đơn lẻ thành `structured` / `preferences` ứng viên; Go theo thứ tự trên để ghi đè field và nối text, không giao quyền quyết định ưu tiên cho LLM.

- `structured`: ghi đè theo field, field cùng tên của nguồn sau ghi đè nguồn trước.
- `preferences`: không ghi đè lẫn nhau, mà nối thành văn bản có thể đọc được theo thứ tự ưu tiên (nguồn ưu tiên cao nằm ở phía sau), để LLM thấy được thứ tự nguồn.

Giới hạn đã biết: `preferences` được sắp theo ưu tiên nhưng Go không tự giải xung đột. Trong các trường hợp dài hạn nếu người dùng lần lượt đưa ra các soft preference mâu thuẫn (như trước “điềm tĩnh, kiềm chế” rồi sau đó “lắm lời”), cả hai đều sẽ nằm trong văn bản và để LLM cân nhắc theo thứ tự cùng ngữ cảnh; nếu cần ghi đè hard một cách xác định, phải biểu đạt thành field `structured` có thể cơ giới hóa.

## Entry ghi xuống đĩa

Chuẩn hóa, gộp, và ghi đĩa là cùng một bộ logic, nhưng có hai bên gọi khác nhau; phải phân biệt rõ, nếu không sẽ trộn phần chuẩn bị startup vào ngữ cảnh sáng tác chính:

- **Mở sách / refresh (phía startup, xác định)**: Host / luồng startup gọi trực tiếp bộ logic này để tạo snapshot ban đầu, không đi vào vòng sáng tác chính. Đây là tác vụ chuẩn bị startup xác định.
- **Cập nhật trong lúc chạy (hành động do can thiệp quyết định)**: action `rules` do Arbiter phân luồng sẽ được Host gọi trực tiếp `userrules.Service.AddRuntimeRule`, tái sử dụng cùng một logic kiểm tra / gộp / ghi đĩa, và gộp rules mới không có điểm bắt đầu tiến độ vào snapshot như `Runtime user update`.

(Về triển khai, nên thu gọn logic này thành một service nội bộ, hai bên gọi dùng chung; tên cụ thể để cho phần triển khai quyết định.)

Dù là bên gọi nào, cuối cùng cũng ghi vào cùng một `meta/user_rules.json`. Logic ghi đĩa chỉ làm ba việc:

1. Kiểm tra field cấu trúc.
2. Gộp vào snapshot của toàn sách hiện tại theo độ ưu tiên ở §quy tắc gộp.
3. Trả về toàn bộ rules facts đã lưu.

Không làm:

- Không phát sinh sub-agent.
- Không sửa outline.
- Không nuốt thầm field không hợp lệ (ghi nhận và hạ cấp, xem §thất bại và hạ cấp).
- Không lấy nguyên văn text làm prompt cuối cùng để tiêm thẳng vào.

Ví dụ cập nhật trong lúc chạy: người dùng nói “về sau cứ như vậy” (không có điểm bắt đầu tiến độ) → Arbiter quyết định đó là action `rules` → Host qua `AddRuntimeRule` chuẩn hóa câu đó → gộp vào snapshot với ưu tiên cao nhất như `Runtime user update` → phản hồi lại qua event stream.

## Phản hồi hiển thị lại

Mỗi lần tạo hoặc cập nhật snapshot `user_rules`, hệ thống đều phải hiển thị lại kết quả chuẩn hóa cho người dùng:

```text
Đã tạo snapshot rules của toàn sách:
- Quy tắc cơ học: mỗi chương 1200-1600 chữ; cấm cụm “某种程度上”
- Sở thích phong cách: nhân vật chính điềm tĩnh, kiềm chế; ít giải thích, đẩy tiến độ bằng hành động và đối thoại
- Chưa nâng lên thành rule cơ học: ít dùng phép so sánh (không có ngưỡng rõ ràng, xử lý như sở thích phong cách)
```

- Startup / refresh: tái sử dụng năng lực log rules khởi động hiện có để in snapshot, không thêm cơ chế mới; trong luồng co-create có thể gộp phần hiển thị này vào bước xác nhận co-create.
- Trong lúc chạy: sau khi `AddRuntimeRule` thành công, phản hồi qua event stream (“rules viết đã được cập nhật và lưu bền vững”).
- Khi hạ cấp: nếu `status=degraded`, phần hiển thị phải nói rõ nguồn nào không parse được, hiện đang chạy theo raw preferences, và có thể tạo lại snapshot.

Phản hồi hiển thị không phải là cổng phê duyệt thứ hai; tác dụng của nó là để người dùng biết hệ thống đã hiểu thành gì, và nếu phát hiện sai thì có thể tạo lại snapshot.

## Cách các agent tiêu thụ

Tất cả agent chỉ đọc:

```json
working_memory.user_rules
```

Phân chia trách nhiệm:

- Architect: điều chỉnh mật độ cốt truyện và số lượng tách chương theo ý muốn số lượng từ trong `preferences`.
- Writer: viết theo các hard rules trong `structured`, điều chỉnh phong cách theo `preferences`.
- Editor: duyệt dựa trên cùng một bộ rules.
- `commit_chapter`: dùng `structured` để kiểm tra cơ học và trả về violations.

Writer không tự hiểu lại startup prompt gốc, cũng không đọc file rules gốc.

## Phân loại can thiệp: ba đích đến

Can thiệp trong lúc chạy được chia thành ba loại theo “muốn sửa cái gì”:

- **Viết như thế nào** (bút pháp / phong cách / chất lượng: số lượng từ, từ ngữ, từ cấm, kiểu câu, tỷ lệ đối thoại, định dạng tiêu đề, v.v.) → action `rules` của Arbiter, chuẩn hóa rồi gộp vào `meta/user_rules.json`. Ví dụ: “mỗi chương 1500 chữ”, “tiêu đề chỉ dùng tiếng Trung”, “tổng thể nhân vật chính điềm tĩnh, kiềm chế”, “tăng tỷ lệ đối thoại lên một chút”.
- **Viết cái gì** (cốt truyện / cấu trúc / hướng đi nhân vật / độ dài) → architect, đi vào compass / outline / hồ sơ nhân vật. Ví dụ: “vòng này viết nhiều tuyến chiến đấu hơn”, “từ chương 30 trở đi giọng nhân vật chính lạnh hơn”, “tăng lên 40 chương”.
- **Sửa phần đã viết** (viết lại / sửa các chương đã chỉ định) → editor, vào hàng đợi `PendingRewrites`.

Tiêu chí phân biệt: **“viết như thế nào” → rules; “viết cái gì” → architect; “sửa phần đã viết” → editor**.

## Các bước triển khai

1. Thêm store `meta/user_rules.json`.
2. Thêm một pass LLM chuẩn hóa riêng (theo nguồn), dùng schema constraints để xuất `structured/preferences/sources/uncertain` ứng viên.
3. Thêm gộp xác định phía Go: theo độ ưu tiên, thực hiện ghi đè field và nối text giữa các nguồn, rồi tạo snapshot.
4. Thu gọn chuẩn hóa / gộp / ghi đĩa thành một logic dùng chung cho hai bên gọi: phía startup gọi trực tiếp để tạo snapshot ban đầu; trong lúc chạy, action `rules` do can thiệp quyết định sẽ đi qua `AddRuntimeRule` để tái sử dụng. Khi thất bại, xử lý theo §thất bại và hạ cấp: nguồn hạ xuống raw preferences, snapshot `status=degraded`, sáng tác chính tiếp tục.
5. Chuyển system default mechanical rules hiện nằm trong `assets/rules/default.md` sang cấu trúc hoặc JSON asset nhúng trong code, giữ lại chú thích nguồn ngưỡng; xóa đường parse YAML của user rules, không làm lớp tương thích.
6. Sau khi đọc file rules, không còn nhét trực tiếp nội dung gốc làm prompt mà chuẩn hóa rồi gộp vào snapshot `user_rules`.
7. `novel_context` chỉ tiêm `working_memory.user_rules` nằm trong `meta/user_rules.json`.
8. `commit_chapter` dùng cùng `user_rules.structured` để kiểm tra.
10. Phân luồng can thiệp (hiện do Arbiter đảm nhiệm, xem `arbiter-intervention.md`) phải tách rõ theo “muốn sửa cái gì” thành ba nhánh: yêu cầu dài hạn về phong cách / chất lượng viết đi qua action `rules` rồi vào snapshot; cốt truyện / cấu trúc / nhân vật / độ dài đi qua architect; sửa lại chương đã viết đi qua editor (xem thêm §Phân loại can thiệp: ba đích đến).

## Tiêu chí nghiệm thu

- Khi người dùng viết trong startup prompt “mỗi chương 1200-1600 chữ”, `novel_context` của chương đầu tiên phải thấy nguyên văn ý muốn này trong `preferences`.
- File rules chỉ viết bằng ngôn ngữ tự nhiên cũng có thể được chuẩn hóa vào cùng một `user_rules` khi tạo snapshot.
- File rules không cần và cũng không hỗ trợ YAML; tất cả đều được chuẩn hóa như quy tắc ngôn ngữ tự nhiên.
- Runtime không còn đọc file rules; chỉ đọc `meta/user_rules.json`.
- Default mechanical rules không còn đến từ YAML rules file, và user rules cũng không có lớp tương thích YAML.
- Chuẩn hóa không dùng regex / keyword hard-code; hiểu ngôn ngữ tự nhiên do LLM thực hiện.
- Rules mơ hồ sẽ không bị nâng lên thành field `structured` cấp error.
- System default rules không qua LLM, mà đi thẳng vào gộp Go.
- Việc ưu tiên nguồn và ghi đè field do Go thực hiện một cách xác định, cùng input sẽ cho cùng snapshot.
- Trong lúc chạy, nếu người dùng nói “về sau cứ như vậy”, nó phải được Arbiter phân vào action `rules` rồi gộp vào snapshot, để các chương sau `novel_context` thấy được cập nhật.
- Thất bại trong chuẩn hóa không được chặn việc viết sách: nguồn thất bại phải hạ xuống raw preferences, snapshot `status=degraded`, sáng tác chính tiếp tục; chỉ khi snapshot không ghi được mới dừng.
- Khi chuẩn hóa thất bại, hệ thống trả `status=degraded`, không đẩy lỗi kỹ thuật lên làm ô nhiễm luồng chính.
- Sau khi tạo hoặc cập nhật snapshot, hệ thống phải hiển thị `structured` / `preferences` / các mục chưa được nâng cấp; khi hạ cấp, phần hiển thị phải nói rõ nguồn nào bị hạ cấp.
- Mở một cuốn sách mới không được thừa hưởng `user_rules` của cuốn trước.
- Field cấu trúc không hợp lệ không được lờ đi im lặng: phải ghi nhận và hạ cấp nguồn đó, không chặn luồng chính.

## Những việc rõ ràng không làm (đã quyết định là không cần, không phải chia giai đoạn)

Những năng lực dưới đây không có lợi ích trong nhu cầu hiện tại, nên không đưa vào thiết kế để tránh phình to quá mức:

- Semantics xóa / hoàn tác theo từng field như `clear_fields`.
- Tự động refresh khi theo dõi thay đổi file rules (đổi file thì tạo lại snapshot một cách rõ ràng là đủ).
- Giải quyết mốc thời gian / ghi đè của `preferences` (nếu cần hard override thì hãy dùng `structured`).
- Lưu mảng `diagnostics` bền vững trong snapshot (lỗi kỹ thuật chỉ cần vào log; snapshot chỉ giữ `status`).
- Tự sinh mô tả field schema từ kiểu Go (chỉ cần tự duy trì một bản mô tả ngắn gọn).

Nguyên tắc thiết kế vẫn không đổi: LLM chịu trách nhiệm hiểu ngôn ngữ tự nhiên, Go chịu trách nhiệm gộp xác định, kiểm tra, ghi đĩa và kiểm tra.