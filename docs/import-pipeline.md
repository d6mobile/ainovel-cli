# Pipeline nhập ngữ nghĩa tiểu thuyết bên ngoài

> Trạng thái: đã triển khai (v1, `internal/host/imp`; cứu tiền tố cắt ngắn đã bao gồm giai đoạn ba·bổ sung)
> Ngày: 2026-07-15
> Mục tiêu: để việc nhập tiểu thuyết bên ngoài vừa có thể hưởng lợi liên tục từ nâng cấp năng lực model, vừa có bảo đảm kỹ thuật về không mất toàn văn, lỗi có thể chẩn đoán, sập có thể khôi phục và phát hành có thể kiểm chứng.
> Sửa đổi: thứ tự SourceUnit theo thứ tự số của `(Line, Part)` (§7.3/§8.3); cứu tiền tố cắt ngắn bị hạ cấp thành tối ưu hiệu suất có thể đặt sau và phải quan sát được (§9.5/§13.3/§19); model semantic function mở thành núm chỉnh (§13.1/§17).
> Sửa đổi 2026-07-16: núm chọn model được hiện thực thành cấu hình roles `import_segment/import_analyze/import_synthesize` (§13.1); cắt lại bằng ngôn ngữ tự nhiên được hiện thực qua `--guide` và đầu vào ngữ nghĩa `guidance.txt` trong workspace (§18.3); lỗi ngữ nghĩa lưu thống nhất phản hồi gốc vào failures/ (§14.2); xác nhận segmentation hỗ trợ nhấn `y` một lần trong panel để cho qua (§8.4); import chưa hoàn tất sẽ chủ động nhắc khi khởi động (§18.2). Chế độ JSON Schema (§13.2 cấp 1) tạm chưa hiện thực, đánh dấu TODO để cải tạo đồng bộ với các điểm gọi model khác trong toàn repo.

## 1. Một câu

Import không phải là “dùng regex cắt text rồi bảo model phun ra JSON của cả cuốn sách trong một lần”, cũng không phải một Import Agent chạy tự do; nó là một **pipeline biên dịch ngữ nghĩa theo giai đoạn**:

> Model chịu trách nhiệm hiểu ngữ nghĩa mở, code chịu trách nhiệm tọa độ, bao phủ, kiểu, hash, thứ tự và tính idempotent; toàn bộ sản phẩm ngữ nghĩa chỉ được phát hành vào trạng thái chính thức của sách sau khi đã xác minh xong trong workspace độc lập.

```text
Văn bản bên ngoài
  → Đọc xác định và chuẩn hóa
  → LLM nhận diện ranh giới chương / quyển / văn bản phụ trợ
  → Code xác minh bao phủ toàn văn
  → Người dùng xác nhận segmentation (có thể ủy quyền chấp nhận tự động một cách rõ ràng)
  → LLM trích xuất fact theo từng batch chương liên tiếp
  → LLM tổng hợp ngữ nghĩa toàn sách theo tầng
  → Code lắp ghép và xác minh Foundation
  → Phát hành Foundation và chương theo cách idempotent
  → Mặc định tạm dừng một lần; chỉ khi có --continue rõ ràng mới tiếp nối theo gate bình thường
```

## 2. Vì sao buộc phải tái cấu trúc

Hiện thực hiện tại là:

```text
Cắt chương bằng regex
  → Đưa toàn bộ chính văn chương vào ReverseFoundation một lần
  → Model xuất một lần premise / characters / world_rules / dàn ý toàn bộ chương / compass
  → Ghi ngay vào Foundation chính thức
  → Sau đó đọc lại cùng chính văn từng chương, phân tích rồi commit
```

Nó có bốn vấn đề mang tính cấu trúc.

### 2.1 Cắt chương đang liệt kê ngữ nghĩa mở

Tiêu đề chương không có ngữ pháp đóng. Tiếp tục thêm regex cho “Chương N”, “Quyển N”, “Chapter N”... thì chỉ bao phủ được những dạng đã từng thấy, không thể bao phủ tiêu đề do tác giả tự đặt, bố cục trộn lẫn, phân cấp quyển/chương và các định dạng tương lai.

Nghiêm trọng hơn, cách cắt hiện tại sẽ làm ranh giới không khớp biến mất khỏi kết quả và còn có thể âm thầm loại bỏ văn bản trước tiêu đề đầu tiên, chương rỗng và nội dung bị đánh giá là nhiễu ở cuối. Code không thể chứng minh rằng những nội dung đó nên bị bỏ.

### 2.2 Input và output của lời gọi Foundation đều tăng tuyến tính theo số chương

`ReverseFoundation` vừa đảm nhiệm hiểu toàn sách vừa tạo dàn ý chi tiết toàn bộ chương: input chứa toàn bộchính văn, output chứa cấu trúc chi tiết của từng chương. Với 54 chương đã có khả năng cắt JSON; tăng `max_tokens` chỉ đẩy điểm lỗi sang một cuốn sách dài hơn.

### 2.3 Trước khi lỗi xảy ra đã sửa trạng thái chính thức

Foundation và chương được phân tích rồi phát hành song song. Khi bước sau thất bại, người dùng nhận được trạng thái sách chính thức nửa nhập xong, nửa chưa phân tích. `from=N` hiện tại chỉ giả định người dùng biết nên khôi phục từ đâu, không thể chứng minh rằng file nguồn, kết quả segmentation và các chương đã có vẫn còn nhất quán.

### 2.4 Có nhiều kết luận ngữ nghĩa bị hard-code

Cách làm hiện tại còn cố định:

- Chính văn nhập vào chỉ có thể là một quyển;
- Chỉ có thể chia thành 1～3 arc;
- Dùng ngưỡng 25/80 theo số chương đã nhập để chọn short/mid/long;
- Vì muốn cho phép viết tiếp mà thiên về ép tạo `open_threads`;
- Mỗi chương phải có nhân vật, số lượng sự kiện cố định và loại hook cố định.

Những điều này không phải là sự thật có thể chứng minh cơ học từ định dạng file; phải do model căn cứ vào chính văn mà 판단, hoặc do người dùng biểu đạt ý định một cách rõ ràng.

## 3. Mục tiêu và ngoài phạm vi

### 3.1 Mục tiêu

1. **Có thể hiểu định dạng mở**: không yêu cầu người dùng chuyển tiểu thuyết sang định dạng tiêu đề có sẵn, cũng không yêu cầu người dùng viết regex.
2. **Có thể giải trình toàn văn**: mọi đoạn nguồn không rỗng đều phải thuộc về một chương rõ ràng hoặc một vùng phụ trợ rõ ràng, cấm mất dữ liệu âm thầm.
3. **Quy mô có thể kiểm soát**: không còn một lời gọi duy nhất vừa đọc toàn bộchính văn vừa xuất toàn bộ đối tượng chương; segmentation, batch chương hai ngân sách, và tổng hợp theo khoảng đều có ranh giới I/O cục bộ, còn output toàn cục chỉ tăng theo độ phức tạp ngữ nghĩa thực sự như nhân vật, quyển, arc.
4. **Lỗi không làm nhiễm trạng thái**: trước khi phân tích ngữ nghĩa và kiểm tra Foundation xong thì không ghi vào trạng thái sáng tác chính thức.
5. **Khôi phục chính xác**: khôi phục dựa trên snapshot nguồn và `InputDigest` của artifact, không phụ thuộc vào `from=N` hay trí nhớ người dùng.
6. **Hưởng lợi trực tiếp từ model tốt hơn**: model mạnh hơn sẽ cải thiện ngay nhận diện ranh giới, trích xuất fact, chia arc quyển và phán đoán viết tiếp, không cần thêm rule Go.
7. **Tái sử dụng ngữ nghĩa của commit chính thức**: phát hành chương tiếp tục dùng khả năng PendingCommit, checkpoint và digest idempotent của `commit_chapter`.
8. **Khả quan sát đầy đủ**: tiến độ, danh tính model, usage, phản hồi lỗi gốc và lỗi cuối cùng đều có điểm ghi rõ ràng.
9. **Tương thích cả tương tác lẫn tự động hóa**: mặc định để người dùng xác nhận các ranh giới ngữ nghĩa rủi ro cao, đồng thời cung cấp ủy quyền không người giám sát rõ ràng; đường tự động không dựa vào phỏng đoán âm thầm.

### 3.2 Ngoài phạm vi

- Không xây Coordinator hay vòng lặp Agent tổng quát.
- Không xây Workflow/PolicyEngine/cây nhiệm vụ tổng quát.
- Không tự sửa hay viết lại văn bản gốc của người dùng.
- Không hiện thực database, vector retrieval hay song song phân tán cho import.
- Không hỗ trợ ghép mơ hồ một tiểu thuyết khác vào sách hiện có.
- Không hiện thực migration trạng thái cũ `from=N` hay tương thích hai đường.
- Không mở rộng EPUB/PDF trong RFC này; phiên bản đầu vẫn chỉ nhận txt/md, tầng đọc giữ cục bộ và có thể thay sau mà không đổi hợp đồng phía sau.

## 4. Ranh giới trách nhiệm

| Vấn đề | Thuộc về | Lý do |
|---|---|---|
| Giải mã byte, chuẩn hóa newline | Go | Định dạng file và chuyển đổi xác định |
| Vị trí nguồn nào là tiêu đề chương, tiêu đề quyển hay văn bản phụ trợ | LLM | Ngữ nghĩa mở, không thể liệt kê hết |
| Tiêu đề tương ứng với vị trí nguồn ổn định nào | Go | SourceUnit, mốc nguyên văn và dải byte có thể kiểm chứng bằng cơ học |
| Một chương kể về điều gì | LLM | Hiểu ngữ nghĩa văn học |
| Nhân vật, quy tắc thế giới, foreshadow và quan hệ được tổng kết ra sao | LLM | Quy nạp ngữ nghĩa xuyên chương |
| Ranh giới arc quyển, câu chuyện đã khép hay chưa, mức độ lập kế hoạch | LLM | Phụ thuộc hình dạng tự sự, không phụ thuộc ngưỡng cố định |
| Phạm vi chương có tăng dần, không chồng lấn, bao phủ toàn bộ hay không | Go | Bất biến có thể chứng minh |
| Kiểu JSON, enum kín, số chương được tham chiếu có hợp lệ hay không | Go | Hợp đồng được kiểu hóa |
| Có thể tái sử dụng phân tích cũ hay không | Host/Workspace | Chỉ khi đầu vào ngữ nghĩa thật sự có thể tái tạo cùng `InputDigest` |
| Khi nào ghi trạng thái sách chính thức | Host/Store | Giao thức phát hành và khôi phục sau sập |
| Có cho phép tiếp tục theo segmentation hiện tại hay không | Người dùng/Intent | Xác nhận tương tác hoặc `--yes` rõ ràng, không do code tự thay lời |

Ở đây, lời gọi LLM không phải control plane kiểu Arbiter, cũng không phải vòng lặp sáng tác của Worker. Chúng là các **semantic function** có ranh giới rõ: đầu vào là fact được kiểu hóa, đầu ra là kết quả ngữ nghĩa được kiểu hóa, Host kiểm tra xong mới thực thi.

