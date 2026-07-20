package host

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/agents"
	"github.com/voocel/ainovel-cli/internal/agents/ctxpack"
	"github.com/voocel/ainovel-cli/internal/arbiter"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/host/exp"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/host/sim"
	modelreg "github.com/voocel/ainovel-cli/internal/models"
	"github.com/voocel/ainovel-cli/internal/notify"
	"github.com/voocel/ainovel-cli/internal/rules"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
	"github.com/voocel/ainovel-cli/internal/userrules"
)

type Host struct {
	cfg             bootstrap.Config
	bundle          assets.Bundle
	store           *storepkg.Store
	models          *bootstrap.ModelSet
	engine          *engine
	thinkingApplier agents.ApplyThinking
	askUser         *tools.AskUserTool
	writerRestore   *ctxpack.WriterRestorePack
	userRules       *userrules.Service
	observer        *observer
	usage           *UsageTracker
	usageCancel     context.CancelFunc
	budget          *BudgetSentinel
	gate            *ChapterAdvanceGate
	notifier        *notify.Notifier
	configPath      string

	events   chan Event
	streamCh chan string
	done     chan struct{}

	mu         sync.Mutex
	lifecycle  lifecycle
	cocreating bool
	exclusive  string
	exclusiveCancel context.CancelFunc
	closeOnce       sync.Once

	interMu sync.Mutex

	runCtx    context.Context
	runCancel context.CancelFunc
}

type lifecycle string

const (
	lifecycleIdle      lifecycle = "idle"
	lifecycleRunning   lifecycle = "running"
	lifecyclePaused    lifecycle = "paused"
	lifecycleCompleted lifecycle = "completed"
)

func New(cfg bootstrap.Config, bundle assets.Bundle) (*Host, error) {
	cfg.FillDefaults()
	if err := cfg.ValidateBase(); err != nil {
		return nil, err
	}
	slog.Info("启动", "module", "boot", "provider", cfg.Provider, "model", cfg.ModelName, "output", cfg.OutputDir)

	modelreg.StartPricingRefresh(modelreg.DefaultRegistry(), bootstrap.DefaultConfigDir())

	store := storepkg.NewStore(cfg.OutputDir)
	if err := store.Init(); err != nil {
		return nil, fmt.Errorf("init store: %w", err)
	}
	if err := store.RunMeta.Init(cfg.Style, cfg.Provider, cfg.ModelName); err != nil {
		return nil, fmt.Errorf("init run meta: %w", err)
	}

	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		return nil, fmt.Errorf("create models: %w", err)
	}
	slog.Info("模型就绪", "module", "boot", "summary", models.Summary())

	usage := NewUsageTracker(models, store)
	loaded, loadErr := usage.LoadFromStore()
	if loadErr != nil {
		slog.Warn("usage 加载失败，将尝试从 sessions 回填", "module", "usage", "err", loadErr)
	}
	if !loaded {
		if n, err := usage.ReplaySessions(cfg.OutputDir); err != nil {
			slog.Warn("usage replay 失败", "module", "usage", "err", err)
		} else if n > 0 {
			slog.Info("usage 从 session 回填完成", "module", "usage", "messages", n)
			if err := usage.SaveNow(); err != nil {
				slog.Warn("usage 回填后保存失败", "module", "usage", "err", err)
			}
		}
	}
	usageCtx, usageCancel := context.WithCancel(context.Background())
	usage.StartAutoSave(usageCtx)

	var onGuardBlock func(agent, reason string, consecutive int32)
	workers, askUser, restore, applyThinking := agents.BuildWorkers(cfg, store, models, bundle, usage.Record,
		func(agent, reason string, consecutive int32) {
			if onGuardBlock != nil {
				onGuardBlock(agent, reason, consecutive)
			}
		})
	store.Signals.ClearStaleSignals()

	h := &Host{
		cfg:             cfg,
		bundle:          bundle,
		store:           store,
		models:          models,
		thinkingApplier: applyThinking,
		askUser:         askUser,
		writerRestore:   restore,
		userRules:       userrules.NewService(store, models.Default, rules.DefaultOptions()),
		usage:           usage,
		usageCancel:     usageCancel,
		configPath:      bootstrap.EffectiveConfigPath(),
		events:          make(chan Event, 100),
		streamCh:        make(chan string, 256),
		done:            make(chan struct{}, 4),
		lifecycle:       lifecycleIdle,
	}
	h.runCtx, h.runCancel = context.WithCancel(context.Background())
	h.observer = newObserver(store, h.emitEvent, h.emitDelta, h.emitClear)
	h.runCtx = agentcore.WithToolProgress(h.runCtx, h.observer.workerProgress)
	if cfg.Notify.IsEnabled() {
		h.notifier = notify.New(cfg.Notify.Command, cfg.Notify.Events)
	}
	if cfg.Budget.Enabled() {
		h.budget = h.newBudgetSentinel(cfg.Budget)
	}
	h.usage.SetOnCost(h.recordBudgetCost)
	h.usage.SetOnMissingUsage(h.recordBudgetMissingUsage)
	h.gate = NewChapterAdvanceGate(store,
		func(reason string) {
			h.abortWithEvent(reason, "info")
			h.sendNotification(notify.Notification{Kind: notify.KindAdvanceGate, Level: "info", Title: "ainovel: 等待验收", Body: reason})
		},
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.sendNotification(notify.Notification{Kind: notify.KindAdvanceGate, Level: level, Title: "ainovel: 章节推进", Body: summary})
		},
	)
	onGuardBlock = func(agent, reason string, n int32) {
		switch reason {
		case "escalated":
			body := fmt.Sprintf("%s 连续 %d 次空转未落盘必要产物，本轮任务终止，交回 Engine 处理", agent, n)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: "StopGuard 升级: " + body, Level: "warn"})
			h.sendNotification(notify.Notification{Kind: notify.KindStopGuard, Level: "warn", Title: "ainovel: StopGuard", Body: body})
		case "hard_stop":
			body := fmt.Sprintf("%s 遭 provider 拒答（safety/content_filter），本轮任务立即终止", agent)
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent, Summary: "StopGuard 升级: " + body, Level: "warn"})
			h.sendNotification(notify.Notification{Kind: notify.KindStopGuard, Level: "warn", Title: "ainovel: StopGuard", Body: body})
		default: // blocked
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Agent: agent,
				Summary: fmt.Sprintf("StopGuard: %s 未完成必要产物就试图结束，已拦截催促（连续第 %d 次）", agent, n), Level: "info"})
		}
	}
	h.engine = &engine{
		store:           store,
		workers:         workers,
		arbiterModel:    newUsageTrackedModel(models.Default, "arbiter", usage.Record),
		failurePrompt:   bundle.Prompts.ArbiterFailure,
		planStartPrompt: bundle.Prompts.ArbiterPlanStart,
		style:           cfg.Style,
		reconsult: h.handleIntervention,
		observer:  h.observer,
		budget:    h.budget,
		gate:      h.gate,
		refresh:   h.refreshWriterRestore,
		emitEvent: h.emitEvent,
		notify: func(kind, level, title, body string) {
			h.sendNotification(notify.Notification{Kind: kind, Level: level, Title: title, Body: body})
		},
		onPause: func(summary string) { h.abortWithEvent(summary, "warn") },
		onDone:  h.runEnded,
	}

	return h, nil
}
func (h *Host) newBudgetSentinel(cfg bootstrap.BudgetConfig) *BudgetSentinel {
	return NewBudgetSentinel(cfg,
		func() float64 { c, _, _, _, _ := h.usage.Totals(); return c },
		func(reason string) { h.abortWithEvent(reason, "error") },
		h.emitBudgetReport,
	)
}

