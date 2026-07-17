# Hệ thống đánh giá của ainovel-cli

> Đánh giá không phải là tự dựng một bộ script kiểm tra mới, mà là lấy **các bộ chẩn đoán sẵn có của dự án (`diag`), bộ thống kê văn phong toàn sách (`stylestat`), và bảy chiều review gốc (`ReviewEntry`) làm evaluator**, rồi bọc thêm một lớp harness batch offline. Một định nghĩa sự thật, không còn trôi lệch ở hai nơi.

---

## 0. Vì sao cần thiết kế lại

Tính ổn định đã chạy thông suốt: tiểu thuyết dài 235 chương / 1.27 triệu chữ viết một mạch hoàn tất, vòng kín lập kế hoạch trượt đã thành hình (xem `architecture.md` §9.1). Nút thắt đã chuyển sang — **chất lượng có thể lặp lại được**:

- Sau khi đổi một prompt, quy trình còn ổn định không? chuỗi công cụ, tiến trình trạng thái, các sự thật lưu bền có còn đúng không?
- Nội dung chính, dàn ý, chất lượng review có thật sự tốt lên hay chỉ là lần đó vô tình trúng kết quả đẹp?
- Trong truyện dài, nhân vật, timeline, foreshadow, context có còn đáng tin qua nhiều chương không?
- **Tính cố định phong cách ở cấp toàn sách** (mỗi chương có hàng chục lần tic câu, hình thái cuối chương đồng dạng, lặp nguyên văn xuyên chương) đã tốt lên hay xấu đi? Đây là thủ phạm thật sự của kết quả thực chứng 196 chương chỉ đạt 6.5/10; review theo từng chương tự thân không thấy được điểm này.

Hiện tại các đánh giá này vẫn dựa vào "cảm giác + đọc thủ công có chọn mẫu". Hệ thống đánh giá phải biến việc thay prompt từ cảm tính thành một quy trình kỹ thuật **có hồi quy, có chứng cứ, có đọc mẫu thủ công**.

Nhưng dự án này không cần, và cũng không nên, sao chép nguyên xi các nền tảng eval phổ biến trong ngành (dataset / experiment / scorer / database / Web UI). Lý do rất đơn giản: **cốt lõi của những năng lực đó — kiểm tra xác định và tín hiệu chất lượng — đã có sẵn trong dự án, lại còn viết bằng Go và dùng chung cùng một mô hình sự thật với runtime.**

---

## 1. Luận điểm cốt lõi: evaluator đã tồn tại

Bốn loại evaluator của hệ thống đánh giá, trong đó ba loại đã được thực hiện trong codebase, chỉ là trước đây chưa từng được gọi với tư cách "evaluator":

| Evaluator | Năng lực sẵn có của dự án | Cửa vào | Đầu ra |
|---|---|---|---|
| **Chẩn đoán sự thật xác định** | Bộ rule artifact + rule runtime của `internal/diag` | `diag.Diagnose(store)` | `Report{Stats, Findings}`, Finding có Severity/Evidence |
| **Hồi quy văn phong cấp toàn sách** | `internal/stylestat` | `stylestat.Compute(input)` | Số lần mẫu câu trung bình mỗi chương, câu lặp xuyên chương, tỷ lệ câu ngắn kết chương, lẫn lộn format tiêu đề |
| **Phán định chất lượng (rubric)** | rubric được version hóa (ban đầu suy ra từ bảy chiều của `editor.md`) | LLM Judge (thước đo cố định để A/B) | consistency/character/pacing/continuity/foreshadow/hook/aesthetic |
| **Xuất dữ liệu đã khử định danh hành vi** | Xuất dữ liệu của `internal/diag` | `diag.WriteExport(store, rep, rc)` | Khung hành vi để người đọc mẫu thủ công và lưu trữ |

`diag.Analyze(s *store.Store)` nhận một Store là có thể sinh ra `Report` đầy đủ — **nó vốn đã chạy offline được trên bất kỳ thư mục đầu ra nào**. `stylestat.Compute` là hàm thuần. Điều này có nghĩa hệ thống đánh giá không cần tái hiện lại "chapter đã được ghi xuống đĩa chưa, progress đã tiến tiếp chưa, checkpoint có tồn tại không, còn pending sót lại không, quy trình có lặp vô hạn không" — những thứ đó diag đã làm rồi, và mỗi rule đều gắn với một lỗi thật từng gặp (`PhaseFlowMismatch`, `OrphanedSteer`, `OutlineExhausted`, `repeatedErrors`/`stuckStep` tương ứng với idleResume / livelock do dàn ý cạn / tool call bị in như văn bản trong lịch sử).

> **Công việc của hệ thống đánh giá không phải là tự tạo kiểm tra, mà là: điều khiển hàng loạt + chạy các evaluator sẵn có trên artifact đầu ra + ánh xạ Finding/thống kê thành gate + tổng hợp báo cáo.**

---

## 2. Nguyên tắc thiết kế

### 2.1 Evaluator tức là diagnoser, tuyệt đối không tái tạo kiểm tra xác định

Các kiểm tra xác định chỉ gọi `diag.Diagnose`, không tự phân tích lại `progress.json` / `checkpoints.jsonl` / `sessions/*.jsonl` ở tầng eval. Lý do là một nguyên tắc DRY của dự án này: **"trạng thái hợp lệ là gì" chỉ được định nghĩa một lần.** Nếu eval dùng Python để tự parse checkpoint và quyết định commit có thiếu hay không, thì sẽ có hai định nghĩa khác nhau về "commit hoàn tất"; một khi runtime đổi rule của diag mà eval không đổi theo, gate sẽ lập tức sai lệch.

