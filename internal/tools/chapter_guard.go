package tools

import (
	"fmt"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// EnsureChapterExpanded verifies that chapter work is in the writing phase and,
// for layered books, inside the currently expanded outline.
func EnsureChapterExpanded(st *store.Store, chapter int) error {
	if st == nil {
		return fmt.Errorf("store không được để trống: %w", errs.ErrToolPrecondition)
	}
	if chapter <= 0 {
		return fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return fmt.Errorf("progress chưa được khởi tạo: %w", errs.ErrToolPrecondition)
	}
	if progress.Phase != domain.PhaseWriting {
		return fmt.Errorf("chỉ được viết chương trong phase writing (phase hiện tại=%s): %w", progress.Phase, errs.ErrToolPrecondition)
	}
	if !progress.Layered {
		return nil
	}
	boundary, err := st.Outline.CheckArcBoundary(chapter)
	if err != nil {
		return fmt.Errorf("check layered outline: %w: %w", errs.ErrStoreRead, err)
	}
	if boundary != nil {
		return nil
	}
	return fmt.Errorf(
		"Chương %d nằm ngoài phạm vi dàn ý phân tầng: trước khi viết phải dùng expand_arc để mở rộng cung truyện hoặc append_volume để thêm tập; nếu toàn truyện đã hoàn tất, hãy gọi save_foundation type=complete_book: %w",
		chapter, errs.ErrToolPrecondition)
}