## 5. Kiến trúc tổng thể

```text
[TUI / Headless]
       │ /import <path> / ủy quyền tự động / xác nhận / hủy
[Host]
       │ độc quyền vòng đời import, event, runtime model
[imp.Runner]
       ├── LoadState → NextAction (chỉ suy ra từ facts trong workspace)
       ├── Source     đọc, giải mã, chuẩn hóa, snapshot
       ├── Segment    chiếu cấu trúc → LLM nhận diện ranh giới → kiểm tra bao phủ
       ├── Analyze    batch liên tiếp hai ngân sách → tạm lưu fact theo chương
       ├── Synthesize tổng hợp theo tầng → BookSynthesis
       ├── Validate   lắp ghép và xác minh Foundation đầy đủ
       └── Publish    Foundation chính thức → commit_chapter
               │
[meta/import workspace]          [Store chính thức]
Snapshot nguồn / segmentation / phân tích / kết quả tổng hợp      Progress / Checkpoint / Artifact / PendingCommit
```

Runner chỉ là một bộ điều phối giai đoạn xác định, không có khả năng quyết định tự do. Mỗi lần nó chỉ thực thi một hành động do `NextAction` suy ra, rồi đọc lại facts sau khi hành động hoàn tất.

## 6. Workspace và suy luận trạng thái

Các facts trong quá trình import được lưu dưới thư mục của sách:

```text
meta/import/
├── manifest.json
├── intent.json
├── source.txt
├── guidance.txt          # Khi tồn tại: hướng dẫn segmentation bằng ngôn ngữ tự nhiên của người dùng (--guide), là đầu vào ngữ nghĩa của segmentation
├── segmentation.json
├── confirmation.json
├── analyses/
│   ├── 000001.json
│   ├── 000002.json
│   └── ...
├── range-digests/
│   ├── 000001-000050.json
│   └── ...
├── synthesis.json
├── story-resolution.json
└── failures/
    ├── last.json
    └── last-response.txt
```

Phiên bản đầu giữ workspace. Nó vừa là căn cứ khôi phục vừa là hồ sơ kiểm toán của import; không thêm cơ chế tự dọn và lưu trữ lịch sử.

`intent.json` lưu các ủy quyền rõ ràng của người dùng khi bắt đầu import (tự xác nhận, preselect trạng thái truyện uncertain, có bỏ qua Hold hoàn tất hay không). Đây là ý định của người dùng phải được tuân thủ sau khôi phục, không phải trạng thái giai đoạn có thể suy ra từ artifact; sau khi tạo thì Runner không được âm thầm ghi đè.

### 6.1 Manifest

```go
type ImportManifest struct {
	Version          int    `json:"version"`
	SourceName       string `json:"source_name"`
	RawSHA256        string `json:"raw_sha256"`
	NormalizedSHA256 string `json:"normalized_sha256"`
	Encoding         string `json:"encoding"`
	SizeBytes        int64  `json:"size_bytes"`
	CreatedAt        string `json:"created_at"`
}

type ImportIntent struct {
	Version             int    `json:"version"`
	AutoConfirm         bool   `json:"auto_confirm,omitempty"`
	StoryResolution     string `json:"story_resolution,omitempty"` // open / closed
	ContinueAfterImport bool   `json:"continue_after_import,omitempty"`
}
```

- `source.txt` là bản snapshot cục bộ đã chuẩn hóa, nên khi khôi phục không còn phụ thuộc vào việc đường dẫn gốc còn tồn tại hay không;
- Manifest không lưu đường dẫn nguồn tuyệt đối để tránh lộ thư mục máy và loại bỏ vấn đề khôi phục khi file bị di chuyển;
- Intent chỉ nhận các giá trị trong tập đóng, lưu chính xác ủy quyền của người dùng trong lệnh khởi động; khi khôi phục không suy ngược ý định cũ từ advance mode hiện tại;
- Nếu schema version không khớp thì yêu cầu rõ ràng dùng phiên bản phù hợp để tiếp tục hoặc nhập lại, không đoán migration.

Khi tạo lần đầu, ghi đầy đủ và kiểm tra manifest, intent, source trong một thư mục tạm cùng cấp, rồi rename thư mục để phát hành thành `meta/import/`; nếu `meta/import/` không tồn tại thì không tính là workspace đang hoạt động. Như vậy bộ ba khởi tạo ban đầu sẽ không bước vào `NextAction` ở trạng thái nửa khởi tạo, và cũng không cần thêm `stage=initializing` cho quá trình tạo. Nếu khởi động phát hiện thư mục khởi tạo còn sót thì phải cảnh báo rõ và giữ thông tin chẩn đoán, không tự coi là workspace thành công, cũng không âm thầm xóa.

### 6.2 Không lưu enum trạng thái dễ trôi

Trạng thái bền vững không ghi `stage=analyzing`, `current=37` hay các trường điều khiển tương tự. Hành động tiếp theo được suy ra từ artifact:

```text
Không có manifest/intent/source   → ingest
Không có segmentation             → segment
Không có confirmation khớp với input digest của segmentation → await_confirmation / auto_confirm
Có phân tích chương thiếu hoặc input digest không khớp       → analyze_first_missing
Thiếu RangeDigest hoặc synthesis khớp với input               → synthesize_first_missing
story_status=uncertain và không có lựa chọn người dùng khớp   → await_story_resolution
Artifact chính thức và synthesis không nhất quán             → publish
Tất cả artifact chính thức nhất quán                         → done
```

`Stage` trong event chỉ dùng để hiển thị UI, không phải nguồn sự thật để khôi phục.

### 6.3 Thống nhất định danh artifact

Không hiện thực dependency graph. Mọi semantic artifact trong workspace đều dùng cùng một quy tắc định danh:

```go
type Artifact[T any] struct {
	SchemaVersion int    `json:"schema_version"`
	InputDigest   string `json:"input_digest"`
	Payload       T      `json:"payload"`
}
```

`InputDigest` bao phủ toàn bộ **đầu vào ngữ nghĩa** mà hành động thực sự tiêu thụ, được mã hóa theo thứ tự cố định rồi tính toán:

- segmentation: nội dung nguồn đã chuẩn hóa, projection của SourceUnit, hướng dẫn người dùng và prompt/schema version của segmentation;
- confirmation: nội dung segmentation và cách xác nhận;
- phân tích chương: phạm vi batch chương liên tiếp vàchính văn, ledger liên tục trước khi vào batch, prompt/schema version và hướng dẫn người dùng;
- RangeDigest/BookSynthesis: nội dung có thứ tự của các analysis hoặc digest tầng dưới mà chúng tiêu thụ, prompt/schema version của tổng hợp;
- story resolution: nội dung synthesis và lựa chọn của người dùng;
- phát hành: nội dung chuẩn hóa của domain object sẽ phát hành.

provider/model, usage, thinking và các fact thực thi được ghi vào provenance/session, không làm phân tích đã thành công trở nên vô hiệu tự động chỉ vì cấu hình model đổi. Khi người dùng yêu cầu phân tích lại thì phải xóa rõ artifact tương ứng. Quyết định tái sử dụng cache chỉ nhìn xem hành động hiện tại có thể tái tạo cùng `InputDigest` hay không.

`NextAction` đi dọc theo pipeline tuyến tính cố định để tìm artifact đầu tiên bị thiếu, lỗi parse hoặc không khớp `InputDigest`. Khi cắt lại, sửa hướng dẫn người dùng hoặc thay đổi facts thượng nguồn thì hạ nguồn tự động không khớp; không cần viết quy tắc vô hiệu kiểu “khi segmentation đổi thì phải xóa thủ công những file nào”.

Khi phát hành, artifact chính thức và kết quả tổng hợp được so từng trường; nếu giống nhau thì bỏ qua theo kiểu idempotent, nếu khác nhau thì báo xung đột, không ghi đè theo phỏng đoán. Vì vậy mới xóa `ResumeFrom`. Khôi phục chỉ cần chạy lại `/import`; Runner sẽ tiếp tục từ facts còn thiếu đầu tiên.

## 7. Đọc file nguồn

### 7.1 Giải mã

Phiên bản đầu hỗ trợ:

- UTF-8 / UTF-8 BOM;
- GB18030 (bao phủ các văn bản tiểu thuyết GBK phổ biến).

Kết quả giải mã phải trả về encoding đã chọn và ghi vào Manifest cùng event tiến độ. Không được biến “thử GB18030” thành fallback âm thầm. Nếu không thể giải mã tin cậy hoặc xuất hiện ký tự thay thế không chấp nhận được thì phải fail ngay, kèm lỗi chứa kết quả nhận diện.

### 7.2 Chuẩn hóa

Chỉ thực hiện các chuyển đổi không làm thay đổi nội dung văn học:

- Xóa BOM;
- Chuẩn hóa CRLF/CR về LF;
- Giữ nguyên dòng trống, thụt lề, dòng tiêu đề và ký tựchính văn;
- Không xóa đoạn đầu, chương rỗng, quảng cáo, thông tin bản quyền hay cái gọi là nhiễu ở cuối.

Mọi quyết định loại trừ đều được để cho kết quả segmentation và hiển thị trong preview.

### 7.3 Tọa độ ổn định

Văn bản đã chuẩn hóa xây dựng một bảng `SourceUnit` thống nhất:

```go
type SourceUnit struct {
	ID        string // L1257; nếu vượt ngân sách dòng thì tách thành L1257.1, L1257.2
	Line      int
	Part      int
	StartByte int
	EndByte   int
	Text      string
}
```

- `ID` chỉ dùng để hiển thị và model tham chiếu; mọi quyết định thứ tự, bao hàm và tăng dần đều so sánh theo tuple số `(Line, Part)`, cấm so sánh chuỗi ID theo thứ tự từ điển (`"L900"` sẽ lớn hơn `"L1000"` theo từ điển); JSON projection giữ id dạng chuỗi, phía Go phải parse thành `(Line, Part)` rồi mới so;
- Dòng bình thường tương ứng một unit, nên đường đi phổ biến vẫn là tọa độ theo số dòng trực quan;
- Khi một dòng đơn vượt ngân sách projection cấu trúc thì Go chỉ sinh nhiều **virtual unit** tại ranh giới ký tự UTF-8;
- Các phần cắt ảo không ghi ngược lại vào `source.txt`, không chèn soft line break, không thay đổi bất kỳ ký tự nguồn nào;
- Khi trong cùng một unit có ranh giới thì model trả về unit ID và một mốc nguyên văn sao chép chính xác từng chữ; Go yêu cầu mốc đó là duy nhất trong unit rồi mới ánh xạ thành vị trí byte chính xác;
- Nếu mốc không tồn tại hoặc không duy nhất thì trả lỗi cụ thể lại cho model, cấm đoán offset, cắt text hay yêu cầu người dùng sửa nguyên tác trước.