→ Harness eval dùng **Go**, gọi in-process `diag` và `stylestat`, dùng chung `internal/domain` và `internal/store` với runtime. Đây là khác biệt căn bản nhất của bản thiết kế này so với bản trước.

### 2.2 Hồi quy văn phong toàn sách là tín hiệu chất lượng số một

LLM Judge theo từng chương có thể xem mỗi chương đều "bình thường", nhưng nút thắt thực sự lại nằm ở việc phong cách bị cố định xuyên nhiều chương. Vì vậy xương sống xác định của hồi quy chất lượng là `stylestat`, không phải LLM Judge.

**Tiền đề: `stylestat.Compute` sẽ trả về nil nếu dưới 5 chương** (`stylestat.go` `minChapters=5`, mẫu quá nhỏ thì tần suất không còn ý nghĩa). Vì vậy hồi quy văn phong **chỉ có hiệu lực ở tầng Quality / Longform với ≥5 chương**; Smoke 1 chương sẽ không có tín hiệu văn phong — điều này quyết định chi phí và chiến lược mặc định phía dưới. Các chỉ số bao gồm:

- số lần trung bình mỗi chương của mẫu câu trong variant so với baseline (`patterns[].per_chapter`)
- tỷ lệ câu ngắn kết chương (`ending.short_ratio` gần 1 là bệnh)
- số câu lặp nguyên văn xuyên chương (`repeated_sentences`)
- lẫn lộn định dạng tiêu đề (`title_formats`)
- tỷ lệ từ chỉ thời gian ở mở đầu (`opening_time_rate`)

Đây là các chỉ số không tốn LLM, xác định được, và đánh đúng vào điểm nghẽn chất lượng. **LLM Judge chỉ là phần bổ sung, còn delta của stylestat là tuyến chính.**

### 2.3 LLM Judge phải khớp với rubric gốc bảy chiều, không tự đặt luật mới

Judge không phát minh ra chiều chấm mới — các chiều phải đúng bằng bảy mục của `domain.DimensionScore`, rồi so sánh baseline/variant.

**Nhưng rubric phải được version hóa và cố định được**, lưu thành snapshot trong `evals/rubrics/*.json`, chứ không đọc `editor.md` trực tiếp lúc runtime. Lý do: khi đối tượng được đánh giá chính là `editor.md`, nếu trọng tài cũng thay đổi theo `editor.md`, thì chuẩn đánh giá sẽ trôi — trọng tài và đối tượng cùng nguồn sẽ khiến việc "sửa editor là tốt hay xấu" không còn xác định được. Vì thế rubric lúc đầu **được suy ra** từ bảy chiều của editor (đảm bảo cùng khẩu độ), sau đó **phát triển độc lập, bump version rõ ràng**; report sẽ ghi dùng rubric phiên bản nào.

### 2.4 Finding xác định quyết định gate, LLM và con người chỉ phán định chất lượng

Để khớp với nguyên tắc kiến trúc "thống kê thuộc về code, phán định thuộc về LLM":

- **Chỉ bằng chứng xác định mới có thể chặn merge**: Finding `SevCritical` của `diag`, hoặc assertion hợp đồng do case khai báo bị fail.
- **LLM Judge và người đọc mẫu thủ công chỉ tạo warning và tín hiệu sắp xếp ưu tiên**, không tự quyết merge.
- Tóm lại: `Finding.Severity` ánh xạ trực tiếp sang mức gate, không đưa vào một hệ thống severity mới.

### 2.5 Đánh giá chỉ quan sát, không can thiệp luồng điều khiển

Eval tái dùng `diag`, nhưng **bỏ `Action` và `Planner` của diag** — đó là phần luồng điều khiển của runtime. Trong ngữ cảnh eval, `diag.Report` chỉ lấy `Stats` và `Findings`; mọi Action đều bị bỏ qua. Eval không tự sửa prompt, không tự rollback, không chạy tiếp. Đây là sự mở rộng của kỷ luật người quan sát (`architecture.md` §2.3) trong ngữ cảnh eval.

### 2.6 Lỗi phải lộ ra rõ ràng

Không mock thành công, không nuốt lỗi, không dùng template giả như là đã qua. Mô hình, công cụ, cấu hình, filesystem, parser, judge — bất kỳ thứ nào fail thì báo cáo phải ghi rõ nguyên nhân. **Chính thất bại là kết quả đánh giá** — một case chạy hỏng thì gate là FAIL, không phải "skip".

### 2.7 Mỗi lần chỉ kiểm tra một biến số

Ràng buộc cứng của A/B: cùng nhu cầu, cùng cấu hình, cùng model/provider, cùng phong cách, output directory tách biệt. Baseline = prompt chính thức hiện tại, Variant = chỉ thay file prompt cần kiểm chứng trong lần này. Một thí nghiệm không được đồng thời sửa Writer/Architect/Editor/Arbiter.

---

## 3. Toàn cảnh kiến trúc