func (h *Host) emitBudgetReport(level, summary string) {
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
	h.sendNotification(notify.Notification{Kind: notify.KindBudget, Level: level, Title: "ainovel: 预算", Body: summary})
}

func (h *Host) sendNotification(nt notify.Notification) {
	h.mu.Lock()
	notifier := h.notifier
	h.mu.Unlock()
	if notifier != nil {
		notifier.Send(nt)
	}
}

func (h *Host) recordBudgetCost(total float64) {
	h.mu.Lock()
	budget := h.budget
	h.mu.Unlock()
	if budget != nil {
		budget.OnCost(total)
	}
}

func (h *Host) recordBudgetMissingUsage() {
	h.mu.Lock()
	budget := h.budget
	h.mu.Unlock()
	if budget != nil {
		budget.OnMissingUsage()
	}
}


//
//
func (h *Host) PrepareUserRules(rawPrompt string) error {
	if err := h.refuseNewBookOverExisting(); err != nil {
		return err
	}
	svc := userrules.NewService(h.store, h.models.Default, rules.DefaultOptions())
	snap, err := svc.Build(context.Background(), rawPrompt)
	if err != nil {
		return fmt.Errorf("用户规则快照落盘失败，无法继续: %w", err)
	}
	logUserRulesSnapshot(snap)
	return nil
}

func (h *Host) ensureUserRules() {
	svc := userrules.NewService(h.store, h.models.Default, rules.DefaultOptions())
	snap, err := svc.GetOrBuild(context.Background())
	if err != nil {
		slog.Warn("用户规则快照读取/生成失败，运行时将退到内置默认", "module", "rules", "err", err)
		return
	}
	logUserRulesSnapshot(snap)
}

func logUserRulesSnapshot(snap *rules.Snapshot) {
	if snap == nil {
		return
	}
	slog.Info("用户规则快照",
		"module", "rules",
		"status", string(snap.Status),
		"来源", snap.Sources,
		"禁用短语", len(snap.Structured.ForbiddenPhrases),
		"疲劳词", len(snap.Structured.FatigueWords),
	)
	if snap.Status == rules.StatusDegraded {
		slog.Warn("部分规则未能解析，已按 raw preferences 运行（可重新生成快照）",
			"module", "rules", "uncertain", snap.Uncertain)
	}
}

func (h *Host) StartPrepared(rawRequirement string) error {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return fmt.Errorf("already running")
	}
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("阶段共创进行中，请先结束共创")
	}
	h.mu.Unlock()

	rawRequirement = strings.TrimSpace(rawRequirement)
	if rawRequirement == "" {
		return fmt.Errorf("prompt is required")
	}
	if err := h.refuseNewBookOverExisting(); err != nil {
		return err
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	if err := h.store.Checkpoints.Reset(); err != nil {
		return fmt.Errorf("reset checkpoints: %w", err)
	}
	if err := h.store.Progress.Init("", 0); err != nil {
		return fmt.Errorf("init progress: %w", err)
	}
	if err := h.store.RunMeta.SetStartPrompt(rawRequirement); err != nil {
		return fmt.Errorf("记录创作需求: %w", err)
	}

	start := time.Now()
	decision, derr := runObservedDecision(h.observer, "启动裁定", func() (arbiter.PlanStartDecision, error) {
		return arbiter.DecidePlanStart(h.runCtx, h.arbiterModel(),
			h.bundle.Prompts.ArbiterPlanStart, rawRequirement, h.cfg.Style)
	})
	rec := storepkg.DecisionRecord{Kind: "plan_start", Decider: "arbiter", Input: rawRequirement,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	var recErr error
	if rec, recErr = h.store.Decisions.Append(rec); recErr != nil {
		slog.Warn("启动裁定审计落盘失败", "module", "host", "err", recErr)
	}
	if derr != nil {
		return fmt.Errorf("启动裁定失败: %w", derr)
	}
	if err := h.store.RunMeta.SetPlanStart(domain.PlanStartRecord{
		RawPrompt: rawRequirement, Planner: decision.Planner, PlannerTask: decision.Task, DecisionID: rec.ID,
	}); err != nil {
		return fmt.Errorf("记录启动裁定: %w", err)
	}

	slog.Info("开始创作", "module", "host", "planner", decision.Planner)
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
		Summary: fmt.Sprintf("开始创作（规划师: %s——%s）", decision.Planner, decision.Reason), Level: "info"})
	if !h.startEngine(&flow.Instruction{Agent: decision.Planner, Task: decision.Task, Reason: decision.Reason}) {
		return fmt.Errorf("Engine 已在运行或正在停止，无法启动新书")
	}
	return nil
}

