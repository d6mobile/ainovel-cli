package host

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

// 预算状态机：单调递进，每次迁移恰好触发一次副作用，不回退。
// 上调预算 = 用户重新授权 = 改配置后重启/新 Host 实例，不在本实例内回退状态。
const (
	budgetNormal      int32 = iota // 未到告警水位
	budgetWarned                   // 已发告警，未越线
	budgetStopPending              // 已越线，等子代理边界停机
	budgetStopped                  // 已执行停机
)

// BudgetSentinel 监视累计成本，执行用户的预算政策（config budget 块）。
//
// 合宪定位（architecture.md §8.3/§10）：不评估模型行为——越线停机等同于用户在
// 那一刻手动 Abort，Host 只是代为执行一条预先签署的指令。它影响控制流，因此
// 不是观察者，定位为与 flow.Dispatcher 平级的 Host 政策组件；Route/工具层不感知。
//
// 停机时机：默认在子代理边界（Host 同步调用 HandleBoundary）；hardStop=true 时越线立即停。
type BudgetSentinel struct {
	mu sync.RWMutex

	enabled   bool
	limit     float64
	warnRatio float64
	hardStop  bool

	costNow func() float64              // 当前累计成本（usage.Totals 包装；可注入测试桩）
	abort   func(reason string)         // Host 停机包装（带原因事件）
	report  func(level, summary string) // 告警出口（emitEvent + notify，由 Host 注入）

	state atomic.Int32

	// 计费盲区检测：注册表无价且 provider 不自报 cost 的模型每笔记账增量为 $0，
	// 预算静默失效。按“连续多笔零增量”判定而非 total==0——后者抓不住长跑中途
	// /model 切到无价模型的场景（total 停在历史值非零但不再增长）。
	// 免费模型同样命中，提示“预算不会触发”对其同样成立。
	lastTotal   atomic.Uint64 // math.Float64bits(上次回调的累计成本)
	zeroStreak  atomic.Int32
	blindWarned atomic.Bool
}

// blindZeroStreak 连续零增量记账多少笔后告警。正常计价模型每笔增量必 > 0
// （cost 是 float 累计不取整），取 5 仅为避免极端毛刺，不是可调策略阈值。
const blindZeroStreak = 5

// NewBudgetSentinel 创建预算哨兵；政策未启用时返回 nil（所有方法 nil 安全）。
func NewBudgetSentinel(cfg bootstrap.BudgetConfig, costNow func() float64, abort func(reason string), report func(level, summary string)) *BudgetSentinel {
	if !cfg.Enabled() {
		return nil
	}
	return &BudgetSentinel{
		enabled:   true,
		limit:     cfg.BookUSD,
		warnRatio: cfg.WarnRatio,
		hardStop:  cfg.HardStop,
		costNow:   costNow,
		abort:     abort,
		report:    report,
	}
}

// UpdateConfig 热更新预算配置；保留当前状态机进度，避免重建 sentinel 丢失 in-flight 状态。
// 禁用预算时仅关闭后续触发；重新启用时沿用同一对象和已有状态。
func (s *BudgetSentinel) UpdateConfig(cfg bootstrap.BudgetConfig) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.enabled = cfg.Enabled()
	s.limit = cfg.BookUSD
	s.warnRatio = cfg.WarnRatio
	s.hardStop = cfg.HardStop
	s.mu.Unlock()

}

func (s *BudgetSentinel) snapshotConfig() (enabled bool, limit, warnRatio float64, hardStop bool) {
	if s == nil {
		return false, 0, 0, false
	}
	s.mu.RLock()
	enabled, limit, warnRatio, hardStop = s.enabled, s.limit, s.warnRatio, s.hardStop
	s.mu.RUnlock()
	return enabled, limit, warnRatio, hardStop
}