```text
[Cases]  evals/cases/*.json —— bộ assertion tầng sự thật, không phải dataset chung chung
   │
[Runner]  internal/eval —— lắp ghép host drive in-process (cắt ngang theo giới hạn số chương), bundle.Prompts ghi đè trong bộ nhớ để làm variant
   │       baseline run ┐
   │       variant  run ┘  mỗi bên có output directory riêng
   ▼
[Collectors]  Với mỗi thư mục đầu ra thu thập:
   ├── diag.Diagnose(store)      → Report{Stats, Findings}      (sự thật + runtime)
   ├── stylestat.Compute(input)  → thống kê văn phong toàn sách  (xương sống của hồi quy chất lượng)
   ├── case assertion hợp đồng    → kỳ vọng checkpoint/phase/hợp đồng công cụ (diag không bao phủ)
   ├── usage / cost / token      → đọc từ meta/usage.json
   └── tool_calls                → đọc tool call thực từ meta/sessions/*.jsonl
   ▼
[Graders]
   ├── Gate xác định: Finding.Severity + assertion hợp đồng → hard_fail / regression
   ├── stylestat delta: chênh lệch chỉ số văn phong của variant so với baseline
   ├── LLM Judge (tuỳ chọn): so sánh A/B theo rubric bảy chiều
   └── Human: người đọc mẫu baseline/variant
   ▼
[Report]  report.json (máy đọc) + report.md (người đọc) + xuất dữ liệu đã khử định danh hành vi
   └── Gate: PASS / WARN / FAIL
```

Hướng phụ thuộc: `eval → host → agents → tools → store → domain`, dùng lại ngang `diag` / `stylestat`. Tầng eval **không phụ thuộc ngược** vào luồng điều khiển runtime, chỉ đọc Store và các evaluator chỉ đọc.

> **Phạm vi triển khai hiện tại đã bao phủ tuyến xác định**: khi không có `--variant` thì là `mode=single`; khi truyền `--variant` thì là `mode=ab`, chạy baseline và variant trong môi trường tách biệt rồi sinh delta. Collectors đã nối `diag.Diagnose`, assertion case, `stylestat.Compute`, `meta/usage.json`, và đếm tool call từ session; Graders đã nối gate xác định, delta diag baseline/variant, delta cost/token/tool call, delta stylestat. Runner trực tiếp `host.New` để lắp ghép và tự cắt theo giới hạn số chương, **không tái dùng `headless.Run`** (cái đó không có giới hạn số chương, và sẽ thiết lập handler hỏi tương tác `ask_user`). LLM Judge và Human vẫn là lớp tùy chọn ở giai đoạn sau, chưa tham gia gate xác định hiện tại.

---

## 4. Vì sao là Go in-process, không phải shell + Python

| Khía cạnh | shell copy source + Python parse (cách cũ) | Go in-process (thiết kế này) |
|---|---|---|
| Kiểm tra xác định | Python parse JSON lại, có hai định nghĩa so với rule của diag | Gọi thẳng `diag.Diagnose(store)`, chỉ một định nghĩa |
| Chuyển variant | Copy cả cây source + build lại hai binary | Ghi đè trong bộ nhớ bằng `bundle.OverridePrompt(...)` rồi lắp host, không copy, không rebuild |
| Hồi quy văn phong | Phải viết lại logic tách câu tiếng Trung của stylestat trong Python | Gọi thẳng `stylestat.Compute` |
| Rubric Judge | Các chiều rải rác trong Python | Tái dùng `domain.DimensionScore`, cùng nguồn với production |
| Rủi ro trôi lệch | Cao: runtime đổi mô hình sự thật, eval không theo | Thấp: thay đổi field lộ ra ngay ở bước biên dịch |

`prompt_ab.sh` cũ phải copy source rồi build lại vì prompt được nhúng vào binary (`go:embed`). Nhưng `assets.Bundle.Prompts` chỉ là một struct bình thường, **runner chỉ cần đổi một field trong bộ nhớ là có thể làm variant**, hoàn toàn không cần copy source. Đây là simplification lớn nhất có được khi viết harness bằng Go.

> **Ràng buộc triển khai**: `assets.Load` thông qua `loadPrompts` sẽ thêm hậu tố `WithSimulationGuidance` cho prompt của Worker (architect/writer/editor) một cách thống nhất. Nếu variant chỉ nhét text thô vào `bundle.Prompts.Writer`, nó sẽ làm mất hậu tố mô tả hình ảnh phỏng theo mà baseline có, dẫn đến A/B không tương đương.
>
> Cách làm đúng là override qua `assets.OverridePrompt`, bên trong đi cùng đường đóng gói y hệt `Load`; eval không tự sao chép logic đóng gói.

> Bản tài liệu trước vẫn giữ `prompt_ab.sh` / `prompt_ab_report.py` và chủ trương "từng bước trích xuất năng lực". Thiết kế này từ bỏ đường đó: những gì chúng giải quyết (chạy cô lập + tổng hợp chỉ số) trong harness Go in-process chỉ là một tập con; cố tái dùng sẽ phải gánh thêm keo dán giao diện cho ba ngôn ngữ shell/Python/Go. **Go harness là đường chính duy nhất**; hiện tại harness Go đã bao phủ chạy cô lập baseline/variant, tổng hợp repeat và delta xác định. Các script cũ (`scripts/prompt_ab.sh`, `scripts/prompt_ab_report.py`) cùng hướng dẫn vận hành `docs/prompt-ab.md` đã được xoá đi khi thiết kế này hạ cánh, không còn giữ lại nữa.

