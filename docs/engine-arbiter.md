# Diễn tiến mặt điều khiển: Engine + Arbiter (loại bỏ vòng lặp dài Coordinator)

> Trạng thái (2026-07-14 v6): **đã hoàn tất triển khai mã** —— Engine/Arbiter đã được thực thi, Coordinator và toàn bộ phần phụ trợ đã bị xóa (danh sách ở §10); đã có xác minh end-to-end cho viết sách hoàn chỉnh, phán quyết thất bại, phán quyết bế tắc, nghiệm thu làm lại (thứ tự hold+editor), boundary hold dừng ngay, bảo toàn can thiệp đua thoát, và duy nhất một giấy phép cho một chương. Toàn bộ các mục chặn từ ba vòng review bên ngoài đã được xử lý (bao gồm khép kín sự thật feedback, bảo vệ crash của PendingSteer, và các đua trong lifecycle).
> **Di chuyển tài liệu đã hoàn tất (2026-07-12)**: phần thân `architecture.md` đã được viết lại toàn bộ theo kiến trúc hiện hành Engine+Arbiter (bao gồm chiến lược xác minh mới/chỉ mục/kỷ luật); các tường thuật kiến trúc cũ trong `README`, `context-management`, `evaluation-system`, `observability`, `user-rules-runtime` đã được dọn sạch (chỉ giữ lại chú thích đối chiếu lịch sử). Cấu hình Coordinator và các đường tương thích session đều đã bị xóa; Arbiter hiện cố ý dùng thống nhất mô hình Default, không mở cấu hình vai trò riêng.
> **Làm rõ ngữ nghĩa thiết kế (vòng review thứ tư/năm)**: ① điểm tiêu thụ của writer feedback **chính là** lần thao tác cấu trúc tiếp theo (`expand_arc`/`append_volume`/`update_compass` sau khi tham chiếu `novel_context` sẽ được xóa sạch) —— nó là "gợi ý cho dàn ý tiếp theo" (nguyên văn schema commit), không phải tín hiệu điều phối tức thời; các lệch nghiêm trọng giữa chừng arc đi qua review editor và kênh can thiệp của người dùng. Với sách không phân tầng thì không có thao tác cấu trúc, **commit không ghi xuống đĩa feedback của nó** (để tránh một sự thật rác vĩnh viễn không có consumer; giá trị trả về vẫn được giữ lại để chẩn đoán). ② `rule_violations` đã khép kín: commit ghi xuống đĩa theo hai đường (**metadata chất lượng best-effort**, và không ở cùng mức nhất quán mạnh với việc submit chương — nếu crash đúng lúc xóa `pending_commit` thì sẽ thiếu một bản ghi, chấp nhận được) → `novel_context(chapter=N)` chèn vào → editor tiêu thụ theo ánh xạ ở §kiểm tra cơ học. ③ bảo vệ crash của `PendingSteer` là **best-effort lưu bền một mục đang bay**: được bảo vệ trong suốt thời gian phán quyết, khi áp dụng hành động thất bại, và khi thoát bình thường/`Abort`; có hai cửa sổ không bảo đảm rõ ràng —— (a) sau khi chuyển đơn vào hàng thực thi trong bộ nhớ (`e.next`), trước khi worker khởi động, nếu tiến trình bị giết cứng (cửa sổ tính bằng mili giây, `defer` không chạy); (b) can thiệp đồng thời đang chờ `interMu` (chưa ghi vào slot). Người dùng có mặt sẽ cảm nhận được, chi phí gửi lại ở mức giây, nên không xây intent/FIFO bền cho việc này. ④ thất bại ở phán quyết khởi động không phải ngõ cụt (bổ túc từ sự cố thật 2026-07-12: tài khoản provider hết hiệu lực làm `plan_start` thất bại, toàn bộ đường phục hồi đều không chạy được): `StartPrompt` (sự thật đầu vào) được ghi xuống đĩa **trước** khi phán quyết; khi `plan_start` chưa hoàn tất, engine dùng `planStartFallback` dựa trên nó để bổ cứu tại chỗ —— thử lại phán quyết đầu tiên không vi phạm việc "không làm lại các phán quyết đã có khi phục hồi"; nếu bổ cứu thất bại thì hồi tiếp rõ ràng sẽ dừng, và bản ghi audit của phán quyết thất bại mang trường `error` (`DecisionRecord.Error`).
> Tài liệu này được giữ lại như hồ sơ quyết định thiết kế; kiến trúc hiện tại xem ở mục kiến trúc của `README` và `docs/engine-rfc.md`. Liên quan: `docs/voice-layer.md` (đã triển khai).

