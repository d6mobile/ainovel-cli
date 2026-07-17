# Chapter Advance Gate

> Trạng thái: đã triển khai
> Ngày: 2026-07-14
> Mục tiêu: nghiệm thu theo từng chương, tạm dừng an toàn sau can thiệp, và cấp phép viết chương chính xác khi khôi phục sau sự cố

## 1. Vì sao cần cơ chế này

Rủi ro lớn nhất của sáng tác tự động dài kỳ không phải là tốn thêm một lần gọi model, mà là trong lúc người dùng đang xem lại, hệ thống vẫn tiếp tục viết chương mới và kéo các tóm tắt, trạng thái nhân vật, cùng phản hồi đề cương dựa trên nội dung cũ vào nguồn факт tiếp theo. Xoá một chương viết thừa không tự động hoàn tác các trạng thái phát sinh này, và người dùng sẽ mất niềm tin vào quy trình sáng tác.

Hệ thống vẫn giữ mặc định là “đã có mục tiêu thì tiếp tục tự chạy đến khi hoàn thành”, vì vậy không biến xác nhận theo từng chương thành hành vi mặc định toàn cục. Hệ thống chỉ cung cấp hai chính sách rõ ràng:

- `auto`: chế độ mặc định, tự động tiến hành liên tục;
- `review`: chế độ nghiệm thu từng chương do người dùng chủ động chọn, mỗi chương mới theo chiều tiến lên đều cần một lần cấp phép chính xác.

Đây không phải là việc trả workflow lại cho Coordinator LLM. Việc nào cần người dùng xác nhận là chính sách của người dùng; bước kế tiếp vẫn do Route suy ra một cách xác định; còn việc có nên dừng lại để nghiệm thu một kết quả can thiệp hay không thì do Arbiter phán đoán ngữ nghĩa.

## 2. Phân định ranh giới

| Vấn đề | Thuộc về | Lý do |
|---|---|---|
| Có đang ở chế độ nghiệm thu theo từng chương không | RunMeta / Host | Ý định chạy bền vững của người dùng |
| Chương nào đã được cấp phép | RunMeta / Gate | Sự thật cơ học, có thể kiểm tra và khôi phục |
| Worker nào sẽ chạy ở bước tiếp theo | `flow.Route` | Suy ra thuần từ dữ liệu sáng tác |
| Một chỉ thị có bắt đầu một chương mới theo chiều tiến lên hay không | `flow.StartsForwardChapter` | Phán định cơ học đã kiểu hoá |
| “Sửa xong cho tôi xem lại” có cần tạm dừng không | Arbiter | Phán định ngữ nghĩa từ ngôn ngữ tự nhiên |
| Khi nào việc tạm dừng được kích hoạt | `ChapterAdvanceGate` | Thực thi xác định cho một ý định dùng một lần |
| Ngân sách có cho phép tiếp tục không | `BudgetSentinel` | Chính sách Host độc lập |

`AdvanceMode`, giấy phép chương và hold dùng một lần không đi vào bảng quyết định của Route, cũng không cho phép model tự sửa. Máy trạng thái sáng tác của Route và chính sách nghiệm thu theo từng chương phải giữ tính trực giao.

## 3. Mô hình trạng thái tối thiểu

Trong `meta/run.json` chỉ thêm ba ý định chạy:

```go
type RunMeta struct {
	AdvanceMode          ChapterAdvanceMode `json:"advance_mode"`
	AdvancePermitChapter int                `json:"advance_permit_chapter,omitempty"`
	AdvanceHold          *AdvanceHold       `json:"advance_hold,omitempty"`
}

const (
	ChapterAdvanceAuto   ChapterAdvanceMode = "auto"
	ChapterAdvanceReview ChapterAdvanceMode = "review"
)

const (
	AdvanceHoldAtBoundary           AdvanceHoldAfter = "boundary"
	AdvanceHoldAfterRewritesDrained AdvanceHoldAfter = "rewrites_drained"
)

type AdvanceHold struct {
	After  AdvanceHoldAfter `json:"after"`
	Reason string           `json:"reason"`
}
```

Không có PolicyEngine tổng quát, không có mảng điều kiện, không có hàng đợi cấp phép, không có thời hạn, cũng không có phiên bản chính sách. Nhu cầu thực tế hiện tại chỉ cần một chế độ bền vững, một giấy phép chính xác và một hold dùng một lần.

### 3.1 Bất biến

