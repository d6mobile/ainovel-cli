package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// PlanChapterTool lưu ý tưởng xây dựng chương, Agent tự quyết định độ chi tiết khi lập kế hoạch.
type PlanChapterTool struct {
	store *store.Store
}

func NewPlanChapterTool(store *store.Store) *PlanChapterTool {
	return &PlanChapterTool{store: store}
}

func (t *PlanChapterTool) Name() string { return "plan_chapter" }
func (t *PlanChapterTool) Description() string {
	return "Lưu ý tưởng xây dựng chương. Agent tự quyết định độ chi tiết khi lập kế hoạch, không bắt buộc tách cảnh"
}
func (t *PlanChapterTool) Label() string { return "Lập kế hoạch chương" }

// Công cụ ghi, cấm chạy song song.
func (t *PlanChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *PlanChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *PlanChapterTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("title", schema.String("Tiêu đề chương")).Required(),
		schema.Property("goal", schema.String("Mục tiêu chương")).Required(),
		schema.Property("conflict", schema.String("Xung đột cốt lõi")).Required(),
		schema.Property("hook", schema.String("Điểm móc cuối chương")).Required(),
		schema.Property("emotion_arc", schema.String("Cung cảm xúc")),
		schema.Property("notes", schema.String("Ghi chú tự do (bất cứ điều gì bạn cần nhớ khi viết)")),
		schema.Property("required_beats", schema.Array("Các nhịp truyện bắt buộc phải hoàn thành trong chương", schema.String(""))),
		schema.Property("forbidden_moves", schema.Array("Các diễn biến rõ ràng không được xảy ra trong chương", schema.String(""))),
		schema.Property("continuity_checks", schema.Array("Các điểm liên tục cần kiểm tra đặc biệt trong chương", schema.String(""))),
		schema.Property("evaluation_focus", schema.Array("Các hạng mục Biên tập viên cần kiểm tra trọng tâm", schema.String(""))),
		schema.Property("emotion_target", schema.String("Tùy chọn: cảm xúc chính mà chương muốn độc giả cảm nhận")),
		schema.Property("payoff_points", schema.Array("Tùy chọn: các điểm cốt truyện hoặc điểm trả lời mà chương then chốt muốn hồi đáp", schema.String(""))),
		schema.Property("hook_goal", schema.String("Tùy chọn: ham muốn đọc tiếp hoặc mục tiêu gây căng thẳng mà cuối chương muốn tạo ra")),
	)
}

func (t *PlanChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	plan, err := decodeChapterPlanArgs(args)
	if err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if plan.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	completed, err := t.store.Progress.IsChapterCompleted(plan.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if completed {
		return json.Marshal(map[string]any{
			"chapter":   plan.Chapter,
			"skipped":   true,
			"completed": true,
			"reason":    fmt.Sprintf("Chương %d đã được lưu hoàn thành, không thể lập kế hoạch lại", plan.Chapter),
		})
	}
	if err := t.store.Progress.ValidateChapterWork(plan.Chapter); err != nil {
		return nil, err
	}
	if err := EnsureChapterExpanded(t.store, plan.Chapter); err != nil {
		return nil, err
	}

	if err := t.store.Drafts.SaveChapterPlan(plan); err != nil {
		return nil, fmt.Errorf("save chapter plan: %w", err)
	}
	if err := t.store.Progress.StartChapter(plan.Chapter); err != nil {
		return nil, fmt.Errorf("mark chapter in progress: %w", err)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(plan.Chapter), "plan",
		fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint chapter plan: %w", err)
	}

	return json.Marshal(map[string]any{
		"planned":   true,
		"chapter":   plan.Chapter,
		"next_step": "Ngay lập tức gọi draft_chapter(chapter=số_chương_này, content=chuỗi_nội_dung_đầy_đủ) để viết nội dung, không lập kế hoạch lại cùng một chương",
	})
}

func decodeChapterPlanArgs(args json.RawMessage) (domain.ChapterPlan, error) {
	var a struct {
		Chapter          int      `json:"chapter"`
		Title            string   `json:"title"`
		Goal             string   `json:"goal"`
		Conflict         string   `json:"conflict"`
		Hook             string   `json:"hook"`
		EmotionArc       string   `json:"emotion_arc"`
		Notes            string   `json:"notes"`
		RequiredBeats    []string `json:"required_beats"`
		ForbiddenMoves   []string `json:"forbidden_moves"`
		ContinuityChecks []string `json:"continuity_checks"`
		EvaluationFocus  []string `json:"evaluation_focus"`
		EmotionTarget    string   `json:"emotion_target"`
		PayoffPoints     []string `json:"payoff_points"`
		HookGoal         string   `json:"hook_goal"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return domain.ChapterPlan{}, err
	}

	return domain.ChapterPlan{
		Chapter:    a.Chapter,
		Title:      a.Title,
		Goal:       a.Goal,
		Conflict:   a.Conflict,
		Hook:       a.Hook,
		EmotionArc: a.EmotionArc,
		Notes:      a.Notes,
		Contract: domain.ChapterContract{
			RequiredBeats:    a.RequiredBeats,
			ForbiddenMoves:   a.ForbiddenMoves,
			ContinuityChecks: a.ContinuityChecks,
			EvaluationFocus:  a.EvaluationFocus,
			EmotionTarget:    a.EmotionTarget,
			PayoffPoints:     a.PayoffPoints,
			HookGoal:         a.HookGoal,
		},
	}, nil
}