func (h *Host) refuseNewBookOverExisting() error {
	progress, err := h.store.Progress.Load()
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if progress == nil || len(progress.CompletedChapters) == 0 {
		return nil
	}
	name := strings.TrimSpace(progress.NovelName)
	if name == "" {
		name = "未定书名"
	}
	return fmt.Errorf("输出目录已有《%s》的 %d 章创作进度，新建会重置其进度与检查点：续写请走恢复入口（重启应用自动恢复），新书请更换输出目录",
		name, len(progress.CompletedChapters))
}

func (h *Host) startEngine(initial *flow.Instruction) bool {
	if active, done := imp.ResumeStatus(h.store); active && !done {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "存在未完成的外部小说导入，请先执行 /import 恢复完成后再继续创作"})
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.exclusive != "" {
		return false
	}
	if h.engine.isRunning() {
		return false
	}
	h.observer.setAborting(false)
	previous := h.lifecycle
	h.lifecycle = lifecycleRunning
	if !h.engine.start(initial) {
		h.lifecycle = previous
		return false
	}
	return true
}

func (h *Host) Reopen(direction string) error {
	h.mu.Lock()
	switch {
	case h.lifecycle == lifecycleRunning:
		h.mu.Unlock()
		return fmt.Errorf("创作引擎运行中，无需重开")
	case h.cocreating:
		h.mu.Unlock()
		return fmt.Errorf("阶段共创进行中，请先结束共创")
	case h.exclusive != "":
		ex := h.exclusive
		h.mu.Unlock()
		return fmt.Errorf("%s进行中，请先完成后再重开", ex)
	}
	h.mu.Unlock()

	if err := h.store.Progress.ReopenContinue(); err != nil {
		return err
	}
	slog.Info("重开已完结书为创作状态", "module", "host", "direction", direction)
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "已重开本书为创作状态（用户撤销完结裁定）", Level: "info"})
	if d := strings.TrimSpace(direction); d != "" {
		if err := h.store.RunMeta.SetPendingSteer(d); err != nil {
			return fmt.Errorf("已重开，但续写方向登记失败：%v，请直接在输入框重新输入方向", err)
		}
	}
	return nil
}

func (h *Host) Resume() (string, error) {
	h.mu.Lock()
	if h.lifecycle == lifecycleRunning {
		h.mu.Unlock()
		return "", fmt.Errorf("already running")
	}
	if h.cocreating {
		h.mu.Unlock()
		return "", fmt.Errorf("đồng sáng tác theo giai đoạn đang chạy, hãy kết thúc trước")
	}
	if h.exclusive != "" {
		ex := h.exclusive
		h.mu.Unlock()
		return "", fmt.Errorf("%s đang chạy, hãy hoàn tất trước khi khôi phục sáng tác", ex)
	}
	h.mu.Unlock()

	label, err := resumeLabel(h.store)
	if err != nil {
		return "", err
	}
	if label == "" {
		return "", nil
	}
	if err := h.budget.Refuse(); err != nil {
		return "", err
	}

	slog.Info("khôi phục sáng tác", "module", "host", "label", label)
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Khôi phục sáng tác: " + label, Level: "info"})
	for _, w := range h.store.CheckConsistency() {
		slog.Warn("cảnh báo nhất quán", "module", "host", "detail", w)
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "Cảnh báo nhất quán: " + w, Level: "warn"})
	}
	h.ensureUserRules()
	h.refreshWriterRestore()
	if meta, _ := h.store.RunMeta.Load(); meta != nil && meta.PendingSteer != "" {
		h.doIntervention(meta.PendingSteer, true)
		if !h.engine.isRunning() {
			if err := h.budget.Refuse(); err == nil {
				if !h.startEngine(nil) {
					return label, fmt.Errorf("Engine 正在完成上一轮停止，请稍后重试恢复")
				}
			}
		}
	} else {
		if !h.startEngine(nil) {
			return label, fmt.Errorf("Engine 正在完成上一轮停止，请稍后重试恢复")
		}
	}
	return label, nil
}

func (h *Host) handleIntervention(text string) {
	h.doIntervention(text, false)
}

func (h *Host) doIntervention(text string, restart bool) {
	h.interMu.Lock()
	defer h.interMu.Unlock()

	if err := h.store.RunMeta.SetPendingSteer(text); err != nil {
		slog.Warn("干预持久化失败(继续裁定,但崩溃保护失效)", "module", "host", "err", err)
	}
	clearPending := func() {
		if err := h.store.ClearHandledSteer(); err != nil {
			slog.Warn("清除已处理干预失败", "module", "host", "err", err)
		}
	}

	facts := arbiter.CollectInterventionFacts(h.store)
	facts.Running = h.engine.isRunning()

	start := time.Now()
	decision, derr := runObservedDecision(h.observer, "用户干预裁定", func() (arbiter.InterventionDecision, error) {
		return arbiter.DecideIntervention(h.runCtx, h.arbiterModel(),
			h.bundle.Prompts.ArbiterIntervention, facts, text)
	})

	rec := storepkg.DecisionRecord{Kind: "intervention", Decider: "arbiter", Input: text,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if cp := h.store.Checkpoints.LatestGlobal(); cp != nil {
		rec.CheckpointSeq = cp.Seq
	}
	if data, err := json.Marshal(facts); err == nil {
		rec.Facts = data
	}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	if _, err := h.store.Decisions.Append(rec); err != nil {
		slog.Warn("裁定审计落盘失败", "module", "host", "err", err)
	}

	if derr != nil {
		h.emitEvent(newInterventionFailureEvent(derr))
		clearPending()
		return
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "裁定: " + decision.Reason, Level: "info"})
	if decision.Answer != "" {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: decision.Answer, Level: "info"})
	}
	actionsFailed := false
	if decision.Rules != "" {
		if snap, _, err := h.userRules.AddRuntimeRule(h.runCtx, decision.Rules); err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "写作规则落盘失败: " + err.Error(), Level: "error"})
			actionsFailed = true
		} else if snap != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "写作规则已更新并持久化", Level: "info"})
		}
	}

	if decision.Hold != nil || decision.Reopen != nil || decision.Dispatch != nil {
		op := controlOp{hold: decision.Hold, reopen: decision.Reopen, dispatch: decision.Dispatch, text: text, facts: facts}
		if !h.engine.enqueue(op) {
			if err := h.engine.applyControlOp(context.Background(), op); err != nil {
				h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "干预动作执行失败,已保留;恢复/继续时将自动重试"})
				return
			}
			if decision.Reopen != nil || decision.Dispatch != nil {
				restart = true
			}
		}
	}
	if actionsFailed {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "部分干预动作未成功,干预已保留;恢复/继续时自动重试"})
		return
	}
	clearPending()

	if restart && !h.engine.isRunning() {
		if err := h.budget.Refuse(); err != nil {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: err.Error(), Level: "warn"})
			return
		}
		h.refreshWriterRestore()
		if !h.startEngine(nil) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "干预已生效，但 Engine 未能立即续跑；请稍后在输入框继续或重启应用恢复"})
		}
	}
}

