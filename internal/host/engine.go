package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/subagent"

	"github.com/voocel/ainovel-cli/internal/arbiter"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/flow"
	"github.com/voocel/ainovel-cli/internal/notify"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
	"github.com/voocel/ainovel-cli/internal/tools"
)

type engine struct {
	store   *storepkg.Store
	workers *subagent.Tool

	arbiterModel    agentcore.ChatModel
	failurePrompt   string
	planStartPrompt string
	style           string
	reconsult func(text string)

	observer  *observer
	budget    *BudgetSentinel
	gate      *ChapterAdvanceGate
	refresh   func()
	emitEvent func(Event)
	notify    func(kind, level, title, body string)
	onPause   func(summary string)
	onDone    func()

	mu      sync.Mutex
	cancel  context.CancelFunc
	running bool
	pending []controlOp
	next    *flow.Instruction
	deferGateForNext bool

	lastKey string
	repeats int
	failedKey string
}

const (
	deadlockConsultAt = 3
	deadlockAbortAt   = 5
)

type controlOp struct {
	hold     *arbiter.AdvanceHoldOp
	reopen   *arbiter.ReopenOp
	dispatch *arbiter.DispatchOp
	text     string
	facts    arbiter.InterventionFacts
}

func (e *engine) start(initial *flow.Instruction) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	ctx = agentcore.WithToolProgress(ctx, e.observer.workerProgress)
	e.cancel = cancel
	e.running = true
	if initial != nil {
		e.next = initial
		e.deferGateForNext = false
	}
	e.lastKey, e.repeats, e.failedKey = "", 0, ""
	go e.run(ctx)
	return true
}

func (e *engine) abort() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *engine) isRunning() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running
}

func (e *engine) enqueue(op controlOp) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.running {
		return false
	}
	e.pending = append(e.pending, op)
	return true
}

func (e *engine) run(ctx context.Context) {
	defer func() {
		e.mu.Lock()
		e.running = false
		e.cancel = nil
		leftover := e.pending
		e.pending = nil
		e.mu.Unlock()
		for _, op := range leftover {
			if op.dispatch != nil {
				if op.text != "" {
					if err := e.store.RunMeta.SetPendingSteer(op.text); err != nil {
						slog.Warn("残留干预回存失败", "module", "engine", "err", err)
					}
				}
				e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "引擎已停,裁定派单未执行;干预已保留,继续创作时自动重新裁定"})
				op.dispatch = nil
			}
			if op.hold != nil || op.reopen != nil {
				_ = e.applyControlOp(context.Background(), op)
			}
		}
		e.onDone()
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		deferGate := e.applyPendingOps(ctx) || e.nextDefersGate()
		if !deferGate {
			if e.gate.HandleBoundary() {
				return
			}
		}

		inst := e.takeNext()
		if inst == nil {
			inst = flow.Route(flow.LoadState(e.store))
		}
		if inst == nil {
			inst = e.planStartFallback(ctx)
		}
		if inst == nil {
			return
		}
		if replaced := e.precheck(inst); replaced != nil {
			inst = replaced
		}
		allowed, gateErr := e.gate.Allow(inst)
		if gateErr != nil {
			e.pauseWithNotify(notify.KindAdvanceGate, "章节推进控制错误，已暂停: "+gateErr.Error())
			return
		}
		if !allowed {
			return
		}
		if stop := e.trackDeadlock(ctx, &inst); stop {
			return
		}
		if inst == nil {
			continue
		}

		err := e.runWorker(ctx, inst)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			if stop := e.handleWorkerError(ctx, inst, err); stop {
				return
			}
		}

		if e.currentBudget().HandleBoundary() {
			return
		}
		if e.gate.HandleBoundary() {
			return
		}
	}
}

func (e *engine) takeNext() *flow.Instruction {
	e.mu.Lock()
	defer e.mu.Unlock()
	inst := e.next
	e.next = nil
	e.deferGateForNext = false
	return inst
}

func (e *engine) nextDefersGate() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.next != nil && e.deferGateForNext
}

func (e *engine) setBudget(budget *BudgetSentinel) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.budget = budget
}

func (e *engine) currentBudget() *BudgetSentinel {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.budget
}

func (e *engine) planStartFallback(ctx context.Context) *flow.Instruction {
	progress, err := e.store.Progress.Load()
	if err != nil || progress == nil {
		return nil
	}
	if progress.Phase == domain.PhaseWriting || progress.Phase == domain.PhaseComplete {
		return nil
	}
	meta, err := e.store.RunMeta.Load()
	if err != nil || meta == nil || meta.PlanningTier != "" {
		return nil
	}
	if len(e.store.FoundationMissing()) == 0 {
		return nil
	}
	if meta.PlanStart != nil {
		return &flow.Instruction{
			Agent:  meta.PlanStart.Planner,
			Task:   meta.PlanStart.PlannerTask,
			Reason: "按已固化的启动裁定开始规划",
		}
	}
	if meta.StartPrompt == "" {
		return nil
	}
	return e.retryPlanStart(ctx, meta.StartPrompt)
}

