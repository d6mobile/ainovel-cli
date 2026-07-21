package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

type SaveFoundationTool struct {
	store *store.Store
}

func NewSaveFoundationTool(store *store.Store) *SaveFoundationTool {
	return &SaveFoundationTool{store: store}
}

func (t *SaveFoundationTool) Name() string { return "save_foundation" }
func (t *SaveFoundationTool) Description() string {
	return "Lưu thiết lập nền tảng của tiểu thuyết (premise/outline/characters/world_rules/compass...). Đây là cửa lưu bền duy nhất: nội dung không đi qua tool này sẽ không vào store. Tham số cố định là {type, content, scale?, volume?, arc?}. type gồm premise / outline / layered_outline / characters / world_rules / expand_arc / append_volume / update_compass / complete_book. premise dùng Markdown; các loại khác ưu tiên mảng JSON hoặc đối tượng JSON. expand_arc chuẩn hóa và mở rộng một cung mẫu chưa viết (cần volume + arc, content là {title, goal, chapters}); append_volume thêm một cuốn mới (content là JSON đầy đủ của VolumeOutline, có cấu trúc cung; nếu ở cấp gốc có \"final\": true thì đánh dấu cuốn kết thúc và toàn bộ chương trong cuốn đó hoàn tất sẽ tự kết thúc truyện, không cần gọi complete_book); update_compass cập nhật hướng kết cục (content là JSON của StoryCompass); complete_book đánh dấu hoàn truyện (content truyền {}. Tool kiểm tra: toàn bộ chương trong dàn ý đã viết xong, không còn hàng đợi cần xử lý lại, compass không có open_threads chưa khép). append_volume / complete_book bắt buộc có reason (một câu giải thích theo bảng tiêu chí hoàn truyện). scale chỉ nhận short / mid / long."
}
func (t *SaveFoundationTool) Label() string { return "Lưu thiết lập" }

func (t *SaveFoundationTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveFoundationTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveFoundationTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("type", schema.Enum("Loại thiết lập", "premise", "outline", "layered_outline", "characters", "world_rules", "expand_arc", "append_volume", "update_compass", "complete_book")).Required(),
		schema.Property("content", map[string]any{
			"description": "Nội dung. premise truyền một chuỗi Markdown; các loại khác truyền trực tiếp mảng JSON hoặc đối tượng JSON, cũng có thể truyền chuỗi JSON. Khi expand_arc, truyền {title, goal, chapters}; title/goal là phương án cung đã được hiệu chỉnh theo các dữ kiện đã hoàn thành.",
		}).Required(),
		schema.Property("scale", schema.Enum("Cấp độ lập kế hoạch", "short", "mid", "long")),
		schema.Property("volume", schema.Int("Số thứ tự cuốn đích (chỉ bắt buộc khi expand_arc)")),
		schema.Property("arc", schema.Int("Số thứ tự cung đích (chỉ bắt buộc khi expand_arc)")),
		schema.Property("reason", schema.String("Lý do phán định ở cuối cuốn (bắt buộc khi append_volume / complete_book): dựa trên bảng tiêu chí hoàn truyện, viết một câu giải thích vì sao tiếp cuốn, công bố chốt hay hoàn truyện")),
	)
}