func newInterventionFailureEvent(err error) Event {
	detail := err.Error()
	return Event{
		Time:     time.Now(),
		Category: "ERROR",
		Agent:    "arbiter",
		Summary:  "干预裁定失败：" + detail + "（未做任何修改）",
		Detail:   detail,
		Kind:     errorKind(err, detail),
		Level:    "error",
	}
}

func (h *Host) arbiterModel() agentcore.ChatModel {
	return newUsageTrackedModel(h.models.Default, "arbiter", h.usage.Record)
}

func (h *Host) Continue(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("text is required")
	}
	h.mu.Lock()
	if h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("阶段共创进行中，请先结束共创")
	}
	if h.exclusive != "" {
		ex := h.exclusive
		h.mu.Unlock()
		return fmt.Errorf("%s进行中，请先完成后再继续创作", ex)
	}
	h.mu.Unlock()
	if err := h.budget.Refuse(); err != nil {
		return err
	}

	h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[继续] " + text, Level: "info"})
	go h.doIntervention(text, true)
	return nil
}

func (h *Host) SetAdvanceMode(mode domain.ChapterAdvanceMode) error {
	h.interMu.Lock()
	defer h.interMu.Unlock()
	if err := h.store.RunMeta.SetAdvanceMode(mode); err != nil {
		return err
	}
	label := "自动推进"
	if mode == domain.ChapterAdvanceReview {
		label = "逐章验收"
	}
	summary := "章节推进模式已切换为" + label
	h.mu.Lock()
	state := h.lifecycle
	h.mu.Unlock()
	if mode == domain.ChapterAdvanceAuto && state != lifecycleRunning && state != lifecycleCompleted {
		summary += "；当前仍暂停，输入继续指令后恢复运行"
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "info"})
	return nil
}

func (h *Host) AdvanceOneChapter() error {
	h.interMu.Lock()
	defer h.interMu.Unlock()

	h.mu.Lock()
	running, cocreating, ex := h.lifecycle == lifecycleRunning, h.cocreating, h.exclusive
	h.mu.Unlock()
	if running || h.engine.isRunning() {
		return fmt.Errorf("创作仍在运行或正在完成暂停，请稍后再执行 /next")
	}
	if cocreating {
		return fmt.Errorf("阶段共创进行中，请先结束共创")
	}
	if ex != "" {
		return fmt.Errorf("%s进行中，请先完成后再执行 /next", ex)
	}
	meta, err := h.store.RunMeta.Load()
	if err != nil {
		return err
	}
	if meta == nil {
		return fmt.Errorf("RunMeta 未初始化")
	}
	if meta.AdvanceMode != domain.ChapterAdvanceReview {
		return fmt.Errorf("/next 仅用于逐章验收模式，请先执行 /review on")
	}
	if meta.AdvanceHold != nil {
		return fmt.Errorf("仍有一次性暂停意图待处理（%s），请先恢复或完成当前干预", meta.AdvanceHold.Reason)
	}
	if err := h.budget.Refuse(); err != nil {
		return err
	}
	progress, err := h.store.Progress.Load()
	if err != nil {
		return err
	}
	if progress == nil || progress.Phase != domain.PhaseWriting {
		phase := "<nil>"
		if progress != nil {
			phase = string(progress.Phase)
		}
		return fmt.Errorf("当前阶段不能授权新章（phase=%s）", phase)
	}
	target := progress.NextChapter()
	if target <= 0 {
		return fmt.Errorf("无法从当前进度推导下一章")
	}
	if err := h.store.RunMeta.GrantAdvancePermit(target); err != nil {
		return err
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
		Summary: fmt.Sprintf("已放行第 %d 章；该章提交后会先完成必要的评审与弧/卷结构维护，再次等待放行", target), Level: "info"})
	h.refreshWriterRestore()
	if !h.startEngine(nil) {
		return fmt.Errorf("章节许可已保存，但 Engine 仍在完成上一轮停止；请稍后重试 /next")
	}
	return nil
}

func (h *Host) Steer(text string) {
	h.emitEvent(Event{Time: time.Now(), Category: "USER", Summary: "[用户干预] " + text, Level: "info"})
	go h.handleIntervention(text)
}

func (h *Host) Abort() bool {
	return h.abortWithEvent("用户手动暂停当前创作", "warn")
}

func (h *Host) abortWithEvent(summary, level string) bool {
	h.mu.Lock()
	running := h.lifecycle == lifecycleRunning
	if running {
		h.lifecycle = lifecyclePaused
	}
	cancelExclusive := h.exclusiveCancel
	h.mu.Unlock()
	if running {
		h.observer.setAborting(true)
		h.engine.abort()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
		return true
	}
	if cancelExclusive != nil {
		cancelExclusive()
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
		return true
	}
	return false
}

//
func (h *Host) Close() {
	h.observer.setAborting(true)
	if h.runCancel != nil {
		h.runCancel()
	}
	h.engine.abort()
	if h.usageCancel != nil {
		h.usageCancel()
		h.usageCancel = nil
	}
	if err := h.usage.SaveNow(); err != nil {
		slog.Warn("usage 退出前落盘失败", "module", "usage", "err", err)
	}
	h.closeOnce.Do(func() {
		close(h.done)
		close(h.events)
		close(h.streamCh)
	})
}