## I. Động lực: giả định lỗi thời bị vá chằng vá đụp bao quanh

Giả định nền tảng của dự án —— "một Prompt, một vòng lặp LLM thường trú để điều khiển cả cuốn sách" —— đã lỗi thời. Sau tái cấu trúc Hybrid vào tháng 4, quyền quyết định thực tế nằm trong tay `flow.Route`, còn Coordinator trong 90% lời gọi của vòng chính chỉ làm việc **chuyển tiếp nguyên dạng**. Hệ sinh thái vá để "duy trì một phiên LLM không được phép dừng":

1. StopGuard + công văn bản động `blockMessage`
2. Giao thức chỉ thị lặp lại của Dispatcher ("ra lệnh lần thứ N")
3. kỷ luật hành vi trong `coordinator.md` (ngoại lệ phục hồi / loại truy vấn phải giao ngay trong cùng vòng / không được dùng biểu đạt dừng máy để nêu lập trường)
4. `completePhaseGate` / `writerExpandedChapterGate`
5. `MaxTurns=100_000`
6. `FlowBoundaryHook`
7. ngoại lệ bất đồng ở kết thúc

**Toàn bộ hệ thống con mới trong dự án (import/simulation/cocreate/userrules) đều không đi qua Coordinator, mà đã dùng mô thức "Host điều phối trực tiếp + LLM làm hàm"** —— phương án này đưa luồng chính về đúng mô thức mà chúng ta đã xác minh.

## II. Hình thái mục tiêu

```
Entry
  ↓
Host(giữ nguyên tên gói; bên trong thêm EngineLoop, không làm đổi tên cơ học đơn thuần)
  ├─ Đọc Store → flow.Route → chạy trực tiếp Worker
  ├─ Tình huống ngữ nghĩa rõ ràng → gọi hàm Arbiter
  └─ Chiếu sự kiện / ngân sách / điểm dừng / thông báo(giữ nguyên trách nhiệm hiện tại)
  ↓
Workers(architect / writer / editor, tự chủ bên trong, giữ guard checkpoint-delta)
  ↓
Tools → Store(nguồn sự thật duy nhất)
```

Trách nhiệm:**Route quản mọi bước tiếp theo có thể tra bảng; Arbiter quản các phán đoán ngữ nghĩa có biên giới rõ; Worker quản sáng tác mở; Engine thi hành quyết định, không tham gia phán đoán văn học; Observer/Diag chỉ quan sát.**

Tóm gọn bằng một câu:**một Engine tuần tự xác định, ba Worker tự chủ, vài hàm Arbiter theo nhu cầu, và một lớp sự thật hệ thống tệp.**

### Đối xứng hai mặt (dự kiến ghi vào `architecture.md` như một luật sắt mới)

```
Mặt xác định:  flow.LoadState   → flow.Route     → Instruction   (kiểm thử đặc tả theo liệt kê đầy đủ)
Mặt ngữ nghĩa:  arbiter.Collect* → arbiter.Decide* → XxxDecision   (ghi quyết định + hồi quy eval)
                └── thu thập sự thật(IO) ──┘└── lõi quyết định (có thể phát lại ngoại tuyến) ──┘└── Engine thực thi ──┘
```

## III. Các tình huống Arbiter (tập cuối cùng, giữ tối thiểu)

