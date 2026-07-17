# Step 2 RFC: Engine trực tiếp chạy Worker (câu trả lời cho 7 câu hỏi bắt buộc)

> Trạng thái: chốt định (2026-07-12). Dựa trên việc quan sát mã của host/observer/subagent/usage/cocreate.
> Kết luận: cả 7 câu đều có lời giải ít rủi ro, có thể triển khai. Liên quan: `docs/engine-arbiter.md`.

## 1. Worker layer → không còn lớp trích xuất: gọi trực tiếp `subagent.Tool`

> Ghi chú sau này (2026-07-12): agentcore sau đó đã thêm điểm vào chương trình `subagent.Tool.Run` (với kiểu cho tham số/kết quả/chuỗi lỗi),
> và Engine đã chuyển sang dùng nó — phần mô tả cũ “đi qua JSON shell của Execute” được giữ lại như tư liệu đối chiếu lịch sử tại thời điểm quyết định;
> bộ sniff kết quả như `parseSubagentResultError` cũng đã bị xoá.

`subagent.Tool.Execute` là một phương thức bình thường, mỗi lần gọi sẽ chạy trọn một `agentcore.AgentLoop`. Engine gọi thẳng nó, nên toàn bộ phần lắp ráp trong build.go vẫn giữ nguyên tác dụng: mô hình vai trò + failover, prompt cache key (mỗi lần spawn thêm `#seq`), ThinkingLevel, UsageRecorder/SessionLogger(OnMessage), Writer ContextManagerFactory, RestorePack, StopGuardFactory, StopAfterTools. `BuildCoordinator` được đổi tên thành `BuildWorkers`, chỉ bỏ các phần chuyên biệt cho coordinator.

**Chiếu sự kiện**: luồng trung gian của tiến độ subagent đọc từ **callback ToolProgress trong ctx** (`agentcore.ReportToolProgress(ctx,...)`). Engine gọi `Execute` với `agentcore.WithToolProgress(ctx, relay)`, nên cơ chế trung gian vẫn chạy bình thường; relay gộp `ProgressPayload` thành `EventToolExecUpdate` rồi đẩy vào `observer.handleToolUpdate` hiện có — phần xử lý ở phía worker của observer (dòng TOOL / nội dung stream / thinking / retry / context) được tái sử dụng khoảng 95%. Dòng DISPATCH thì Engine tự phát và tự kết thúc (thêm hai điểm vào observer).

Coordinator không còn đóng vai trò kể chuyện ở cột trái; thay vào đó Engine tự kể luồng sự kiện.

**/model và mức suy luận**: đổi model thông qua `ModelSet` swap (giữ nguyên wrapper failover, cơ chế cũ); đổi mức suy luận qua `subagentTool.SetThinkingLevel` (giữ `applyThinking`, bỏ nhánh coordinator).

## 2. Vòng đời của Engine

Chạy tuần tự trong một goroutine; `ctx` cancel = tạm dừng / dừng hẳn (truyền vào worker loop, checkpoint đảm bảo không mất dữ liệu); Resume/Continue = tạo vòng lặp mới. Việc một Worker chạy tuần tự đã được bảo đảm bởi cấu trúc vòng lặp. Các sentry về ngân sách / ranh giới của từng vòng do Engine gọi trực tiếp ở mép mỗi vòng (thay cho subscription sự kiện và `FlowBoundaryHook`).

## 3. Giao thức ghi trạng thái → nhờ tuần tự mà gần như biến mất

Mỗi vòng, Engine chỉ `LoadState+Route` trước khi spawn, nên chỉ thị luôn dựa trên dữ kiện mới nhất — chỉ thị Route không có TOCTOU, không cần đối soát Expect. Snapshot Expect chỉ còn dùng cho **dispatch do Arbiter quyết định** (giữa lúc hỏi và lúc thực thi có worker chạy xen vào): trước khi đi qua ranh giới, so sánh `{Phase, QueueHead}`, nếu không khớp thì loại bỏ và hỏi lại theo dữ kiện mới. Phần precheck trước đây thuộc Gate nay trở thành code thường trong Engine: `phase=complete` thì không dispatch; writer mà mục tiêu chương chưa mở rộng thì đổi sang `architect_long expand` (xác định, không cần văn bản hướng dẫn).

Các hành động điều khiển trạng thái (hold/reopen/dispatch) của can thiệp được đưa vào biên vòng của hàng đợi Engine; answer/rules thì chạy ngay.

## 4. Phân loại lỗi (ưu tiên xác định)

- retryable (mạng / giới hạn tốc độ / stream-idle): `subagent` đã tự xử lý gần nguồn với `MaxRetries=7`, không thoát ra ngoài vòng;
- worker trả error (escalate/hard_stop/lỗi công cụ nặng): cùng một chỉ thị, Engine thử lại 1 lần → vẫn lỗi → hỏi Arbiter qua `worker_failure` (retry/reroute/abort) → abort, hoặc Arbiter 자체 lỗi → tạm dừng + notify;
- lỗi tham số / agent không xác định và các lỗi xác định khác: tạm dừng ngay + notify (bug của code, retry không có ích).

## 5. Giao thức bế tắc

Mỗi vòng ghi lại khoá chỉ thị `Agent+Task`. Nếu sau khi vòng trước chạy xong, Route vẫn sinh ra cùng một khoá, nghĩa là điều kiện hậu của nhiệm vụ chưa thoả, `repeat++`; nếu chỉ thị đổi thì reset về 0. Các checkpoint trung gian của Worker như `plan/draft/edit` không tính là tiến bộ cấp Engine.

`repeat==3` → hỏi Arbiter về `deadlock`; Arbiter có thể khuyên retry nhưng **không reset**; `repeat==5` → ngắt cứng: tạm dừng + notify.

(Thời coordinator, việc “không đặt ngưỡng” dựa vào khả năng tự chủ của nó; Engine xác định thì bắt buộc phải có giới hạn.)

## 6. Ngữ nghĩa crash → coi như miễn phí

Không cần xác định “Worker trước đó có tạo ra sự thật hữu ích hay không”: checkpoint tầng công cụ + digest đã idempotent, Route được tính lại từ store; phát lại cùng chỉ thị là an toàn (cùng một nguyên tắc với `ToolsAreIdempotent`). Khôi phục = quay lại vòng lặp ngay. `PendingSteer` được đưa qua Arbiter trước khi vòng lặp bắt đầu.

## 7. Kiểm chứng nguyên mẫu

Test tích hợp end-to-end (fake ChatModel): từ lập kế hoạch → bổ sung → viết chương → đánh giá / tóm tắt cuối arc → mở rộng → hoàn sách; xử lý can thiệp được lưu vào store; tạm dừng / phục hồi; ngắt bế tắc; ghi usage; dạng sự kiện observer (`DISPATCH` / `TOOL`, delta stream). Kết hợp thêm các bộ hồi quy sẵn có như spec Route 60k, contract của agentcore, và test luồng editor.

## 8. Kết luận ở giai đoạn hoàn tất

Phần tổng kết khi hoàn sách sẽ chuyển sang **sinh xác định**: store đã có toàn bộ dữ kiện (tóm tắt chương / nhân vật / sổ phục bút / số từ), nên Engine có thể render báo cáo trực tiếp, không cần thêm một lần gọi LLM để tạo văn bản nghi thức. Phần tổng kết LLM cũ của coordinator bị loại bỏ (`engine-arbiter.md §3: tổng kết không phải phán quyết`).