Vì vậy, văn bản phân chương bình thường vẫn giữ mô hình số dòng; các đoạn liền nhau không có xuống dòng, một dòng có nhiều chương hoặc dòng quá dài bất thường cũng dùng cùng một loại tọa độ để xử lý.

## 8. Chia đoạn ngữ nghĩa

### 8.1 Projection cấu trúc

Model nhìn thấy projection cấu trúc được chia theo ngân sách ngữ cảnh:

```json
{
  "owned_units": {"start": "L1200", "end": "L1800"},
  "context_units": {"start": "L1180", "end": "L1820"},
  "units": [
    {"id": "L1200", "line": 1200, "text": "Gió từ ngoài cổng thành thổi vào.", "blank_before": true},
    {"id": "L1257", "line": 1257, "text": "Quyển hai·Bắc cảnh", "blank_before": true, "blank_after": true}
  ],
  "user_guidance": ""
}
```

Vùng ngữ cảnh có thể chồng lấn, nhưng mỗi lần gọi chỉ được trả kết quả cho `owned_units`, vì vậy không tồn tại bỏ phiếu trên block chồng lấn hay hợp nhất xung đột. Kỷ luật tọa độ do Go thực thi (sửa 2026-07-16): ranh giới model trả về trong vùng context không kích hoạt hỏi lại ngữ nghĩa — ranh giới đó thuộc block lân cận quản lý (nó sẽ báo lại trong khoảng `owned` của mình), code cắt bỏ trực tiếp và trả lời giải thích; retry ngữ nghĩa chỉ dành cho lỗi ngữ nghĩa thật sự (ID ảo ngoài projection, kind không hợp lệ, v.v.). Hành vi cũ với phản hồi vượt biên đã bị hỏi lại; model yếu thường tiêu hết cả 3 lần thử làm sập cả block.

Kích thước block được tính từ context window và ngân sách giữ lại của model architect hiện tại, không chia theo số dòng cố định hay số chương cố định. Khi context của model tăng lên thì số lần gọi tự nhiên giảm xuống. Ngân sách lập kế hoạch không dùng hết mức tối đa (sửa 2026-07-16): chính văn `owned` chỉ là một phần của yêu cầu, khi lập kế hoạch phải trừ đi độ dài thực của system prompt và guidance, rồi nhân chiết khấu 3/4 để bù cho phình ra của JSON projection; context bên ngoài còn có ngưỡng byte riêng (chunkBytes/8, tối thiểu 4096), nhằm chặn các dòng quá dài làm nổ ngân sách do virtual split. Phía output có cơ chế đối xứng để bảo vệ: khi JSON ranh giới của một block bị cắt do giới hạn độ dài (nhiều chương ngắn) thì sẽ chia đôi block và thử lại đệ quy — half-block có đường cache độc lập, kết quả thử lại không phải trả phí lại; nếu cấp unit vẫn bị cắt thì đó mới là thiếu dung lượng thật sự.

Quyết định ranh giới của mỗi block được lưu thành artifact (`segment-chunks/chunk-*.json`, định danh = định danh segmentation + MaxUnitBytes + phạm vi owned của block — bảng unit được xác định duy nhất bởi “source đã chuẩn hóa + MaxUnitBytes”, đổi mức model làm tái cấu trúc virtual split của các dòng quá dài thì cache tự nhiên không khớp, không thể tái sử dụng sai ranh giới cũ), bất kỳ block nào thất bại hoặc bị gián đoạn thì khi chạy lại các block đã xong được tái sử dụng không gọi lại — cùng một triết lý với analyze theo chương và synthesize theo khoảng; sau khi segmentation cuối cùng được ghi xuống thì cache cấp block bị xóa. Khi tích hợp cuối (resolve) thất bại cũng xóa cache block và ghi snapshot quyết định vào `failures/`: lúc này digest cache vẫn khớp tuyệt đối, nếu giữ lại sẽ khiến chạy lại đọc lại cùng một loạt ranh giới với zero-call và tái hiện chính xác cùng một lỗi. Ranh giới chương có phầnchính văn rỗng (thường gặp ở nguồn web novel thực tế như tiêu đề giữ chỗ “đã khóa/chap trả phí”) không được fail toàn cục: gộp vào đoạn trước (không mất một chữ nào), ghi vào `Segmentation.Notes` để hiển thị trong preview xác nhận, nếu người dùng không chấp nhận có thể dùng `--guide` để quyết định.

### 8.2 Output của model

```go
type BoundaryDecision struct {
	UnitID    string   `json:"unit_id"`
	Anchor    string   `json:"anchor,omitempty"`
	Kind      string   `json:"kind"` // chapter / group / front_matter / back_matter
	Title     string   `json:"title,omitempty"`
	Uncertain bool     `json:"uncertain,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}
```

- `chapter` là đơn vịchính văn có thể submit, bao gồm cả việc chương mở đầu, đoạn dạo đầu, ngoại truyện có được tính là chương hay không — đây là quyết định ngữ nghĩa;
- `group` là bằng chứng cho tầng cấu trúc cao hơn như quyển, bộ, thiên, không trực tiếp tính là chương;
- `front_matter` / `back_matter` đánh dấu vùng phụ trợ rõ ràng không đi vàochính văn chương;
- `anchor` phải được sao chép nguyên văn từ unit tương ứng; nếu ranh giới nằm ngay ở đầu unit thì có thể bỏ qua;
- `uncertain` chỉ dùng để nhắc trong preview, code không tự đặt ngưỡng độ tin cậy.

Không để model sinh regex. Regex vẫn sẽ ép ngữ nghĩa mở quay về ngữ pháp hữu hạn và đưa vào các giả định về escape, khớp cục bộ và định dạng thống nhất.

### 8.3 Kiểm tra của code

Go chỉ kiểm tra:

1. Tất cả unit ID đều tồn tại và nằm trong projection đã gọi (owned + context; ngoài projection là ảo giác và sẽ bị phản hồi để hỏi lại);
2. Loại `kind` ở biên của vùng owned thuộc tập đóng, anchor không rỗng phải duy nhất trong unit tương ứng và ánh xạ được tới biên byte UTF-8, các ranh giới cùng vị trí nhưng khác nghĩa (kind/title không giống nhau thì Go không tự quyết giữ cái nào, mà hỏi lại model; nếu hoàn toàn trùng nhau thì đó chỉ là dư thừa cơ học, cho qua rồi khử trùng lặp âm thầm), block đầu phải ôm được điểm bắt đầu văn bản (đoạn đầu có phải lời mở đầu hay không là model quyết định, Go không thay lời) — tất cả đều được kiểm tra ngay lúc gọi (sửa 2026-07-16): giá trị xấu nếu bị đưa vào cache block rồi mới phát hiện ở cuối pipeline thì digest vẫn khớp và lỗi sẽ tái hiện y hệt; ranh giới trong context bị cắt bỏ chắc chắn nên không cần hỏi lại vì chúng;
2a. **Gọi lại tiêu đề** (sửa 2026-07-16, seg-v2): sau khi normalize, title của boundary chapter/group phải thật sự tồn tại trong văn bản của unit biên, nếu không thì phải hỏi lại ngay lúc gọi — thực tế ở một nguồn crawl phân trang, trong 157 chương có tới 67 chương là model bịa ranh giới và tiêu đề trên đoạn văn tiếp nối trong thân chương (do mơ hồ về kỷ luật bao phủ buộc mỗi block đầu khối phải có ranh giới), tất cả đều có thể chặn lại bằng đối chiếu này. Quyền quyết định ngữ nghĩa vẫn thuộc về model: với nguồn thực sự không có quy ước tiêu đề, đặt `uncertain=true` để giữ tiêu đề tổng hợp (preview hiển thị dấu hiệu nghi ngờ); tiêu đề mô tả của front/back matter là rủi ro thấp, không đối chiếu. Prompt cũng được siết lại: ranh giới chỉ nằm ở chỗ ngắt cấu trúc thật, khi đầu block là văn xuôi nối tiếp của chương trước thì trả về boundaries rỗng là output đúng (trừ phần bao phủ đầu block đầu tiên);
3. Thứ tự ranh giới và trùng lặp được Go sửa một cách xác định thay vì bác bỏ (sửa 2026-07-16): phục hồi thứ tự thật bằng cách sắp theo byte đã parse — thứ tự giữa các block được bảo đảm bởi việc các owned range không chồng lấn, nên đảo thứ tự chỉ có thể xảy ra trong nội bộ block, sắp xếp không mất thông tin; nếu trùng byte thì giữ mục xuất hiện trước và ghi vào `Notes`. Hành vi cũ yêu cầu tăng nghiêm ngặt, nếu không thì toàn bộ thất bại; thực tế 319 boundary đã thua vì chỉ 1 vị trí đảo trong block, và cache block sẽ làm lỗi đó tái hiện chắc chắn. Khi xét thứ tự thì luôn dựa theo thứ tự số của `(Line, Part)`, không so sánh ID theo thứ tự từ điển;
4. Phạm vichính văn của mỗi chương đầu ra không được rỗng (các placeholder chương rỗng được gộp vào đoạn trước, xem §8.1);
5. Mọi văn bản nguồn không rỗng đều phải thuộc đúng một chương, một tiêu đề group, hoặc một vùng front/back matter rõ ràng (đoạn không được gán ở đầu — như phần giới thiệu / quảng cáo ở đầu sách bị bỏ sót boundary — sẽ được Go thuần xác định gom thành front_matter và ghi `Notes` để đưa vào preview xác nhận, không bác bỏ ở bước cuối);
5a. Chương trùng tên (sau khi bỏ khoảng trắng thì giống nhau) được ghi vào `Notes` để người dùng kiểm tra thủ công (sửa 2026-07-16) — với nguồn có quy ước tiêu đề thì tên chương không nên lặp; lặp là tín hiệu xác định của “bị cắt nhầm cùng một chương”. Việc có gộp hay không không do Go quyết định, miễn `Notes` không rỗng thì `--yes` sẽ bị chặn;
6. Không có chồng lấn, vượt biên hay vùng chưa được gán;
7. `group` không bị tính nhầm vào tổng số chương.

“L1257 về mặt ngữ nghĩa có phải là tiêu đề chương hay không” không do Go xét lại.

### 8.4 Xác nhận của người dùng

Trong chế độ tương tác, trước khi xác nhận thì không gọi phân tích chương và cũng không ghi Store chính thức. Preview ít nhất phải hiển thị:

- Số quyển/group và số chương;
- Tất cả tiêu đề chương, có thể cuộn để xem;
- Phạm vi và tóm tắt của văn bản phụ trợ ở đầu và cuối;
- Các boundary là chương rỗng, chương quá dài bất thường và boundary model gắn `uncertain`;
- Dòng bắt đầu và kết thúc của từng chương để người dùng đối chiếu với bản gốc.

Người dùng có thể:

- Xác nhận (trong panel preview của TUI nhấn `y`, nội bộ rerun bằng AcceptSegmentation; cho qua segmentation hiện tại một lần, không ghi vào intent, confirmation ghi `method=user_confirmed`);
- Nhập mô tả bằng ngôn ngữ tự nhiên rồi nhận diện lại, ví dụ `/import --guide=phần xen kẽ·X cũng là một chương độc lập`;
- Hủy và giữ lại workspace (Esc).

`/import <path> --yes` là ủy quyền không người giám sát rõ ràng: sau khi kiểm tra bao phủ qua, Runner ghi cùng một confirmation artifact, ghi nhận `method=auto_authorized`, rồi tiếp tục phân tích. `--yes` dù tồn tại boundary `uncertain` vẫn có nghĩa là người dùng chọn tin vào segmentation lần này, nhưng `uncertain` vẫn được giữ trong artifact và log. **Ngoại lệ (sửa 2026-07-16)**: nếu segmentation có ghi chú dung sai (`Notes` không rỗng — đã xảy ra hấp thụ chương rỗng, chốt đầu sách, khử trùng lặp chồng lấn) thì `--yes` không tự động cho qua nữa, vẫn dừng ở preview xác nhận — cấu trúc đã bị viết lại một cách xác định, ủy quyền mù mà chưa xem preview không nên nuốt nó; `y` (AcceptSegmentation) sau khi đã xem preview thì không bị giới hạn này.

`--yes` chỉ bỏ qua xác nhận segmentation, không thay người dùng quyết định `story_status=uncertain`, cũng không bỏ qua Hold hoàn tất import. Người dùng không cần viết regex hay điền thủ công `from=N`.

## 9. Trích xuất fact từng chương theo batch liên tiếp

Sau khi xác nhận, từ phân tích còn thiếu đầu tiên, các chương liên tiếp được nhóm thành batch theo **hai ngân sách đầu vào và đầu ra** của model hiện tại. Phiên bản đầu tiên chạy tuần tự giữa các batch, không song song giữa các cửa sổ: ID foreshadow, bí danh nhân vật và các biến trạng thái có tính thứ tự thời gian, còn ledger gọn sinh từ batch trước là đầu vào của batch sau.

Việc chạy tuần tự chỉ ràng buộc chiến lược thực thi của phiên bản đầu tiên, không phải giới hạn kiến trúc vĩnh viễn; artifact phân tích vẫn được ghi theo từng chương riêng lẻ, và khi sau này có bằng chứng cho thấy hợp nhất song song vẫn giữ được chất lượng ngữ nghĩa thì chỉ cần thay bộ điều phối batch.

### 9.1 Output của batch, artifact theo từng chương

Bỏ envelope hỗn hợp `=== TAG ===`. Mỗi lần gọi trả về một đối tượng batch có cấu trúc, nhưng mỗi phần tử mảng vẫn là fact của một chương:

```go
type ImportedChapterFacts struct {
	Chapter             int                        `json:"chapter"`
	Title               string                     `json:"title"`
	Summary             string                     `json:"summary"`
	KeyEvents           []string                   `json:"key_events"`
	CoreEvent           string                     `json:"core_event"`
	Hook                string                     `json:"hook"`
	Scenes              []string                   `json:"scenes"`
	Characters          []string                   `json:"characters"`
	CharacterEvidence   []ImportedCharacterFact    `json:"character_evidence,omitempty"`
	WorldEvidence       []ImportedWorldFact        `json:"world_evidence,omitempty"`
	TimelineEvents      []domain.TimelineEvent      `json:"timeline_events,omitempty"`
	ForeshadowUpdates   []domain.ForeshadowUpdate  `json:"foreshadow_updates,omitempty"`
	RelationshipChanges []domain.RelationshipEntry `json:"relationship_changes,omitempty"`
	StateChanges        []domain.StateChange       `json:"state_changes,omitempty"`
	HookType            string                     `json:"hook_type"`
	DominantStrand      string                     `json:"dominant_strand"`
}