| Tình huống | Kích hoạt | Ghi chú |
|------|------|------|
| `plan_start` | Khởi tạo sách mới | Chọn planner ngắn/dài + mở rộng yêu cầu quá ngắn |
| `intervention` | Người dùng can thiệp | Truy vấn / quy tắc dài hạn / điều chỉnh cấu trúc cốt truyện / làm lại phần đã viết / làm lại hoặc từ chối sau khi hoàn tất |
| `worker_failure` | Worker báo lỗi **và phân loại xác định không có lối ra** | Lỗi mạng/tham số/thiếu hiện vật tiền đề… sẽ được mã xác định phân loại trước, không gửi Arbiter |
| `deadlock` | Sau vòng trước vẫn tạo ra cùng một chỉ thị route | Quy tắc đếm và ngữ nghĩa kết thúc xem ở §8, câu hỏi bắt buộc 5 |
| `completion_dispute` | **Dự bị, chỉ thêm khi có chứng cứ** | Việc xác định kết thúc ở cuối volume đã do Route giao cho architect (nhánh 10) đảm nhiệm; chỉ khi "chưa tới biên cấu trúc nhưng câu chuyện đã nên khép lại" mới cần tình huống bất đồng ở giữa chừng, tỷ lệ thực tế chưa biết nên chưa dựng sẵn |

Phần tổng kết hoàn tất không phải là phán quyết, mà là nhiệm vụ sinh ra —— do Engine trực tiếp giao cho editor hoặc thực hiện bằng một lời gọi LLM bình thường, không chiếm chỗ của tình huống Arbiter.

## IV. Thiết kế Arbiter

### 4.1 Kiểu Decision theo từng tình huống (bản sửa v3: từ bỏ cấu trúc vạn năng)

```go
// Kiểu con dùng chung, tránh lệch giữa các tình huống
type DispatchDecision struct {
    Instruction flow.Instruction
    Expect      DispatchExpect // xem §V
}

type PlanStartDecision struct {
    Planner string // architect_long | architect_short
    Task    string // bao gồm yêu cầu đã được mở rộng
    Reason  string
}

type InterventionDecision struct {
    Answer   string
    Rules    string
    Hold     *AdvanceHoldOp
    Reopen   *ReopenOp
    Dispatch *DispatchDecision
    Reason   string
}

type FailureDecision struct {
    Action   string // retry | reroute | abort
    Dispatch *DispatchDecision
    Reason   string
}
```

Lịch sử tiến hóa: danh sách hành động (kiểm tra thứ tự là thừa, mảng đa hình dễ sai) → cấu trúc phẳng vạn năng (không biểu đạt được thứ tự bất hợp lệ, nhưng tổ hợp bất hợp lệ phải dựa vào ma trận kiểm tra tình huống × hành động) → **kiểu theo từng tình huống (hành động không khớp tình huống thì không thể biểu đạt, ma trận kiểm tra biến mất, schema một tình huống nhỏ hơn, đầu ra của LLM ổn định hơn, eval có thể chấm theo từng tình huống)**. `Validate` co lại thành kiểm tra sự thật theo từng kiểu (ràng buộc phase, v.v.).

### 4.2 API: một cặp hàm rõ ràng cho mỗi tình huống

```go
func CollectInterventionFacts(st *store.Store) InterventionFacts        // biên IO, cùng kỷ luật với flow.LoadState
func DecideIntervention(ctx, model, facts, text) (InterventionDecision, error) // ngoài một lần gọi LLM thì không có IO, có thể phát lại ngoại tuyến
// các tình huống còn lại có dạng tương tự; Collect/Decide thống nhất hình dạng, không dựng khung Question/Decision dùng chung
```

- **Đường lỗi**: lỗi phân tích JSON hoặc kiểm tra hợp lệ sẽ bị hỏi lại có kèm lỗi (tối đa 3 lần); vẫn thất bại hoặc gọi model thất bại —— can thiệp sẽ phản hồi rõ lỗi thật và không phát sinh ghi chép, khởi động sẽ báo lỗi rõ ràng, failure/deadlock dùng phương án dự phòng xác định (kết thúc và cảnh báo)
- **Bộ nhớ can thiệp**: `decisions.jsonl` kiêm lịch sử can thiệp, `CollectInterventionFacts` đưa vào các tóm tắt phán quyết gần nhất trong N bản ghi
- **Mô hình**: Arbiter dùng thống nhất Default, không phơi lộ role riêng; chỉ khi xuất hiện yêu cầu rõ ràng về năng lực hoặc chi phí mới mở rộng hợp đồng cấu hình