---

## 5. Case Manifest

Case là đơn vị nhỏ nhất của đầu vào đánh giá, đồng thời là một bộ **assertion ở tầng sự thật**. Dùng JSON để mô tả, tránh rule bị rải lẫn trong các tham số dòng lệnh.

```json
{
  "id": "writer_first_chapter_xianxia",
  "category": "smoke",
  "role": "writer",
  "description": "Xác minh chất lượng nội dung chương đầu của Writer và độ ổn định của chuỗi công cụ",
  "prompt": "Viết một tiểu thuyết tu tiên dài, nhân vật chính bắt đầu từ một tạp dịch ở biên thành, dựa vào trí nhớ bất thường để phá án cũ của tông môn và bị cuốn vào cục diện trường sinh.",
  "style": "fantasy",
  "max_chapters": 1,
  "target_prompts": ["writer.md"],
  "rubric": "writer_chapter",

  "expect": {
    "phase": "writing",
    "min_completed_chapters": 1,
    "required_checkpoints": ["chapter:1:plan", "chapter:1:draft", "chapter:1:commit"],
    "no_pending": ["pending_commit", "pending_steer"]
  },

  "gate": {
    "max_severity": "warning",
    "max_cost_delta_ratio": 0.3,
    "max_tool_call_delta_ratio": 0.3,
    "stylestat_regression": "warn"
  }
}
```

**Ý nghĩa các field**:

- `expect`: assertion hợp đồng ở cấp case, **chỉ khai báo các kỳ vọng cụ thể liên quan chặt chẽ tới case này mà các rule chung của diag không bao phủ được** (ví dụ: "case smoke này phải sinh đúng `chapter:1:commit`"). Các điều chung như "không còn pending", "phase-flow nhất quán", "không có gap chương" giao cho diag, không lặp lại trong case.
- `category`: tầng đánh giá ∈ `smoke` / `workflow` / `quality` / `longform` / `recovery` / `steering`. Quyết định chạy bộ gate nào và có bật stylestat/Judge mặc định hay không.
- `role`: vai được đánh giá ∈ `writer` / `architect` / `editor`. Độc lập với `category` — tầng quyết định "đánh sâu tới đâu", vai quyết định "đánh Worker nào". Tầng Workflow chọn bộ assertion theo `role`.
- `max_severity`: mức severity tối đa mà Finding của diag được phép có. Vượt mức này là hard fail.
- `gate.max_cost_delta_ratio` / `gate.max_tool_call_delta_ratio`: ngưỡng tăng chi phí và số lần gọi công cụ của variant so với baseline; khi bỏ trống mặc định là `0.3`, đặt rõ `0` nghĩa là không cho phép tăng, số âm nghĩa là tắt gate delta đó.
- `rubric`: bật bảng chấm LLM Judge version hóa nào. Nếu bỏ mặc định thì không chạy Judge.
- `gate.stylestat_regression`: `block` / `warn` / `off`, điều khiển việc hồi quy văn phong có chặn hay không (chỉ có hiệu lực với case ≥5 chương).

---

## 6. Phân tầng đánh giá

Mỗi tầng đều chỉ rõ **dùng evaluator sẵn có nào**, tránh việc "tầng đánh giá lại tự viết thêm một bộ phán đoán".

### 6.1 Smoke (phải chạy mỗi lần prompt thay đổi, bộ tối thiểu)

Chỉ kiểm tra hệ thống còn chạy ổn định không, không đánh văn phong. 1 chương / giai đoạn lập kế hoạch cũng đủ lộ vấn đề.

| case | Mục tiêu | Evaluator chính |
|---|---|---|
| `writer_first_chapter` | Writer hoàn tất chương đầu và commit | `expect.required_checkpoints` + diag |
| `architect_short` | Lập kế hoạch truyện ngắn phải lưu đủ premise/outline/characters/world_rules | kiểm tra foundation cùng nguồn với diag `MissingSummaries` + `expect` |
| `architect_long` | Lập kế hoạch truyện dài phải lưu layered_outline/compass, phần mở đầu arc phải triển khai | diag `OutlineExhausted`/`CompassDrift` + `expect` |
| `editor_review` | Tới điểm review, Editor phải lưu review (đủ bảy chiều) | assertion trên field của `ReviewEntry` |

Chi phí: 1 chương × baseline+variant, từ mức giây đến phút, không bật Judge, không chạy stylestat (số chương không đủ 5 nên `Compute` trả nil). CI mặc định chỉ chạy tầng này.

### 6.2 Workflow (xác minh hành vi Agent khớp với hợp đồng kiến trúc)

**Kỷ luật then chốt: assertion hợp đồng, không assertion chuỗi công cụ chính xác.** Kiến trúc đặt cược vào việc LLM tự quyết luồng hành động (`architecture.md` §2.1), nên nếu viết cứng thứ tự công cụ vào eval thì sẽ tái tạo lại kiểu "viết cứng hành vi LLM" bị §10.13 bác bỏ ngay trong tầng đánh giá. Vì vậy ở đây chỉ assertion những sự thật **không thể tránh**:

- Writer: checkpoint `chapter:N:commit` phải tồn tại; sau commit subagent kết thúc lượt này (không có đoạn văn thừa kéo dài phía sau); draft checkpoint phải đứng trước commit. **Không** assertion "bắt buộc phải đi đúng chuỗi novel_context→read_chapter→plan→draft→check→commit".
- Architect: trong giai đoạn viết, outline chỉ được tăng thêm chứ không được ghi đè toàn bộ (`expand_arc`/`append_volume` có checkpoint, không có lần thứ hai ghi toàn bộ `layered_outline`); sau khi mở rộng, outline phẳng và số chapter của layered phải khớp.
- Editor: `ReviewEntry.Verdict` phải hợp lệ (`accept`/`polish`/`rewrite`); `rewrite`/`polish` phải sinh ra các chapter bị ảnh hưởng; cuối arc phải có checkpoint `arc_summary`, cuối volume phải có `volume_summary`.
- Phân phối Engine: lệnh Route phải khớp với Worker thực sự đang chạy (đọc từ session trace, diag `repeatedErrors` là vòng lặp dự phòng); phán định ngữ nghĩa kiểm tra chéo với `meta/decisions.jsonl`.

Phần lớn các điều này có thể bao phủ trực tiếp bằng rule của diag + assertion checkpoint; một phần nhỏ (đoạn văn thừa sau commit) cần thêm một kiểm tra trace nhẹ trong collector.

### 6.3 Quality (chỉ chạy sau khi luồng đã thông, để đánh giá chất lượng nội dung)

Hai chân:

1. **stylestat delta (xác định, tuyến chính)**: chênh lệch chỉ số văn phong của variant so với baseline. Đây là bằng chứng cứng cho hồi quy chất lượng. **Yêu cầu case phải chạy đủ ≥5 chương** (nếu không `Compute` trả nil, mục này sẽ được đánh dấu `insufficient_sample`), nên case Quality chỉ có 1 chương sẽ không có hồi quy văn phong; cần đặt `max_chapters` lên 5 trở lên.
2. **LLM Judge (phụ trợ)**: rubric A/B bảy chiều (xem §8).

Chỉ case nào qua §6.1/§6.2 mới được vào Quality — nếu luồng còn sai, bàn chất lượng là vô nghĩa.

### 6.4 Longform & Recovery (thay đổi lớn / nightly)

Không cần chạy mỗi lần. Bao phủ tính ổn định truyện dài và năng lực phục hồi, đúng là sân nhà của rule runtime và rule context của diag:

- Viết liên tục 3 chương / 5 chương đầu → diag `GhostCharacter`/`TimelineGaps`/`RelationshipStagnation`/`ChapterGaps` + trùng lặp xuyên chương của stylestat.
- Review ở cuối arc + mở arc kế tiếp → `OutlineExhausted`/`StaleForeshadow`/`CompassDrift`.
- Người dùng can thiệp giữa chừng (steering case) → user_rules có được ghi vào `meta/user_rules.json` không, và các chương sau có tuân theo không.
- Phục hồi sau crash: chạy tới draft của chương N rồi kill → Resume → diag xác nhận `checkpoints.jsonl` không lặp step, không ghi đè draft đã lưu, `pending_commit` cuối cùng được dọn sạch.
- Bùng nổ tool call / chi phí bất thường → diag `repeatedErrors`/`stuckStep`/`streamIdleStorm` + delta usage.

---

## 7. Gate xác định

Mức gate được suy ra trực tiếp từ **Severity của Finding trong diag** + **assertion hợp đồng của case**, không tự lập ra một hệ phân loại khác.

### 7.1 Hard Fail (chặn merge)

- Tiến trình panic / headless trả về error.
- diag tạo Finding `SevCritical` (`InvalidPendingRewrites` / `PhaseFlowMismatch` v.v.).
- assertion hợp đồng `expect` của case fail: thiếu checkpoint commit, phase không tới đúng kỳ vọng, pending đã khai báo nhưng không được dọn sạch.
- Số lỗi / số critical Finding của variant nhiều hơn baseline (thoái hóa sang trạng thái xấu hơn).

### 7.2 Regression (mặc định là warning, có chặn hay không do gate của case quyết định)

- diag có thêm Finding `SevWarning` mới (variant nhiều hơn baseline).
- số tool call / cost / input token / output token tăng vượt ngưỡng của case (mặc định 30%).
- **stylestat hồi quy**: số lần trung bình của mẫu câu mỗi chương tăng, tỷ lệ câu ngắn kết chương tăng, câu lặp xuyên chương nhiều hơn, lẫn lộn tiêu đề xuất hiện — tùy `gate.stylestat_regression` mà warn/block.
- Số chữ của chương thấp hơn baseline 60% hoặc cao hơn 180% (cùng ngưỡng với diag `WordCountAnomaly`).

### 7.3 Quality Gate (chốt hạ bằng người)

- LLM Judge chỉ dùng để hỗ trợ và sắp xếp ưu tiên.
- Judge chấm variant rõ ràng tệ hơn → bắt buộc phải có người đọc mẫu xác nhận.
- Người đọc mẫu kết luận suy giảm → chặn.
- Judge chấm variant tốt hơn nhưng phần xác định có hard fail → vẫn chặn.

### 7.4 Điều kiện khuyến nghị khi merge

Sửa prompt hằng ngày: Smoke phải qua hết + workflow của vai mục tiêu phải qua hết (Smoke 1 chương không có hồi quy văn phong; nếu có chạy Quality case ≥5 chương thì stylestat không được hồi quy đáng kể).