func (t *SaveFoundationTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
		Scale   string          `json:"scale"`
		Volume  int             `json:"volume"`
		Arc     int             `json:"arc"`
		Reason  string          `json:"reason"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("tham số không hợp lệ: %w: %w", errs.ErrToolArgs, err)
	}
	content, err := normalizeFoundationContent(a.Content)
	if err != nil {
		return nil, err
	}
	if a.Scale != "" {
		switch domain.PlanningTier(a.Scale) {
		case domain.PlanningTierShort, domain.PlanningTierMid, domain.PlanningTierLong:
		default:
			return nil, fmt.Errorf("cấp độ %q không hợp lệ, chỉ nhận short/mid/long: %w", a.Scale, errs.ErrToolArgs)
		}
		if err := t.store.RunMeta.SetPlanningTier(domain.PlanningTier(a.Scale)); err != nil {
			return nil, fmt.Errorf("lưu cấp độ lập kế hoạch: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	result := map[string]any{"saved": true, "type": a.Type, "scale": a.Scale}

	if (a.Type == "outline" || a.Type == "layered_outline") && t.isWriting() {
		return nil, fmt.Errorf(
			"Trong giai đoạn viết, không được dùng %s để ghi đè toàn bộ dàn ý. Hãy dùng expand_arc để mở rộng cung mẫu, hoặc append_volume để thêm cuốn mới: %w", a.Type, errs.ErrToolPrecondition)
	}
	if a.Scale != "" {
		if err := t.store.RunMeta.SetPlanningTier(domain.PlanningTier(a.Scale)); err != nil {
			return nil, fmt.Errorf("save planning tier: %w: %w", errs.ErrStoreWrite, err)
		}
	}

	volumeEnd := a.Type == "append_volume" || a.Type == "complete_book"
	if volumeEnd && strings.TrimSpace(a.Reason) == "" {
		return nil, fmt.Errorf("%s phải có tham số reason: dựa trên bảng tiêu chí hoàn truyện, viết một câu giải thích vì sao lần này tiếp cuốn, công bố chốt hay hoàn truyện: %w", a.Type, errs.ErrToolArgs)
	}
	var volumeEndFacts json.RawMessage
	if volumeEnd {
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress for volume-end facts: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil {
			volumeEndFacts, err = json.Marshal(map[string]any{
				"completed_chapters": len(p.CompletedChapters),
				"total_chapters":     p.TotalChapters,
			})
			if err != nil {
				return nil, fmt.Errorf("marshal volume-end facts: %w", err)
			}
		}
	}

	decode := func(typeName string, out any) error {
		return decodeFoundationJSON(typeName, content, out)
	}

	switch a.Type {
	case "premise":
		name := domain.ExtractNovelNameFromPremise(content)
		if err := t.store.Outline.SavePremise(content); err != nil {
			return nil, fmt.Errorf("lưu premise: %w: %w", errs.ErrStoreWrite, err)
		}
		if name != "" {
			if err := t.store.Progress.SetNovelName(name); err != nil {
				return nil, fmt.Errorf("save novel name: %w: %w", errs.ErrStoreWrite, err)
			}
			result["novel_name"] = name
		}
		if err := t.store.Progress.UpdatePhase(domain.PhasePremise); err != nil {
			return nil, fmt.Errorf("update premise phase: %w: %w", errs.ErrStoreWrite, err)
		}

	case "outline":
		var entries []domain.OutlineEntry
		if err := decode("outline", &entries); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveOutline(entries); err != nil {
			return nil, fmt.Errorf("lưu outline: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
			return nil, fmt.Errorf("update outline phase: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetTotalChapters(len(entries)); err != nil {
			return nil, fmt.Errorf("set total chapters: %w: %w", errs.ErrStoreWrite, err)
		}
		if domain.PlanningTier(a.Scale) != domain.PlanningTierLong {
			if err := t.store.Progress.SetLayered(false); err != nil {
				return nil, fmt.Errorf("disable layered mode: %w: %w", errs.ErrStoreWrite, err)
			}
			if err := t.store.Progress.UpdateVolumeArc(0, 0); err != nil {
				return nil, fmt.Errorf("reset volume/arc: %w: %w", errs.ErrStoreWrite, err)
			}
			if err := t.store.Outline.ClearLayeredOutline(); err != nil {
				return nil, fmt.Errorf("clear layered outline: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		result["chapters"] = len(entries)

	case "layered_outline":
		var volumes []domain.VolumeOutline
		if err := decode("layered_outline", &volumes); err != nil {
			return nil, err
		}
		if err := t.store.Outline.SaveLayeredOutline(volumes); err != nil {
			return nil, fmt.Errorf("lưu layered_outline: %w: %w", errs.ErrStoreWrite, err)
		}
		flat := domain.FlattenOutline(volumes)
		if err := t.store.Outline.SaveOutline(flat); err != nil {
			return nil, fmt.Errorf("lưu dàn ý đã làm phẳng: %w: %w", errs.ErrStoreWrite, err)
		}
		total := domain.TotalChapters(volumes)
		if err := t.store.Progress.UpdatePhase(domain.PhaseOutline); err != nil {
			return nil, fmt.Errorf("update outline phase: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetTotalChapters(total); err != nil {
			return nil, fmt.Errorf("set total chapters: %w: %w", errs.ErrStoreWrite, err)
		}
		if err := t.store.Progress.SetLayered(true); err != nil {
			return nil, fmt.Errorf("enable layered mode: %w: %w", errs.ErrStoreWrite, err)
		}
		if len(volumes) > 0 && len(volumes[0].Arcs) > 0 {
			if err := t.store.Progress.UpdateVolumeArc(volumes[0].Index, volumes[0].Arcs[0].Index); err != nil {
				return nil, fmt.Errorf("set initial volume/arc: %w: %w", errs.ErrStoreWrite, err)
			}
		}
		result["volumes"] = len(volumes)
		result["chapters"] = total

	case "characters":
		var chars []domain.Character
		if err := decode("characters", &chars); err != nil {
			return nil, err
		}
		if err := t.store.Characters.Save(chars); err != nil {
			return nil, fmt.Errorf("lưu characters: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(chars)

	case "world_rules":
		var rules []domain.WorldRule
		if err := decode("world_rules", &rules); err != nil {
			return nil, err
		}
		if err := t.store.World.SaveWorldRules(rules); err != nil {
			return nil, fmt.Errorf("lưu world_rules: %w: %w", errs.ErrStoreWrite, err)
		}
		result["count"] = len(rules)

	case "expand_arc":
		if a.Volume <= 0 || a.Arc <= 0 {
			return nil, fmt.Errorf("expand_arc cần tham số volume và arc: %w", errs.ErrToolArgs)
		}
		var expansion domain.ArcExpansion
		if err := decode("expand_arc", &expansion); err != nil {
			return nil, err
		}
		if err := t.store.ExpandArc(a.Volume, a.Arc, expansion); err != nil {
			return nil, fmt.Errorf("mở rộng cung: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = a.Volume
		result["arc"] = a.Arc
		result["title"] = expansion.Title
		result["goal"] = expansion.Goal
		result["chapters"] = len(expansion.Chapters)
		t.consumeWriterFeedback()

	case "append_volume":
		if p, _ := t.store.Progress.Load(); p != nil && p.Phase == domain.PhaseComplete {
			return nil, fmt.Errorf("toàn bộ truyện đã hoàn tất (phase=complete), không được thêm cuốn mới: %w", errs.ErrToolPrecondition)
		}
		var vol domain.VolumeOutline
		if err := decode("append_volume", &vol); err != nil {
			return nil, err
		}
		prior, err := t.store.Outline.LoadLayeredOutline()
		if err != nil {
			return nil, fmt.Errorf("load layered outline: %w: %w", errs.ErrStoreRead, err)
		}
		if err := t.store.AppendVolume(vol); err != nil {
			return nil, fmt.Errorf("thêm cuốn: %w: %w", errs.ErrStoreWrite, err)
		}
		result["volume"] = vol.Index
		if vol.Final {
			result["final_volume"] = true
		} else if domain.FinaleVolume(prior) > 0 {
			result["finale_released"] = true
		}
		result["arcs"] = len(vol.Arcs)
		chCount := 0
		for _, arc := range vol.Arcs {
			chCount += len(arc.Chapters)
		}
		if chCount > 0 {
			result["chapters"] = chCount
		}
		t.consumeWriterFeedback()

	case "complete_book":
		progress, perr := t.store.Progress.Load()
		if perr != nil {
			return nil, fmt.Errorf("tải progress: %w: %w", errs.ErrStoreRead, perr)
		}
		if progress == nil {
			return nil, fmt.Errorf("progress chưa được khởi tạo: %w", errs.ErrToolPrecondition)
		}
		if progress.Phase != domain.PhaseWriting {
			return nil, fmt.Errorf("complete_book chỉ được gọi trong giai đoạn writing (phase hiện tại=%s): %w", progress.Phase, errs.ErrToolPrecondition)
		}
		if len(progress.PendingRewrites) > 0 {
			return nil, fmt.Errorf("còn %d chương trong hàng đợi làm lại, hãy xử lý xong rồi mới gọi complete_book: %w", len(progress.PendingRewrites), errs.ErrToolPrecondition)
		}
		if len(progress.CompletedChapters) == 0 {
			return nil, fmt.Errorf("chưa viết chương nào thì không thể hoàn truyện; sau khi lập kế hoạch xong, hệ thống sẽ tự đẩy sang viết, không cần gọi complete_book: %w", errs.ErrToolPrecondition)
		}
		if next := progress.NextChapter(); progress.TotalChapters > 0 && next <= progress.TotalChapters {
			return nil, fmt.Errorf("Trong dàn ý vẫn còn chương chưa viết (chương tiếp theo %d/tổng %d), không thể hoàn truyện; nếu muốn chốt sớm hãy dùng append_volume và đặt JSON gốc của cuốn là \"final\": true để công bố cuốn chốt: %w", next, progress.TotalChapters, errs.ErrToolPrecondition)
		}
		if compass, _ := t.store.Outline.LoadCompass(); compass != nil && len(compass.OpenThreads) > 0 {
			return nil, fmt.Errorf("compass vẫn còn %d tuyến dài đang mở chưa khép (ví dụ: %s), không thể hoàn truyện. Nếu đã khép hết hãy update_compass để xóa open_threads rồi mới gọi complete_book; nếu vẫn cần mở rộng hãy dùng append_volume (có thể kèm \"final\": true để công bố cuốn chốt): %w",
				len(compass.OpenThreads), compass.OpenThreads[0], errs.ErrToolPrecondition)
		}
		if err := t.store.Progress.MarkComplete(); err != nil {
			return nil, fmt.Errorf("đánh dấu hoàn tất: %w: %w", errs.ErrStoreWrite, err)
		}
		result["book_complete"] = true
		result["phase"] = string(domain.PhaseComplete)

	case "update_compass":
		var compass domain.StoryCompass
		if err := decode("compass", &compass); err != nil {
			return nil, err
		}
		if p, _ := t.store.Progress.Load(); p != nil {
			compass.LastUpdated = p.LatestCompleted()
		}
		if err := t.store.Outline.SaveCompass(compass); err != nil {
			return nil, fmt.Errorf("lưu compass: %w: %w", errs.ErrStoreWrite, err)
		}
		result["ending_direction"] = compass.EndingDirection
		result["last_updated"] = compass.LastUpdated
		t.consumeWriterFeedback()

	default:
		return nil, fmt.Errorf("loại %q không hợp lệ, chỉ nhận premise/outline/layered_outline/characters/world_rules/expand_arc/append_volume/update_compass/complete_book: %w", a.Type, errs.ErrToolArgs)
	}

	// checkpoint
	scope := domain.GlobalScope()
	if a.Type == "expand_arc" {
		scope = domain.ArcScope(a.Volume, a.Arc)
	} else if a.Type == "append_volume" {
		scope = domain.GlobalScope()
	}
	if _, err := t.store.Checkpoints.AppendArtifact(scope, a.Type, foundationArtifact(a.Type)); err != nil {
		return nil, fmt.Errorf("ghi checkpoint thiết lập %s: %w: %w", a.Type, errs.ErrStoreWrite, err)
	}

	if volumeEnd {
		t.recordVolumeEndDecision(a.Type, a.Reason, volumeEndFacts, result)
	}

	remaining := t.store.FoundationMissing()
	ready := len(remaining) == 0
	result["remaining"] = remaining
	result["foundation_ready"] = ready
	if ready {
		p, err := t.store.Progress.Load()
		if err != nil {
			return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
		}
		if p != nil &&
			p.Phase != domain.PhaseWriting && p.Phase != domain.PhaseComplete {
			if err := t.store.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
				return nil, fmt.Errorf("update writing phase: %w: %w", errs.ErrStoreWrite, err)
			}
			result["phase"] = string(domain.PhaseWriting)
		}
	}
	return json.Marshal(result)
}

func foundationArtifact(t string) string {
	switch t {
	case "premise":
		return "premise.md"
	case "outline":
		return "outline.json"
	case "layered_outline", "expand_arc", "append_volume":
		return "layered_outline.json"
	case "complete_book":
		return "meta/progress.json"
	case "characters":
		return "characters.json"
	case "world_rules":
		return "world_rules.json"
	case "update_compass":
		return "meta/compass.json"
	default:
		return ""
	}
}

func decodeFoundationJSON(typeName, content string, out any) error {
	err := json.Unmarshal([]byte(content), out)
	if err == nil {
		return nil
	}
	hint := `Nguyên nhân thường gặp: dấu nháy kép trong chuỗi chưa escape thành \", xuống dòng chưa escape thành \n, hoặc thiếu dấu phẩy giữa các trường object. Hãy tạo lại toàn bộ đoạn.`
	if se, ok := err.(*json.SyntaxError); ok {
		line, col := offsetToLineCol(content, int(se.Offset))
		return fmt.Errorf("phân tích JSON %s (dòng %d cột %d): %w — %s", typeName, line, col, err, hint)
	}
	return fmt.Errorf("phân tích JSON %s: %w — %s", typeName, err, hint)
}