type AnalysisBatchResult struct {
	Chapters []ImportedChapterFacts `json:"chapters"`
}

type ChapterAnalysisPayload struct {
	BatchStart int                  `json:"batch_start"`
	BatchEnd   int                  `json:"batch_end"`
	Facts      ImportedChapterFacts `json:"facts"`
}
```

Mỗi `analyses/NNNNNN.json` đều là `Artifact[ChapterAnalysisPayload]`. Các bản ghi chương ghi xuống cùng một batch mang cùng `BatchStart/BatchEnd`; `InputDigest` của chúng dùng cách **ràng buộc theo từng chương**: định danh segmentation (tức `InputDigest` của artifact segmentation) + prompt/schema version + số chương + chính văn của từng chương. Sở dĩ ràng buộc theo từng chương chứ không theo phân rã batch là vì ranh giới batch thay đổi theo năng lực input/output của model (đổi model mạnh hơn thì batch tự nhiên lớn hơn); nếu đưa cách chia batch vào danh tính thì sau khi đổi model, những phân tích đã thành công sẽ mất khớp toàn bộ và buộc phải tính lại, tốn phí lặp lại. Ràng buộc với định danh segmentation thì đảm bảo rằng “segmentation đổi, prompt/schema version đổi, source đổi” sẽ làm phân tích hạ nguồn tự không khớp, còn chỉ đổi model thì không bị ảnh hưởng oan — đó mới là ngữ nghĩa vô hiệu hóa mà khôi phục thực sự cần.

`ImportedCharacterFact` và `ImportedWorldFact` là các quan sát nén để tổng hợp toàn sách, không ghi trực tiếp thành nhân vật hay quy tắc thế giới chính thức. Chúng ít nhất phải mang số chương để kết quả tổng hợp có nguồn ổn định.

### 9.2 Gộp batch với hai ngân sách

Lập kế hoạch batch phải đồng thời thỏa:

```text
input ước tính + system/prompt/ledger + phần dự trữ suy luận + output có thể thấy ước tính ≤ context window
output có thể thấy ước tính ≤ giới hạn completion có sẵn của provider/model
```

- Ước tính input bao phủ title,chính văn của mỗi chương và ledger trước batch;
- Ước tính output được tạo từ chi phí cấu trúc cố định của analyzer schema và phần dự trữ fact bảo thủ cho mỗi chương, chỉ quyết định lần này nhét được bao nhiêu chương, không cắt bất kỳ trường nào;
- Những model dùng reasoning token và JSON nhìn thấy chung một ngân sách completion thì phải trừ phần dự trữ suy luận trước;
- Provider/model càng mạnh về output thì batch càng lớn tự nhiên; không được viết quy tắc cố định “mỗi batch 10/20 chương”;
- Nếu chính input của một chương không thể đi vào context, hoặc output cấu trúc tối thiểu của một chương cũng không thể đi vào completion, thì phải báo rõ chương đó và năng lực model, không cắtchính văn hay giả tạo thành công rút gọn.

Vì vậy tổng số chương tăng chỉ làm tăng số batch, chứ không còn làm cho bất kỳ response nào tăng vô hạn theo quy mô toàn sách; đồng thời cũng không đẩy #83 từ mức toàn sách xuống một batch không bị ràng buộc bởi output.

### 9.3 Context của batch

Một lời gọi batch đơn lẻ chỉ bao gồm:

- Chính văn và title của phạm vi chương liên tiếp hiện tại;
- Bảng bí danh nhân vật rút gọn được suy ra từ các chương trước;
- Các ID foreshadow đang hoạt động và trạng thái một câu;
- Bản tóm tắt trạng thái gần nhất cần thiết.

Model xử lý các chương trong batch theo thứ tự của mảng, có thể tiếp nối bí danh, foreshadow và trạng thái trong nội bộ batch; khi batch kết thúc, Go cập nhật ledger gọn theo thứ tự fact đã được kiểm tra. Nó không phụ thuộc vào Premise toàn sách chưa được tạo, và cũng không đọc lại toàn bộ văn trước. Fact của chương là đầu vào cho Foundation, chứ không phải ngược lại tạo thành vòng phụ thuộc.

### 9.4 Kiểm tra toàn bộ phản hồi

Code kiểm tra hai lớp về cấu trúc, miền giá trị và tham chiếu, không hard-code chất lượng văn chương:

- Cấp batch: mảng chapters phải liên tục đúng số chương dự kiến, không trùng, không thiếu, phạm vi batch, `InputDigest` và schema version phải khớp;
- Cấp từng chương: chapter/title phải khớp với segmentation của nguồn, summary/core_event không được rỗng, các trường domain đóng như hook type, strand hợp lệ, các trường timeline, foreshadow và state change hợp lệ về kiểu.

Code không yêu cầu “phải có 3～6 sự kiện”, “phải có nhân vật xuất hiện”, hay “phải có ba cảnh”. Chương yên lặng, thư tín, chương môi trường hay chương không có tên nhân vật đều là hình thức văn học hợp lệ.

Khi phản hồi hoàn chỉnh có lỗi JSON hoặc lỗi kiểm tra ngữ nghĩa thì không được submit bất kỳ chương mới nào trong đó; phải phản hồi lỗi cụ thể lại cho cùng model, đi theo retry lớp output ở §13.3. Model có thể viết lại các object trước đó sau khi sửa, nên thất bại kiểm tra thông thường không được tự ý lưu một phần mảng.

### 9.5 Tiền tố liên tục khi bị cắt do độ dài

> Định vị triển khai: mục này là **tối ưu token trên đường lỗi**, không phải phụ thuộc cho tính đúng của khôi phục. v1 (giai đoạn ba) bị cắt thì coi là “fail + thu nhỏ batch và gộp lại”, tự nó đã đúng và có thể khôi phục; cứu tiền tố liên tục được hiện thực ở một tiểu giai đoạn độc lập (giai đoạn ba·bổ sung), có thể bật riêng, nghiệm thu riêng.

Chỉ khi response rõ ràng đánh dấu `StopReasonLength` và trả về một phần text có thể parse thì mới được phép lưu **tiền tố hợp lệ liên tục lớn nhất** từ phản hồi lỗi:

1. Dùng JSON decoder dạng stream để đi vào mảng `chapters` ở cấp gốc;
2. Từ chương đầu của batch, đọc lần lượt các object JSON đã đóng hoàn chỉnh;
3. Mỗi object được kiểm tra độc lập theo §9.4 ở cấp từng chương, và sau khi ghép với các object trước đó thành một chuỗi liên tục bắt đầu từ chương đầu batch thì ghi nguyên tử ngay vào artifact phân tích của chương tương ứng;
4. Gặp object đầu tiên không hoàn chỉnh, không hợp lệ, nhảy số hoặc trùng lặp thì dừng ngay, mọi byte phía sau không được diễn giải;
5. Cấm thêm dấu ngoặc, nối tiếp nửa JSON, đoán field bị thiếu hoặc kéo object không liên tục từ phía sau;
6. Phản hồi gốc, StopReason, phạm vi tiền tố đã lưu và chương lỗi đầu tiên đều phải ghi vào failure artifact, event và log;
7. `NextAction` sẽ gộp batch lại từ phân tích đầu tiên còn thiếu, không làm lại tiền tố hợp lệ đã nộp.

typed-call phải ghi nhận lần này có lấy được text một phần dùng được hay không: chế độ JSON Schema hay cấu trúc không streaming có thể khi dừng do độ dài thì không cung cấp tiền tố có thể parse. Nếu provider không trả phần text, không thể chứng minh rõ là cắt do độ dài, hoặc chưa hoàn thành được dù chỉ một object hợp lệ, thì không lưu kết quả nào, phát event/log `prefix_salvage=unavailable` và quay về “fail + thu nhỏ batch và gộp lại”, chứ không được im lặng quay vòng. Nếu batch chỉ còn một chương mà vẫn bị cắt thì báo thẳng model output không đủ dung lượng, không tiếp tục thu nhỏ hay bịa ra fact rỗng.

Cắt do độ dài là lỗi dung lượng, không tiêu hao ba lần retry ngữ nghĩa “gửi lỗi kiểm tra lại cho cùng model”, cũng không được retry nguyên batch y như cũ.

### 9.6 Khôi phục

Mỗi lần phân tích chương thành công đều được ghi nguyên tử vào `analyses/NNNNNN.json`. Sau khi sập:

- Phân tích có `InputDigest` khớp thì tái sử dụng trực tiếp, không thu phí lại;
- Phân tích đầu tiên còn thiếu hoặc lệch trở thành điểm bắt đầu của batch tiếp theo;
- Khi input ngữ nghĩa thượng nguồn thay đổi, những phân tích không thể tái tạo cùng `InputDigest` sẽ tự hết hiệu lực;
- Tiền tố hợp lệ liên tục đã nộp trong trường hợp cắt do độ dài và artifact hoàn thành bình thường đều dùng cùng một quy tắc khôi phục;
- Không cho phép người dùng vượt qua một chương lỗi rồi tiếp tục sinh các fact ngữ nghĩa không liên tục phía sau.

## 10. Tổng hợp phân tầng

### 10.1 Vì sao không thể tiếp tục xuất một lần cho cả sách

Tổng hợp toàn sách cần hiểu xuyên chương, nhưng không cần đọc lại toàn bộchính văn, cũng không nên xuất lại từng object chương chi tiết. Fact của từng chương đã chứa ngữ nghĩa cấp chương; phần tổng hợp chỉ xử lý những fact nén này.

### 10.2 Hình dạng Map/Reduce

```text
ImportedChapterFacts × N
        ↓ chia theo khoảng liên tiếp trong context window hiện tại
