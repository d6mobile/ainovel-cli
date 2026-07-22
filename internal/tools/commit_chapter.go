package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/rules"
	"github.com/voocel/ainovel-cli/internal/store"
)

type CommitChapterTool struct {
	store *store.Store
}

func NewCommitChapterTool(store *store.Store) *CommitChapterTool {
	return &CommitChapterTool{store: store}
}

type commitOutput struct {
	domain.CommitResult
	RuleViolations []rules.Violation `json:"rule_violations,omitempty"`
}

// commitArgs 是提交 Saga 的规范化结构化载荷。首次执行把它与正文快照一起写入
// PendingCommit；崩溃恢复一律重放这份冻结意图，忽略新 Worker 生成的参数和草稿。
type commitArgs struct {
	Chapter             int                        `json:"chapter"`
	Summary             string                     `json:"summary"`
	Characters          []string                   `json:"characters"`
	KeyEvents           []string                   `json:"key_events"`
	TimelineEvents      []domain.TimelineEvent     `json:"timeline_events"`
	ForeshadowUpdates   []domain.ForeshadowUpdate  `json:"foreshadow_updates"`
	RelationshipChanges []domain.RelationshipEntry `json:"relationship_changes"`
	StateChanges        []domain.StateChange       `json:"state_changes"`
	CastIntros          []domain.CastIntro         `json:"cast_intros"`
	HookType            string                     `json:"hook_type"`
	DominantStrand      string                     `json:"dominant_strand"`
	Feedback            *domain.OutlineFeedback    `json:"feedback"`
}

func (t *CommitChapterTool) Name() string { return "commit_chapter" }
func (t *CommitChapterTool) Description() string {
	return "Lưu bản thảo cuối của chương. Công cụ đọc bản nháp, lưu thành bản chính thức, cập nhật dòng thời gian, phục bút, quan hệ, trạng thái nhân vật/thực thể và tiến độ. " +
		"Trả về dữ liệu có cấu trúc: next_chapter / review_required / arc_end / volume_end / needs_expansion / book_complete / flow."
}
func (t *CommitChapterTool) Label() string { return "Lưu chương" }

func (t *CommitChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *CommitChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *CommitChapterTool) Schema() map[string]any {
	timelineSchema := schema.Object(
		schema.Property("time", schema.String("Thời điểm trong truyện")).Required(),
		schema.Property("event", schema.String("Mô tả sự kiện")).Required(),
		schema.Property("characters", schema.Array("Nhân vật liên quan", schema.String(""))),
	)
	foreshadowSchema := schema.Object(
		schema.Property("id", schema.String("ID phục bút ổn định")).Required(),
		schema.Property("action", schema.Enum("Thao tác", "plant", "advance", "resolve")).Required(),
		schema.Property("description", schema.String("Mô tả phục bút; bắt buộc về mặt nội dung khi action=plant")),
	)
	relationshipSchema := schema.Object(
		schema.Property("character_a", schema.String("Nhân vật A")).Required(),
		schema.Property("character_b", schema.String("Nhân vật B")).Required(),
		schema.Property("relation", schema.String("Mô tả quan hệ hiện tại")).Required(),
	)
	stateChangeSchema := schema.Object(
		schema.Property("entity", schema.String("Tên nhân vật hoặc thực thể")).Required(),
		schema.Property("field", schema.String("Thuộc tính thay đổi")).Required(),
		schema.Property("old_value", schema.String("Giá trị trước thay đổi")),
		schema.Property("new_value", schema.String("Giá trị sau thay đổi")).Required(),
		schema.Property("reason", schema.String("Nguyên nhân thay đổi")),
	)
	castIntroSchema := schema.Object(
		schema.Property("name", schema.String("Tên nhân vật")).Required(),
		schema.Property("brief_role", schema.String("Định vị một câu, ví dụ: chủ quán trọ / tay đánh bạc")).Required(),
	)
	feedbackSchema := schema.Object(
		schema.Property("deviation", schema.String("Mô tả chỗ lệch khỏi dàn ý")).Required(),
		schema.Property("suggestion", schema.String("Đề xuất điều chỉnh dàn ý tiếp theo")).Required(),
	)
	for _, s := range []map[string]any{timelineSchema, foreshadowSchema, relationshipSchema, stateChangeSchema, castIntroSchema, feedbackSchema} {
		s["additionalProperties"] = false
	}
	feedbackSchema["description"] = "Đối tượng góp ý cho dàn ý tiếp theo; truyền JSON object thật, không truyền chuỗi JSON."
	root := schema.Object(
		schema.Property("chapter", schema.Int("Số chương")).Required(),
		schema.Property("summary", schema.String("Tóm tắt chương, tối đa khoảng 200 từ")).Required(),
		schema.Property("characters", schema.Array("Tên chính thức các nhân vật xuất hiện trong chương", schema.String(""))).Required(),
		schema.Property("key_events", schema.Array("Các sự kiện quan trọng của chương", schema.String(""))).Required(),
		schema.Property("timeline_events", schema.Array("Các sự kiện trên dòng thời gian; phải là JSON array thật, không stringify", timelineSchema)),
		schema.Property("foreshadow_updates", schema.Array("Thao tác phục bút; phải là JSON array thật, không stringify", foreshadowSchema)),
		schema.Property("relationship_changes", schema.Array("Thay đổi quan hệ; phải là JSON array thật, không stringify", relationshipSchema)),
		schema.Property("state_changes", schema.Array("Thay đổi trạng thái nhân vật hoặc thực thể; phải là JSON array thật, không stringify", stateChangeSchema)),
		schema.Property("cast_intros", schema.Array("Nhân vật phụ lần đầu xuất hiện và có thể tái xuất; phải là JSON array thật", castIntroSchema)),
		schema.Property("hook_type", schema.Enum("Loại móc cuối chương", "crisis", "mystery", "desire", "emotion", "choice")),
		schema.Property("dominant_strand", schema.Enum("Tuyến tự sự chủ đạo", "quest", "fire", "constellation")),
		schema.Property("feedback", feedbackSchema),
	)
	root["additionalProperties"] = false
	return root
}