Thay đổi lớn: thêm 2-3 case Quality + 1-2 case Longform + đọc mẫu thủ công.

---

## 8. LLM Judge

Judge là bộ hỗ trợ chất lượng, về bản chất là **dùng rubric được version hóa (ban đầu suy ra từ bảy chiều của editor.md) để so sánh baseline/variant offline**. Rubric là thước đo cố định, phát triển độc lập với `editor.md` đang chạy (lý do xem §2.3), report sẽ ghi rõ đã dùng version rubric nào.

### 8.1 Input (khống chế kích thước, tuyệt đối không nhét cả cuốn sách)

- Yêu cầu gốc của người dùng + dàn ý/hợp đồng chương hiện tại.
- Nội dung chính của **cùng một chương** ở baseline và variant.
- Tóm tắt 1-2 chương gần nhất + tóm tắt trạng thái nhân vật (đọc từ store).
- Slice stylestat liên quan đến chương đó (để Judge thấy các sự thật kiểu "câu này lặp 7 lần trong toàn sách").

### 8.2 Output (có cấu trúc, khớp bảy chiều)

```json
{
  "scores": {
    "consistency": 8, "character": 7, "pacing": 8, "continuity": 8,
    "foreshadow": 7, "hook": 7, "aesthetic": 6
  },
  "winner": "variant",
  "confidence": "medium",
  "reasons": ["variant đẩy nhịp hành động tập trung hơn", "baseline nặng phần nhắc lại bối cảnh hơn"],
  "risks": ["variant hơi thiếu lớp đệm động cơ cho nhân vật phụ"]
}
```

- Các chiều phải đúng bằng bảy mục của `domain.DimensionScore`, mỗi mục 0-10.
- `winner` ∈ baseline/variant/tie; `confidence` ∈ low/medium/high.
- Mỗi mục trong `reasons`/`risks` dài tối đa 80 ký tự, trích nguyên văn phải ngắn.

### 8.3 Giới hạn

Judge **không được**: quyết định luồng có qua hay không, sửa artifact, tự sửa prompt, làm bằng chứng duy nhất cho merge, hay sinh trích đoạn nguyên văn dài.
Judge **được phép**: xếp thứ tự cho người review thủ công, đánh dấu suy giảm rõ ràng, tóm tắt khác biệt A/B, và phơi bày tác dụng phụ của thay đổi prompt.

---

## 9. Báo cáo

Mỗi lần thí nghiệm sẽ sinh `report.json` (máy đọc, có thể tái sinh markdown) + `report.md` (người đọc) + `artifacts/{case_id}/{baseline,variant}/` (artifact gốc). Khi dùng `--repeat N`, đường dẫn là `artifacts/{case_id}/rN/{baseline,variant}/`.

### 9.1 Delta chỉ số

Report hiển thị chênh lệch của variant so với baseline, đồng thời nêu cả giá trị tuyệt đối lẫn tỷ lệ:

```text
completed: baseline=5 variant=5   ← ≥5 chương thì chỉ số văn phong mới có ý nghĩa
tool_calls: baseline=12 variant=16  +4 (+33.3%)
cost_usd: baseline=0.42 variant=0.55  +0.13 (+31.0%)
output_tokens: baseline=8200 variant=9100  +900 (+11.0%)
critical_findings: baseline=0 variant=0
warning_findings: baseline=1 variant=2  +1
stylestat.pattern_top_per_chapter: baseline=3.1 variant=5.4  +2.3   ← hồi quy văn phong
stylestat.ending_short_ratio: baseline=0.42 variant=0.71  +0.29     ← hình thái kết chương đồng dạng nặng hơn
```

### 9.2 Tổng hợp repeat

Khi dùng `--repeat N`, không chỉ nhìn lần cuối; bản triển khai hiện tại hiển thị tỷ lệ pass, số hard fail, số warning, và min/avg/max của cost/tool_calls. Khi đã nối Judge thì sẽ cộng thêm phân bố winner để tránh trộn nhiễu của trọng tài mô hình vào report xác định mặc định.

```text
writer_first_chapter_xianxia repeat=3
- pass_rate: 3/3
- cost_usd: avg=0.41 min=0.38 max=0.44
- tool_calls: avg=13 min=12 max=15
- stylestat.pattern_top_per_chapter: avg delta=+0.4（không có hồi quy rõ rệt）
```

### 9.3 Báo cáo tối thiểu khả dụng

```text
Gate: FAIL

Hard Fail:
- writer_first_chapter_xianxia: missing checkpoint chapter:1:commit

Warnings:
- writer_dialogue_density: tool_calls +35%
- writer_anti_ai_tone: ending_short_ratio +0.28 (hồi quy văn phong)

Quality:
- writer_anti_ai_tone: judge prefers variant, confidence=medium

Artifacts:
- workspace/evals/20260629-120000/report.json
```

---

## 10. Cấu trúc thư mục và lệnh