RangeDigest × M
        ↓ nếu cần thì tiếp tục hợp nhất
BookSynthesis
```

Nếu sách ngắn mà một lần có thể chứa hết fact chương thì tạo trực tiếp `BookSynthesis`; chỉ sách dài mới sinh `RangeDigest`. Việc có chia tầng hay không do ngân sách token quyết định một cách cơ học, không do ngưỡng số chương quyết định.

`RangeDigest` chứa tiến triển cốt truyện của khoảng liên tiếp đó, thay đổi nhân vật, fact thế giới, foreshadow đã mở/đã đóng và các ranh giới cấu trúc ứng viên. Kích thước output của nó bị giới hạn bởi từng khoảng; tổng hợp cuối cùng không xuất lại N bản chi tiết của chương, mà chỉ xuất fact toàn cục và phạm vi arc quyển.

### 10.3 Kết quả tổng hợp cuối cùng

```go
type BookSynthesis struct {
	Premise       string                 `json:"premise"`
	Characters    []domain.Character     `json:"characters"`
	WorldRules    []domain.WorldRule     `json:"world_rules"`
	Structure     []ImportedVolumeRange  `json:"structure"`
	Compass       domain.StoryCompass    `json:"compass"`
	PlanningTier  domain.PlanningTier    `json:"planning_tier"`
	StoryStatus   string                 `json:"story_status"` // open / closed / uncertain
	StatusReason  string                 `json:"status_reason"`
}
```

Cấu trúc chỉ trả về các khoảng, không lặp lại toàn bộ chương:

```go
type ImportedVolumeRange struct {
	Title string             `json:"title"`
	Theme string             `json:"theme"`
	Arcs  []ImportedArcRange `json:"arcs"`
}