### 4.3 Audit (nhỏ và ổn định; audit ≠ nguồn phục hồi)

```json
{"schema_version":1,"id":"...","kind":"intervention","checkpoint_seq":123,
 "input":"...","facts":{...},"decision":{...},"reason":"...","duration_ms":1200}
```

(token/chi phí không nằm trong bản ghi —— model phán quyết được bọc bởi `usageTrackedModel`, mức dùng được dồn thống nhất vào `UsageTracker`/ngân sách, cùng một sổ với các Worker.)

- `facts` chỉ lưu sự thật có cấu trúc + tóm tắt + tham chiếu artifact/checkpoint, **không sao chép nguyên văn, không lưu gói ngữ cảnh đầy đủ**; giới hạn kích thước mỗi bản ghi, vượt giới hạn thì cắt bớt và đánh dấu
- **`input` được giữ trong bản ghi** (cần cho phát lại ngoại tuyến `Decide*(facts, input)` — audit không có `input` thì không thể hồi quy); việc ẩn danh xảy ra ở **biên `diag export`**, không xảy ra lúc ghi xuống đĩa
- Nhật ký audit không phải event sourcing, cũng không phải nguồn dữ liệu phục hồi

## V. Giao thức chốt trạng thái (vòng lặp Engine tuần tự)

```
Đọc sự thật → Route / Arbiter tạo quyết định → kiểm tra điều kiện tiên quyết → thực thi hành động
           → Worker chạy → tính lại điều kiện hậu của Route → vòng tiếp theo
```

- **Bất biến: trạng thái điều khiển chỉ thay đổi tuần tự ở biên Engine.** Can thiệp có thể đồng thời xin ý kiến trong khi Worker đang chạy (chỉ đọc an toàn, người dùng nhìn thấy ngay Answer/Reason hồi tiếp trong vài giây), nhưng **các hành động thay đổi trạng thái điều khiển (hold/reopen/dispatch) đi vào hàng đợi Engine, và chỉ được chốt sau khi đối chiếu tại biên**; answer (không trạng thái) và rules (mặt nội dung, theo ngữ nghĩa "quy tắc cũ của chương này sẽ có hiệu lực ở chương sau") được thực thi ngay
- Mỗi Dispatch mang theo snapshot tại thời điểm Collect, đối chiếu ở biên, nếu không khớp → bỏ, ghi `decision_stale`, và hỏi lại bằng sự thật mới:

```go
type DispatchExpect struct {
    CheckpointSeq int64
    Phase         domain.Phase
    Flow          domain.FlowState
    QueueHead     int
}
```

- Điều kiện tiên quyết rõ ràng tốt hơn global Store hash (dễ đọc, dễ chẩn đoán); không dùng digest toàn cục

## VI. Mô hình phục hồi (chỉ phục hồi sự thật, không phục hồi session)

```
Khởi động → đọc Progress → đọc Checkpoint mới nhất → tra PendingSteer/AdvanceHold/giấy phép chương → Gate đối chiếu → Route → tiếp tục chạy Worker
```

Việc phục hồi của `plan_start` phụ thuộc vào một sự thật bền duy nhất (bên trong RunMeta), **phán quyết được ghi sự thật trước, rồi mới bắt đầu thực thi**:

```go
type PlanStartRecord struct {
    RawPrompt   string
    Planner     string
    PlannerTask string
    DecisionID  string // liên kết tới bản ghi audit
    Status      string // decided | dispatched | done —— biểu hiện rõ trạng thái trung gian của giao dịch khởi động
}
```

Bất kỳ điểm nào bị crash: nếu Record tồn tại thì tiếp tục theo `Status`, không hỏi lại; nếu Record không có thì coi như sách mới và hỏi lại (việc hỏi lại là chấp nhận được, audit giữ hai bản ghi).

## VII. Lộ trình di chuyển (sắp xếp lại v3: Engine đi trước, Arbiter nối sau)