func offsetToLineCol(s string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(s) {
		offset = len(s)
	}
	line, col := 1, 1
	for i := 0; i < offset; i++ {
		if s[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return line, col
}

func normalizeFoundationContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("content là bắt buộc: %w", errs.ErrToolArgs)
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	if !json.Valid(raw) {
		return "", fmt.Errorf("content không hợp lệ: cần chuỗi Markdown hoặc giá trị JSON hợp lệ: %w", errs.ErrToolArgs)
	}
	return string(raw), nil
}

func (t *SaveFoundationTool) isWriting() (bool, error) {
	p, err := t.store.Progress.Load()
	if err != nil {
		return false, err
	}
	return p != nil && p.Phase == domain.PhaseWriting, nil
}

func (t *SaveFoundationTool) recordVolumeEndDecision(action, reason string, facts json.RawMessage, result map[string]any) {
	decision := map[string]any{"action": action}
	if v, ok := result["volume"]; ok {
		decision["volume"] = v
	}
	if _, ok := result["final_volume"]; ok {
		decision["final"] = true
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		slog.Error("卷末裁定序列化失败", "module", "tools", "action", action, "err", err)
		return
	}
	if _, err := t.store.Decisions.Append(store.DecisionRecord{
		Kind:     "volume_end",
		Decider:  "architect",
		Facts:    facts,
		Decision: raw,
		Reason:   reason,
	}); err != nil {
		slog.Warn("Lưu audit phán định cuối cuốn thất bại", "module", "tools", "action", action, "err", err)
	}
}

func (t *SaveFoundationTool) consumeWriterFeedback() {
	if err := t.store.Outline.ClearOutlineFeedback(); err != nil {
		slog.Warn("Xóa hồ phản hồi của writer thất bại", "module", "tools", "err", err)
	}
}