type ImportedArcRange struct {
	Title        string `json:"title"`
	Goal         string `json:"goal"`
	StartChapter int    `json:"start_chapter"`
	EndChapter   int    `json:"end_chapter"`
}
```

Model tự quyết định số quyển và số arc, có thể tham khảo tiêu đề group trong source, nhưng không bị ràng buộc bởi “một quyển” hay “1～3 arc”. Go dùng `ImportedChapterFacts` để ghép `title/core_event/hook/scenes` thành `OutlineEntry` chính thức.

### 10.4 Trạng thái truyện

Import chỉ tái tạo fact củachính văn, không bịa các tuyến dài chưa khép để ép Engine chạy tiếp:

- `open`: trongchính văn thật sự còn mục tiêu hoặc căng thẳng chưa khép, bình thường sinh Compass;
- `closed`: phát hành như tác phẩm đã hoàn tất, quyển cuối gắn Final; nếu cần viết tiếp thì người dùng phải reopen rõ ràng và nêu hướng mới;
- `uncertain`: trước khi phát hành, yêu cầu người dùng chọn xử lý như chưa hoàn hay đã hoàn; nếu Intent đã lưu lựa chọn qua `--story=open|closed` thì dùng ngay, nếu không thì chờ tương tác. Lựa chọn được lưu thành artifact `story-resolution.json` với input là synthesis hiện tại.

Code không âm thầm đoán ý người dùng chỉ vì `open_threads` có rỗng hay không.

## 11. Ghép và xác minh Foundation

Model xuất ngữ nghĩa tổng hợp, còn Go chịu trách nhiệm ghép các domain object chính thức. Trước khi phát hành phải thỏa:

1. Premise có tiêu đề sách hợp lệ; nếu không xác nhận được tên sách từchính văn thì dùng basename của file nguồn và đánh dấu nguồn là filename, không để model khẳng định đó là “tên thật của sách”;
2. Tất cả quyển và arc phải liên tiếp theo đúng thứ tự;
3. Khoảng đầu tiên phải bắt đầu từ chương 1, khoảng cuối cùng kết thúc ở chương N;
4. Mỗi chương thuộc đúng một arc;
5. Sau `FlattenOutline` thì số chương là N, title khớp với fact từng chương;
6. Tên nhân vật, quy tắc thế giới và Compass thỏa các ràng buộc kiểu domain hiện có;
7. PlanningTier là một giá trị hợp lệ trong tập đóng, nhưng lý do lựa chọn phải đến từ model chứ không phải ngưỡng số chương;
8. Trạng thái closed/open phải nhất quán với Final và hình thái phát hành của Compass;
9. InputDigest của artifact Synthesis phải có thể được tái tạo từ tập analysis có thứ tự hiện tại.

Nếu vi phạm ràng buộc cấu trúc thì phản hồi lỗi cụ thể lại cho model để sinh lại; sau số lần retry cấu trúc hữu hạn thì fail rõ ràng, không ghi các phần dở dang.

## 12. Phát hành chính thức

### 12.1 Điều kiện tiên quyết trước khi phát hành

Import mới chỉ được phép đi vào:

- Không có chương nào đã hoàn tất;
- Không có chương đang xử lý hay PendingCommit;
- Không có workspace import khác không cùng nguồn;
- Foundation chính thức trống, hoặc có digest hoàn toàn khớp với digest đã phát hành từ workspace hiện tại.

Ý nghĩa gộp giữa sách đã có và văn bản ngoài mới là không rõ ràng; phiên bản đầu tiên từ chối thẳng, không đoán ghi đè hay nối thêm.

### 12.2 Phát hành Foundation

Phát hành theo đúng thứ tự phụ thuộc chính thức:

```text
planning tier
→ premise
→ characters
→ world rules
→ layered outline + flat outline
→ compass
→ progress đối soát
```

Mỗi bước:

1. Tính digest của nội dung sẽ phát hành;
2. Nếu artifact chính thức chưa tồn tại thì ghi nguyên tử và thêm checkpoint;
3. Nếu đã tồn tại và digest giống nhau thì bỏ qua theo kiểu idempotent;
4. Nếu đã tồn tại nhưng khác thì trả lỗi xung đột, không ghi đè.

Khi đang chạy mà sập thì chỉ cần đối soát lại từ mục đầu tiên, không cần giao dịch xuyên file hay state machine Foundation Pending.

### 12.3 Phát hành chương

Theo thứ tự chương, tái sử dụng flow hiện có:

```text
lưu draft
→ Progress.StartChapter
→ commit_chapter(fact từng chương)
```

`commit_chapter` đã có saga PendingCommit, checkpoint và kiểm tra idempotent cho chương đã hoàn tất. Import không sao chép thêm một bộ logic commit khác.

Cửa sổ sập:

| Cửa sổ | Hành vi khôi phục |
|---|---|
| Trước draft | Lưu lại cùng mộtchính văn |
| Sau draft, trước StartChapter | Đối soát digest rồi tiếp tục |
| Sau StartChapter, trước PendingCommit | Thực thi lại commit của cùng chương |
| Trong PendingCommit | Khôi phục bởi saga commit hiện có |
| Sau chapter complete | Nếu digest/checkpoint khớp thì bỏ qua |
| Nội dung chính thức xung đột với digest nguồn | Dừng rõ ràng, báo chương xung đột |

### 12.4 Ranh giới hoàn tất import

Sau khi tất cả chương đã được commit ổn định, đặt một lần `AdvanceHoldAtBoundary`, với lý do rõ ràng là “import tiểu thuyết bên ngoài hoàn tất, chờ nghiệm thu rồi mới viết tiếp”. Nó chỉ bảo vệ lần import xuyên hệ thống này, không thay đổi chế độ `auto/review` dài hạn của người dùng.

`--yes` chỉ ủy quyền tự động chấp nhận segmentation, không thể ngầm bỏ qua Hold này. Chỉ khi người dùng đồng thời truyền thêm `--continue` riêng thì Runner mới không tạo Hold dành riêng cho import; sau đó vẫn tuân thủ advance mode bình thường: `auto` có thể viết tiếp, `review` vẫn chờ `/next`.

Mặc định TUI không còn tự động tiếp nối mà không báo trước. Sau khi người dùng kiểm tra Foundation và trạng thái chương, họ dùng điểm vào tiếp tục hiện có để nối mạch sáng tác.

**Điểm đóng panel**: sau khi import hoàn tất thành công từ trang chào, nếu nhấn Esc đóng panel thì sẽ chạy bổ sung một lần `Resume()` (gate khôi phục của bootstrap chỉ chạy một lần lúc khởi động), người dùng sẽ rơi thẳng vào workspace, bị Hold hoàn tất import chặn ở ranh giới chương kế tiếp để nghiệm thu — thay vì ở lại trang chào có thể bấm nhầm Enter để “tạo sách mới”. Trạng thái kết thúc lỗi và cảnh của workspace chỉ đóng panel.

**Rào chắn tạo mới**: `PrepareUserRules` / `StartPrepared` sẽ từ chối tạo mới khi trong thư mục sách đã có chương hoàn tất (`CompletedChapters` không rỗng) — vì ngay đầu `StartPrepared` đã reset checkpoints và progress, nếu bấm nhầm thì cả cuốn sách (kể cả toàn bộ chương vừa import) sẽ bị xóa âm thầm. Các phần chuẩn bị của plan chưa có chương thì cho qua, giữ lại đường tự hồi phục qua Ctrl+S cùng phiên và cơ chế cắt bù khôi phục của cộng tác.

### 12.5 Gate phát hành xuyên restart

Khi workspace tồn tại và `NextAction != done`, `Host.New/Resume` phải nhận diện sách là import chưa hoàn tất:

- Cho phép xem, chẩn đoán và chạy phục hồi `/import`;
- Cấm khởi động Engine bình thường, Continue, hoặc phát Writer;
- Hiển thị rõ hành động khôi phục hiện tại, không coi Foundation/chương đã phát hành một phần là một cuốn sách hoàn chỉnh có thể viết tiếp.

Gate này đọc trực tiếp workspace và Store chính thức để suy luận, không thêm `published bool`. Nhờ vậy, dù sập ở bất kỳ cửa sổ phát hành nào, nó cũng sẽ không bị luồng sáng tác bình thường tiêu thụ trạng thái nửa phát hành khi Runner chưa kịp khôi phục.

## 13. Kernel gọi model

Bên trong `imp` giữ một helper typed-call nhỏ, chuyên dụng, không xây khung workflow LLM tổng quát.

### 13.1 Chọn model

- Mặc định dùng model của role architect;
- Các semantic function có núm model mở: segment/analyze/synthesize có thể khai báo mức riêng, mặc định rơi về architect; lớp cấu hình có thể chỉ segment — vốn cơ học hơn — sang mức rẻ hơn. Đây là cấu hình gọi, không đổi bất kỳ hợp đồng ngữ nghĩa nào, cũng không biến “một role duy nhất” thành tiền đề kiến trúc — mục tiêu là để phần lợi ích chi phí từ “model mức rẻ mạnh hơn” cũng đi vào được;
  - Điểm hiện thực: cấu hình roles hỗ trợ key `import_segment` / `import_analyze` / `import_synthesize` ba role; nếu không cấu hình thì rơi về architect. Ngân sách kép và tùy chọn thinking/cấu trúc của từng function được suy ra độc lập theo năng lực thật của từng mức (window nhỏ của mức rẻ chỉ giới hạn đúng function của nó), usage được ghi công theo role mức thực tế;
- Kế thừa failover đã cấu hình cho role được chọn;
- Dùng reasoning effort của role được chọn, và qua kiểm tra năng lực để quyết định có gửi tham số thinking hay không;
- Ghi metadata session và usage theo provider/model thật;
- Kết hợp các sentinel ngân sách hiện có (đã triển khai 2026-07-16): trước khi khởi động chạy cùng kỷ luật với `Refuse()` và Start/Resume/Continue; trong khi chạy, hard stop ngân sách dùng `abortWithEvent` để hủy context riêng của import (Host đăng ký cancel cho job độc quyền, không còn chỉ tạm dừng một Engine chưa chạy).

### 13.2 Năng lực output có cấu trúc

Hiện tại thống nhất dùng hợp đồng prompt JSON, không gửi `response_format`. Cách trước đây bật JSON Object mode dựa trên năng lực provider đã bị gỡ bỏ (2026-07-16): bảng năng lực của litellm là cấp provider, còn hỗ trợ `response_format` là sự thật ở **cấp model** — nếu qua gateway tổng hợp (openrouter) mà phát `json_object` theo năng lực provider thì các model không hỗ trợ sẽ trả HTTP 400 ngay (thử nghiệm tencent/hy3 chỉ hỗ trợ `json_schema`, bị Novita ở lớp trên từ chối). Pipeline vốn đã dựa vào hợp đồng prompt + extractJSONObject + validate để phản hồi và hỏi lại, object mode chỉ là phần cộng thêm, không đáng để đưa vào một loại hard failure mới.

**TODO(json-schema)**: sẽ cải tạo đồng bộ với các điểm gọi model khác trong toàn repo (arbiter ra quyết định, engine semantic tools, v.v.) sang output ràng buộc JSON Schema; khi đó phải kiểm tra theo năng lực **ở cấp model** rồi mới gửi (xem TODO trong `imp/call.go` callProfile). Trước khi cải tạo, hành vi vẫn đúng, chỉ là mất một phần độ mạnh của ràng buộc.

Dù provider có ràng buộc output hay không, Go vẫn thực thi cùng một bộ parse và kiểm tra. Không được im lặng xóa tham số rồi thử lại sau khi request báo lỗi; việc nhận diện năng lực sai hoặc provider từ chối phải được bộc lộ.

### 13.3 Tách lỗi request, lỗi ngữ nghĩa và lỗi dung lượng

- Lỗi tầng request: chỉ retry các timeout, rate limit, lỗi mạng mà adapter xác định rõ là retryable, dùng lại ngữ nghĩa backoff hiện có;
- Lỗi tầng output: phản hồi lỗi JSON parse hoặc Validate cụ thể lại cho cùng model, tối đa 3 lần; nếu vẫn thất bại thì dừng hành động hiện tại.
- Không được retry một cách âm thầm: mỗi lần backoff của tầng request (“retry lần N/7 · sẽ thử lại sau Xs”) và mỗi lần hỏi lại của tầng output đều phải hiển thị thành event tiến độ trên panel import — backoff theo cấp số nhân có thể cộng dồn trên 2 phút, không có phản hồi người dùng sẽ tưởng là bị treo. Event backoff chỉ mang thời điểm hạn chót (`RetryAt`), số giây còn lại do tầng render tính theo từng tick để tạo đếm ngược thời gian thực (dùng chung cơ chế với panel event của workspace sáng tác); spinner thường trực ở đầu panel cùng thời lượng đã dùng, cuối log còn có con trỏ sao kiểu stream tương tự.
- Không được hồi đáp lỗi một cách chung chung: message từ gateway thường chỉ có một câu “Provider returned error”; khi hiển thị và khi viết failure, phải kèm luôn fact có cấu trúc từ adapter (loại lỗi / HTTP status / provider / model, `modelErrDetail` được trích từ chuỗi lỗi litellm bằng errors.As), fact phải đặt lên trước để nếu bị cắt thì vẫn giữ được điều cốt lõi.
- Các giai đoạn kéo dài không được im lặng: segmentation theo từng block, tổng hợp theo từng khoảng đều gọi model bên trong hàm (một block có thể kéo dài vài phút), phải hiển thị tiến độ theo từng block/từng khoảng qua `callProfile.step` (“segmentation block N/M, đã nhận diện K ranh giới”). Key của event chỉ dành cho backoff request (một dao động thoáng qua trong cùng một lời gọi, hiển thị tại chỗ); retry ngữ nghĩa do kiểm tra lại là một sự kiện xuyên lời gọi, mỗi cái giữ một dòng lịch sử riêng — dùng chung key sẽ làm block sau ghi đè block trước, mất sạch đầu mối điều tra.
- Chuyển ngữ log đầy đủ: mọi event tiến độ (kể cả các dòng retry bị panel đè ngay tại chỗ) đều được ghi vào **log riêng của import** `<gốc sách>/logs/import.log` (không trộn với tui.log, mỗi lần import một file để xem transcript đầy đủ); backoff request và hỏi lại ngữ nghĩa cũng phải ghi chung một log với toàn bộ chuỗi lỗi.
- Hiển thị ngữ nghĩa model chứ không chỉ đếm cơ học: segmentation hiển thị title model nhận diện được (“model nhận ra: Chương mười hai Gió tuyết đêm / … (tổng N chỗ)”), analysis hiển thị core event của từng chương (“Chương 12〈Gió tuyết đêm〉: …”), synthesis hiển thị tóm lược toàn sách (tóm tắt premise) — người dùng phải thấy model đã đọc hiểu điều gì.
- Lỗi dung lượng: `StopReasonLength` không retry nguyên trạng, cũng không tiêu hao ba lần retry ngữ nghĩa; analysis batch nếu có thể parse một phần text thì theo §9.5 lưu tiền tố hợp lệ liên tục, nếu không thì ghi `prefix_salvage=unavailable` và thu nhỏ batch để gộp lại; các semantic function khác thì fail rõ ràng và giữ nguyên phản hồi gốc.

Xác thực, quyền hạn, model không hỗ trợ và xung đột trạng thái phải fail ngay. Không có thành công giả lập, không có object rỗng cứu hộ, và không bỏ qua chương lỗi.

### 13.4 Ngân sách input và output

Mỗi semantic function có schema riêng, ngân sách input riêng, phần dự trữ suy luận riêng và ngân sách output nhìn thấy riêng:

- Output của segmentation chỉ gồm boundary của range owned hiện tại;
- analysis batch đồng thời bị ràng buộc bởi context window và giới hạn completion, output là fact của một dải chương liên tiếp hữu hạn;
- RangeDigest chỉ chứa một khoảng liên tục;
- BookSynthesis chỉ chứa fact toàn cục và phạm vi arc quyển, không lặp lại object chương.

Mỗi request trước khi gửi đều ghi lại input ước tính, phần dự trữ suy luận, max tokens được xin và output có thể thấy ước tính. Ước tính chỉ quyết định cách chia block/batch, không xóachính văn hay field fact. Vì vậy không tồn tại cấu trúc kiểu “tổng số chương càng nhiều thì một response bất kỳ nhất định phải dài hơn”, cũng không thể chỉ vì input nhét vừa mà bỏ qua rủi ro cắt output.

## 14. Event, log và chẩn đoán

### 14.1 Giai đoạn event

```go
const (
	StageIngesting            Stage = "ingesting"
	StageSegmenting           Stage = "segmenting"
	StageAwaitingConfirmation Stage = "awaiting_confirmation"
	StageAnalyzing            Stage = "analyzing"
	StageSynthesizing         Stage = "synthesizing"
	StageAwaitingStoryStatus  Stage = "awaiting_story_status"
	StageValidating           Stage = "validating"
	StagePublishing           Stage = "publishing"
	StageDone                 Stage = "done"
	StageError                Stage = "error"
)
```

Mỗi event bao gồm action, chương/khoảng hiện tại, tổng số, thời lượng và lỗi tùy chọn. Event của analysis batch còn bao gồm phạm vi batch, ước tính ngân sách, StopReason và phạm vi tiền tố đã nộp. Event chỉ là projection, không tham gia khôi phục.

### 14.2 Lỗi phải đến đủ ba nơi

1. Panel import của TUI: tự động xuống dòng, giữ đầy đủ chuỗi lỗi;
2. `tui.log`: ghi có cấu trúc stage, chapter/range, model, attempt và error;
3. `meta/import/failures/`: lưu metadata của lần thất bại cuối cùng và phản hồi model chưa bị cắt xén.

Chính văn gốc của tiểu thuyết không được ghi vào log thường, cũng không đi vào bản export chẩn đoán mặc định đã khử định danh. Phản hồi lỗi nằm trong thư mục sách của chính người dùng, và thông tin lỗi phải chỉ rõ đường dẫn.

### 14.3 Session và Usage

Mỗi lần gọi ngữ nghĩa đều ghi:

- tên task ổn định, như `import/segment/0003`, `import/analyze/0054-0061`;
- phản hồi assistant nguyên gốc;
- provider/model và usage;
- structured mode, thinking level và kết quả kiểm tra output.

Usage được quy về role architect một cách thống nhất, để ngân sách chi phí import được nhìn thấy rõ.

## 15. Vòng đời và đồng thời

- Import và Engine, sáng tác theo giai đoạn, simulation có các thao tác ghi loại trừ lẫn nhau;
- Trong lúc import, cùng một cuốn sách chỉ cho phép một Runner;
- Hủy của người dùng sẽ hủy các lời gọi model đang thực thi, các facts workspace đã được ghi nguyên tử vẫn giữ;
- Hủy trước khi xác nhận sẽ không sửa Store chính thức;
- Sau khi phát hành bắt đầu, hủy sẽ không đoán hồi phục ngược; lần sau chỉ có thể khôi phục phát hành một cách chính xác;
- Phiên bản đầu của analysis batch chạy tuần tự giữa các batch, còn trong batch model trả fact theo thứ tự chương; phát hành chính thức vẫn theo chương tuần tự;
- `Host.New/Resume` khi import chưa hoàn tất sẽ thực thi gate ở §12.5, nên tính loại trừ lẫn nhau vẫn giữ đúng qua cả restart tiến trình;
- Việc export có cho phép song song hay không vẫn giữ ngữ nghĩa chỉ đọc hiện có, nhưng nó chỉ nhìn thấy những chương đã được phát hành chính thức.

## 16. Bất biến cốt lõi

1. Mỗi artifact workspace đều được định danh bởi `SchemaVersion + InputDigest + Payload`; chỉ khi có thể tái tạo cùng `InputDigest` từ đầu vào ngữ nghĩa thật sự hiện tại thì mới được tái sử dụng.
2. Manifest tương ứng với một snapshot nguồn chuẩn hóa duy nhất; mỗi đoạn nguồn không rỗng phải có đúng một nơi thuộc về.
3. Model chỉ được tham chiếu SourceUnit, mốc nguyên văn và số chương do Host cung cấp; Go chỉ chấp nhận tọa độ có thể ánh xạ duy nhất trở lại byte nguồn.
4. analysis batch chỉ được nộp khi phản hồi đầy đủ, hoặc khi `StopReasonLength` thì nộp tiền tố hợp lệ liên tục lớn nhất tính từ chương đầu; chỉ cần thiếu một chương là sẽ chặn phân tích và tổng hợp phía sau.
5. Phạm vi arc quyển phải liên tiếp, không chồng lấn và bao phủ đầy đủ `1..N`; Foundation chính thức chỉ được phát hành từ Synthesis đã qua kiểm tra đầy đủ.
6. Chương chính thức chỉ được phát hành theo thứ tự qua `commit_chapter`; artifact chính thức đã tồn tại chỉ có thể tái sử dụng idempotent khi digest nội dung giống nhau, khác nhau thì fail xung đột.
7. Bất kỳ lỗi model nào cũng không được diễn giải thành “không có nội dung” hay “đi sang chương tiếp theo”, không được sửa nửa JSON hay bỏ qua chương lỗi.
8. `done` phải được chứng minh đồng thời bởi workspace artifact, artifact chính thức, Progress, PendingCommit và checkpoint; trước `done` thì Engine bình thường không được khởi động.

## 17. Cấu trúc package và interface hẹp

Giữ `internal/host/imp`, tách theo trách nhiệm:

```text
imp/
├── types.go       Công khai Options/Event và các semantic DTO
├── source.go      Đọc, giải mã, chuẩn hóa, SourceUnit/anchor
├── workspace.go   artifact nguyên tử của meta/import và InputDigest
├── call.go        typed LLM call chuyên cho import
├── segment.go     projection cấu trúc, semantic function boundary, kiểm tra bao phủ
├── analyze.go     batch liên tiếp hai ngân sách, fact theo từng chương và tiền tố cắt ngắn
├── synthesize.go  RangeDigest và BookSynthesis
├── publish.go     đối soát Foundation và phát hành commit_chapter
└── runner.go      LoadState → NextAction → thực thi
```

Không thêm `ImportEngine`, `Task`, `WorkflowInstance`, repository tổng quát hay registry plugin.

Deps do Host tiêm vào giữ hẹp:

```go
type Deps struct {
	Store         *store.Store
	CommitChapter ChapterCommitter
	Model         agentcore.ChatModel
	Runtime       ModelRuntime
	Prompts       Prompts
	Emit          func(Event)
}
```

`ModelRuntime` chỉ mang context window, giới hạn completion, thinking, callback session/usage và các fact gọi khác, đồng thời dành chỗ chọn mức model cho từng semantic function (mặc định architect); không để `imp` phụ thuộc ngược vào toàn bộ Host, cũng không hàn cứng một role duy nhất thành tiền đề kiến trúc.

## 18. Giao diện người dùng

### 18.1 Import mới

```text
/import <path> [--yes] [--story=open|closed] [--continue] [--guide=<hướng_dẫn_segmentation>]
```

Hành vi mặc định: tạo source snapshot, segmentation ngữ nghĩa và mở preview xác nhận, sau khi phát hành xong thì đặt một Hold riêng cho import. Bỏ `from=N`.

Ba tùy chọn đầu là các ủy quyền rõ ràng độc lập với nhau và được ghi vào `intent.json`:

- `--yes`: sau khi kiểm tra bao phủ qua thì tự động chấp nhận segmentation; không quyết định trạng thái truyện uncertain, không bỏ qua Hold hoàn tất;
- `--story=open|closed`: chỉ khi synthesis trả về uncertain thì mới cung cấp trước lựa chọn của người dùng; nếu model đã xác định rõ open/closed thì không ghi đè fact của model;
- `--continue`: không tạo Hold riêng cho import; không vượt qua advance mode bình thường, với `review` vẫn phải chờ `/next`.

`--guide` khác với ba tùy chọn kia: nó không phải là ủy quyền khởi động mà là đầu vào ngữ nghĩa của segmentation, được ghi ra workspace `guidance.txt` (có thể chứa dấu cách, phải đặt ở cuối lệnh). Xem §18.3.

Vì vậy `/import book.txt --yes` vẫn sẽ dừng lại sau khi import hoàn tất; chỉ khi truyền thêm `--continue` thì mới ủy quyền cho luồng sáng tác tiếp tục khi gate bình thường cho phép.

### 18.2 Khôi phục

Khi cùng một cuốn sách đã có workspace đang hoạt động thì chạy `/import` không tham số sẽ suy ra ngay bước tiếp theo từ facts và intent đã lưu; đường dẫn file nguồn và tham số khởi động không phải là điều kiện bắt buộc cho khôi phục. `/import <path>` với path mới không được ghi đè workspace đang hoạt động.

Import chưa hoàn tất phải được nhìn thấy chủ động, không chờ đến khi người dùng sáng tác bị gate từ chối mới lộ ra. Hiện thực bằng ba tầng nhắc nhở tăng dần:

1. Lúc khởi động, TUI kiểm tra một lần (`imp.ResumeSummary`, sinh mô tả theo giai đoạn dựa trên `NextAction`), màn hình chào làm nổi bật thông báo “phát hiện import chưa hoàn tất (đã phân tích N/M chương), nhập /import để khôi phục từ điểm dừng”;
2. Khi người dùng bỏ qua nhắc nhở và cố sáng tác, gate xuyên restart (§12.5) sẽ từ chối khởi động Engine và phát event cảnh báo;
3. Trong lúc khôi phục chạy, panel import hiển thị thời gian thực giai đoạn và tiến độ hiện tại.

### 18.3 Cắt lại

Sau khi kiểm tra preview, người dùng có thể dùng `/import --guide=<mô tả bằng ngôn ngữ tự nhiên>` để nhận diện lại, ví dụ `--guide=phần xen kẽ·X cũng là một chương độc lập`. Hướng dẫn được ghi vào `guidance.txt` trong workspace và được đưa vào `InputDigest` của segmentation: khi guidance đổi thì segmentation cũ, confirmation cũ, phân tích cũ và synthesis cũ đều không thể tái tạo cùng `InputDigest`, nên tự động phải làm lại toàn bộ; không cung cấp trình chỉnh sửa regex.

### 18.4 Hủy

Hủy trước khi xác nhận chỉ giữ workspace; trước khi phát hành có thể cố ý bỏ cả workspace. Sau khi phát hành bắt đầu thì không còn thao tác bỏ kiểu “giả như chưa có gì xảy ra”, mà chỉ cho phép hoàn tất khôi phục hoặc để người dùng tự xử lý sách chính thức theo cách khác.

## 19. Trình tự triển khai

### Giai đoạn một: workspace và suy luận trạng thái thuần túy

- Manifest, Intent, source snapshot, `Artifact/InputDigest`, đọc/ghi nguyên tử;
- `LoadState/NextAction`;
- Kiểm tra tiền đề cho sách trống và khôi phục cùng nguồn;
- Xóa phụ thuộc thiết kế `ResumeFrom`.

Giai đoạn này không gọi model, trước hết phải chứng minh facts khôi phục là không mơ hồ.

### Giai đoạn hai: segmentation ngữ nghĩa và xác nhận

- SourceUnit, virtual split cho dòng quá dài, mốc nguyên văn và chia block theo ngân sách context;
- BoundaryDecision typed call;
- Kiểm tra bao phủ toàn văn;
- Preview TUI, nhận diện lại bằng ngôn ngữ tự nhiên, `--yes` và artifact confirmation.

Trước tiên dùng tiêu đề không chuẩn, tiêu đề quyển, lời mở đầu và hậu chú để kiểm chứng “không rơi mất một chữ”.

### Giai đoạn ba: fact theo chương với batch liên tiếp

- `ImportedChapterFacts`;
- Lập kế hoạch batch với hai ngân sách context/completion;
- Phân tích tuần tự giữa các batch và ledger liên tục rút gọn;
- Khôi phục artifact `InputDigest` theo từng chương;
- Khi bị cắt thì coi là “fail + thu nhỏ batch và gộp lại”, đồng thời ghi lại xem text một phần có dùng được hay không;
- Nối dây session, usage, failover, thinking, lỗi dung lượng và retry phản hồi cấu trúc.

### Giai đoạn ba·bổ sung: cứu tiền tố khi bị cắt (tối ưu hiệu suất, có thể làm sau)

- Phân tích tiền tố hợp lệ liên tục cho `StopReasonLength` (§9.5);
- Chỉ bật khi có thể parse một phần text, không đổi tính đúng của khôi phục; bật riêng, nghiệm thu riêng.

### Giai đoạn bốn: tổng hợp phân tầng và Foundation

- RangeDigest nhận biết theo context;
- BookSynthesis;
- Cấu trúc arc quyển dạng phạm vi;
- StoryStatus;
- Ghép đầy đủ và kiểm tra Foundation.

### Giai đoạn năm: phát hành và bàn giao

- Đối soát digest theo từng artifact của Foundation;
- Tái sử dụng `commit_chapter` để phát hành;
- Hủy / khôi phục sau sập;
- Gate Engine xuyên restart;
- Hold hoàn tất import mặc định và `--continue` rõ ràng;
- Log / failure artifact / TUI đầy đủ.

### Giai đoạn sáu: xóa hiện thực cũ

- Xóa quyết định format chương trong `splitter.go`;
- Xóa tagged envelope;
- Xóa lời gọi `ReverseFoundation` cho cả sách;
- Xóa ngưỡng số chương `pickScale`;
- Xóa `ResumeFrom/from=N`;
- Xóa ràng buộc prompt kiểu “một quyển cố định, 1～3 arc, open threads bắt buộc”;
- Sau khi hiện thực xong mới cập nhật README và các mô tả luồng cũ trong architecture.

## 20. Kiểm thử và nghiệm thu

### 20.1 Kiểm thử hàm thuần và kiểm thử thuộc tính

- Mọi segmentation hợp lệ đều thỏa phạm vi toàn văn không chồng lấn, không có lỗ hổng;
- SourceUnit không hợp lệ, mốc nguyên văn không duy nhất, boundary đảo thứ tự và trùng lặp đều phải bị từ chối;
- Thứ tự boundary phải được xét theo thứ tự số `(Line, Part)`; tạo bộ unit mà kết luận giữa thứ tự từ điển và thứ tự số trái ngược, rồi assert theo thứ tự số là qua;
- Cả dòng bình thường và virtual split đều ánh xạ không mất mát trở lại cùng một nguồn byte đã chuẩn hóa;
- Bất kỳ dải arc quyển hợp lệ nào cũng phải bao phủ đúng `1..N`;
- Cùng một đầu vào ngữ nghĩa phải tạo ra cùng `InputDigest`; bất kỳ thay đổi input thật nào cũng làm artifact tương ứng lệch khớp;
- Gộp batch hai ngân sách không vượt giới hạn context/completion đã cho;
- NextAction phải cố định với cùng một snapshot facts.

Thực hiện fuzz/property test cho ánh xạ tọa độ, ghép phạm vi, ngân sách batch và `InputDigest`, không assert model sẽ xuất đúng một tiêu đề cố định nào.

### 20.2 Kiểm thử hợp đồng model

- Tên chương không chuẩn và cấu trúc quyển/chương trộn lẫn;
- Mở đầu / dạo đầu / ngoại truyện được model quyết định ngữ nghĩa là chương;
- front/back matter được hiển thị rõ ràng chứ không bị bỏ;
- Cả cuốn một dòng, một dòng nhiều chương và dòng vượt ngân sách đều được segmentation chính xác qua SourceUnit + anchor;
- Chương yên lặng được phép có characters rỗng;
- JSON không hợp lệ, thiếu field, phạm vi vượt biên đi vào luồng retry phản hồi;
- analysis batch trả về các object chương liên tiếp, không được nhảy số hoặc trùng;
- `StopReasonLength` chỉ lưu tiền tố hợp lệ liên tục lớn nhất, không lưu nửa object và không lưu object không liên tục phía sau;
- Khi chế độ structured không cho ra text một phần có thể parse, phải xác nhận đi theo “fail + thu nhỏ batch và gộp lại” và log phải đánh dấu `prefix_salvage=unavailable`;
- `StopReasonStop` bình thường với JSON bị hỏng không đi vào đường cứu tiền tố;
- Khi batch chỉ còn một chương mà vẫn bị cắt thì fail rõ ràng, không sinh fact rỗng;
- Khi thử lại 3 lần vẫn fail thì giữ nguyên phản hồi gốc và dừng;
- Model không hỗ trợ thinking/JSON Schema thì không được nhận tham số không hợp lệ.

Các bài test model kiểm tra hợp đồng và bất biến, không kiểm tra quyết định văn chương chính xác tới từng chữ.

### 20.3 Ma trận sập

Ít nhất phải bao phủ:

- Sau khi có source snapshot;
- Sau segmentation, trước xác nhận;
- Trước và sau khi artifact thứ N của analysis batch được ghi;
- Trước và sau khi chương cuối cùng của tiền tố cắt do độ dài được ghi;
- Giữa RangeDigest;
- Sau Synthesis, trước Foundation;
- Trước và sau mỗi artifact của Foundation;
- Các cửa sổ draft / StartChapter / PendingCommit / progress / checkpoint;
- Sau khi commit chương cuối cùng, trước và sau AdvanceHold;
- Phát hành một phần Foundation / chương rồi restart và thử `Host.Resume` bình thường.

Sau restart ở mỗi cửa sổ, hệ thống chỉ được phép tiếp tục đúng hành động hiện tại, không được tiêu thụ lại lời gọi model đã thành công, cũng không được vượt qua artifact lỗi. Khi `NextAction != done`, Engine bình thường phải bị gate chặn cho đến khi khôi phục import hoàn tất.

### 20.4 Hình dạng hồi quy #83

Tạo input 54 chương trở lên, kiểm chứng:

1. Không có lời gọi đơn lẻ nào yêu cầu output dàn ý chi tiết của 54 chương;
2. analysis batch vừa dựa vào input context vừa dựa vào completion output có thể thấy để gộp batch, không nhồi quá nhiều chương chỉ vì input còn nhét vừa;
3. Giai đoạn ba·bổ sung: mô phỏng `StopReasonLength` với “13 chương đầu hoàn chỉnh, chương 14 bị cắt”, chỉ nộp 13 chương đầu, bước tiếp theo bắt đầu từ chương 14; nếu chưa hiện thực cứu tiền tố thì sau khi fail cả batch sẽ gộp lại từ chương đầu batch;
4. Mô phỏng response bị cắt mà không có object hoàn chỉnh, lỗi phải hiển thị đầy đủ, ghi log, lưu phản hồi gốc và không ghi artifact phân tích;
5. Mô phỏng JSON hỏng bình thường, đi theo retry phản hồi cấu trúc thay vì cứu tiền tố;
6. Sau khi sửa thì chỉ chạy lại hành động thiếu đầu tiên, không làm lại các chương đã xong;
7. Tên chương không chuẩn đi qua segmentation ngữ nghĩa để vào preview, không sửa bằng regex mới.

### 20.5 Tiêu chí nghiệm thu cuối cùng

1. Chế độ tương tác mặc định cho người dùng thấy và xác nhận toàn bộ ranh giới chương trước khi ghi chính thức; `--yes` có thể tự động chấp nhận một cách rõ ràng và để lại artifact kiểm toán tương đương.
2. Bất kỳ đoạn nguồn không rỗng nào cũng có thể tìm được nơi thuộc về duy nhất từ segmentation.
3. 200～500 chương sẽ không tạo thành một lời gọi model đọc toàn bộchính văn và xuất toàn bộ object chương; mỗi batch phân tích vừa bị ràng buộc bởi ngân sách input vừa bởi ngân sách output, và output toàn cục chỉ biểu đạt fact toàn cục cùng phạm vi arc quyển.
4. Sau bất kỳ giai đoạn nào sập cũng có thể khôi phục chính xác mà không cần `from=N`.
5. Trạng thái chính thức giữ nguyên trước khi toàn bộ kiểm tra ngữ nghĩa hoàn tất.
6. Khi phát hành bị gián đoạn, saga commit hiện có có thể khôi phục và không commit chương hai lần.
7. Import chưa hoàn tất sau restart không thể khởi động Engine bình thường; chỉ có thể xem, chẩn đoán hoặc khôi phục import.
8. `--yes` không bỏ qua Hold hoàn tất; chỉ `--continue` riêng biệt mới bỏ qua, và nó cũng không vượt qua review gate.
9. Năng lực, usage, StopReason, ước tính ngân sách và lỗi của model/provider đều có thể quan sát được.
10. Chỉ cần đổi sang model mạnh hơn là có thể cải thiện chất lượng segmentation, phân tích và tổng hợp, đồng thời tự động mở rộng batch an toàn, giảm số lần gọi, mà không phải sửa rule văn học trong Go.

## 21. Khả năng mở rộng hướng tương lai

Khả năng mở rộng của phương án này đến từ ranh giới ổn định, chứ không phải từ việc dựng sẵn abstraction:

- Năng lực hiểu của model tăng: ba loại semantic function Boundary/Chapter/Synthesis trực tiếp trở nên chính xác hơn;
- Cửa sổ context hoặc output tăng: bộ tính ngân sách kép tự động mở rộng batch an toàn của analysis, đồng thời giảm số lần chia block và số lớp Reduce;
- Năng lực output có cấu trúc tăng: typed-call tự chọn ràng buộc provider mạnh hơn;
- Model mức rẻ mạnh lên: segmentation cơ học hơn có thể chuyển sang mức rẻ hơn, và lợi ích chi phí đi vào ngay, không đổi hợp đồng ngữ nghĩa;
- Định dạng input mới: chỉ cần chuyển EPUB, v.v. sang cùng một text chuẩn hóa và tọa độ SourceUnit;
- Ngữ nghĩa toàn sách mới: thêm trường có consumer rõ ràng vào `ImportedChapterFacts` hoặc `BookSynthesis`, không đổi giao thức khôi phục và phát hành;
- Tăng cường cộng tác với người dùng: ở ranh giới xác nhận có thể thêm sửa bằng ngôn ngữ tự nhiên, không viết kiến thức định dạng vào code.

Phần bất biến là bao phủ toàn văn, danh tính `InputDigest`, kiểm tra phạm vi và phát hành idempotent. Đây là phần bookkeeping mà model mạnh hơn mấy cũng không đáng giao cho model; mọi ngữ nghĩa biến đổi đều để trong các semantic function, vì vậy lợi ích từ model nâng cấp có thể xuyên thẳng đến kết quả sản phẩm.

## 22. Quyết định cuối cùng

Áp dụng **pipeline nhập ngữ nghĩa theo giai đoạn**, từ chối hai hướng:

1. Tiếp tục mở rộng regex chương và các ngưỡng số chương / số arc;
2. Để một Agent vòng lặp dài tự do tiếp quản toàn bộ import.

Ranh giới cuối cùng là:

> **Model quyết định văn bản có nghĩa là gì; code bảo đảm mỗi chữ đi đâu, mỗi kết quả tương ứng với input nào, mỗi lời gọi có đủ chỗ cho input và output hay không, sau khi lỗi thì tiếp tục từ đâu, và khi nào mới đủ tư cách trở thành fact chính thức.**

Điều này vừa giữ được năng lực tự chủ của model và lợi ích tương lai, vừa giữ cho kiến trúc hiện tại của ainovel-cli gồm Engine + semantic function được kiểu hóa + lớp facts từ file là đủ gọn.
