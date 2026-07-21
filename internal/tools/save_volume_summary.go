package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

// SaveVolumeSummaryTool lưu tóm tắt cấp tập truyện, Biên tập viên gọi khi kết thúc tập.
type SaveVolumeSummaryTool struct {
	store *store.Store
}

func NewSaveVolumeSummaryTool(store *store.Store) *SaveVolumeSummaryTool {
	return &SaveVolumeSummaryTool{store: store}
}

func (t *SaveVolumeSummaryTool) Name() string { return "save_volume_summary" }
func (t *SaveVolumeSummaryTool) Description() string {
	return "Lưu tóm tắt cấp tập truyện (chế độ dài tập, gọi khi kết thúc tập)"
}
func (t *SaveVolumeSummaryTool) Label() string { return "Lưu tóm tắt tập" }

// Công cụ ghi, cấm chạy đồng thời.
func (t *SaveVolumeSummaryTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveVolumeSummaryTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveVolumeSummaryTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("volume", schema.Int("Số thứ tự tập")).Required(),
		schema.Property("title", schema.String("Tiêu đề tập")).Required(),
		schema.Property("summary", schema.String("Tóm tắt tập (không quá 500 chữ)")).Required(),
		schema.Property("key_events", schema.Array("Các sự kiện quan trọng trong tập", schema.String(""))).Required(),
	)
}

func (t *SaveVolumeSummaryTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Volume    int      `json:"volume"`
		Title     string   `json:"title"`
		Summary   string   `json:"summary"`
		KeyEvents []string `json:"key_events"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if a.Volume <= 0 {
		return nil, fmt.Errorf("volume must be > 0")
	}

	volSummary := domain.VolumeSummary{
		Volume:    a.Volume,
		Title:     a.Title,
		Summary:   a.Summary,
		KeyEvents: a.KeyEvents,
	}
	if err := t.store.Summaries.SaveVolumeSummary(volSummary); err != nil {
		return nil, fmt.Errorf("save volume summary: %w", err)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.VolumeScope(a.Volume), "volume_summary",
		fmt.Sprintf("summaries/vol-v%02d.json", a.Volume),
	); err != nil {
		return nil, fmt.Errorf("checkpoint volume summary: %w", err)
	}

	result := map[string]any{
		"saved": true, "type": "volume_summary", "volume": a.Volume,
	}
	// 收官主路径的完结触发点：卷末收尾三连的最后一块拼图是卷摘要，落盘后若全书已
	// 满足完结条件则就地 MarkComplete（完结检查始终发生在最后一块事实落地的工具里，
	// 与 commit_chapter 同一模式；谓词见 commit_chapter.go 的 layeredComplete）。
	p, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress: %w", err)
	}
	if p != nil && p.Layered && p.Phase == domain.PhaseWriting {
		complete, err := layeredComplete(t.store, p)
		if err != nil {
			return nil, fmt.Errorf("evaluate book completion: %w", err)
		}
		if complete {
			if err := t.store.Progress.MarkComplete(); err != nil {
				return nil, fmt.Errorf("mark book complete: %w", err)
			}
			result["book_complete"] = true
		}
	}
	return json.Marshal(result)
}
