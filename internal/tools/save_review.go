package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

type SaveReviewTool struct {
	store *store.Store
}

func NewSaveReviewTool(store *store.Store) *SaveReviewTool {
	return &SaveReviewTool{store: store}
}

func (t *SaveReviewTool) Name() string { return "save_review" }
func (t *SaveReviewTool) Description() string {
	return "Lưu kết quả đánh giá và cập nhật trạng thái luồng. verdict là một trong accept/polish/rewrite. " +
		"Bên trong tool sẽ áp dụng cổng chấm điểm (có thể nâng verdict), rồi cập nhật flow và pending_rewrites của Progress. " +
		"Kết quả trả về là các dữ kiện có cấu trúc: final_verdict / affected_chapters / escalation_reason / next_flow / next_chapter"
}
func (t *SaveReviewTool) Label() string { return "Lưu đánh giá" }

func (t *SaveReviewTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *SaveReviewTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *SaveReviewTool) Schema() map[string]any {
	issueSchema := schema.Object(
		schema.Property("type", schema.Enum("Loại vấn đề", "consistency", "character", "pacing", "continuity", "foreshadow", "hook", "aesthetic")).Required(),
		schema.Property("severity", schema.Enum("Mức độ nghiêm trọng", "critical", "error", "warning")).Required(),
		schema.Property("description", schema.String("Mô tả vấn đề")).Required(),
		schema.Property("evidence", schema.String("Bằng chứng: trích đoạn nguyên văn, tình tiết cụ thể hoặc dữ liệu trạng thái")).Required(),
		schema.Property("suggestion", schema.String("Gợi ý sửa")),
	)
	dimensionSchema := schema.Object(
		schema.Property("dimension", schema.Enum("Chiều đánh giá", "consistency", "character", "pacing", "continuity", "foreshadow", "hook", "aesthetic")).Required(),
		schema.Property("score", schema.Int("Điểm (0-100)")).Required(),
		schema.Property("verdict", schema.Enum("Kết luận theo chiều (có thể bỏ qua: hệ thống tự suy ra từ score, ≥80 pass / ≥60 warning / <60 fail)", "pass", "warning", "fail")),
		schema.Property("comment", schema.String("Kết luận ngắn cho chiều này; mỗi chiều bắt buộc có, aesthetic phải trích nguyên văn hoặc số liệu cụ thể")).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("Số chương được đánh giá (đánh giá toàn cục thì dùng chương mới nhất)")).Required(),
		schema.Property("scope", schema.Enum("Phạm vi đánh giá", "chapter", "global", "arc")).Required(),
		schema.Property("dimensions", schema.Array("Điểm theo từng chiều (mỗi trong bảy chiều một mục)", dimensionSchema)).Required(),
		schema.Property("issues", schema.Array("Các vấn đề phát hiện", issueSchema)).Required(),
		schema.Property("contract_status", schema.Enum("Mức độ hoàn thành hợp đồng chương", "met", "partial", "missed")),
		schema.Property("contract_misses", schema.Array("Các mục contract chưa hoàn thành hoặc vi phạm", schema.String(""))),
		schema.Property("contract_notes", schema.String("Ghi chú ngắn về mức độ thực hiện contract")),
		schema.Property("verdict", schema.Enum("Kết luận đánh giá", "accept", "polish", "rewrite")).Required(),
		schema.Property("summary", schema.String("Tóm tắt đánh giá")).Required(),
		schema.Property("affected_chapters", schema.Array("Danh sách chương cần viết lại hoặc đánh bóng (bắt buộc khi verdict là polish/rewrite)", schema.Int(""))),
	)
}

func (t *SaveReviewTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var r domain.ReviewEntry
	if err := json.Unmarshal(args, &r); err != nil {
		return nil, fmt.Errorf("tham số không hợp lệ: %w", err)
	}
	if r.Chapter <= 0 {
		return nil, fmt.Errorf("chương phải lớn hơn 0")
	}
	for i := range r.Dimensions {
		r.Dimensions[i].Verdict = expectedDimensionVerdict(r.Dimensions[i].Score)
	}
	if err := validateReviewEntry(r); err != nil {
		return nil, err
	}

	finalVerdict := r.Verdict
	var escalationReason string

	if r.Verdict == "accept" {
		if r.ContractStatus == "missed" {
			finalVerdict = "rewrite"
			escalationReason = "trạng thái thực hiện contract là missed, nâng lên viết lại"
		} else if r.ContractStatus == "partial" {
			finalVerdict = "polish"
			escalationReason = "trạng thái thực hiện contract là partial, nâng lên đánh bóng"
		}
		if finalVerdict == "accept" {
			if gate := evaluateScorecardGate(r.Dimensions); gate != "" {
				if strings.Contains(gate, "rewrite") {
					finalVerdict = "rewrite"
				} else {
					finalVerdict = "polish"
				}
				escalationReason = gate
			}
		}
	}

	flow := domain.FlowWriting
	if finalVerdict == "rewrite" {
		flow = domain.FlowRewriting
	} else if finalVerdict == "polish" {
		flow = domain.FlowPolishing
	}

	affected := r.AffectedChapters
	if finalVerdict == "rewrite" || finalVerdict == "polish" {
		if len(affected) == 0 && r.Chapter > 0 {
			affected = []int{r.Chapter}
		}
		if err := t.store.Progress.ValidatePendingRewrites(affected); err != nil {
			return nil, fmt.Errorf("xác thực pending rewrites: %w", err)
		}
	}

	// Áp dụng trạng thái điều khiển theo cách nguyên tử rồi mới lưu artifact review.
	// Nếu bước lưu artifact lỗi, ý định làm lại vẫn còn; sau khi writer xả hàng đợi,
	// router sẽ phái Editor lại vì thiếu artifact review, nên không bỏ qua review.
	progress, err := t.store.Progress.ApplyReviewOutcome(flow, affected, r.Summary)
	if err != nil {
		return nil, fmt.Errorf("apply review outcome: %w", err)
	}
	if err := t.store.World.SaveReview(r); err != nil {
		return nil, fmt.Errorf("lưu review: %w", err)
	}

	latest, _ := t.store.Progress.Load()
	nextFlow := string(domain.FlowWriting)
	nextChapter := 0
	if latest != nil {
		nextFlow = string(latest.Flow)
		nextChapter = latest.NextChapter()
	}

	scope := domain.ChapterScope(r.Chapter)
	if r.Scope == "arc" {
		vol, arc := 0, 0
		if progress != nil {
			vol, arc = progress.CurrentVolume, progress.CurrentArc
		}
		scope = domain.ArcScope(vol, arc)
	}
	artifact := fmt.Sprintf("reviews/%02d.json", r.Chapter)
	if r.Scope == "global" {
		artifact = fmt.Sprintf("reviews/%02d-global.json", r.Chapter)
	}
	if _, err := t.store.Checkpoints.AppendArtifact(scope, "review", artifact); err != nil {
		return nil, fmt.Errorf("ghi checkpoint review: %w", err)
	}

	result := map[string]any{
		"saved":             true,
		"chapter":           r.Chapter,
		"scope":             r.Scope,
		"verdict":           r.Verdict,
		"final_verdict":     finalVerdict,
		"affected_chapters": affected,
		"issues":            len(r.Issues),
		"next_flow":         nextFlow,
		"next_chapter":      nextChapter,
	}
	if escalationReason != "" {
		result["escalation_reason"] = escalationReason
	}
	return json.Marshal(result)
}