```text
internal/eval/
  case.go        Cấu trúc Case manifest + tải vào
  eval.go        Điều phối CLI: single / A/B / repeat
  runner.go      Lắp host driver (cắt theo giới hạn số chương + drain đến Done), ghi đè bundle.OverridePrompt trong bộ nhớ
  collect.go     Chạy diag.Diagnose + stylestat.Compute + usage/tool_calls + assertion hợp đồng trên thư mục đầu ra
  grade.go       Ánh xạ Finding→gate + delta baseline/variant + quyết định stylestat gate
  report.go      report.json + report.md

cmd/ainovel-cli  entry của subcommand eval

evals/
  cases/         smoke/ workflow/ quality/ longform/ recovery/ steering/
  rubrics/       writer_chapter.json / architect_outline.json / editor_review.json
  variants/      writer-anti-ai-tone/writer.md v.v. (mỗi thư mục chỉ chứa prompt cần thay thế)
  reports/       lưu trữ report lịch sử
```

Lệnh:

```bash
# Batch nhiều case (CI mặc định chỉ chạy smoke, không bật judge)
ainovel-cli eval --cases evals/cases/smoke \
  --variant evals/variants/writer-anti-ai-tone \
  --out workspace/evals/writer-anti-ai-tone --ci
```

**Các tham số đã được triển khai trong giai đoạn này**: `--cases` (thư mục hoặc manifest đơn), `--variant` (thư mục prompt biến thể; khi truyền vào sẽ tự chạy baseline+variant A/B), `--repeat N` (lặp mỗi case N lần), `--config`, `--out`, `--max-chapters N` (ghi đè mặc định của case), `--timeout` (giới hạn wall-clock cho một case), `--ci` (giảm output theo từng event; exit code khác 0 là hard fail, không truyền vẫn có hiệu lực).

**Đang lập kế hoạch (chưa triển khai, đừng dùng trên command line nếu không sẽ báo flag không tồn tại)**: `--judge`/`--no-judge` (Phase 3 LLM Judge). Với thay đổi prompt lớn hiện tại có thể dùng A/B xác định + repeat trước:

```bash
# Thay đổi prompt lớn: A/B + repeat giảm ngẫu nhiên
ainovel-cli eval --cases evals/cases/quality \
  --variant evals/variants/writer-anti-ai-tone \
  --repeat 3 --ci
```

---

## 11. Những việc rõ ràng không làm

Vi phạm nghĩa là đánh giá đã lệch khỏi định vị.

1. **Không sao chép logic chẩn đoán chung của diag ở tầng eval** — các kiểm tra chung (pending còn sót, phase/flow nhất quán, thiếu chương, vòng lặp chết) đều đi qua `diag`; sự thật chỉ có một định nghĩa. Assertion hợp đồng ở cấp case (`expect.required_checkpoints` v.v.) có thể đọc trực tiếp `store`/checkpoint API, nhưng chỉ làm **thin assertion** — xác minh kỳ vọng cụ thể gắn chặt với case này, tuyệt đối không viết lại toàn bộ rule chung mà diag đã có.
2. **Không tự tái hiện rule xác định** — diag đã có bộ rule artifact + rule runtime. Thiếu rule thì đi thêm vào diag; tầng eval chỉ tiêu thụ.
3. **Không tự viết lại logic văn phong tiếng Trung của stylestat trong Python** — gọi trực tiếp Go package.
4. **Không để LLM Judge quyết định luồng có qua hay không** — gate chỉ tin bằng chứng xác định.
5. **Không để eval can thiệp vào luồng điều khiển** — bỏ Action/Planner của diag, không tự sửa prompt, không rollback, không chạy tiếp, không publish.
6. **Không assertion chuỗi gọi công cụ chính xác** — chỉ assertion hợp đồng (commit có xảy ra, checkpoint có tồn tại), để bảo vệ giả định "LLM điều khiển luồng".
7. **Không đưa vào database / Web UI / nền tảng eval online** — giai đoạn hiện tại cần hồi quy cục bộ có thể lặp lại, dễ triển khai, chi phí thấp.
8. **Không copy source rồi build lại để làm variant** — ghi đè `bundle.Prompts` trong bộ nhớ.
9. **Không mock thành công, không nuốt lỗi** — mọi lỗi phải được ghi rõ, case chạy hỏng là FAIL.
10. **Case không được thay đổi thường xuyên theo prompt** — case là bộ test ổn định; sửa case để variant qua là gian lận.

---

## 12. Triển khai theo giai đoạn

### Phase 1 · Runner + gate xác định (MVP, chứng minh giả thuyết trước)

- `internal/eval`: cấu trúc Case + runner (headless in-process + bundle override) + collect (gọi `diag.Diagnose`) + grade (Finding→gate + `expect` contract).
- Đặt 3-4 case trong `evals/cases/smoke/`.
- Báo cáo trước hết xuất `report.json` + markdown tối thiểu.

**Tiêu chí chấp nhận**: chạy xong smoke bằng một lệnh; Writer bỏ qua commit, còn sót pending, thiếu checkpoint, phase không đúng **đều phải bị gate chặn** (những thứ này diag vốn đã kiểm tra được; điều cần xác minh là harness nối đúng).

### Phase 2 · A/B + repeat + hồi quy stylestat (đã triển khai)

- `--variant` tự chạy baseline và variant, xuất artifact tách biệt.
- `--repeat N` tổng hợp pass rate, hard fail runs, warning runs, cost/tool_calls min/avg/max.
- collect thêm `stylestat.Compute`, grade thêm delta văn phong.
- Report hiển thị so sánh baseline-variant của số lần mẫu câu trung bình, tỷ lệ câu ngắn kết chương, câu lặp xuyên chương, và lẫn lộn tiêu đề.