func (h *Host) runEnded() {
	defer func() { recover() }()
	h.observer.finalize()

	h.mu.Lock()
	progress, _ := h.store.Progress.Load()
	if progress != nil && progress.Phase == domain.PhaseComplete {
		h.lifecycle = lifecycleCompleted
		summary := completionSummary(h.store)
		h.mu.Unlock()
		slog.Info(summary, "module", "host")
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "success"})
		h.sendNotification(notify.Notification{
			Kind: notify.KindRunEnd, Level: "info", Title: "ainovel: 创作完成",
			Body: h.runEndBody(progress.NovelName, summary),
		})
	} else {
		wasRunning := h.lifecycle == lifecycleRunning
		if wasRunning {
			h.lifecycle = lifecycleIdle
		}
		completed := 0
		name := ""
		if progress != nil {
			completed = len(progress.CompletedChapters)
			name = progress.NovelName
		}
		h.mu.Unlock()
		if wasRunning {
			summary := fmt.Sprintf("引擎停止 (已完成 %d 章)", completed)
			slog.Warn(summary, "module", "host")
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: "warn"})
			h.sendNotification(notify.Notification{
				Kind: notify.KindRunEnd, Level: "warn", Title: "ainovel: 创作停止",
				Body: h.runEndBody(name, summary),
			})
		}
	}

	select {
	case h.done <- struct{}{}:
	default:
	}
}

func (h *Host) runEndBody(novelName, summary string) string {
	if name := strings.TrimSpace(novelName); name != "" {
		summary = "《" + name + "》" + summary
	}
	cost, _, _, _, _ := h.usage.Totals()
	if cost > 0 {
		summary += fmt.Sprintf(" · 花费 $%.2f", cost)
	}
	return summary
}


const StreamClearSentinel = "\x00\x00CLEAR\x00\x00"

func (h *Host) Events() <-chan Event        { return h.events }
func (h *Host) Stream() <-chan string       { return h.streamCh }
func (h *Host) Done() <-chan struct{}       { return h.done }
func (h *Host) Dir() string                 { return h.store.Dir() }
func (h *Host) AskUser() *tools.AskUserTool { return h.askUser }