1. `AdvanceMode` chỉ có thể là `auto` hoặc `review`; giá trị lạ trả về `UnsupportedAdvanceModeError`.
2. Chế độ không hợp lệ thì không được khởi động Host, cũng không được ghi đè RunMeta.
3. Ở chế độ `auto`, giấy phép phải bằng `0`.
4. Ở chế độ `review`, giấy phép chỉ có thể là `0` hoặc một số thứ tự chương dương.
5. Cấp lại cùng một đích phải idempotent; đích khác không được ghi đè giấy phép đang có.
6. Giấy phép chỉ ràng buộc “bắt đầu một chương mới theo chiều tiến lên mà chưa hoàn tất”; các bước lập kế hoạch, đánh giá, sửa lại, hoàn thiện và khôi phục khi commit không bị chặn.
7. Giấy phép gắn với số chương, không gắn với một lần chạy tiến trình hay một lần gọi Worker cụ thể.
8. Chỉ khi chương mục tiêu đã nằm trong `CompletedChapters`, `PendingCommit` tương ứng đã rỗng, và tồn tại checkpoint `commit` của chương đó thì giấy phép mới được coi là đã tiêu thụ ổn định.
9. Chương đã hoàn tất nhưng thiếu checkpoint `commit` là trạng thái hỏng: phải báo lỗi rõ ràng và tạm dừng, không được tự đoán cách sửa.
10. Giấy phép chưa hoàn tất phải đúng bằng `Progress.NextChapter()`. `PendingRewrites` không làm đổi `NextChapter()`, nên việc sửa lại và giấy phép tiến lên đang treo có thể cùng tồn tại theo cơ học.
11. `AdvanceHold` chỉ được dùng `boundary` hoặc `rewrites_drained`, và phải có lý do không rỗng.
12. Hold và giấy phép đều dùng compare-and-clear; trạng thái bị một hành động mới thay thế thì không được xoá nhầm.

## 4. API của Store

RunMetaStore cung cấp các thao tác nguyên tử, hẹp và có kiểu rõ ràng:

```go
SetAdvanceMode(mode domain.ChapterAdvanceMode) error
GrantAdvancePermit(chapter int) error
ClearAdvancePermit(chapter int) error
SetAdvanceHold(hold domain.AdvanceHold) error
ClearAdvanceHold(expected domain.AdvanceHold) error
```

- Khi chuyển về `auto`, cần xoá giấy phép chương trong cùng một khoá ghi, nhưng không xoá hold do một can thiệp người dùng khác tạo ra;
- Chỉ được cấp phép trong `review`;
- Thao tác xoá chỉ được tiêu thụ đúng mục tiêu mà bên gọi vừa đọc;
- Khi khởi tạo RunMeta, chế độ mặc định là `auto`, đồng thời giữ lại chế độ, giấy phép và hold đã ghi trên đĩa.

Hiện dự án không có dữ liệu lịch sử cần di trú, nên phần triển khai không bao gồm đọc trường cũ, ghi kép hay nhánh suy giảm.

## 5. Ngữ nghĩa hàm thuần

### 5.1 Nhận diện chương mới theo chiều tiến lên

```go
func StartsForwardChapter(
	inst *Instruction,
	progress *domain.Progress,
	pending *domain.PendingCommit,
) bool
```

Chỉ trả về `true` khi đồng thời thoả tất cả điều kiện sau:

- Worker là `writer`;
- phase là `writing`;
- không có `PendingCommit`;
- không có hàng đợi sửa lại;
- không có `InProgressChapter`;
- chương mục tiêu đúng bằng `NextChapter()`.

Hàm này chỉ đọc các field đã kiểu hoá, không phân tích văn bản của Task hay Reason.

### 5.2 Hold dùng một lần

`ResolveAdvanceHold` trả về theo hold và Progress:

- `keep`: điều kiện chưa thoả;
- `consume`: ở trạng thái hoàn tất chỉ cần dọn ý định;
- `consume-and-stop`: dọn ý định rồi tạm dừng.

`boundary` được kích hoạt ở ranh giới Worker hiện tại; `rewrites_drained` được kích hoạt sau khi hàng đợi sửa lại đã rỗng. Điều kiện không hợp lệ và dữ kiện thiếu đều phải báo lỗi.

## 6. ChapterAdvanceGate

Gate là thành phần chính sách tiến lên sáng tác duy nhất ngoài ngân sách. Nó chỉ có hai nhiệm vụ:

1. Ở ranh giới vòng lặp, phân giải và tiêu thụ hold dùng một lần;
2. Trước khi phát writer, kiểm tra giấy phép theo từng chương, rồi đối soát ở ranh giới xem giấy phép đã được tiêu thụ ổn định chưa.

Trình tự của Engine là:

```text
Áp dụng các can thiệp đang chờ
→ Gate kiểm tra ranh giới
→ Route / nhận điều phối từ Arbiter
→ precheck
→ Gate kiểm tra giấy phép phát
→ Worker
→ Budget kiểm tra ranh giới
→ Gate kiểm tra ranh giới
→ vòng lặp tiếp theo
```

Khi `auto && hold == nil`, bước kiểm tra ranh giới sẽ đọc RunMeta rồi trả về ngay, không đọc Progress, PendingCommit hay checkpoint.

### 6.1 hold + dispatch

Arbiter có thể rút gọn một yêu cầu kiểu “viết lại chương 3, sửa xong cho tôi xem” thành:

```json
{
  "hold": {
    "after": "rewrites_drained",
    "reason": "Chờ người dùng nghiệm thu sau khi viết lại xong"
  },
  "dispatch": {
    "agent": "editor",
    "task": "Rà soát chương 3 và tạo hàng đợi sửa lại theo kết quả"
  }
}
```

Hai hành động này phải được thực hiện theo cặp: trước hết phát đúng tác vụ để Editor tạo ra sự thật về sửa lại, sau đó Gate mới được quyền quyết định xem hàng đợi đã rỗng chưa. Engine sẽ gắn “lần phát này bị hoãn qua Gate” với chính lệnh trong bộ nhớ này, và xoá nó khi lấy lệnh ra; các lệnh Arbiter bình thường không được quyền bỏ qua Gate.

### 6.2 permit và sửa lại

Lệnh `reopen` sau khi hoàn tác chỉ có thể xảy ra ở `complete`, còn `/next` chỉ có thể xảy ra ở `writing`; hai trạng thái này loại trừ lẫn nhau theo cơ học. `PendingRewrites` đã tồn tại trong giai đoạn viết không làm thay đổi chương hoàn thành lớn nhất, nên giấy phép vẫn khớp với cùng một `NextChapter()`; Worker sửa lại vẫn có thể chạy, nhưng sẽ không tiêu thụ giấy phép tiến lên.

## 7. Khôi phục khi crash

Việc submit chương là một saga nhiều bước, nên giấy phép không thể được biểu diễn bằng một cờ kiểu “lần chạy tiếp theo được viết một chương”. Khi khôi phục, Gate đối soát dựa trên ba loại sự thật:

| Cửa sổ sự thật | Hành vi của Gate |
|---|---|
| Chương mục tiêu chưa hoàn tất, không có PendingCommit | Giữ nguyên giấy phép, cho phép bắt đầu/khôi phục chương đó |
| PendingCommit thuộc chương mục tiêu | Giữ nguyên giấy phép, để hoàn tất việc khôi phục submit |
| Chương mục tiêu đã hoàn tất, PendingCommit đã rỗng, và checkpoint `commit` tồn tại | Tiêu thụ giấy phép |
| Chương mục tiêu đã hoàn tất nhưng thiếu checkpoint | Báo lỗi và tạm dừng |
| Giấy phép trỏ tới một chương chưa hoàn tất nhưng không phải `NextChapter` | Báo lỗi và tạm dừng |

Vì vậy nếu tiến trình crash trong bất kỳ cửa sổ nào của bước viết nháp, ghi trạng thái, đánh dấu tiến độ hay ghi tín hiệu, thì cùng một giấy phép sẽ không bao giờ bị dùng nhầm cho chương tiếp theo.

## 8. Arbiter

Schema can thiệp dùng `AdvanceHoldOp`:

```go
type AdvanceHoldOp struct {
	Cancel bool                    `json:"cancel,omitempty"`
	After  domain.AdvanceHoldAfter `json:"after,omitempty"`
	Reason string                  `json:"reason,omitempty"`
}
```

Quy tắc:

- Khi người dùng nói rõ “tạm dừng trước đã” thì dùng `boundary`;
- Ở chế độ `auto`, câu “sửa lại chương đã viết, sửa xong cho tôi nghiệm thu” thì dùng `rewrites_drained`;
- Khi đã ở `review` thì đã dừng theo từng chương rồi, không tạo thêm hold đồng nghĩa;
- Lệnh “tiếp tục” có thể huỷ hold hiện có, nhưng không được cấp giấy phép chương;
- Chuyển chế độ chỉ dùng `/review on|off`, còn giải phóng chỉ dùng `/next`.

