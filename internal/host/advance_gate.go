package host

import (
	"fmt"
	"slices"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/store"
)

// ChapterAdvanceGate 是 Host 唯一的创作前进政策组件：
//   - AdvanceHold：执行本次干预签署的一次性暂停；
//   - review permit：阻止未获许可的正向新章。
//
// 它不参与 Route，不解释 Task/Reason，也不做文学判断。
type ChapterAdvanceGate struct {
	store  *store.Store
	pause  func(reason string)
	report func(level, summary string)
}

func NewChapterAdvanceGate(s *store.Store, pause func(reason string), report func(level, summary string)) *ChapterAdvanceGate {
	return &ChapterAdvanceGate{store: s, pause: pause, report: report}
}

// HandleBoundary 消费命中的 hold，并对账章节许可。返回 true 表示 Engine 必须停止。
// auto 且无 hold 时只读一次 RunMeta，不触碰 Progress/PendingCommit/checkpoint。
func (g *ChapterAdvanceGate) HandleBoundary() bool {
	if g == nil || g.store == nil {
		return false
	}
	meta, err := g.store.RunMeta.Load()
	if err != nil {
		return g.fail(fmt.Errorf("Đọc RunMeta: %w", err))
	}
	if meta == nil {
		return g.fail(fmt.Errorf("RunMeta chưa được khởi tạo"))
	}
	if !meta.AdvanceMode.Valid() {
		return g.fail(&domain.UnsupportedAdvanceModeError{Mode: meta.AdvanceMode})
	}
	if meta.AdvanceMode == domain.ChapterAdvanceAuto && meta.AdvancePermitChapter != 0 {
		return g.fail(fmt.Errorf("Chế độ auto còn sót giấy phép chương %d", meta.AdvancePermitChapter))
	}

	if meta.AdvanceHold != nil {
		if g.handleHold(*meta.AdvanceHold) {
			return true
		}
		// handleHold 可能消费完本 hold；继续对账 permit。
	}
	if meta.AdvanceMode == domain.ChapterAdvanceAuto {
		return false
	}
	return g.reconcilePermit(meta.AdvancePermitChapter)
}

func (g *ChapterAdvanceGate) handleHold(hold domain.AdvanceHold) bool {
	progress, err := g.store.Progress.Load()
	if err != nil {
		return g.fail(fmt.Errorf("读取 Progress 解析一次性暂停: %w", err))
	}
	resolution, err := flow.ResolveAdvanceHold(&hold, progress)
	if err != nil {
		return g.fail(err)
	}
	switch resolution {
	case flow.AdvanceHoldKeep:
		return false
	case flow.AdvanceHoldConsume:
		if err := g.store.RunMeta.ClearAdvanceHold(hold); err != nil {
			return g.fail(fmt.Errorf("消费一次性暂停: %w", err))
		}
		g.reportEvent("info", withAdvanceReason("全书已完结，一次性暂停意图已解除", hold.Reason))
		return false
	case flow.AdvanceHoldConsumeAndStop:
		if err := g.store.RunMeta.ClearAdvanceHold(hold); err != nil {
			return g.fail(fmt.Errorf("消费一次性暂停: %w", err))
		}
		msg := "已按用户要求在当前工作边界暂停"
		if hold.After == domain.AdvanceHoldAfterRewritesDrained {
			msg = "返工队列已排空，已暂停等待验收"
		}
		g.pauseNow(withAdvanceReason(msg, hold.Reason))
		return true
	default:
		return g.fail(fmt.Errorf("未知一次性暂停解析结果 %d", resolution))
	}
}