func (h *Host) emitEvent(ev Event) {
	defer func() { recover() }()
	if ev.Summary != "" || ev.Detail != "" {
		level := slog.LevelInfo
		switch ev.Level {
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
		msg := ev.Detail
		if msg == "" {
			msg = ev.Summary
		}
		attrs := []any{"module", "event", "category", ev.Category, "agent", ev.Agent}
		if ev.Kind != "" {
			attrs = append(attrs, "kind", ev.Kind)
		}
		slog.Log(context.Background(), level, msg, attrs...)
	}
	select {
	case h.events <- ev:
	default:
		select {
		case <-h.events:
		default:
		}
		select {
		case h.events <- ev:
		default:
		}
	}
}

func (h *Host) emitDelta(delta string) {
	defer func() { recover() }()
	select {
	case h.streamCh <- delta:
	default:
		select {
		case <-h.streamCh:
		default:
		}
		select {
		case h.streamCh <- delta:
		default:
		}
	}
}

func (h *Host) emitClear() {
	h.emitDelta(StreamClearSentinel)
}


func (h *Host) Snapshot() UISnapshot {
	h.mu.Lock()
	state := h.lifecycle
	provider, model, _ := h.models.CurrentSelection("default")
	modelWindow, _ := h.cfg.ResolveContextWindow(provider, model)
	thinkingLevel := h.cfg.ResolveReasoningEffort("default")
	style := h.cfg.Style
	h.mu.Unlock()

	cost, tokIn, tokOut, cacheRead, cacheWrite := h.usage.Totals()
	saved := h.usage.SavedUSD()
	overallCapable := h.usage.OverallCacheCapable()
	recentRead, recentInput, recentSamples := h.usage.OverallRecent()
	perAgent := h.usage.PerAgent()
	cacheStats := make([]AgentCacheStat, 0, len(perAgent))
	for _, a := range perAgent {
		cacheStats = append(cacheStats, AgentCacheStat{
			Role:            a.Role,
			Input:           a.Input,
			Output:          a.Output,
			CacheRead:       a.CacheRead,
			CacheWrite:      a.CacheWrite,
			Cost:            a.Cost,
			Saved:           a.Saved,
			CacheCapable:    a.CacheCapable,
			RecentCacheRead: a.RecentCacheRead,
			RecentInput:     a.RecentInput,
			RecentSamples:   a.RecentSamples,
		})
	}
	perModel := h.usage.PerModel()
	modelStats := make([]AgentCacheStat, 0, len(perModel))
	for _, a := range perModel {
		modelStats = append(modelStats, AgentCacheStat{
			Model:        a.Model,
			Input:        a.Input,
			Output:       a.Output,
			CacheRead:    a.CacheRead,
			CacheWrite:   a.CacheWrite,
			Cost:         a.Cost,
			Saved:        a.Saved,
			CacheCapable: a.CacheCapable,
		})
	}

	snap := UISnapshot{
		Provider:               provider,
		ModelName:              model,
		ModelContextWindow:     modelWindow,
		ThinkingLevel:          thinkingLevel,
		Style:                  style,
		RuntimeState:           string(state),
		IsRunning:              state == lifecycleRunning,
		TotalInputTokens:       tokIn,
		TotalOutputTokens:      tokOut,
		TotalCacheReadTokens:   cacheRead,
		TotalCacheWriteTokens:  cacheWrite,
		TotalCostUSD:           cost,
		TotalSavedUSD:          saved,
		BudgetLimitUSD:         h.budget.Limit(),
		OverallCacheCapable:    overallCapable,
		OverallRecentCacheRead: recentRead,
		OverallRecentInput:     recentInput,
		OverallRecentSamples:   recentSamples,
		TotalCacheBreaks:       h.usage.OverallCacheBreaks(),
		CachePerAgent:          cacheStats,
		CachePerModel:          modelStats,
		MissingAssistantUsage:  h.usage.MissingAssistantUsage(),
	}

	progress, _ := h.store.Progress.Load()
	if progress != nil {
		snap.NovelName = strings.TrimSpace(progress.NovelName)
		snap.Phase = string(progress.Phase)
		snap.Flow = string(progress.Flow)
		snap.CurrentChapter = progress.CurrentChapter
		snap.TotalChapters = progress.TotalChapters
		snap.CompletedCount = len(progress.CompletedChapters)
		snap.TotalWordCount = progress.TotalWordCount
		snap.InProgressChapter = progress.InProgressChapter
		snap.PendingRewrites = progress.PendingRewrites
		snap.RewriteReason = progress.RewriteReason
		snap.Layered = progress.Layered
		if progress.CurrentVolume > 0 {
			snap.CurrentVolumeArc = fmt.Sprintf("第%d卷·第%d弧", progress.CurrentVolume, progress.CurrentArc)
		}
	}
	if snap.NovelName == "" {
		if premise, _ := h.store.Outline.LoadPremise(); premise != "" {
			snap.NovelName = domain.ExtractNovelNameFromPremise(premise)
		}
	}
	if meta, _ := h.store.RunMeta.Load(); meta != nil {
		snap.PendingSteer = meta.PendingSteer
		snap.AdvanceMode = string(meta.AdvanceMode)
		snap.AdvancePermitChapter = meta.AdvancePermitChapter
		if meta.AdvanceHold != nil {
			snap.HasAdvanceHold = true
			snap.AdvanceHoldReason = meta.AdvanceHold.Reason
		}
	}

	snap.Agents = h.observer.agentSnapshots()
	h.fillContextStatus(&snap)
	snap.StatusLabel = deriveStatusLabel(snap)

	if label, err := resumeLabel(h.store); err == nil && label != "" {
		snap.RecoveryLabel = label
	}

	h.fillDetails(&snap, progress)

	return snap
}

func (h *Host) fillContextStatus(_ *UISnapshot) {}

func (h *Host) fillDetails(snap *UISnapshot, progress *domain.Progress) {
	if premise, _ := h.store.Outline.LoadPremise(); premise != "" {
		snap.Premise = truncate(premise, 80)
	}
	if outline, _ := h.store.Outline.LoadOutline(); len(outline) > 0 {
		for _, e := range outline {
			snap.Outline = append(snap.Outline, OutlineSnapshot{
				Chapter: e.Chapter, Title: e.Title, CoreEvent: e.CoreEvent,
			})
		}
	}
	if progress != nil && progress.Layered {
		if compass, _ := h.store.Outline.LoadCompass(); compass != nil {
			snap.CompassDirection = compass.EndingDirection
			snap.CompassScale = compass.EstimatedScale
		}
		if volumes, _ := h.store.Outline.LoadLayeredOutline(); len(volumes) > 0 {
			for _, v := range volumes {
				if v.Index > progress.CurrentVolume {
					snap.NextVolumeTitle = v.Title
					break
				}
			}
		}
	}
	if chars, _ := h.store.Characters.Load(); len(chars) > 0 {
		for _, c := range chars {
			label := c.Name
			if c.Role != "" {
				label += "（" + c.Role + "）"
			}
			snap.Characters = append(snap.Characters, label)
		}
	}
	if ledger, _ := h.store.Cast.Load(); len(ledger) > 0 {
		snap.SupportingCount = len(ledger)
		recent, _ := h.store.Cast.RecentActive(5)
		for _, e := range recent {
			label := e.Name
			if e.BriefRole != "" {
				label += "（" + e.BriefRole + "）"
			}
			snap.RecentSupporting = append(snap.RecentSupporting, label)
		}
	}
	if progress != nil && len(progress.CompletedChapters) > 0 {
		lastCh := progress.CompletedChapters[len(progress.CompletedChapters)-1]
		wc := progress.ChapterWordCounts[lastCh]
		snap.LastCommitSummary = fmt.Sprintf("Chương %d · %d chữ", lastCh, wc)
	}
	currentCh := 1
	if progress != nil && len(progress.CompletedChapters) > 0 {
		currentCh = progress.CompletedChapters[len(progress.CompletedChapters)-1]
	}
	if review, err := h.store.World.LoadLastReview(currentCh); err == nil && review != nil {
		snap.LastReviewSummary = fmt.Sprintf("verdict=%s · %d vấn đề", review.Verdict, len(review.Issues))
		if len(review.AffectedChapters) > 0 {
			snap.LastReviewSummary += fmt.Sprintf(" ảnh hưởng %v", review.AffectedChapters)
		}
	}
	if cp := h.store.Checkpoints.LatestGlobal(); cp != nil {
		snap.LastCheckpointName = fmt.Sprintf("%s.%s", cp.Scope, cp.Step)
	}
	if progress != nil {
		for i := len(progress.CompletedChapters) - 1; i >= 0 && len(snap.RecentSummaries) < 2; i-- {
			ch := progress.CompletedChapters[i]
			if summary, err := h.store.Summaries.LoadSummary(ch); err == nil && summary != nil {
				snap.RecentSummaries = append(snap.RecentSummaries,
					fmt.Sprintf("Chương %d: %s", ch, truncate(summary.Summary, 50)))
			}
		}
	}
}

func deriveStatusLabel(s UISnapshot) string {
	switch {
	case s.Phase == string(domain.PhaseComplete):
		return "COMPLETE"
	case s.Flow == string(domain.FlowReviewing):
		return "REVIEW"
	case s.Flow == string(domain.FlowRewriting) || s.Flow == string(domain.FlowPolishing):
		return "REWRITE"
	case s.RuntimeState == "running":
		return "RUNNING"
	default:
		return "READY"
	}
}


func (h *Host) ConfiguredProviders() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	providers := make([]string, 0, len(h.cfg.Providers))
	for name := range h.cfg.Providers {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	return providers
}

func (h *Host) ConfiguredModels(provider string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.CandidateModels(provider)
}

func (h *Host) CurrentModelSelection(role string) (string, string, bool) {
	return h.models.CurrentSelection(role)
}

func (h *Host) SwitchModel(role, provider, model string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if provider == "" || model == "" {
		return fmt.Errorf("provider and model are required")
	}
	if err := h.models.Swap(role, provider, model); err != nil {
		return err
	}
	if role == "" || role == "default" {
		h.cfg.Provider = provider
		h.cfg.ModelName = model
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.Provider = provider
		rc.Model = model
		h.cfg.Roles[role] = rc
	}
	if h.configPath != "" {
		if err := bootstrap.SaveConfig(h.configPath, h.cfg); err != nil {
			slog.Warn("保存配置失败", "module", "host", "err", err)
		}
	}
	h.applyThinkingLocked(role)
	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	window, source := h.cfg.ResolveContextWindow(provider, model)
	bootstrap.LogContextWindowChoice(logRole, model, window, source)


	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("模型已切换：%s → %s/%s", role, provider, model),
		Level:    "info",
	})
	return nil
}