Engine gọi trực tiếp RunMetaStore để áp dụng hành động có cấu trúc, không giả lập nó thành một Tool của LLM.

## 9. Giao diện người dùng

### 9.1 `/review on|off`

- `/review on`: lưu ngay chính sách nghiệm thu theo từng chương; nếu Worker đang chạy, sau khi công việc hiện tại xong sẽ dừng trước chương tiến lên kế tiếp;
- `/review off`: quay lại chế độ tự động tiến lên và xoá giấy phép một cách nguyên tử; việc này không tự khởi động lại Engine đang bị dừng, mà sẽ báo rõ cho người dùng nhập lệnh tiếp tục.

### 9.2 `/next`

Chỉ dùng được khi tất cả điều kiện sau cùng đúng:

- Engine không chạy;
- không phải chế độ cộng tác theo giai đoạn;
- đang ở chế độ `review`;
- không có hold đang chờ;
- ngân sách còn cho phép;
- phase là `writing`.

Lệnh này cấp giấy phép chính xác cho `NextChapter()` rồi khởi động Engine. Thông báo sẽ nói rõ: sau khi chương này được submit xong, các bước đánh giá cần thiết và việc duy trì cấu trúc arc/volume vẫn sẽ hoàn tất, rồi hệ thống lại chờ cho phép tiếp.

### 9.3 Hiển thị trạng thái

`UISnapshot` là nguồn sự thật duy nhất của TUI, gồm:

- `AdvanceMode`;
- `AdvancePermitChapter`;
- `HasAdvanceHold`;
- `AdvanceHoldReason`.

Thanh bên hiển thị trạng thái tự động / nghiệm thu theo từng chương và chương đã được cho phép; khi chờ thì ô nhập hiển thị “Nhập ý kiến sửa đổi, hoặc `/next` để cho phép chương tiếp theo”. Loại thông báo là `advance_gate`.

## 10. Kiểm chứng

Các test cần bao phủ:

- Chuyển trạng thái nguyên tử của RunMeta cho mode, giấy phép và hold, cùng compare-and-clear;
- Chế độ không hợp lệ phải thất bại rõ ràng và không ghi đè RunMeta;
- Nhận diện hàm thuần cho chương tiến lên, sửa lại và khôi phục;
- Ngữ nghĩa hold cho `boundary`, hàng đợi sửa lại chưa rỗng, hàng đợi sửa lại đã rỗng và trạng thái hoàn tất;
- Chặn khi không có giấy phép, cho qua khi có giấy phép chính xác, báo lỗi khi lệch chương;
- Trong lúc PendingCommit còn tồn tại thì giấy phép phải được giữ lại, và chỉ tiêu thụ sau khi commit ổn định;
- Khi completion marker và checkpoint xung đột phải tạm dừng;
- Khi permit xen kẽ với PendingRewrites thì không báo nhầm;
- Chứng minh end-to-end rằng một giấy phép chỉ ổn định đúng một chương mới;
- Khi Gate đã đánh dấu dừng nhưng goroutine cũ của Engine vẫn đang thoát ra, `/next` phải từ chối vào lại rõ ràng, và thử lại sau đó sẽ khôi phục idempotent theo cùng một giấy phép chương;
- Hồi quy cho hold-only, hold+dispatch và các cuộc đua lúc thoát.

## 11. Những gì cố ý không làm

- Không để model quyết định mode chạy hoặc tự cấp giấy phép;
- Không chỉnh Route để phục vụ chính sách xác nhận của người dùng;
- Không biến sửa lại, lập kế hoạch, đánh giá và duy trì cấu trúc thành một chuỗi xác nhận từng bước;
- Không thêm PolicyEngine tổng quát, danh sách StopCondition hay DSL cho chính sách;
- Không hỗ trợ cấp phép sẵn cho nhiều chương hoặc hàng đợi giấy phép;
- Không giữ lại mô hình tạm dừng cũ, field tương thích, DTO di trú hay đường ghi kép;
- Không tự âm thầm suy giảm cho một mode tương lai chưa biết.

Nếu sau này xuất hiện nhu cầu mới về ranh giới tự chủ đã được kiểm chứng lặp lại, khi đó sẽ mở rộng mode dựa trên bằng chứng; còn hiện tại, chi phí hối hận thấp chính là khả năng tương thích trong tương lai.
