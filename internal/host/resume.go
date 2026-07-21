package host

import (
	"fmt"
	"os"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// resumeLabel 基于事实生成 Resume 的 UI 标签。
// label 为空表示无可恢复状态（应走新建）。恢复本身不需要任何 prompt——
// Engine 只恢复事实：从 store 重算路由续跑（docs/engine-rfc.md §6）。
func resumeLabel(store *storepkg.Store) (string, error) {
	progress, err := store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if progress == nil || progress.Phase == domain.PhaseComplete {
		return "", nil
	}
	return describeResume(store, progress)
}

// describeResume 生成人类可读的恢复标签；不影响 Engine 路由。
// 所有执行路由由 Flow Router 按事实推导；这里仅面向 UI 的 "恢复：xxx"。
func describeResume(store *storepkg.Store, progress *domain.Progress) (string, error) {
	switch progress.Phase {
	case domain.PhasePremise, domain.PhaseOutline:
		return fmt.Sprintf("Khôi phục: giai đoạn lập kế hoạch (%s)", progress.Phase)
	case domain.PhaseWriting:
		// Ưu tiên khớp thứ tự quyết định của Router để label phản ánh đúng bước sắp chạy.
		if pending, _ := store.Signals.LoadPendingCommit(); pending != nil {
			return fmt.Sprintf("Khôi phục: chương %d đang lưu dở", pending.Chapter)
		}
		if len(progress.PendingRewrites) > 0 {
			verb := "viết lại"
			if progress.Flow == domain.FlowPolishing {
				verb = "chỉnh sửa"
			}
			return fmt.Sprintf("Khôi phục: còn %d chương cần %s", len(progress.PendingRewrites), verb)
		}
		if progress.Flow == domain.FlowReviewing {
			return "Khôi phục: đánh giá bị gián đoạn"
		}
		if progress.InProgressChapter > 0 {
			return fmt.Sprintf("Khôi phục: chương %d đang thực hiện", progress.InProgressChapter)
		}
		label, err := describeArcEndLabel(store, progress)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Khôi phục: tiếp tục từ chương %d", progress.NextChapter())
	}
	return "Khôi phục"
}

// describeArcEndLabel 为弧末/卷末的多种中间状态生成贴合 UI 的标签。
// 与 flow.Route 的弧末分支保持同序，保证 label 与 Router 首条指令对齐。
func describeArcEndLabel(store *storepkg.Store, progress *domain.Progress) (string, error) {
	if !progress.Layered || len(progress.CompletedChapters) == 0 {
		return "", nil
	}
	lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
	boundary, err := store.Outline.CheckArcBoundary(lastCh)
	if err != nil {
		return "", fmt.Errorf("检查弧边界: %w", err)
	}
	if boundary == nil || !boundary.IsArcEnd {
		return "", nil
	}
	vol, arc := boundary.Volume, boundary.Arc
	switch {
	case !store.World.HasArcReview(lastCh):
		return fmt.Sprintf("Khôi phục: chờ đánh giá cuối cung (T%d C%d)", vol, arc)
	case !store.Summaries.HasArcSummary(vol, arc):
		return fmt.Sprintf("Khôi phục: chờ tạo tóm tắt cung (T%d C%d)", vol, arc)
	case boundary.IsVolumeEnd && !store.Summaries.HasVolumeSummary(vol):
		return fmt.Sprintf("Khôi phục: chờ tạo tóm tắt tập (T%d)", vol)
	case boundary.NeedsExpansion && boundary.NextArc > 0:
		return fmt.Sprintf("Khôi phục: chờ mở rộng cung tiếp theo (T%d C%d)", boundary.NextVolume, boundary.NextArc)
	case boundary.NeedsNewVolume:
		return fmt.Sprintf("Khôi phục: chờ quyết định tập tiếp theo (cuối T%d)", vol)
	}
	hasArcSummary, err := store.Summaries.HasArcSummary(vol, arc)
	if err != nil {
		return "", fmt.Errorf("读取弧摘要: %w", err)
	}
	hasVolumeSummary := false
	if boundary.IsVolumeEnd {
		hasVolumeSummary, err = store.Summaries.HasVolumeSummary(vol)
		if err != nil {
			return "", fmt.Errorf("读取卷摘要: %w", err)
		}
	}
	switch {
	case !hasArcReview:
		return fmt.Sprintf("恢复：弧末评审待处理（V%d A%d）", vol, arc), nil
	case !hasArcSummary:
		return fmt.Sprintf("恢复：弧摘要待生成（V%d A%d）", vol, arc), nil
	case boundary.IsVolumeEnd && !hasVolumeSummary:
		return fmt.Sprintf("恢复：卷摘要待生成（V%d）", vol), nil
	case boundary.NeedsExpansion && boundary.NextArc > 0:
		return fmt.Sprintf("恢复：待展开下一弧（V%d A%d）", boundary.NextVolume, boundary.NextArc), nil
	case boundary.NeedsNewVolume:
		return fmt.Sprintf("恢复：待决策下一卷（V%d 末）", vol), nil
	}
	return "", nil
}