var concreteThinkingRoles = []string{"architect", "writer", "editor"}

func (h *Host) CurrentThinking(role string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cfg.ResolveReasoningEffort(strings.ToLower(strings.TrimSpace(role)))
}

func (h *Host) AvailableThinking(role string) []agentcore.ThinkingLevel {
	h.mu.Lock()
	model := h.models.ForRole(strings.ToLower(strings.TrimSpace(role)))
	h.mu.Unlock()
	return agents.AvailableThinkingForModel(model)
}

func (h *Host) resolveThinkingForRoleLocked(role string) agentcore.ThinkingLevel {
	parsed, _ := agents.ParseThinkingLevel(h.cfg.ResolveReasoningEffort(role))
	resolved, _ := agents.ResolveThinkingForModel(h.models.ForRole(role), parsed)
	return resolved
}

func (h *Host) applyThinkingLocked(role string) {
	if h.thinkingApplier == nil {
		return
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" || role == "default" {
		for _, r := range concreteThinkingRoles {
			h.thinkingApplier(r, h.resolveThinkingForRoleLocked(r))
		}
		return
	}
	h.thinkingApplier(role, h.resolveThinkingForRoleLocked(role))
}

func (h *Host) SetRoleThinking(role, level string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	parsed, err := agents.ParseThinkingLevel(level)
	if err != nil {
		return err
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if role == "" || role == "default" {
		h.cfg.ReasoningEffort = string(parsed)
	} else {
		if h.cfg.Roles == nil {
			h.cfg.Roles = make(map[string]bootstrap.RoleConfig)
		}
		rc := h.cfg.Roles[role]
		rc.ReasoningEffort = string(parsed)
		h.cfg.Roles[role] = rc
	}
	if h.configPath != "" {
		if err := bootstrap.SaveConfig(h.configPath, h.cfg); err != nil {
			slog.Warn("保存配置失败", "module", "host", "err", err)
		}
	}

	h.applyThinkingLocked(role)

	logRole := role
	if logRole == "" {
		logRole = "default"
	}
	shown := string(parsed)
	if shown == "" {
		shown = "默认(继承)"
	}
	h.emitEvent(Event{
		Time:     time.Now(),
		Category: "SYSTEM",
		Summary:  fmt.Sprintf("推理强度已切换：%s → %s", logRole, shown),
		Level:    "info",
	})
	return nil
}


func (h *Host) ReplayQueue(afterSeq int64) ([]domain.RuntimeQueueItem, error) {
	if h.store == nil || h.store.Runtime == nil {
		return nil, nil
	}
	items, err := h.store.Runtime.LoadQueueAfter(afterSeq)
	if err != nil {
		return nil, err
	}
	for i := range items {
		sanitizeRuntimeQueueItem(&items[i])
	}
	return items, nil
}

func sanitizeRuntimeQueueItem(item *domain.RuntimeQueueItem) {
	if item == nil || item.Kind != domain.RuntimeQueueUIEvent {
		return
	}
	item.Summary = sanitizeGeneratedLabel(item.Summary)
	switch p := item.Payload.(type) {
	case Event:
		p.Summary = sanitizeGeneratedLabel(p.Summary)
		p.Detail = sanitizeGeneratedLabel(p.Detail)
		item.Payload = p
	case map[string]any:
		if s, ok := p["Summary"].(string); ok {
			p["Summary"] = sanitizeGeneratedLabel(s)
		}
		if s, ok := p["Detail"].(string); ok {
			p["Detail"] = sanitizeGeneratedLabel(s)
		}
	}
}

func sanitizeGeneratedLabel(s string) string {
	if s == "" {
		return s
	}
	replacements := []struct{ old, new string }{
		{"恢复创作", "Khôi phục sáng tác"},
		{"恢复：", "Khôi phục: "},
		{"恢复:", "Khôi phục:"},
		{" 错误:", " lỗi:"},
		{"·草稿", "·bản nháp"},
		{"·对话", "·đối thoại"},
		{"本弧", "cung này"},
		{"全局", "toàn cục"},
	}
	for _, r := range replacements {
		s = strings.ReplaceAll(s, r.old, r.new)
	}
	chapterLabelRe := regexp.MustCompile(`第\s*(\d+)\s*章`)
	s = chapterLabelRe.ReplaceAllString(s, "chương $1")
	return s
}


func (h *Host) CoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, coCreateSystemPrompt, history, onProgress)
}

func (h *Host) StageCoCreateStream(ctx context.Context, history []CoCreateMessage, onProgress func(kind, text string)) (CoCreateReply, error) {
	return coCreateStream(ctx, h.models, h.store.Sessions, stageSystemPrompt(h.store), history, onProgress)
}

const stagePlanPrefix = "[阶段规划] 我暂停创作，和共创助手一起梳理了下面的后续方向，请按你的干预分类裁定如何落地，然后继续创作。后续方向如下：\n\n"

func (h *Host) PauseForCoCreate() bool {
	h.mu.Lock()
	if h.cocreating || h.lifecycle == lifecycleCompleted {
		h.mu.Unlock()
		return false
	}
	h.cocreating = true
	running := h.lifecycle == lifecycleRunning
	h.mu.Unlock()

	if running {
		h.abortWithEvent("进入阶段共创，创作已暂停", "info")
	} else {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "进入阶段共创", Level: "info"})
	}
	return true
}

func (h *Host) ResumeFromCoCreate(draft string) error {
	draft = strings.TrimSpace(draft)
	if draft == "" {
		return fmt.Errorf("draft is required")
	}
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return fmt.Errorf("not in co-create")
	}
	h.cocreating = false
	h.mu.Unlock()

	for h.engine.isRunning() {
		time.Sleep(20 * time.Millisecond)
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "阶段共创完成，已注入后续方向并恢复创作", Level: "info"})
	return h.Continue(stagePlanPrefix + draft)
}

func (h *Host) CancelCoCreate() {
	h.mu.Lock()
	if !h.cocreating {
		h.mu.Unlock()
		return
	}
	h.cocreating = false
	h.mu.Unlock()
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "已退出阶段共创，创作保持暂停（可在输入框继续）", Level: "info"})
}