type commitChapterArgs struct {
	Chapter             int                        `json:"chapter"`
	Summary             string                     `json:"summary"`
	Characters          []string                   `json:"characters"`
	KeyEvents           []string                   `json:"key_events"`
	TimelineEvents      []domain.TimelineEvent     `json:"timeline_events"`
	ForeshadowUpdates   []domain.ForeshadowUpdate  `json:"foreshadow_updates"`
	RelationshipChanges []domain.RelationshipEntry `json:"relationship_changes"`
	StateChanges        []domain.StateChange       `json:"state_changes"`
	CastIntros          []domain.CastIntro         `json:"cast_intros"`
	HookType            string                     `json:"hook_type"`
	DominantStrand      string                     `json:"dominant_strand"`
	Feedback            *domain.OutlineFeedback    `json:"feedback"`
}

func decodeCommitChapterArgs(args json.RawMessage) (commitChapterArgs, error) {
	var out commitChapterArgs
	raw, err := normalizeRootObject(args)
	if err != nil {
		return out, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return out, err
	}
	allowed := map[string]bool{
		"chapter": true, "summary": true, "characters": true, "key_events": true,
		"timeline_events": true, "foreshadow_updates": true, "relationship_changes": true,
		"state_changes": true, "cast_intros": true, "hook_type": true, "dominant_strand": true,
		"feedback": true,
	}
	for k := range fields {
		if !allowed[k] {
			return out, fmt.Errorf("trường không thuộc schema: %s", k)
		}
	}
	for _, k := range []string{"chapter", "summary", "characters", "key_events"} {
		if _, ok := fields[k]; !ok {
			return out, fmt.Errorf("thiếu trường bắt buộc: %s", k)
		}
	}
	if err := json.Unmarshal(fields["chapter"], &out.Chapter); err != nil {
		return out, fmt.Errorf("chapter phải là số nguyên: %w", err)
	}
	if out.Chapter <= 0 {
		return out, fmt.Errorf("chapter phải lớn hơn 0")
	}
	if err := json.Unmarshal(fields["summary"], &out.Summary); err != nil || strings.TrimSpace(out.Summary) == "" {
		return out, fmt.Errorf("summary phải là chuỗi không rỗng")
	}
	if err := decodeStringArray(fields["characters"], &out.Characters); err != nil || len(out.Characters) == 0 {
		return out, fmt.Errorf("characters phải là mảng chuỗi không rỗng")
	}
	if err := decodeStringArray(fields["key_events"], &out.KeyEvents); err != nil || len(out.KeyEvents) == 0 {
		return out, fmt.Errorf("key_events phải là mảng chuỗi không rỗng")
	}
	if rawField, ok := fields["timeline_events"]; ok {
		r, err := normalizeJSONStringRaw(rawField)
		if err != nil {
			return out, fmt.Errorf("timeline_events: %w", err)
		}
		if err := validateObjectArray(r, "timeline_events", []string{"chapter", "time", "event", "characters"}, []string{"time", "event"}); err != nil {
			return out, err
		}
		if err := json.Unmarshal(r, &out.TimelineEvents); err != nil {
			return out, fmt.Errorf("timeline_events: %w", err)
		}
	}
	if rawField, ok := fields["foreshadow_updates"]; ok {
		r, err := normalizeJSONStringRaw(rawField)
		if err != nil {
			return out, fmt.Errorf("foreshadow_updates: %w", err)
		}
		if err := validateObjectArray(r, "foreshadow_updates", []string{"id", "action", "description"}, []string{"id", "action"}); err != nil {
			return out, err
		}
		if err := json.Unmarshal(r, &out.ForeshadowUpdates); err != nil {
			return out, fmt.Errorf("foreshadow_updates: %w", err)
		}
		for i, f := range out.ForeshadowUpdates {
			if f.Action != "plant" && f.Action != "advance" && f.Action != "resolve" {
				return out, fmt.Errorf("foreshadow_updates[%d].action không hợp lệ: %q", i, f.Action)
			}
		}
	}
	if rawField, ok := fields["relationship_changes"]; ok {
		r, err := normalizeJSONStringRaw(rawField)
		if err != nil {
			return out, fmt.Errorf("relationship_changes: %w", err)
		}
		if err := validateObjectArray(r, "relationship_changes", []string{"chapter", "character_a", "character_b", "relation"}, []string{"character_a", "character_b", "relation"}); err != nil {
			return out, err
		}
		if err := json.Unmarshal(r, &out.RelationshipChanges); err != nil {
			return out, fmt.Errorf("relationship_changes: %w", err)
		}
	}
	if rawField, ok := fields["state_changes"]; ok {
		r, err := normalizeJSONStringRaw(rawField)
		if err != nil {
			return out, fmt.Errorf("state_changes: %w", err)
		}
		if err := validateObjectArray(r, "state_changes", []string{"chapter", "entity", "field", "old_value", "new_value", "reason"}, []string{"entity", "field", "new_value"}); err != nil {
			return out, err
		}
		if err := json.Unmarshal(r, &out.StateChanges); err != nil {
			return out, fmt.Errorf("state_changes: %w", err)
		}
	}
	if rawField, ok := fields["cast_intros"]; ok {
		r, err := normalizeJSONStringRaw(rawField)
		if err != nil {
			return out, fmt.Errorf("cast_intros: %w", err)
		}
		if err := validateObjectArray(r, "cast_intros", []string{"name", "brief_role"}, []string{"name", "brief_role"}); err != nil {
			return out, err
		}
		if err := json.Unmarshal(r, &out.CastIntros); err != nil {
			return out, fmt.Errorf("cast_intros: %w", err)
		}
	}
	if rawField, ok := fields["hook_type"]; ok {
		if err := json.Unmarshal(rawField, &out.HookType); err != nil {
			return out, fmt.Errorf("hook_type phải là chuỗi: %w", err)
		}
		if out.HookType != "" && out.HookType != "crisis" && out.HookType != "mystery" && out.HookType != "desire" && out.HookType != "emotion" && out.HookType != "choice" {
			return out, fmt.Errorf("hook_type không hợp lệ: %q", out.HookType)
		}
	}
	if rawField, ok := fields["dominant_strand"]; ok {
		if err := json.Unmarshal(rawField, &out.DominantStrand); err != nil {
			return out, fmt.Errorf("dominant_strand phải là chuỗi: %w", err)
		}
		if out.DominantStrand != "" && out.DominantStrand != "quest" && out.DominantStrand != "fire" && out.DominantStrand != "constellation" {
			return out, fmt.Errorf("dominant_strand không hợp lệ: %q", out.DominantStrand)
		}
	}
	if rawField, ok := fields["feedback"]; ok {
		r, err := normalizeJSONStringRaw(rawField)
		if err != nil {
			return out, fmt.Errorf("feedback: %w", err)
		}
		if err := validateObject(r, "feedback", []string{"deviation", "suggestion"}, []string{"deviation", "suggestion"}); err != nil {
			return out, err
		}
		if err := json.Unmarshal(r, &out.Feedback); err != nil {
			return out, fmt.Errorf("feedback: %w", err)
		}
	}
	return out, nil
}