func validateReviewEntry(r domain.ReviewEntry) error {
	if strings.TrimSpace(r.Scope) == "" {
		return fmt.Errorf("scope là bắt buộc")
	}
	if strings.TrimSpace(r.Summary) == "" {
		return fmt.Errorf("summary là bắt buộc")
	}
	for _, issue := range r.Issues {
		if strings.TrimSpace(issue.Description) == "" {
			return fmt.Errorf("mô tả vấn đề là bắt buộc")
		}
		if strings.TrimSpace(issue.Evidence) == "" {
			return fmt.Errorf("bằng chứng vấn đề là bắt buộc")
		}
	}
	if err := validateDimensions(r.Dimensions); err != nil {
		return err
	}
	if (r.Verdict == "rewrite" || r.Verdict == "polish") && len(r.AffectedChapters) == 0 {
		return fmt.Errorf("affected_chapters là bắt buộc khi verdict=%s", r.Verdict)
	}
	return nil
}

// reviewFlow 是文学裁定与持久化协议之间唯一的映射点。verdict 由 Editor 决定；
// 这里只接受 Router 能恢复的三种控制结果。
func reviewFlow(verdict string) (domain.FlowState, error) {
	switch verdict {
	case "accept":
		return domain.FlowWriting, nil
	case "polish":
		return domain.FlowPolishing, nil
	case "rewrite":
		return domain.FlowRewriting, nil
	default:
		return "", fmt.Errorf("invalid review verdict: %q", verdict)
	}
}

func validateDimensions(dimensions []domain.DimensionScore) error {
	if len(dimensions) == 0 {
		return fmt.Errorf("dimensions phải chứa ít nhất một đánh giá có bằng chứng")
	}

	seen := make(map[string]struct{}, len(dimensions))
	for _, dim := range dimensions {
		name := strings.TrimSpace(dim.Dimension)
		if name == "" {
			return fmt.Errorf("tên chiều đánh giá là bắt buộc")
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("chiều bị trùng: %s", name)
		}
		seen[name] = struct{}{}
		if dim.Score < 0 || dim.Score > 100 {
			return fmt.Errorf("điểm không hợp lệ cho %s: %d", dim.Dimension, dim.Score)
		}
		if strings.TrimSpace(dim.Comment) == "" {
			return fmt.Errorf("bình luận cho chiều là bắt buộc: %s", dim.Dimension)
		}
	}
	return nil
}

func expectedDimensionVerdict(score int) string {
	switch {
	case score >= 80:
		return "pass"
	case score >= 60:
		return "warning"
	default:
		return "fail"
	}
}

var criticalDimensions = map[string]struct{}{
	"consistency": {},
	"character":   {},
	"continuity":  {},
}

func evaluateScorecardGate(dimensions []domain.DimensionScore) string {
	var criticalFails []string
	var polishIssues []string

	for _, dim := range dimensions {
		_, isCritical := criticalDimensions[dim.Dimension]
		if isCritical && (dim.Verdict == "fail" || dim.Score < 60) {
			criticalFails = append(criticalFails, fmt.Sprintf("%s(%d)", dim.Dimension, dim.Score))
		} else if dim.Verdict == "warning" || (isCritical && dim.Score < 80) {
			polishIssues = append(polishIssues, fmt.Sprintf("%s(%d)", dim.Dimension, dim.Score))
		}
	}

	if len(criticalFails) > 0 {
		return fmt.Sprintf("rewrite: các chiều trọng yếu không đạt %v", criticalFails)
	}
	if len(polishIssues) > 0 {
		return fmt.Sprintf("polish: một số chiều cần đánh bóng %v", polishIssues)
	}
	return ""
}