func (s *BudgetSentinel) applyCost(total float64, trackBlind bool) {
	if s == nil {
		return
	}
	enabled, limit, warnRatio, hardStop := s.snapshotConfig()
	if !enabled {
		return
	}

	if trackBlind {
		if prev := s.lastTotal.Swap(math.Float64bits(total)); total == math.Float64frombits(prev) {
			if s.zeroStreak.Add(1) >= blindZeroStreak && s.blindWarned.CompareAndSwap(false, true) {
				s.report("warn", fmt.Sprintf("vùng mù ngân sách: ghi nhận liên tiếp nhưng tổng chi phí kẹt ở $%.2f và không tăng nữa (model hiện tại không có giá trong registry, provider không tự báo cost, hoặc là model miễn phí) — giới hạn ngân sách sẽ không kích hoạt", total))
			}
		} else {
			s.zeroStreak.Store(0)
		}
	}

	if total >= limit*warnRatio && s.state.CompareAndSwap(budgetNormal, budgetWarned) {
		s.report("warn", fmt.Sprintf("预算告警: 已花费 $%.2f，达到预算 $%.2f 的 %.0f%%", total, limit, warnRatio*100))
	}
	if total >= limit && s.state.CompareAndSwap(budgetWarned, budgetStopPending) {
		if hardStop {
			s.report("error", fmt.Sprintf("预算用尽: 已花费 $%.2f，超出预算 $%.2f，立即停机", total, limit))
			s.stop(total)
			return
		}
		s.report("error", fmt.Sprintf("预算用尽: 已花费 $%.2f，超出预算 $%.2f，将在当前子代理任务结束后停机", total, limit))
	}
}

// OnCost 由 UsageTracker 每次记账后携带最新累计成本调用（锁外）。
// 一次回调可能连跨两级（normal→warned→stopPending），两次副作用各触发一次。
func (s *BudgetSentinel) OnCost(total float64) {
	s.applyCost(total, true)
}

// OnMissingUsage 处理“模型返回消息但没有 usage”的盲区告警。
func (s *BudgetSentinel) OnMissingUsage() {
	if s == nil {
		return
	}
	enabled, _, _, _ := s.snapshotConfig()
	if !enabled {
		return
	}
	const blind = "预算盲区: 模型未返回 usage 数据，成本统计为 0，预算上限不会触发（自定义模型请确认注册表价格或上游 include_usage）"
	s.report("warn", blind)
}

// HandleEvent 在子代理边界执行待定的停机。订阅必须先于 Dispatcher。
// 不跳过 IsError——出错返回同样是边界，停机不应因子代理失败而推迟。
func (s *BudgetSentinel) HandleEvent(ev agentcore.Event) {
	if s == nil {
		return
	}
	if ev.Type != agentcore.EventToolExecEnd || ev.Tool != "subagent" {
		return
	}
	s.HandleBoundary()
}

func (s *BudgetSentinel) HandleBoundary() bool {
	if s == nil {
		return false
	}
	enabled, _, _, _ := s.snapshotConfig()
	if !enabled || s.state.Load() != budgetStopPending {
		return false
	}
	s.stop(s.costNow())
	return true
}

func (s *BudgetSentinel) stop(total float64) {
	if s.state.CompareAndSwap(budgetStopPending, budgetStopped) {
		s.abort(fmt.Sprintf("预算停机: 已花费 $%.2f，超出预算 $%.2f；上调 budget.book_usd 后可恢复续跑", total, s.limit))
	}
}

// Refuse 启动前置检查：预算已超返回拒绝错误（Start/Resume/Continue 恢复路径调用）。
// 用户上调预算 = 重新授权，新配置下 Refuse 自然放行。
func (s *BudgetSentinel) Refuse() error {
	if s == nil {
		return nil
	}
	enabled, limit, _, _ := s.snapshotConfig()
	if !enabled {
		return nil
	}
	if cost := s.costNow(); cost >= limit {
		return fmt.Errorf("本书已花费 $%.2f，达到预算上限 $%.2f；请上调配置 budget.book_usd 后重试", cost, limit)
	}
	return nil
}

// Limit 返回预算上限（UI 展示用）；未启用返回 0。
func (s *BudgetSentinel) Limit() float64 {
	if s == nil {
		return 0
	}
	enabled, limit, _, _ := s.snapshotConfig()
	if !enabled {
		return 0
	}
	return limit
}