func normalizeRootObject(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("tham số rỗng")
	}
	if trimmed[0] == '{' {
		return trimmed, nil
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return nil, err
	}
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "{") {
		return json.RawMessage(s), nil
	}
	obj := extractFirstJSONObject(s)
	if obj == "" {
		return nil, fmt.Errorf("không tìm thấy JSON object")
	}
	return json.RawMessage(obj), nil
}

func normalizeJSONStringRaw(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("giá trị rỗng")
	}
	if trimmed[0] != '"' {
		return trimmed, nil
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return nil, err
	}
	s = strings.TrimSpace(s)
	if !json.Valid([]byte(s)) {
		return nil, fmt.Errorf("chuỗi không chứa JSON hợp lệ")
	}
	return json.RawMessage(s), nil
}

func decodeStringArray(raw json.RawMessage, out *[]string) error {
	r, err := normalizeJSONStringRaw(raw)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(r)) == 0 || bytes.TrimSpace(r)[0] != '[' {
		return fmt.Errorf("cần một mảng")
	}
	return json.Unmarshal(r, out)
}

func validateObjectArray(raw json.RawMessage, field string, allowed, required []string) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '[' {
		return fmt.Errorf("%s phải là JSON array", field)
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	for i, item := range items {
		if err := validateObject(item, fmt.Sprintf("%s[%d]", field, i), allowed, required); err != nil {
			return err
		}
	}
	return nil
}

func validateObject(raw json.RawMessage, path string, allowed, required []string) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return fmt.Errorf("%s phải là JSON object", path)
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	allowedSet := map[string]bool{}
	for _, k := range allowed {
		allowedSet[k] = true
	}
	for k := range fields {
		if !allowedSet[k] {
			return fmt.Errorf("%s.%s không thuộc schema", path, k)
		}
	}
	for _, k := range required {
		rawValue, ok := fields[k]
		if !ok {
			return fmt.Errorf("thiếu trường bắt buộc: %s.%s", path, k)
		}
		var s string
		if err := json.Unmarshal(rawValue, &s); err != nil || strings.TrimSpace(s) == "" {
			return fmt.Errorf("%s.%s phải là chuỗi không rỗng", path, k)
		}
	}
	return nil
}

func extractFirstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			switch c {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func (t *CommitChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	a, err := decodeCommitChapterArgs(args)
	if err != nil {
		return nil, fmt.Errorf("tham số không hợp lệ: %w: %w", errs.ErrToolArgs, err)
	}
	existingPending, err := t.store.Signals.LoadPendingCommit()
	if err != nil {
		return nil, fmt.Errorf("tải lượt lưu chương đang chờ: %w: %w", errs.ErrStoreRead, err)
	}
	if existingPending != nil && existingPending.Chapter != a.Chapter {
		return nil, fmt.Errorf("Có lượt lưu chương chưa khôi phục: chương %d (giai đoạn %s), hãy khôi phục hoặc lưu lại chương đó trước: %w", existingPending.Chapter, existingPending.Stage, errs.ErrToolConflict)
	}

	progress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("tải progress: %w: %w", errs.ErrStoreRead, err)
	}
	if existingPending != nil && (existingPending.Stage == domain.CommitStageProgressMarked || existingPending.Stage == domain.CommitStageSignalSaved) {
		return t.finishPendingCommit(*existingPending, progress)
	}
	if existingPending != nil && len(existingPending.Payload) > 0 {
		if err := json.Unmarshal(existingPending.Payload, &a); err != nil {
			return nil, fmt.Errorf("giải mã payload commit đang chờ: %w: %w", errs.ErrStoreRead, err)
		}
		if a.Chapter != existingPending.Chapter {
			return nil, fmt.Errorf("payload commit đang chờ lệch chương: record=%d payload=%d: %w", existingPending.Chapter, a.Chapter, errs.ErrToolConflict)
		}
	}

	if t.store.Progress.IsChapterCompleted(a.Chapter) {
		if progress != nil && slices.Contains(progress.PendingRewrites, a.Chapter) {
			return t.executeRewriteCommit(a.Chapter, a.Summary, a.Characters, a.KeyEvents,
				a.HookType, a.DominantStrand, progress)
		}
		return t.buildSkipResult(a.Chapter, progress)
	}
	if err := t.store.Progress.ValidateChapterWork(a.Chapter); err != nil {
		if errors.Is(err, errs.ErrToolConflict) {
			return nil, err
		}
		return nil, fmt.Errorf("Chương hiện không được phép lưu: %w: %w", errs.ErrToolPrecondition, err)
	}

	if progress == nil {
		return nil, fmt.Errorf("progress chưa được khởi tạo: %w", errs.ErrToolPrecondition)
	}

	var boundary *store.ArcBoundary
	if progress.Layered {
		b, bErr := t.store.Outline.CheckArcBoundary(a.Chapter)
		if bErr != nil {
			return nil, fmt.Errorf("Kiểm tra ranh giới cung thất bại chapter=%d: %w: %w", a.Chapter, errs.ErrStoreRead, bErr)
		}
		if b == nil {
			return nil, fmt.Errorf(
				"Chương %d không nằm trong phạm vi dàn ý phân tầng: cần gọi expand_arc để mở rộng cung hoặc append_volume để thêm tập trước; nếu sách đã hoàn tất hãy gọi save_foundation type=complete_book: %w",
				a.Chapter, errs.ErrToolPrecondition)
		}
		boundary = b
	}

	var content string
	var wordCount int
	if existingPending != nil {
		content = existingPending.DraftContent
		wordCount = len([]rune(content))
	} else {
		content, wordCount, err = t.store.Drafts.LoadChapterContent(a.Chapter)
		if err != nil {
			return nil, fmt.Errorf("tải nội dung chương: %w: %w", errs.ErrStoreRead, err)
		}
	}
	if content == "" {
		return nil, fmt.Errorf("không tìm thấy nội dung cho chương %d: %w", a.Chapter, errs.ErrToolPrecondition)
	}

	var pending domain.PendingCommit
	if existingPending != nil {
		pending = *existingPending
	} else {
		payload, err := json.Marshal(a)
		if err != nil {
			return nil, fmt.Errorf("mã hóa payload commit: %w", err)
		}
		now := time.Now().Format(time.RFC3339)
		pending = domain.PendingCommit{
			Chapter: a.Chapter, Stage: domain.CommitStageStarted, Payload: payload, DraftContent: content,
			Summary: a.Summary, HookType: a.HookType, DominantStrand: a.DominantStrand,
			StartedAt: now, UpdatedAt: now,
		}
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("lưu lượt commit đang chờ: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	if err := t.store.Drafts.SaveFinalChapter(a.Chapter, content); err != nil {
		return nil, fmt.Errorf("lưu bản hoàn chỉnh của chương: %w: %w", errs.ErrStoreWrite, err)
	}

	summary := domain.ChapterSummary{
		Chapter:    a.Chapter,
		Summary:    a.Summary,
		Characters: a.Characters,
		KeyEvents:  a.KeyEvents,
	}
	if err := t.store.Summaries.SaveSummary(summary); err != nil {
		return nil, fmt.Errorf("lưu tóm tắt: %w: %w", errs.ErrStoreWrite, err)
	}

	if len(a.TimelineEvents) > 0 {
		for i := range a.TimelineEvents {
			a.TimelineEvents[i].Chapter = a.Chapter
		}
		if err := t.store.World.AppendTimelineEvents(a.TimelineEvents); err != nil {
			return nil, fmt.Errorf("ghi thêm timeline: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.ForeshadowUpdates) > 0 {
		if err := t.store.World.UpdateForeshadow(a.Chapter, a.ForeshadowUpdates); err != nil {
			return nil, fmt.Errorf("cập nhật foreshadow: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.RelationshipChanges) > 0 {
		for i := range a.RelationshipChanges {
			a.RelationshipChanges[i].Chapter = a.Chapter
		}
		if err := t.store.World.UpdateRelationships(a.RelationshipChanges); err != nil {
			return nil, fmt.Errorf("cập nhật quan hệ: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if len(a.StateChanges) > 0 {
		for i := range a.StateChanges {
			a.StateChanges[i].Chapter = a.Chapter
		}
		if err := t.store.World.AppendStateChanges(a.StateChanges); err != nil {
			return nil, fmt.Errorf("ghi thêm biến động trạng thái: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	if len(a.Characters) > 0 {
		coreNames := loadCoreCharacterNameSet(t.store)
		if err := t.store.Cast.MergeAppearances(a.Chapter, a.Characters, a.CastIntros, coreNames); err != nil {
			slog.Warn("Cộng dồn sổ nhân vật phụ thất bại, bỏ qua", "module", "commit", "chapter", a.Chapter, "err", err)
		}
	}

	pending.Stage = domain.CommitStageStateApplied
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("cập nhật giai đoạn commit đang chờ: %w: %w", errs.ErrStoreWrite, err)
	}

	if err := t.store.Progress.MarkChapterComplete(a.Chapter, wordCount, a.HookType, a.DominantStrand); err != nil {
		return nil, fmt.Errorf("đánh dấu chương hoàn thành: %w: %w", errs.ErrStoreWrite, err)
	}

	progress, err = t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("tải progress: %w: %w", errs.ErrStoreRead, err)
	}
	completedCount := 0
	if progress != nil {
		completedCount = len(progress.CompletedChapters)
	}

	var arcEnd, volumeEnd, needsExpansion, needsNewVolume bool
	var vol, arc, nextVol, nextArc int
	if progress != nil && progress.Layered && boundary != nil {
		arcEnd = boundary.IsArcEnd
		volumeEnd = boundary.IsVolumeEnd
		vol = boundary.Volume
		arc = boundary.Arc
		needsExpansion = boundary.NeedsExpansion
		needsNewVolume = boundary.NeedsNewVolume
		nextVol = boundary.NextVolume
		nextArc = boundary.NextArc
		if err := t.store.Progress.UpdateVolumeArc(vol, arc); err != nil {
			return nil, fmt.Errorf("update volume/arc: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	var reviewRequired bool
	var reviewReason string
	if progress != nil && progress.Layered {
		reviewRequired, reviewReason = domain.ShouldArcReview(arcEnd, volumeEnd, vol, arc)
	} else {
		reviewRequired, reviewReason = domain.ShouldReview(completedCount)
	}

	result := domain.CommitResult{
		Chapter:        a.Chapter,
		Committed:      true,
		WordCount:      wordCount,
		NextChapter:    a.Chapter + 1,
		ReviewRequired: reviewRequired,
		ReviewReason:   reviewReason,
		HookType:       a.HookType,
		DominantStrand: a.DominantStrand,
		Feedback:       a.Feedback,
		ArcEnd:         arcEnd,
		VolumeEnd:      volumeEnd,
		Volume:         vol,
		Arc:            arc,
		NeedsExpansion: needsExpansion,
		NeedsNewVolume: needsNewVolume,
		NextVolume:     nextVol,
		NextArc:        nextArc,
	}

	bookComplete, err := t.applyCompletion(&result, progress)
	if err != nil {
		return nil, err
	}
	if bookComplete {
		result.BookComplete = true
	}
	latestProgress, err := t.store.Progress.Load()
	if err != nil {
		return nil, fmt.Errorf("load progress after completion: %w: %w", errs.ErrStoreRead, err)
	}
	if latestProgress != nil {
		result.Flow = string(latestProgress.Flow)
	}

	layered := progress != nil && progress.Layered
	if layered && a.Feedback != nil && (strings.TrimSpace(a.Feedback.Deviation) != "" || strings.TrimSpace(a.Feedback.Suggestion) != "") {
		if err := t.store.Outline.AppendOutlineFeedback(store.ChapterFeedback{
			Chapter: a.Chapter, Deviation: a.Feedback.Deviation, Suggestion: a.Feedback.Suggestion,
		}); err != nil {
			slog.Warn("Lưu phản hồi dàn ý thất bại", "module", "tools", "chapter", a.Chapter, "err", err)
		}
	}

	// 机械规则是输出的一部分，必须在 ProgressMarked 前固化，恢复时直接返回同一输出。
	violations := t.checkRules(content)
	output, err := json.Marshal(commitOutput{CommitResult: result, RuleViolations: violations})
	if err != nil {
		return nil, fmt.Errorf("marshal commit output: %w", err)
	}

	pending.Stage = domain.CommitStageProgressMarked
	pending.Result = &result
	pending.Output = output
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("cập nhật kết quả commit đang chờ: %w: %w", errs.ErrStoreWrite, err)
	}

	if err := t.appendCommitCheckpoint(a.Chapter); err != nil {
		return nil, fmt.Errorf("ghi checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}
	pending.Stage = domain.CommitStageSignalSaved
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("update pending commit checkpoint stage: %w: %w", errs.ErrStoreWrite, err)
	}

	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("xóa trạng thái đang viết: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("xóa commit đang chờ: %w: %w", errs.ErrStoreWrite, err)
	}

	if err := t.store.World.SaveRuleViolations(a.Chapter, violations); err != nil {
		slog.Warn("Lưu vi phạm quy tắc cơ học thất bại", "module", "tools", "chapter", a.Chapter, "err", err)
	}
	return output, nil
}

// finishPendingCommit 收尾 ProgressMarked/SignalSaved 中断窗口。Checkpoint 追加按
// digest 幂等；只有 checkpoint 与中间态清理都成功后才删除恢复记录。
func (t *CommitChapterTool) finishPendingCommit(pending domain.PendingCommit, progress *domain.Progress) (json.RawMessage, error) {
	if pending.Stage == domain.CommitStageProgressMarked {
		if err := t.appendCommitCheckpoint(pending.Chapter); err != nil {
			return nil, fmt.Errorf("checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
		}
		pending.Stage = domain.CommitStageSignalSaved
		pending.UpdatedAt = time.Now().Format(time.RFC3339)
		if err := t.store.Signals.SavePendingCommit(pending); err != nil {
			return nil, fmt.Errorf("update pending commit checkpoint stage: %w: %w", errs.ErrStoreWrite, err)
		}
	}
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}
	if len(pending.Output) > 0 {
		return append(json.RawMessage(nil), pending.Output...), nil
	}
	if pending.Result != nil {
		return json.Marshal(pending.Result)
	}
	return t.buildSkipResult(pending.Chapter, progress)
}

func (t *CommitChapterTool) validateRewriteDraft(chapter int, progress *domain.Progress) (string, error) {
	content, _, err := t.store.Drafts.LoadChapterContent(chapter)
	if err != nil {
		return "", fmt.Errorf("rewrite: load chapter content: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return "", fmt.Errorf("no content found for chapter %d: %w", chapter, errs.ErrToolPrecondition)
	}
	existingFinal, err := t.store.Drafts.LoadChapterText(chapter)
	if err != nil {
		return "", fmt.Errorf("rewrite: load final chapter: %w: %w", errs.ErrStoreRead, err)
	}
	if existingFinal == "" || existingFinal != content {
		return content, nil
	}
	mode := "sửa lại"
	if progress != nil && progress.Flow == domain.FlowPolishing {
		mode = "trau chuốt"
	}
	return "", fmt.Errorf("Nội dung drafts và chapters của chương %d hoàn toàn giống nhau, chưa phát hiện thay đổi sau khi %s. Hãy gọi draft_chapter(mode=write, chapter=%d) để ghi bản mới sau khi %s, rồi mới commit_chapter: %w",
		chapter, mode, chapter, mode, errs.ErrToolPrecondition)
}

func (t *CommitChapterTool) appendCommitCheckpoint(chapter int) error {
	_, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(chapter), "commit",
		fmt.Sprintf("chapters/%02d.md", chapter),
	)
	return err
}

func (t *CommitChapterTool) checkRules(text string) []rules.Violation {
	violations := rules.Lint(text)
	structured := rules.SystemDefaults().Structured
	if snap, err := t.store.UserRules.Load(); err == nil && snap != nil {
		structured = snap.Structured
	}
	return append(violations, rules.Check(text, structured)...)
}

func (t *CommitChapterTool) executeRewriteCommit(
	chapter int,
	summary string,
	characters, keyEvents []string,
	hookType, dominantStrand string,
	progress *domain.Progress,
) (json.RawMessage, error) {
	content, wordCount, err := t.store.Drafts.LoadChapterContent(chapter)
	if err != nil {
		return nil, fmt.Errorf("rewrite: tải nội dung chương: %w: %w", errs.ErrStoreRead, err)
	}
	if content == "" {
		return nil, fmt.Errorf("không tìm thấy nội dung cho chương %d: %w", chapter, errs.ErrToolPrecondition)
	}

	now := time.Now().Format(time.RFC3339)
	pending := domain.PendingCommit{
		Chapter:        chapter,
		Stage:          domain.CommitStageStarted,
		Rewrite:        true,
		RewriteMode:    "rewrite",
		DraftContent:   content,
		Summary:        summary,
		HookType:       hookType,
		DominantStrand: dominantStrand,
		StartedAt:      now,
		UpdatedAt:      now,
	}
	if progress != nil && progress.Flow == domain.FlowPolishing {
		pending.RewriteMode = "polish"
	}
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("rewrite: lưu lượt commit đang chờ: %w: %w", errs.ErrStoreWrite, err)
	}

	existingFinal, _ := t.store.Drafts.LoadChapterText(chapter)
	if existingFinal != "" && existingFinal == content {
		mode := "viết lại"
		if progress != nil && progress.Flow == domain.FlowPolishing {
			mode = "chỉnh sửa"
		}
		return nil, fmt.Errorf("Nội dung drafts và chapters của chương %d hoàn toàn giống nhau, chưa phát hiện thay đổi %s. Hãy gọi draft_chapter(mode=write, chapter=%d) để ghi bản mới sau khi %s, rồi mới commit_chapter: %w",
			chapter, mode, chapter, mode, errs.ErrToolPrecondition)
	}

	if err := t.store.Drafts.SaveFinalChapter(chapter, content); err != nil {
		return nil, fmt.Errorf("rewrite: lưu bản hoàn chỉnh của chương: %w: %w", errs.ErrStoreWrite, err)
	}

	if err := t.store.Summaries.SaveSummary(domain.ChapterSummary{
		Chapter:    chapter,
		Summary:    summary,
		Characters: characters,
		KeyEvents:  keyEvents,
	}); err != nil {
		return nil, fmt.Errorf("rewrite: lưu tóm tắt: %w: %w", errs.ErrStoreWrite, err)
	}

	if err := t.store.Progress.MarkChapterComplete(chapter, wordCount, hookType, dominantStrand); err != nil {
		return nil, fmt.Errorf("rewrite: cập nhật số chữ: %w: %w", errs.ErrStoreWrite, err)
	}

	if err := t.store.Progress.CompleteRewrite(chapter); err != nil {
		return nil, fmt.Errorf("rewrite: hoàn tất lượt viết lại: %w: %w", errs.ErrStoreWrite, err)
	}

	// 6. Checkpoint
	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(chapter), "commit",
		fmt.Sprintf("chapters/%02d.md", chapter),
	); err != nil {
		return nil, fmt.Errorf("rewrite: ghi checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}

	mode := "rewrite"
	if progress.Flow == domain.FlowPolishing {
		mode = "polish"
	}
	latest, _ := t.store.Progress.Load()
	remaining := []int{}
	nextChapter := chapter + 1
	flow := string(domain.FlowWriting)
	if latest != nil {
		remaining = append(remaining, latest.PendingRewrites...)
		nextChapter = latest.NextChapter()
		flow = string(latest.Flow)
	}
	drained := len(remaining) == 0

	bookComplete := false
	if drained && latest != nil {
		reComplete := false
		switch {
		case latest.Layered && latest.ReopenedFromComplete:
			reComplete, err = layeredStructurallyComplete(t.store, latest)
		case latest.Layered:
			reComplete, err = layeredComplete(t.store, latest)
		default:
			reComplete = latest.TotalChapters > 0 && len(latest.CompletedChapters) >= latest.TotalChapters
		}
		if err != nil {
			return nil, fmt.Errorf("rewrite: đánh giá hoàn tất: %w: %w", errs.ErrStoreRead, err)
		}
		if reComplete {
			if err := t.store.Progress.MarkComplete(); err != nil {
				return nil, fmt.Errorf("rewrite: mark complete: %w: %w", errs.ErrStoreWrite, err)
			}
			bookComplete = true
			p, err := t.store.Progress.Load()
			if err != nil {
				return nil, fmt.Errorf("rewrite: reload completed progress: %w: %w", errs.ErrStoreRead, err)
			}
			if p != nil {
				flow = string(p.Flow)
			}
		}
	}

	violations := t.checkRules(content)
	output, err := json.Marshal(map[string]any{
		"chapter": chapter, "rewritten": true, "mode": mode, "word_count": wordCount,
		"remaining_queue": remaining, "queue_drained": drained, "next_chapter": nextChapter,
		"flow": flow, "book_complete": bookComplete, "rule_violations": violations,
	})
	if err != nil {
		return nil, fmt.Errorf("rewrite: marshal output: %w", err)
	}
	pending.Stage = domain.CommitStageProgressMarked
	pending.Output = output
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("rewrite: update pending progress stage: %w: %w", errs.ErrStoreWrite, err)
	}

	// 7. Checkpoint 后再标 signal_saved，最后清理 PendingCommit。
	if err := t.appendCommitCheckpoint(chapter); err != nil {
		return nil, fmt.Errorf("rewrite: checkpoint commit: %w: %w", errs.ErrStoreWrite, err)
	}
	pending.Stage = domain.CommitStageSignalSaved
	pending.UpdatedAt = time.Now().Format(time.RFC3339)
	if err := t.store.Signals.SavePendingCommit(pending); err != nil {
		return nil, fmt.Errorf("rewrite: update pending checkpoint stage: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Progress.ClearInProgress(); err != nil {
		return nil, fmt.Errorf("rewrite: clear in-progress: %w: %w", errs.ErrStoreWrite, err)
	}
	if err := t.store.Signals.ClearPendingCommit(); err != nil {
		return nil, fmt.Errorf("rewrite: clear pending commit: %w: %w", errs.ErrStoreWrite, err)
	}

	if err := t.store.World.SaveRuleViolations(chapter, violations); err != nil {
		slog.Warn("Lưu vi phạm quy tắc cơ học thất bại", "module", "tools", "chapter", chapter, "err", err)
	}
	return output, nil
}

func (t *CommitChapterTool) buildSkipResult(chapter int, progress *domain.Progress) (json.RawMessage, error) {
	_, wordCount, err := t.store.Drafts.LoadChapterContent(chapter)
	if err != nil {
		return nil, fmt.Errorf("load completed chapter: %w: %w", errs.ErrStoreRead, err)
	}

	result := domain.CommitResult{
		Chapter:     chapter,
		Committed:   true,
		WordCount:   wordCount,
		NextChapter: chapter + 1,
	}

	if progress != nil && progress.Layered {
		boundary, err := t.store.Outline.CheckArcBoundary(chapter)
		if err != nil {
			return nil, fmt.Errorf("check completed chapter boundary: %w: %w", errs.ErrStoreRead, err)
		}
		if boundary != nil {
			result.ArcEnd = boundary.IsArcEnd
			result.VolumeEnd = boundary.IsVolumeEnd
			result.Volume = boundary.Volume
			result.Arc = boundary.Arc
			result.NeedsExpansion = boundary.NeedsExpansion
			result.NeedsNewVolume = boundary.NeedsNewVolume
			result.NextVolume = boundary.NextVolume
			result.NextArc = boundary.NextArc
		}
		result.ReviewRequired, result.ReviewReason = domain.ShouldArcReview(result.ArcEnd, result.VolumeEnd, result.Volume, result.Arc)
	} else if progress != nil {
		result.ReviewRequired, result.ReviewReason = domain.ShouldReview(len(progress.CompletedChapters))
	}

	if progress != nil {
		if progress.Phase == domain.PhaseComplete {
			result.BookComplete = true
		}
		result.Flow = string(progress.Flow)
	}

	return json.Marshal(result)
}

func loadCoreCharacterNameSet(s *store.Store) map[string]bool {
	chars, err := s.Characters.Load()
	if err != nil || len(chars) == 0 {
		return nil
	}
	set := make(map[string]bool, len(chars)*2)
	for _, c := range chars {
		if c.Name != "" {
			set[c.Name] = true
		}
		for _, alias := range c.Aliases {
			if alias != "" {
				set[alias] = true
			}
		}
	}
	return set
}

func (t *CommitChapterTool) applyCompletion(result *domain.CommitResult, progress *domain.Progress) (bool, error) {
	if progress == nil {
		return false, nil
	}
	if progress.Phase == domain.PhaseComplete {
		return true, nil
	}
	if progress.Layered {
		complete, err := layeredComplete(t.store, progress)
		if err != nil {
			return false, fmt.Errorf("evaluate layered completion: %w: %w", errs.ErrStoreRead, err)
		}
		if complete {
			if err := t.store.Progress.MarkComplete(); err != nil {
				return false, fmt.Errorf("mark book complete: %w: %w", errs.ErrStoreWrite, err)
			}
			return true, nil
		}
		return false, nil
	}
	if progress.TotalChapters > 0 && result.NextChapter > progress.TotalChapters {
		if err := t.store.Progress.MarkComplete(); err != nil {
			return false, fmt.Errorf("mark book complete: %w: %w", errs.ErrStoreWrite, err)
		}
		return true, nil
	}
	return false, nil
}

// Các hàm hoàn tất phân tầng dùng chung cho commit_chapter và save_volume_summary.

func layeredStructurallyComplete(st *store.Store, progress *domain.Progress) (bool, error) {
	if len(progress.PendingRewrites) > 0 {
		return false, nil
	}
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return false, fmt.Errorf("tải dàn ý phân tầng: %w", err)
	}
	if len(volumes) == 0 {
		return false, nil
	}
	for i := range volumes {
		for j := range volumes[i].Arcs {
			if !volumes[i].Arcs[j].IsExpanded() {
				return false, nil
			}
		}
	}
	expanded := len(domain.FlattenOutline(volumes))
	return expanded > 0 && len(progress.CompletedChapters) >= expanded, nil
}

func finaleWrapped(st *store.Store, progress *domain.Progress) (bool, error) {
	last := progress.LatestCompleted()
	if last <= 0 {
		return false, nil
	}
	b, err := st.Outline.CheckArcBoundary(last)
	if err != nil {
		return false, fmt.Errorf("kiểm tra ranh giới hồi kết: %w", err)
	}
	if b == nil || !b.IsArcEnd {
		return false, nil
	}
	hasReview, err := st.World.HasArcReview(last)
	if err != nil {
		return false, fmt.Errorf("tải review hồi kết: %w", err)
	}
	hasArcSummary := st.Summaries.HasArcSummary(b.Volume, b.Arc)
	hasVolumeSummary := st.Summaries.HasVolumeSummary(b.Volume)
	return hasReview && hasArcSummary && hasVolumeSummary, nil
}

func layeredComplete(st *store.Store, progress *domain.Progress) (bool, error) {
	volumes, err := st.Outline.LoadLayeredOutline()
	if err != nil {
		return false, fmt.Errorf("tải dàn ý phân tầng: %w", err)
	}
	if domain.FinaleVolume(volumes) > 0 {
		structural, err := layeredStructurallyComplete(st, progress)
		if err != nil || !structural {
			return structural, err
		}
		return finaleWrapped(st, progress)
	}
	return layeredBookComplete(st, progress)
}

func layeredBookComplete(st *store.Store, progress *domain.Progress) (bool, error) {
	structural, err := layeredStructurallyComplete(st, progress)
	if err != nil || !structural {
		return structural, err
	}
	active, err := st.World.LoadActiveForeshadow()
	if err != nil || len(active) > 0 {
		return false, err
	}
	compass, err := st.Outline.LoadCompass()
	if err != nil || compass == nil || len(compass.OpenThreads) > 0 {
		return false, err
	}
	return true, nil
}