func (e *engine) retryPlanStart(ctx context.Context, prompt string) *flow.Instruction {
	start := time.Now()
	decision, derr := runObservedDecision(e.observer, "启动补裁", func() (arbiter.PlanStartDecision, error) {
		return arbiter.DecidePlanStart(ctx, e.arbiterModel, e.planStartPrompt, prompt, e.style)
	})
	rec := storepkg.DecisionRecord{Kind: "plan_start", Decider: "arbiter", Input: prompt,
		Reason: decision.Reason, DurationMs: time.Since(start).Milliseconds()}
	if derr == nil {
		if data, err := json.Marshal(decision); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	rec, recErr := e.store.Decisions.Append(rec)
	if recErr != nil {
		slog.Warn("启动补裁审计落盘失败", "module", "engine", "err", recErr)
	}
	if derr != nil {
		e.pauseWithNotify(notify.KindPlanStart, "启动裁定失败,已暂停(请检查模型/网络配置后继续): "+truncate(derr.Error(), 200))
		return nil
	}
	if err := e.store.RunMeta.SetPlanStart(domain.PlanStartRecord{
		RawPrompt: prompt, Planner: decision.Planner, PlannerTask: decision.Task, DecisionID: rec.ID,
	}); err != nil {
		e.pauseWithNotify(notify.KindPlanStart, "启动裁定无法落盘,已暂停: "+err.Error())
		return nil
	}
	e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "info",
		Summary: fmt.Sprintf("启动裁定已补齐(规划师: %s——%s)", decision.Planner, decision.Reason)})
	return &flow.Instruction{Agent: decision.Planner, Task: decision.Task, Reason: decision.Reason}
}

func (e *engine) precheck(inst *flow.Instruction) *flow.Instruction {
	progress, _ := e.store.Progress.Load()
	if progress != nil && progress.Phase == domain.PhaseComplete {
		slog.Warn("完本期派发被丢弃", "module", "engine", "agent", inst.Agent)
		return &flow.Instruction{}
	}
	if inst.Agent == "writer" {
		if ch := writerTargetChapter(e.store); ch > 0 {
			if err := tools.EnsureChapterExpanded(e.store, ch); err != nil {
				return &flow.Instruction{
					Agent:  "architect_long",
					Task:   fmt.Sprintf("下一弧为骨架(%s)。调用 save_foundation(type=expand_arc) 展开下一弧;若当前卷已写完,改用 type=append_volume 追加并展开下一卷。", err),
					Reason: "写作目标章未展开,先展开再续写",
				}
			}
		}
		e.refresh()
	}
	return nil
}

func writerTargetChapter(st *storepkg.Store) int {
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return 0
	}
	if len(progress.PendingRewrites) > 0 {
		return progress.PendingRewrites[0]
	}
	return progress.NextChapter()
}

func (e *engine) trackDeadlock(ctx context.Context, inst **flow.Instruction) (stop bool) {
	in := *inst
	if in == nil || in.Agent == "" {
		*inst = nil
		return false
	}
	key := in.Agent + "\x00" + in.Task
	if key == e.lastKey {
		e.repeats++
	} else {
		e.lastKey, e.repeats = key, 1
	}
	if e.repeats < deadlockConsultAt {
		return false
	}
	if e.repeats >= deadlockAbortAt {
		e.pauseWithNotify(notify.KindDeadlock, fmt.Sprintf("僵局熔断: 指令连续 %d 次无进展(%s),已暂停等待人工介入", e.repeats, in.Agent))
		return true
	}
	facts := e.failureFacts("deadlock", in, "")
	decision, err := runObservedDecision(e.observer, "僵局裁定", func() (arbiter.FailureDecision, error) {
		return arbiter.DecideFailure(ctx, e.arbiterModel, e.failurePrompt, facts)
	})
	e.recordFailureDecision("deadlock", in, facts, decision, err)
	if err != nil {
		e.pauseWithNotify(notify.KindDeadlock, "僵局裁定失败,已暂停等待人工介入: "+err.Error())
		return true
	}
	switch decision.Action {
	case "retry":
		return false
	case "reroute":
		*inst = &flow.Instruction{Agent: decision.Dispatch.Agent, Task: decision.Dispatch.Task, Reason: decision.Reason}
		return false
	default: // abort
		e.pauseWithNotify(notify.KindDeadlock, "僵局裁定: "+decision.Reason)
		return true
	}
}

