package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// DraftChapterTool viết toàn bộ bản nháp một chương, thay thế pipeline cũ write_scene + polish_chapter.
// Agent tự quyết định viết một lần hay chia nhỏ để tiếp tục.
type DraftChapterTool struct {
	store *store.Store
}

func NewDraftChapterTool(store *store.Store) *DraftChapterTool {
	return &DraftChapterTool{store: store}
}

func (t *DraftChapterTool) Name() string { return "draft_chapter" }
func (t *DraftChapterTool) Description() string {
	return "Viết nội dung chính của chương. mode=write ghi đè toàn bộ chương, mode=append nối thêm vào bản nháp hiện có (tiếp tục/chỉnh sửa)"
}
func (t *DraftChapterTool) Label() string { return "Viết chương" }

// Công cụ ghi, cấm chạy đồng thời (race condition đọc-sửa-ghi).
func (t *DraftChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *DraftChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *DraftChapterTool) Schema() map[string]any {
	// mode được đánh dấu required để tương thích với OpenAI strict tool calling —
	// chế độ strict yêu cầu tất cả properties đều có mặt trong danh sách required.
	// Hành vi cũ "bỏ qua mode thì mặc định là write" nay yêu cầu model truyền
	// tường minh mode="write"; nhánh default trong Execute không đổi.
	return schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("content", schema.String("Nội dung chính của chương")).Required(),
		schema.Property("mode", schema.Enum("Chế độ viết", "write", "append")).Required(),
	)
}

// StrictSchema bật strict tool calling của OpenAI, buộc model tuân thủ schema nghiêm ngặt:
// tất cả trường required phải được điền, arguments không thể "EOT sớm" ra object rỗng.
// litellm chuyển tiếp trường strict; các backend hỗ trợ như OpenAI / xAI sẽ thực thi,
// các backend khác bỏ qua trường không nhận biết theo thông lệ HTTP/JSON.
// Anthropic/Gemini/Bedrock đi qua chuỗi chuyển đổi riêng nên không thấy trường này.
func (t *DraftChapterTool) StrictSchema() bool { return true }

func (t *DraftChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Chapter int    `json:"chapter"`
		Content string `json:"content"`
		Mode    string `json:"mode"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tham số không hợp lệ: %w: %w", errs.ErrToolArgs, err)
	}
	if a.Chapter <= 0 {
		return nil, fmt.Errorf("chapter phải lớn hơn 0: %w", errs.ErrToolArgs)
	}
	if a.Content == "" {
		return nil, fmt.Errorf("content không được để trống: %w", errs.ErrToolArgs)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		return nil, err
	}
	if err := EnsureChapterExpanded(t.store, a.Chapter); err != nil {
		return nil, err
	}
	if t.store.Progress.IsChapterCompleted(a.Chapter) {
		// Luồng chỉnh sửa/viết lại: chương đã hoàn thành nhưng vẫn còn trong pending_rewrites, cho phép ghi đè bản nháp
		progress, _ := t.store.Progress.Load()
		inRewriteQueue := progress != nil && slices.Contains(progress.PendingRewrites, a.Chapter)
		if !inRewriteQueue {
			return json.Marshal(map[string]any{
				"chapter":   a.Chapter,
				"skipped":   true,
				"completed": true,
				"reason":    fmt.Sprintf("Chương %d đã được lưu hoàn thành, không thể ghi đè", a.Chapter),
			})
		}
	}
	if err := t.store.Progress.StartChapter(a.Chapter); err != nil {
		return nil, fmt.Errorf("đánh dấu chương đang viết: %w", err)
	}

	switch a.Mode {
	case "append":
		if err := t.store.Drafts.AppendDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("nối thêm bản nháp: %w", err)
		}
		full, err := t.store.Drafts.LoadDraft(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("tải bản nháp sau khi nối thêm: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifact(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		); err != nil {
			return nil, fmt.Errorf("ghi checkpoint bản nháp: %w", err)
		}
		return json.Marshal(map[string]any{
			"written":    true,
			"chapter":    a.Chapter,
			"mode":       "append",
			"word_count": utf8.RuneCountInString(full),
			"next_step":  "Trước tiên read_chapter(source=draft) để đọc lại bản nháp, rồi gọi check_consistency, cuối cùng commit_chapter",
		})
	default: // write
		if err := t.store.Drafts.SaveDraft(a.Chapter, a.Content); err != nil {
			return nil, fmt.Errorf("lưu bản nháp: %w", err)
		}
		if _, err := t.store.Checkpoints.AppendArtifact(
			domain.ChapterScope(a.Chapter), "draft",
			fmt.Sprintf("drafts/%02d.draft.md", a.Chapter),
		); err != nil {
			return nil, fmt.Errorf("ghi checkpoint bản nháp: %w", err)
		}
		return json.Marshal(map[string]any{
			"written":    true,
			"chapter":    a.Chapter,
			"mode":       "write",
			"word_count": utf8.RuneCountInString(a.Content),
			"next_step":  "Trước tiên read_chapter(source=draft) để đọc lại bản nháp, rồi gọi check_consistency, cuối cùng commit_chapter",
		})
	}
}