**Tiêu chí chấp nhận**: dùng một case ≥5 chương + một variant "tic câu bị tăng nặng" thì hồi quy văn phong phải bị đánh dấu warning; case thiếu số chương phải hiện rõ `insufficient_sample` thay vì bị cho qua sai.

### Phase 3 · LLM Judge

- `evals/rubrics/` + `judge.go`, rubric A/B bảy chiều.
- Judge fail (JSON không hợp lệ) → report ghi thất bại, không ảnh hưởng kết quả xác định.

**Tiêu chí chấp nhận**: Judge xuất ra json+md và không làm ô nhiễm gate xác định.

### Phase 4 · Longform & Recovery

- Case 3-5 chương liên tục / review cuối arc / can thiệp người dùng / replay `pending_commit` / áp lực nén context.
- Tái dùng rule context + runtime của diag.

**Tiêu chí chấp nhận**: có thể phát hiện timeline trùng lặp, pending còn sót, thiếu summary cuối arc, vòng lặp công cụ.

---

## 13. Quy chuẩn duy trì case

- **Số lượng tiết chế**: Smoke 3-5, Workflow mỗi vai 3-5, Quality 2-4, Longform/Recovery mỗi loại 2-3. Quá nhiều thì không ai muốn chạy.
- **Case tốt**: input ngắn và rõ, bao phủ rủi ro thực tế, vấn đề lộ ra trong ít chương, không phụ thuộc mô hình tạo đúng một câu cố định, không viết quá chi li về sở thích phong cách.
- **Case xấu**: input quá dài, nhiều mục tiêu cùng lúc, phải chạy hàng chục chương mới kết luận được, chỉ dựa được vào cảm nhận chủ quan.
- **Đặt tên Variant**: `writer-anti-ai-tone` / `architect-rolling-outline` / `editor-strict-review`, mỗi thư mục chỉ chứa prompt cần thay thế.

---

## 14. Rủi ro và giới hạn

- **Tính ngẫu nhiên của model**: cùng prompt chạy nhiều lần vẫn có thể khác. Với thay đổi quan trọng hãy dùng `--repeat 3` để xem xu hướng.
- **Chi phí**: Judge và longform đều tốn tiền. Mặc định local chỉ chạy **smoke** (1 chương × baseline+variant, gate xác định diag, không bật Judge, không chạy stylestat); **stylestat chỉ bật ở Quality/Longform có ≥5 chương** (smoke thiếu số chương nên `Compute` trả nil, report sẽ ghi `insufficient_sample`); full suite để dành cho thay đổi lớn.
- **Sai lệch Judge**: Judge cũng là model, nó thích văn bản giải thích gọn gàng hơn có thể không đồng nghĩa với tiểu thuyết hay — vì thế chỉ dùng làm phụ trợ, stylestat mới là tuyến xác định chính.
- **Đo quá mức**: số chữ / số lần gọi công cụ / chi phí / thống kê văn phong đều là tín hiệu chứ không phải mục tiêu. Việc số stylestat có thành bệnh hay không phải do con người căn cứ theo thể loại mà phán, **ngưỡng không viết cứng** (giống `editor.md`).
- **Không làm rollback tự động trên online**: đây là công cụ hồi quy offline, không chịu trách nhiệm tự sửa prompt / phát hành trên production.

---

## 15. Tổng kết

Giá trị của hệ thống đánh giá này không phải là tự động phán đoán chất lượng văn chương, mà là biến thay đổi prompt từ "cảm tính" thành "có hồi quy, có bằng chứng, có đọc mẫu thủ công".

Khác biệt căn bản duy nhất so với bản thiết kế trước nằm ở một câu: **evaluator đã có sẵn trong codebase rồi.** `diag` là bộ chẩn đoán sự thật xác định, `stylestat` là bộ hồi quy văn phong toàn sách, bảy chiều của `ReviewEntry` là rubric gốc. Công việc của hệ thống đánh giá chỉ là một lớp Go harness mỏng — điều khiển batch, thu thập, ánh xạ Finding và thống kê thành gate, tổng hợp báo cáo — chứ không phải viết lại các phán đoán sự thật đó bằng một ngôn ngữ khác.

Một định nghĩa sự thật, không bao giờ trôi lệch. Đó chính là kỷ luật xuyên suốt của dự án này từ kiến trúc đến đánh giá: **harness tối thiểu, tái dùng tối đa, phần xác định thuộc về code, phần phán định thuộc về LLM và con người.**

---

## 16. Tài liệu tham khảo

Cấu trúc LLM eval phổ biến trong ngành (dataset / experiment / scorer / trace / regression gate) là nguồn cảm hứng của thiết kế này, nhưng **cố ý không sao chép nguyên xi** — "scorer" của dự án này chính là `diag`/`stylestat` sẵn có, "trace" là tầng sự thật checkpoint/session sẵn có, "dataset" là case gắn assertion vào tầng sự thật.

- OpenAI Evals · https://developers.openai.com/api/docs/guides/evals （lưu ý: nền tảng Evals được host của họ đã công bố lịch ngừng hỗ trợ; ở đây chỉ mượn **ý tưởng** về kiểm thử có cấu trúc / chấm điểm tự động / hiệu chỉnh thủ công, không xem như phụ thuộc tương lai）
- Braintrust · https://www.braintrust.dev/foundations/what-is-an-eval
- LangSmith · https://docs.langchain.com/langsmith/evaluation-concepts