func (e *engine) runWorker(ctx context.Context, inst *flow.Instruction) error {
	slog.Info("engine 派发", "module", "engine", "agent", inst.Agent, "reason", inst.Reason)
	e.observer.dispatchStart(inst.Agent, inst.Task)
	if inst.Agent == "writer" && inst.Chapter > 0 {
		if err := e.store.Progress.ValidateChapterWork(inst.Chapter); err != nil {
			e.observer.dispatchFinish(inst.Agent, true)
			return fmt.Errorf("%w: %w", errInvalidWriteTarget, err)
		}
		if err := e.store.Progress.StartChapter(inst.Chapter); err != nil {
			slog.Warn("预标进行中失败", "module", "engine", "chapter", inst.Chapter, "err", err)
		}
	}

	runCtx := agentcore.WithToolProgress(ctx, func(p agentcore.ProgressPayload) {
		e.observer.workerProgress(p)
	})
	_, err := e.workers.Run(runCtx, inst.Agent, inst.Task)
	if err == nil {
		e.failedKey = ""
	}
	e.observer.dispatchFinish(inst.Agent, err != nil)
	return err
}

func (e *engine) handleWorkerError(ctx context.Context, inst *flow.Instruction, werr error) (stop bool) {
	msg := werr.Error()
	e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Agent: inst.Agent,
		Summary: truncate(fmt.Sprintf("%s 失败: %s", inst.Agent, msg), 120), Detail: msg, Level: "error"})

	if isDeterministicWorkerError(werr) {
		e.pauseWithNotify(notify.KindWorkerFailure, "确定性错误(重试无意义),已暂停等待人工介入: "+truncate(msg, 200))
		return true
	}

	key := inst.Agent + "\x00" + inst.Task
	if e.failedKey != key {
		e.failedKey = key
		return false
	}
	e.failedKey = ""
	facts := e.failureFacts("worker_failure", inst, msg)
	decision, err := runObservedDecision(e.observer, "失败裁定", func() (arbiter.FailureDecision, error) {
		return arbiter.DecideFailure(ctx, e.arbiterModel, e.failurePrompt, facts)
	})
	e.recordFailureDecision("worker_failure", inst, facts, decision, err)
	if err != nil {
		e.pauseWithNotify(notify.KindWorkerFailure, "失败裁定不可用,已暂停等待人工介入: "+msg+contentFilterAdvice(werr))
		return true
	}
	switch decision.Action {
	case "retry":
		return false
	case "reroute":
		e.mu.Lock()
		e.next = &flow.Instruction{Agent: decision.Dispatch.Agent, Task: decision.Dispatch.Task, Reason: decision.Reason}
		e.deferGateForNext = false
		e.mu.Unlock()
		return false
	default: // abort
		e.pauseWithNotify(notify.KindWorkerFailure, "失败裁定: "+decision.Reason+contentFilterAdvice(werr))
		return true
	}
}

func contentFilterAdvice(werr error) string {
	if !errors.Is(werr, agentcore.ErrProviderContentFilter) {
		return ""
	}
	return "。这是服务商内容审核拦截(非本地错误),可选: /model 切到无审核层的服务商后输入「继续」;或修改本章草稿(drafts/)措辞后再继续;原样重试大概率仍被拦"
}

var errInvalidWriteTarget = errors.New("非法写作目标")

func isDeterministicWorkerError(err error) bool {
	return errors.Is(err, subagent.ErrUnknownAgent) || errors.Is(err, errInvalidWriteTarget)
}

func (e *engine) failureFacts(kind string, inst *flow.Instruction, errMsg string) arbiter.FailureFacts {
	f := arbiter.FailureFacts{Kind: kind, Agent: inst.Agent, Task: inst.Task, Error: errMsg, Repeats: e.repeats}
	f.FoundationGap = e.store.FoundationMissing()
	if p, err := e.store.Progress.Load(); err == nil && p != nil {
		f.Phase = string(p.Phase)
		f.NextChapter = p.NextChapter()
		f.PendingQueue = p.PendingRewrites
	}
	return f
}

func (e *engine) recordFailureDecision(kind string, inst *flow.Instruction, facts arbiter.FailureFacts, d arbiter.FailureDecision, derr error) {
	rec := storepkg.DecisionRecord{Kind: kind, Decider: "arbiter", Input: inst.Agent + ": " + inst.Task, Reason: d.Reason}
	if data, err := json.Marshal(facts); err == nil {
		rec.Facts = data
	}
	if derr == nil {
		if data, err := json.Marshal(d); err == nil {
			rec.Decision = data
		}
	} else {
		rec.Error = derr.Error()
	}
	if _, err := e.store.Decisions.Append(rec); err != nil {
		slog.Warn("裁定审计落盘失败", "module", "engine", "kind", kind, "err", err)
	}
}