Cơ sở của việc đổi thứ tự: hướng "Arbiter đi trước" cần một đường ống chuyển tiếp (phán quyết bị ngụy trang thành chỉ thị Host để bơm vào Coordinator); **khi Engine được đặt xuống trước thì toàn bộ đường ống đó không cần dựng**, Arbiter nối thẳng vào executor của Engine. Mỗi bước đều là xóa bớt thứ gì đó, không dựng cầu tạm; nỗi lo về "tùy chọn hai não" bị giải quyết mang tính cấu trúc bởi chính thứ tự này.

| # | Bước | Trạng thái |
|---|------|------|
| 0 | Mục không điều kiện: bổ sung planning vào Router (đặc tả liệt kê đầy đủ đi trước); audit `decisions.jsonl`. Cải tiến triển khai: danh tính planner suy ra từ `RunMeta.PlanningTier` sẵn có, không cần thêm cơ chế ghi mới | ✅ 2026-07-12 |
| 1 | Phần bàn giao tầng văn phong (`docs/voice-layer.md`) | ✅ 2026-07-12 |
| 2 | Chốt RFC Step 2 (`docs/engine-rfc.md`, bảy câu hỏi bắt buộc) | ✅ 2026-07-12 |
| 3 | `WorkerRunner`: xác nhận `subagent.Tool` có thể gọi trực tiếp bằng chương trình, sự kiện đi qua trung chuyển `ctx ToolProgress` | ✅ 2026-07-12 |
| 4-5 | Engine tiếp quản toàn bộ dispatch + nối bốn tình huống Arbiter (`plan_start`/`intervention`/`failure`/`deadlock`), nối thẳng vào executor của Engine (trong quá trình thực hiện nhận ra Engine đi trước khiến đường ống chuyển tiếp steering hoàn toàn không cần dựng, nên 4/5 được gộp triển khai) | ✅ 2026-07-12 |
| 6 | Xóa Coordinator và toàn bộ phần phụ trợ (§10 đều đã thực hiện); kiểm thử tích hợp end-to-end (viết thật công cụ để hoàn thành sách/ phán quyết thất bại/ phán quyết bế tắc) | ✅ 2026-07-12 |

## VIII. Câu hỏi bắt buộc của RFC Step 2 (chưa chốt thì chưa vào bước 3)

1. **Mặt trích xuất Worker**: API của `WorkerRunner`; quyền sở hữu và vòng đời toàn bộ phần lắp ráp `build.go` —— mô hình vai trò/failover, `prompt cache key`, `ThinkingLevel`, `UsageRecorder`, `SessionLogger`, `Writer ContextManagerFactory`, `RestorePack`, `StopGuardFactory`, `StopAfterTools`, chiếu sự kiện lồng nhau của Observer
2. **Vòng đời Engine**: khởi động/tạm dừng/chấm dứt/khôi phục; bảo đảm tuần tự một Worker; chuyển runtime của `/model` và `thinking`
3. **Hoàn thiện giao thức chốt trạng thái**: đối chiếu `Expect` của §V theo mọi tình huống; danh sách điều kiện tiên quyết của Engine sau khi gỡ `Gate`
4. **Phân loại lỗi**: phân loại xác định (`retry`/`reroute`/`terminal`) đi trước, chỉ những gì không còn đường ra mới gửi `worker_failure`; phân tầng với retry ở tầng `agentcore`
5. **Giao thức bế tắc**: cùng một `Agent+Task` lặp lại liên tiếp tức là điều kiện hậu của route chưa thỏa; checkpoint trung gian bên trong Worker không bị xóa sạch; `Arbiter` quyết định `retry` không xóa sạch; hỏi 3 lần, đốt cứng 5 lần
6. **Ngữ nghĩa crash**: cách xác định Worker trước đó đã tạo ra sự thật hợp lệ hay chưa
7. **Nghiệm thu nguyên mẫu**: đối chiếu từng bit giữa hiện trạng và 5 mục Observer/Usage/Context/chuyển mô hình/khôi phục

## IX. Sổ giá trị