func (h *Host) refreshWriterRestore() {
	if h.writerRestore != nil {
		h.writerRestore.Refresh(h.store)
	}
}

func truncate(s string, maxRunes int) string {
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}

func (h *Host) ImportFrom(ctx context.Context, opts imp.Options) (<-chan imp.Event, error) {
	if err := h.budget.Refuse(); err != nil {
		return nil, err
	}
	if err := h.acquireExclusive("导入"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	h.mu.Lock()
	h.exclusiveCancel = cancel
	h.mu.Unlock()

	deps := imp.Deps{
		Store:         h.store,
		CommitChapter: tools.NewCommitChapterTool(h.store),
		Segment:       h.importCaller("segment"),
		Analyze:       h.importCaller("analyze"),
		Synthesize:    h.importCaller("synthesize"),
		Prompts: imp.Prompts{
			Segment:    h.bundle.Prompts.ImportSegment,
			Analyze:    h.bundle.Prompts.ImportAnalyze,
			Synthesize: h.bundle.Prompts.ImportSynthesize,
			Range:      h.bundle.Prompts.ImportRange,
		},
	}
	ch, err := imp.Run(ctx, deps, opts)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return h.superviseImport(ch, opts), nil
}

func (h *Host) ImportResumeHint() string {
	return imp.ResumeSummary(h.store)
}

func (h *Host) importCaller(fn string) imp.Caller {
	role := "import_" + fn
	if _, _, explicit := h.models.CurrentSelection(role); !explicit {
		role = "architect"
	}
	model := newUsageTrackedModel(h.models.ForRole(role), role, h.usage.Record)
	return imp.Caller{Model: model, Runtime: h.importModelRuntime(role, model)}
}

func (h *Host) importModelRuntime(role string, model agentcore.ChatModel) imp.ModelRuntime {
	var rt imp.ModelRuntime
	provider, name, _ := h.models.CurrentSelection(role)
	if name == "" {
		name = bootstrap.ModelName(model)
		provider = bootstrap.ModelProvider(model)
	}
	rt.ContextTokens, _ = h.cfg.ResolveContextWindow(provider, name)
	if entry, ok := modelreg.DefaultRegistry().Resolve(name); ok {
		rt.MaxOutputTokens = entry.MaxTokens
	}
	if level, err := agents.ParseThinkingLevel(h.cfg.ResolveReasoningEffort(role)); err == nil {
		if resolved, ok := agents.ResolveThinkingForModel(model, level); ok {
			rt.Thinking = resolved
		}
	}
	return rt
}

func (h *Host) Simulate(ctx context.Context) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("生成仿写画像"); err != nil {
		return nil, err
	}

	wd, err := os.Getwd()
	if err != nil {
		h.releaseExclusive()
		return nil, fmt.Errorf("get working dir: %w", err)
	}
	deps := sim.Deps{
		Store: h.store,
		LLM:   h.models.ForRole("architect"),
		Prompts: sim.Prompts{
			Source: h.bundle.Prompts.SimulationSource,
			Merge:  h.bundle.Prompts.SimulationMerge,
		},
	}
	ch, err := sim.Run(ctx, deps, sim.Options{SourceDir: filepath.Join(wd, "simulate")})
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}

func (h *Host) ImportSimulationProfile(ctx context.Context, path string) (<-chan sim.Event, error) {
	if err := h.acquireExclusive("导入仿写画像"); err != nil {
		return nil, err
	}
	ch, err := sim.RunImport(ctx, h.store, path)
	if err != nil {
		h.releaseExclusive()
		return nil, err
	}
	return superviseExclusive(h, ch), nil
}

func (h *Host) acquireExclusive(action string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch {
	case h.lifecycle == lifecycleRunning || h.engine.isRunning():
		return fmt.Errorf("创作引擎运行中或正在停止，请稍候再%s", action)
	case h.cocreating:
		return fmt.Errorf("阶段共创进行中，请先结束共创后再%s", action)
	case h.exclusive != "":
		return fmt.Errorf("%s进行中，请先完成后再%s", h.exclusive, action)
	}
	h.exclusive = action
	return nil
}

func (h *Host) releaseExclusive() {
	h.mu.Lock()
	cancel := h.exclusiveCancel
	h.exclusive = ""
	h.exclusiveCancel = nil
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func superviseExclusive[T any](h *Host, src <-chan T) <-chan T {
	out := make(chan T, 32)
	go func() {
		defer close(out)
		defer h.releaseExclusive()
		for ev := range src {
			out <- ev
		}
	}()
	return out
}

func (h *Host) superviseImport(src <-chan imp.Event, opts imp.Options) <-chan imp.Event {
	out := make(chan imp.Event, 32)
	go func() {
		defer close(out)
		released := false
		release := func() {
			if !released {
				released = true
				h.releaseExclusive()
			}
		}
		defer release()
		for ev := range src {
			if ev.Stage == imp.StageDone {
				release()
				ev.Continued = h.continueAfterImport(opts)
			}
			out <- ev
		}
	}()
	return out
}

func (h *Host) continueAfterImport(opts imp.Options) bool {
	want := opts.ContinueAfter
	if !want {
		if in, err := imp.OpenWorkspace(h.store.Dir()).LoadIntent(); err == nil && in != nil {
			want = in.ContinueAfterImport
		}
	}
	if !want {
		return false
	}
	meta, err := h.store.RunMeta.Load()
	if err != nil || meta == nil {
		slog.Warn("导入自动接力读取 RunMeta 失败", "module", "host", "err", err)
		return false
	}
	if meta.AdvanceMode != domain.ChapterAdvanceAuto {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
			Summary: "导入完成；当前为逐章验收模式，输入继续或 /next 接力续写"})
		return false
	}
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info", Summary: "导入完成，自动接力续写"})
	if !h.startEngine(nil) {
		h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
			Summary: "自动接力启动失败，请输入继续指令手动恢复"})
		return false
	}
	return true
}

//
func (h *Host) Export(ctx context.Context, opts exp.Options) (*exp.Result, error) {
	return exp.Run(ctx, exp.Deps{Store: h.store}, opts)
}