func (g *ChapterAdvanceGate) reconcilePermit(permit int) bool {
	if permit == 0 {
		return false
	}
	if permit < 0 {
		return g.fail(fmt.Errorf("Giấy phép chương không được âm: %d", permit))
	}
	progress, err := g.store.Progress.Load()
	if err != nil {
		return g.fail(fmt.Errorf("Đọc Progress để đối chiếu giấy phép chương: %w", err))
	}
	if progress == nil {
		return g.fail(fmt.Errorf("Thiếu Progress, không thể đối chiếu giấy phép chương %d", permit))
	}
	pending, err := g.store.Signals.LoadPendingCommit()
	if err != nil {
		return g.fail(fmt.Errorf("Đọc PendingCommit để đối chiếu giấy phép chương: %w", err))
	}
	completed := slices.Contains(progress.CompletedChapters, permit)
	if completed {
		if pending != nil {
			if pending.Chapter != permit {
				return g.fail(fmt.Errorf("Giấy phép chương %d xung đột với PendingCommit chương %d", permit, pending.Chapter))
			}
			return false
		}
		if g.store.Checkpoints.LatestByStep(domain.ChapterScope(permit), "commit") == nil {
			return g.fail(fmt.Errorf("Chương %d đã đánh dấu hoàn thành nhưng thiếu commit checkpoint", permit))
		}
		if err := g.store.RunMeta.ClearAdvancePermit(permit); err != nil {
			return g.fail(fmt.Errorf("Tiêu thụ giấy phép chương %d: %w", permit, err))
		}
		return false
	}
	if permit != progress.NextChapter() {
		return g.fail(fmt.Errorf("Giấy phép chương %d không khớp chương kế tiếp hiện tại %d", permit, progress.NextChapter()))
	}
	return false
}

// Allow 在 Worker 派发前执行最终许可检查。
func (g *ChapterAdvanceGate) Allow(inst *flow.Instruction) (bool, error) {
	if g == nil || g.store == nil {
		return true, nil
	}
	meta, err := g.store.RunMeta.Load()
	if err != nil {
		return false, fmt.Errorf("Đọc RunMeta: %w", err)
	}
	if meta == nil {
		return false, fmt.Errorf("RunMeta chưa được khởi tạo")
	}
	if !meta.AdvanceMode.Valid() {
		return false, &domain.UnsupportedAdvanceModeError{Mode: meta.AdvanceMode}
	}
	if meta.AdvanceMode == domain.ChapterAdvanceAuto {
		if meta.AdvancePermitChapter != 0 {
			return false, fmt.Errorf("Chế độ auto còn sót giấy phép chương %d", meta.AdvancePermitChapter)
		}
		return true, nil
	}
	progress, err := g.store.Progress.Load()
	if err != nil {
		return false, fmt.Errorf("Đọc Progress: %w", err)
	}
	pending, err := g.store.Signals.LoadPendingCommit()
	if err != nil {
		return false, fmt.Errorf("Đọc PendingCommit: %w", err)
	}
	if !flow.StartsForwardChapter(inst, progress, pending) {
		return true, nil
	}
	target := inst.Chapter
	if target == 0 {
		target = progress.NextChapter()
	}
	if meta.AdvancePermitChapter == target {
		return true, nil
	}
	if meta.AdvancePermitChapter != 0 {
		return false, fmt.Errorf("Lượt điều phối chương %d không khớp giấy phép chương %d", target, meta.AdvancePermitChapter)
	}
	latest := progress.LatestCompleted()
	message := fmt.Sprintf("Đã hoàn thành đến chương %d, nghiệm thu từng chương đang chờ cho phép chương %d; dùng /next để tạo, hoặc nhập ý kiến chỉnh sửa", latest, target)
	if latest == 0 {
		message = fmt.Sprintf("Dàn ý đã sẵn sàng, nghiệm thu từng chương đang chờ cho phép chương %d; dùng /next để tạo, hoặc nhập ý kiến chỉnh sửa", target)
	}
	g.pauseNow(message)
	return false, nil
}

func (g *ChapterAdvanceGate) fail(err error) bool {
	g.pauseNow("Lỗi kiểm soát tiến độ chương, đã tạm dừng: " + err.Error())
	return true
}

func (g *ChapterAdvanceGate) pauseNow(reason string) {
	if g.pause != nil {
		g.pause(reason)
		return
	}
	g.reportEvent("error", reason)
}

func (g *ChapterAdvanceGate) reportEvent(level, summary string) {
	if g.report != nil {
		g.report(level, summary)
	}
}

func withAdvanceReason(msg, reason string) string {
	if reason == "" {
		return msg
	}
	return msg + "（诉求：" + reason + "）"
}