| Khía cạnh | Hiện trạng | Kết cục |
|------|------|------|
| Chi phí LLM mỗi chương | Mỗi biên là một lời gọi chuyển tiếp | Loại bỏ; vấn đề chuyển tiếp thất bại biến mất |
| Khả năng kiểm thử của phán quyết | ~0 (trộn trong session dài) | Phát lại ngoại tuyến theo từng tình huống + hồi quy eval |
| Phản hồi can thiệp | Chờ đến biên chương (mức phút) | Hỏi ngay, chốt ở biên trạng thái điều khiển |
| Độ phức tạp | Hệ sinh thái bảy loại vá | Giảm ròng 1500+ dòng, ba loại vấn đề bị khai tử |
| Phục hồi khi crash | Phát lại session + giao thức phục hồi | Đọc store rồi chạy tiếp |
| Rủi ro giai đoạn chuyển tiếp | — | Tập trung ở bước 3/4 (trích xuất Worker), được kiểm soát bởi RFC + cổng nguyên mẫu; bước 0/1 là điều kiện vô điều kiện |

## X. Danh sách xóa ở trạng thái cuối

Coordinator và logic khôi phục session của nó, Coordinator StopGuard, giao thức steering của Dispatcher và giao thức văn bản `[Host hạ lệnh]`, `FlowBoundaryHook`, `completePhaseGate` / `writerExpandedChapterGate` (di chuyển phần kiểm tra sang điều kiện tiên quyết của Engine), `MaxTurns=100_000`, toàn bộ kỷ luật hành vi trong `coordinator.md`.

## XI. Bất đồng và ghi nhận review

1. *"Độ đúng của phán quyết sẽ không tăng"* —— đúng; khác biệt thực là tập trung/biên tập/kiểm tra trước so với trí nhớ session, giá trị ròng nhỉnh hơn một chút và lần đầu tiên đo được
2. *"Hiện tại chạy được, đụng mặt điều khiển thì mạo hiểm"* —— thừa nhận; nền móng được sinh ra cho việc này, làm từng bước có thể dừng và có thể lùi
3. *"Kiến trúc không phải nút thắt, chất lượng nội dung mới là"* —— đúng một phần, tầng văn phong đi trước
4. **Review 1 (2026-07-12)**: thiếu giao thức chốt trạng thái → §V; Step 2 quá mỏng → §VIII câu hỏi bắt buộc + cổng nguyên mẫu; trình tự khởi động → §VI; tuyên bố "trạng thái bất hợp lệ không thể biểu đạt" quá đà → lịch sử tiến hóa ở 4.1; sai факт về white-list vai trò arbiter → 4.2; vệ sinh audit → 4.3
5. **Review 2 (2026-07-12)**: kiểu `Decision` theo từng tình huống (chấp nhận, 4.1); sắp xếp lại thứ tự di chuyển, Engine đi trước (chấp nhận, §VII); thống nhất chốt ở biên (chấp nhận, §V); `PlanStartRecord` (chấp nhận, §VI); không đổi tên host (chấp nhận); đề xuất giữ số chữ trong file giao thức (chấp nhận, xem `voice-layer`). **Ý kiến giữ lại**: audit phải giữ `input` nếu không thì không thể phát lại (4.3); `completion_dispute` hạ xuống thành tình huống dự bị (§III)

## XII. Kỷ luật và điều không làm

**Kỷ luật**: ① mọi điểm quyết định mới trước hết phải qua bộ ba ở §II, cấm mặc định "thêm rule vào prompt"; ② mỗi điểm quyết định LLM đều phải có danh sách sự thật, đầu ra có cấu trúc, đường hạ cấp, audit ghi bền; ③ chỉ viết hàng rào sự thật, không viết hàng rào hành vi; ④ bất biến mang tính khai báo tốt hơn script mang tính thủ tục; ⑤ mọi thay đổi mặt điều khiển phải sửa đặc tả liệt kê đầy đủ trước rồi mới sửa triển khai.

**Không làm**: viết lại theo event sourcing; trừu tượng hóa Store cho đa tenant giả định; DSL workflow dùng chung; `State Digest` toàn cục; đổi tên gói `host`; dựng sẵn `completion_dispute`.
