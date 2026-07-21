package host

import (
	"fmt"
	"os"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// resumeLabel tạo nhãn UI cho Resume dựa trên dữ kiện đã lưu.
// Nhãn rỗng nghĩa là không có trạng thái cần khôi phục.
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

// describeResume tạo nhãn người đọc được; Engine vẫn tự định tuyến từ store.
func describeResume(store *storepkg.Store, progress *domain.Progress) (string, error) {
	switch progress.Phase {
	case domain.PhasePremise, domain.PhaseOutline:
		return fmt.Sprintf("Khôi phục: giai đoạn lập kế hoạch (%s)", progress.Phase), nil
	case domain.PhaseWriting:
		pending, err := store.Signals.LoadPendingCommit()
		if err != nil {
			return "", fmt.Errorf("đọc commit đang chờ khôi phục: %w", err)
		}
		if pending != nil {
			return fmt.Sprintf("Khôi phục: chương %d đang lưu dở", pending.Chapter), nil
		}
		if len(progress.PendingRewrites) > 0 {
			verb := "viết lại"
			if progress.Flow == domain.FlowPolishing {
				verb = "chỉnh sửa"
			}
			return fmt.Sprintf("Khôi phục: còn %d chương cần %s", len(progress.PendingRewrites), verb), nil
		}
		if progress.Flow == domain.FlowReviewing {
			return "Khôi phục: đánh giá bị gián đoạn", nil
		}
		if progress.InProgressChapter > 0 {
			return fmt.Sprintf("Khôi phục: chương %d đang thực hiện", progress.InProgressChapter), nil
		}
		label, err := describeArcEndLabel(store, progress)
		if err != nil {
			return "", err
		}
		if label != "" {
			return label, nil
		}
		return fmt.Sprintf("Khôi phục: tiếp tục từ chương %d", progress.NextChapter()), nil
	}
	return "Khôi phục", nil
}

// describeArcEndLabel tạo nhãn sát với nhánh Route ở cuối cung/tập.
func describeArcEndLabel(store *storepkg.Store, progress *domain.Progress) (string, error) {
	if !progress.Layered || len(progress.CompletedChapters) == 0 {
		return "", nil
	}
	lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
	boundary, err := store.Outline.CheckArcBoundary(lastCh)
	if err != nil {
		return "", fmt.Errorf("kiểm tra ranh giới cung: %w", err)
	}
	if boundary == nil || !boundary.IsArcEnd {
		return "", nil
	}
	vol, arc := boundary.Volume, boundary.Arc
	hasArcReview, err := store.World.HasArcReview(lastCh)
	if err != nil {
		return "", fmt.Errorf("đọc review cung: %w", err)
	}
	hasArcSummary := store.Summaries.HasArcSummary(vol, arc)
	hasVolumeSummary := false
	if boundary.IsVolumeEnd {
		hasVolumeSummary = store.Summaries.HasVolumeSummary(vol)
	}
	switch {
	case !hasArcReview:
		return fmt.Sprintf("Khôi phục: chờ đánh giá cuối cung (T%d C%d)", vol, arc), nil
	case !hasArcSummary:
		return fmt.Sprintf("Khôi phục: chờ tạo tóm tắt cung (T%d C%d)", vol, arc), nil
	case boundary.IsVolumeEnd && !hasVolumeSummary:
		return fmt.Sprintf("Khôi phục: chờ tạo tóm tắt tập (T%d)", vol), nil
	case boundary.NeedsExpansion && boundary.NextArc > 0:
		return fmt.Sprintf("Khôi phục: chờ mở rộng cung tiếp theo (T%d C%d)", boundary.NextVolume, boundary.NextArc), nil
	case boundary.NeedsNewVolume:
		return fmt.Sprintf("Khôi phục: chờ quyết định tập tiếp theo (cuối T%d)", vol), nil
	}
	return "", nil
}