func (e *engine) applyPendingOps(ctx context.Context) (deferGate bool) {
	for {
		e.mu.Lock()
		ops := e.pending
		e.pending = nil
		e.mu.Unlock()
		if len(ops) == 0 {
			return deferGate
		}
		for _, op := range ops {
			pairedHoldDispatch := op.hold != nil && !op.hold.Cancel && op.dispatch != nil
			err := e.applyControlOp(ctx, op)
			if err != nil {
				if op.text != "" {
					if serr := e.store.RunMeta.SetPendingSteer(op.text); serr != nil {
						slog.Warn("干预回存失败", "module", "engine", "err", serr)
					}
				}
				e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
					Summary: "干预动作执行失败,已保留;恢复/继续时自动重试"})
			} else if pairedHoldDispatch && e.nextDefersGate() {
				deferGate = true
			}
		}
	}
}

func (e *engine) applyControlOp(ctx context.Context, op controlOp) error {
	var firstErr error
	fail := func(err error) {
		if firstErr == nil {
			firstErr = err
		}
	}
	if op.dispatch != nil {
		fresh := arbiter.CollectInterventionFacts(e.store)
		if fresh.Phase != op.facts.Phase || fresh.Flow != op.facts.Flow ||
			fresh.QueueHead() != op.facts.QueueHead() {
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Level: "warn",
				Summary: "裁定派单已过时(事实推进),以最新事实重新裁定"})
			e.recordStale(op)
			if op.text != "" && e.reconsult != nil {
				e.reconsult(op.text)
			}
			return nil
		}
	}
	if op.hold != nil {
		if op.hold.Cancel {
			meta, err := e.store.RunMeta.Load()
			if err != nil {
				e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "读取一次性暂停失败: " + err.Error(), Level: "error"})
				return err
			}
			if meta != nil && meta.AdvanceHold != nil {
				if err := e.store.RunMeta.ClearAdvanceHold(*meta.AdvanceHold); err != nil {
					e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "取消一次性暂停失败: " + err.Error(), Level: "error"})
					return err
				}
			}
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "已取消一次性暂停", Level: "info"})
		} else {
			hold := domain.AdvanceHold{After: op.hold.After, Reason: op.hold.Reason}
			if err := e.store.RunMeta.SetAdvanceHold(hold); err != nil {
				e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "设置一次性暂停失败: " + err.Error(), Level: "error"})
				return err
			}
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: "已设置一次性暂停: " + op.hold.Reason, Level: "info"})
		}
	}
	if op.reopen != nil {
		args, _ := json.Marshal(map[string]any{"chapters": op.reopen.Chapters, "reason": op.reopen.Reason})
		if _, err := tools.NewReopenBookTool(e.store).Execute(ctx, args); err != nil {
			e.emitEvent(Event{Time: time.Now(), Category: "ERROR", Summary: "重开返工失败: " + err.Error(), Level: "error"})
			fail(err)
		} else {
			e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM",
				Summary: fmt.Sprintf("已重开全书返工: 第 %v 章入队", op.reopen.Chapters), Level: "info"})
		}
	}
	if op.dispatch != nil {
		e.mu.Lock()
		e.next = &flow.Instruction{Agent: op.dispatch.Agent, Task: op.dispatch.Task, Reason: "用户干预裁定"}
		e.deferGateForNext = op.hold != nil && !op.hold.Cancel
		e.mu.Unlock()
	}
	return firstErr
}

func (e *engine) recordStale(op controlOp) {
	rec := storepkg.DecisionRecord{Kind: "decision_stale", Decider: "engine", Input: op.text}
	if data, err := json.Marshal(op.facts); err == nil {
		rec.Facts = data
	}
	if _, err := e.store.Decisions.Append(rec); err != nil {
		slog.Warn("stale 记录失败", "module", "engine", "err", err)
	}
}

func (e *engine) pauseWithNotify(kind, body string) {
	e.notify(kind, "warn", "ainovel: 引擎暂停", body)
	if e.onPause != nil {
		e.onPause(body)
		return
	}
	e.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: body, Level: "warn"})
	e.abort()
}

func completionSummary(st *storepkg.Store) string {
	progress, err := st.Progress.Load()
	if err != nil || progress == nil {
		return "创作完成"
	}
	var b strings.Builder
	name := progress.NovelName
	if name == "" {
		name = "本书"
	}
	fmt.Fprintf(&b, "《%s》创作完成: 共 %d 章 %d 字", name, len(progress.CompletedChapters), progress.TotalWordCount)
	return b.String()
}
