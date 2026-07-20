package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

type ReopenBookTool struct {
	store *store.Store
}

func NewReopenBookTool(s *store.Store) *ReopenBookTool {
	return &ReopenBookTool{store: s}
}

func (t *ReopenBookTool) Name() string  { return "reopen_book" }
func (t *ReopenBookTool) Label() string { return "Mở lại để viết lại" }

func (t *ReopenBookTool) Description() string {
	return "Mở lại toàn bộ truyện đã hoàn tất (phase=complete) để vào trạng thái viết lại, dùng khi người dùng yêu cầu viết lại/chỉnh sửa một vài chương sau khi hoàn truyện. " +
		"chapters là danh sách số chương đã hoàn thành cần viết lại; sau khi gọi, các chương này vào hàng đợi rewrite, Host sẽ lần lượt giao writer viết lại, sửa xong toàn bộ sẽ tự hoàn truyện lại. " +
		"Chỉ dùng khi toàn bộ truyện đã hoàn tất và người dùng yêu cầu rõ việc sửa chương đã viết; nếu người dùng muốn thêm tình tiết/mở rộng độ dài thì không phải viết lại, không dùng tool này."
}

func (t *ReopenBookTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *ReopenBookTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *ReopenBookTool) ActivityDescription(_ json.RawMessage) string {
	return "Mở lại toàn truyện để viết lại"
}

func (t *ReopenBookTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapters", schema.Array("Danh sách số chương đã hoàn thành cần viết lại (ít nhất một chương)", schema.Int(""))).Required(),
		schema.Property("reason", schema.String("Lý do viết lại (tùy chọn, ví dụ \"dọn ký tự đặc biệt\")")),
	)
}

func (t *ReopenBookTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapters []int  `json:"chapters"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tham số không hợp lệ: %w: %w", errs.ErrToolArgs, err)
	}
	if len(a.Chapters) == 0 {
		return nil, fmt.Errorf("chapters không được để trống, cần chỉ rõ chương phải viết lại: %w", errs.ErrToolArgs)
	}

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("tải progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return nil, fmt.Errorf("progress chưa được khởi tạo: %w", errs.ErrToolPrecondition)
	}
	// Chỉ được viết lại các chương đã viết; số chương không thuộc tập đã hoàn thành là thuộc luồng viết tiếp/vượt phạm vi, cần từ chối rõ ràng và hướng người dùng sang điều chỉnh độ dài.
	var invalid []int
	for _, ch := range a.Chapters {
		if !slices.Contains(progress.CompletedChapters, ch) {
			invalid = append(invalid, ch)
		}
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("các chương %v chưa viết xong; reopen chỉ viết lại chương đã hoàn thành (muốn thêm/mở rộng tình tiết thì hãy đi theo luồng điều chỉnh độ dài): %w", invalid, errs.ErrToolPrecondition)
	}

	if err := t.store.Progress.Reopen(a.Chapters, a.Reason); err != nil {
		return nil, fmt.Errorf("mở lại để viết lại: %w: %w", errs.ErrStoreWrite, err)
	}

	// checkpoint: đối xứng với complete_book (GlobalScope + meta/progress.json).
	if _, err := t.store.Checkpoints.AppendArtifact(domain.GlobalScope(), "reopen", "meta/progress.json"); err != nil {
		return nil, fmt.Errorf("ghi checkpoint reopen: %w: %w", errs.ErrStoreWrite, err)
	}

	return json.Marshal(map[string]any{
		"reopened":         true,
		"phase":            string(domain.PhaseWriting),
		"pending_rewrites": a.Chapters,
		"next_step":        "Đã mở lại và đưa các chương mục tiêu vào hàng đợi. Hãy chờ Host chỉ định writer viết lại từng chương; sau khi sửa xong toàn bộ sẽ tự hoàn truyện lại.",
	})
}
