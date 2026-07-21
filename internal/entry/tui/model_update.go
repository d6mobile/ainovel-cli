package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/utils"
)

const maxPromptEventCols = 160

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// body height depends on the live top/bottom bar height (new-page mode bar and multiline input can both change it),
	// we sync it before every message so the viewport does not stick to an old height and leave blank space at the bottom; this is idempotent and cheap.
	if m.width > 0 {
		m.updateViewportSize()
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizeTextarea()
		m.updateViewportSize()
		m.refreshDetailViewport()
		m.refreshStateViewport()
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.MouseMsg:
		return m.handleMouseMsg(msg)
	default:
		if next, cmd, handled := m.handleRuntimeMsg(msg); handled {
			return next, cmd
		}
		return m.handleTextareaMsg(msg)
	}
}

func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if next, cmd, handled := m.handleOverlayKeyMsg(msg); handled {
		return next, cmd
	}

	if msg.Type == tea.KeyCtrlC {
		if m.quitPending {
			return m, tea.Quit
		}
		m.quitPending = true
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return quitResetMsg{} })
	}
	m.quitPending = false

	if next, cmd, handled := m.handleCommandPaletteKey(msg); handled {
		return next, cmd
	}

	return m.handleBaseKeyMsg(msg)
}

func (m Model) handleOverlayKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch {
	case m.askState != nil:
		return m.handleBlockingModalKey(msg, m.handleAskUserKey)
	case m.cocreate != nil:
		return m.handleBlockingModalKey(msg, m.handleCoCreateKey)
	case m.modelConfig != nil:
		return m.handleBlockingModalKey(msg, m.handleModelConfigKey)
	case m.help != nil:
		return m.handleBlockingModalKey(msg, m.handleHelpKey)
	case m.modelSwitch != nil:
		return m.handleBlockingModalKey(msg, m.handleModelSwitchKey)
	case m.report != nil:
		return m.handleBlockingModalKey(msg, m.handleReportKey)
	case m.importer != nil:
		return m.handleBlockingModalKey(msg, m.handleImportKey)
	case m.simulator != nil:
		return m.handleBlockingModalKey(msg, m.handleSimulationKey)
	default:
		return m, nil, false
	}
}

func (m Model) handleBlockingModalKey(msg tea.KeyMsg, next func(tea.KeyMsg) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd, bool) {
	if msg.Type == tea.KeyCtrlC {
		if m.quitPending {
			return m, tea.Quit, true
		}
		m.quitPending = true
		return m, tea.Tick(time.Second, func(time.Time) tea.Msg { return quitResetMsg{} }), true
	}
	m.quitPending = false
	// Global cross-modal shortcut: mouse reporting must still toggle while a modal is open, otherwise co-create/help/report, etc.
	// In a lock-screen modal, the user cannot use native drag selection to copy.
	if msg.Type == tea.KeyCtrlR {
		next, cmd := m.toggleMouseReporting()
		return next, cmd, true
	}
	model, cmd := next(msg)
	return model, cmd, true
}

// toggleMouseReporting toggles mouse reporting. Off -> on lets the user copy with native drag selection;
// on -> off restores click-to-focus / wheel behavior. the base path and blocking-modal path share this code.
func (m Model) toggleMouseReporting() (Model, tea.Cmd) {
	// the welcome page (modeNew) already keeps mouse reporting off, so native drag selection can copy; ignore Ctrl+R here,
	// to avoid accidentally enabling reporting and breaking native copy. Mouse reporting is enabled by enterRunning when entering the workbench.
	if m.mode == modeNew {
		return m, nil
	}
	m.mouseOff = !m.mouseOff
	if m.mouseOff {
		return m, tea.DisableMouse
	}
	return m, tea.EnableMouseCellMotion
}

// donePlaceholder is the input placeholder for the done state: it is shared by in-session completion (doneMsg) and restarting a completed book (bootstrap).
const donePlaceholder = "Sáng tác đã hoàn thành · Có thể nhập yêu cầu làm lại (ví dụ \"viết lại chương 3\"), /reopen viết tiếp quyển mới, /export xuất"

// enterRunning enters the creative workbench: it enables mouse reporting (the workbench needs click-to-switch panels / wheel scrolling /
// dragging the sidebar). the caller must Batch the returned command into the final result.
func (m *Model) enterRunning() tea.Cmd {
	m.mode = modeRunning
	m.mouseOff = false
	return tea.EnableMouseCellMotion
}

func (m Model) handleCommandPaletteKey(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	if !m.compActive {
		return m, nil, false
	}

	switch msg.Type {
	case tea.KeyEsc:
		m.clearCommandPalette()
		return m, nil, true
	case tea.KeyUp:
		if m.compIdx > 0 {
			m.compIdx--
		}
		return m, nil, true
	case tea.KeyDown:
		if m.compIdx < len(m.compItems)-1 {
			m.compIdx++
		}
		return m, nil, true
	case tea.KeyTab:
		m.acceptCommandCompletion()
		return m, nil, true
	case tea.KeyEnter:
		item, ok := m.acceptCommandCompletion()
		if !ok {
			return m, nil, true
		}
		if item.AutoExecute {
			m.textarea.Reset()
			next, cmd := m.handleSlashCommand(slashCommand{name: item.Name})
			return next, cmd, true
		}
		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m Model) handleBaseKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Throttle defense: pasted \\n in a terminal without bracketed paste degrades into a stream of KeyEnter events;
	// a real Enter key press is usually more than 100ms after the previous character; under 50ms is very likely a paste fragment.
	// Only KeyRunes (character input) count — function keys (↑↓/Tab/Ctrl-x) should not affect throttling,
	// otherwise Enter right after browsing history could be swallowed.
	if msg.Type == tea.KeyRunes {
		m.lastKeyAt = time.Now()
	}
	switch msg.Type {
	case tea.KeyEscape:
		if m.mode == modeRunning && m.snapshot.IsRunning {
			return m, abortRuntime(m.runtime)
		}
		m.textarea.Reset()
		m.historyIdx = len(m.inputHistory)
		m.historyDraft = ""
		m.refitTextareaHeight()
		m.clearCommandPalette()
		return m, nil
	case tea.KeyCtrlL:
		m.resetOutputPanels()
		return m, nil
	case tea.KeyCtrlU:
		// Clear the current input and exit history browsing.
		m.textarea.Reset()
		m.historyIdx = len(m.inputHistory)
		m.historyDraft = ""
		m.refitTextareaHeight()
		m.clearCommandPalette()
		return m, nil
	case tea.KeyCtrlR:
		return m.toggleMouseReporting()
	case tea.KeyTab:
		if m.mode == modeNew {
			if m.cocreate != nil {
				return m, nil
			}
			if m.startupMode == startupModeQuick {
				m.startupMode = startupModeCoCreate
			} else {
				m.startupMode = startupModeQuick
			}
			m.textarea.Placeholder = placeholderForNewMode(m.startupMode)
			return m, nil
		}
		m.focusPane = (m.focusPane + 1) % focusPaneCount
		return m, nil
	case tea.KeyEnter:
		// Alt+Enter inserts a line break on purpose and lets textarea.Update handle it (KeyMap.InsertNewline is already bound to this key).
		if msg.Alt {
			break
		}
		// If the gap from the previous non-Enter key is too short, treat it as a pasted \n fragment:
		// replace it with a space to preserve visual spacing, matching cleanHumanKeyRunes semantics ("abc\ndef" -> "abc def").
		// Defend against terminal environments where bracketed paste fails (old SSH / some tmux configs).
		if !m.lastKeyAt.IsZero() && time.Since(m.lastKeyAt) < 50*time.Millisecond {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
			m.refitTextareaHeight()
			return m, cmd
		}
		return m.handleEnterKey()
	case tea.KeyUp:
		// Multiline input: let textarea handle cursor movement within the line (handled by textarea.Update after the switch).
		if m.textareaIsMultiline() {
			break
		}
		// Single line: prefer history navigation; if no history is available, fall back to event-stream scrolling.
		if m.tryHistoryUp() {
			return m, nil
		}
		return m.handleVerticalScrollKey(msg, true)
	case tea.KeyDown:
		if m.textareaIsMultiline() {
			break
		}
		if m.tryHistoryDown() {
			return m, nil
		}
		return m.handleVerticalScrollKey(msg, false)
	case tea.KeyPgUp:
		return m.handleVerticalScrollKey(msg, true)
	case tea.KeyPgDown:
		return m.handleVerticalScrollKey(msg, false)
	case tea.KeyEnd:
		switch m.focusPane {
		case focusStream:
			m.streamScroll = true
			m.streamVP.GotoBottom()
		case focusDetail:
			m.detailVP.GotoBottom()
		case focusState:
			m.stateVP.GotoBottom()
		default:
			m.autoScroll = true
			m.viewport.GotoBottom()
		}
		return m, nil
	}

	if msg.Type == tea.KeyRunes && (containsSGRFragment(string(msg.Runes)) || isCSILeak(msg.Runes)) {
		return m, nil
	}
	var ok bool
	if msg, ok = cleanHumanKeyRunes(msg); !ok {
		return m, nil
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.refitTextareaHeight()
	m.updateCommandPalette()
	return m, cmd
}

func (m Model) handleEnterKey() (tea.Model, tea.Cmd) {
	text := utils.CleanInputLine(m.textarea.Value())
	if text == "" {
		return m, nil
	}
	m.clearCommandPalette()
	if cmd, ok := parseSlashCommand(text); ok {
		m.pushInputHistory(text)
		m.textarea.Reset()
		m.refitTextareaHeight()
		return m.handleSlashCommand(cmd)
	}

	m.pushInputHistory(text)
	m.textarea.Reset()
	m.refitTextareaHeight()
	switch m.mode {
	case modeNew:
		m.err = nil
		if m.startupMode == startupModeQuick {
			plan, err := startup.PrepareQuick(startup.Request{
				Mode:        startup.ModeQuick,
				UserPrompt:  text,
				OutputDir:   m.runtime.Dir(),
				Interactive: true,
			})
			if err != nil {
				m.err = err
				return m, nil
			}
			cmd := m.enterStarting(plan.RawPrompt)
			return m, tea.Batch(startRuntime(m.runtime, plan), cmd)
		}
		m.cocreate = newCoCreateState(text)
		return m, m.sendCoCreate()
	case modeRunning:
		// Do not locally echo USER events — the Host.Continue/Steer entry points already emit "USER" events,
		// and they flow back into the TUI through the events channel. Architecture §2.3: the observation layer only observes; it does not create facts.
		if !m.snapshot.IsRunning {
			return m, continueRuntime(m.runtime, text)
		}
		return m, steerRuntime(m.runtime, text)
	case modeDone:
		// After completion, user input (rewrite / continue requests) wakes a new run. Continue uses Inject while the engine is stopped
		// to auto-resume; Arbiter decides on user intervention; when rewriting already-written chapters, the Engine restarts the book and queues it.
		// Switch back to modeRunning and re-enter the workbench; when this round finishes,
		// doneMsg(complete) will set modeDone again. Slash commands are handled above and do not reach this branch.
		m.mode = modeRunning
		return m, continueRuntime(m.runtime, text)
	default:
		return m, nil
	}
}

func (m Model) handleVerticalScrollKey(msg tea.KeyMsg, upward bool) (tea.Model, tea.Cmd) {
	if m.focusPane == focusStream {
		if upward {
			m.streamScroll = false
		}
		var cmd tea.Cmd
		m.streamVP, cmd = m.streamVP.Update(msg)
		if !upward && m.streamVP.AtBottom() {
			m.streamScroll = true
		}
		return m, cmd
	}
	if m.focusPane == focusDetail {
		var cmd tea.Cmd
		m.detailVP, cmd = m.detailVP.Update(msg)
		return m, cmd
	}
	if m.focusPane == focusState {
		var cmd tea.Cmd
		m.stateVP, cmd = m.stateVP.Update(msg)
		return m, cmd
	}
	if upward {
		m.autoScroll = false
	}
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	if !upward && m.viewport.AtBottom() {
		m.autoScroll = true
	}
	return m, cmd
}

func (m Model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if m.cocreate != nil {
		// Route mouse events by X coordinate: the left half of the screen is the conv panel, the right half is the prompt panel.
		// the modal is centered and conv takes about 58% of the left side, so using the screen midpoint is accurate enough.
		// Scrolling the wheel in the conv area automatically stops follow so the user can keep the view stable on a specific history position.
		var cmd tea.Cmd
		if msg.X < m.width/2 {
			m.cocreate.convFollow = false
			m.cocreate.convVP, cmd = m.cocreate.convVP.Update(msg)
			if m.cocreate.convVP.AtBottom() {
				m.cocreate.convFollow = true
			}
		} else {
			m.cocreate.promptVP, cmd = m.cocreate.promptVP.Update(msg)
		}
		return m, cmd
	}
	if m.modelSwitch != nil || m.modelConfig != nil || m.askState != nil {
		return m, nil
	}
	if pane, ok := m.paneAtMouse(msg.X, msg.Y); ok {
		m.hoverPane = pane
		m.hoverActive = true
		if msg.Action == tea.MouseActionPress {
			m.focusPane = pane
		}
	} else {
		m.hoverActive = false
	}

	var cmd tea.Cmd
	if m.focusPane == focusStream {
		m.streamVP, cmd = m.streamVP.Update(msg)
		if msg.Action == tea.MouseActionPress {
			m.streamScroll = m.streamVP.AtBottom()
		}
		return m, cmd
	}
	if m.focusPane == focusDetail {
		m.detailVP, cmd = m.detailVP.Update(msg)
		return m, cmd
	}
	if m.focusPane == focusState {
		m.stateVP, cmd = m.stateVP.Update(msg)
		return m, cmd
	}
	m.viewport, cmd = m.viewport.Update(msg)
	if msg.Action == tea.MouseActionPress {
		m.autoScroll = m.viewport.AtBottom()
	}
	return m, cmd
}

func (m Model) handleRuntimeMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case eventMsg:
		ev := host.Event(msg)
		m.applyEvent(ev)
		m.refreshEventViewport()
		return m, listenEvents(m.runtime), true
	case bootstrapMsg:
		// Replay historical events before handling errors: a rejected Resume (for example due to budget limits) is the normal path,
		// and the user needs to see the rejection reason with the history still visible, not an empty event stream.
		m.applyRuntimeReplay(msg.replay)
		if msg.err != nil {
			m.err = msg.err
			return m, fetchSnapshot(m.runtime), true
		}
		// modeNew: bootstrap resume / import completion lands here; modeDone: /reopen returns to the creative workbench.
		if msg.resumed && (m.mode == modeNew || m.mode == modeDone) {
			enableMouse := m.enterRunning()
			m.resizeTextarea()
			m.textarea.Placeholder = defaultSteerPlaceholder()
			return m, tea.Batch(fetchSnapshot(m.runtime), enableMouse), true
		}
		// Completed books land in the done-state workbench (enterRunning enables mouse reporting and then switches to modeDone), not the welcome page —
		// the welcome page says nothing about existing books, so the user would think the book vanished; /reopen, /export, and rewrite input all live in the workbench.
		if msg.completed && m.mode == modeNew {
			enableMouse := m.enterRunning()
			m.mode = modeDone
			m.resizeTextarea()
			m.textarea.Placeholder = donePlaceholder
			return m, tea.Batch(fetchSnapshot(m.runtime), enableMouse, m.textarea.Focus()), true
		}
		return m, fetchSnapshot(m.runtime), true
	case askUserMsg:
		m.askState = newAskUserState(askUserRequest(msg))
		m.textarea.Blur()
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "SYSTEM", Summary: "Đang chờ người dùng bổ sung thông tin then chốt", Level: "info",
		})
		m.refreshEventViewport()
		return m, listenAskUser(m.askBridge), true
	case snapshotMsg:
		m.snapshot = host.UISnapshot(msg)
		m.syncRuntimePlaceholder()
		m.refreshEventViewport()
		m.refreshStreamViewport()
		m.refreshDetailViewport()
		m.refreshStateViewport()
		return m, tickSnapshot(m.runtime), true
	case doneMsg:
		m.snapshot.IsRunning = false
		m.refreshEventViewport()
		m.refreshStreamViewport()
		m.refreshStateViewport()
		if msg.complete {
			m.abortPending = false
			m.mode = modeDone
			// the done state does not lock the input box: automatic continuation stops, but the user can still enter rewrite requests (modeDone input is
			// handled by Continue to wake a new run; Arbiter decides between rewrite or continued creation; /export and /model
			// commands also need to remain available, so the input box must stay focused (issues #27 and #38).
			m.textarea.Placeholder = donePlaceholder
			return m, tea.Batch(fetchSnapshot(m.runtime), listenDone(m.runtime), m.textarea.Focus()), true
		}
		if m.abortPending {
			m.abortPending = false
			m.snapshot.RuntimeState = "paused"
			m.syncRuntimePlaceholder()
		} else {
			m.textarea.Placeholder = "Phiên chạy bị gián đoạn, nhập bất kỳ nội dung nào để khôi phục sáng tác"
		}
		return m, tea.Batch(fetchSnapshot(m.runtime), listenDone(m.runtime)), true
	case abortResultMsg:
		if msg.stopped {
			m.abortPending = true
			m.textarea.Placeholder = "Đang tạm dừng sáng tác..."
		}
		return m, nil, true
	case reportLoadedMsg:
		if m.report == nil || msg.reqID != m.report.reqID {
			return m, nil, true
		}
		boxW, _ := reportModalSize(m.width, m.height)
		m.report.load(msg.report, paddedModalContentWidth(boxW), msg.exportPath, msg.finishedAt)
		return m, nil, true
	case importEventMsg:
		if m.importer == nil || msg.reqID != m.importer.reqID {
			return m, nil, true
		}
		boxW, _ := reportModalSize(m.width, m.height)
		m.importer.appendEvent(msg.ev, paddedModalContentWidth(boxW))
		if msg.ev.Stage == imp.StageError {
			return m, nil, true
		}
		if msg.ev.Stage == imp.StageDone {
			if msg.ev.Continued {
				// the host has truly started Engine auto-handoff (Continued is set by the host as the source of truth, not guessed by the TUI).
				// Closing the panel lands in the workbench, where the always-on listenEvents/listenDone from Init carry engine events and tickSnapshot refreshes the running state.
				m.importer = nil
				enableMouse := m.enterRunning()
				m.resizeTextarea()
				m.textarea.Placeholder = defaultSteerPlaceholder()
				return m, tea.Batch(enableMouse, m.textarea.Focus()), true
			}
			// No handoff (default / review / handoff failed): stay on the panel and let the user review the foundation and chapters; Esc closes it.
			return m, nil, true
		}
		return m, listenImportEvent(msg.reqID, msg.ch), true
	case importClosedMsg:
		// If the channel closes before a terminal state, the pipeline stops at awaiting (waiting for --yes / --story). Mark the panel as closable,
		// otherwise Esc only cancels an already-finished ctx and the panel can never be closed (stuck).
		if m.importer == nil || msg.reqID != m.importer.reqID || m.importer.done {
			return m, nil, true
		}
		m.importer.paused = true
		boxW, _ := reportModalSize(m.width, m.height)
		m.importer.refresh(paddedModalContentWidth(boxW))
		return m, nil, true
	case simEventMsg:
		if m.simulator == nil || msg.reqID != m.simulator.reqID {
			return m, nil, true
		}
		boxW, _ := reportModalSize(m.width, m.height)
		m.simulator.appendEvent(msg.ev, paddedModalContentWidth(boxW))
		if msg.terminal() {
			return m, nil, true
		}
		return m, listenSimulationEvent(msg.reqID, msg.ch), true
	case exportDoneMsg:
		if msg.err != nil {
			m.applyEvent(host.Event{
				Time: time.Now(), Category: "ERROR", Summary: "Xuất thất bại: " + msg.err.Error(), Level: "error",
			})
		} else if msg.result != nil {
			m.applyEvent(host.Event{
				Time: time.Now(), Category: "SYSTEM", Summary: formatExportSuccess(msg.result), Level: "success",
			})
		}
		m.refreshEventViewport()
		return m, nil, true
	case modelConfigSavedMsg:
		if m.modelConfig == nil {
			return m, nil, true
		}
		if msg.err != nil {
			m.modelConfig.saving = false
			m.modelConfig.message = msg.err.Error()
			return m, nil, true
		}
		m.modelConfig = nil
		return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus()), true
	case startResultMsg:
		next, cmd := m.handleStartResultMsg(msg)
		return next, cmd, true
	case cocreateDeltaMsg:
		if m.cocreate == nil || msg.reqID != m.cocreate.reqID {
			return m, nil, true
		}
		m.cocreate.applyDelta(msg.kind, msg.text)
		return m, listenCoCreateDelta(m.cocreate), true
	case cocreateDoneMsg:
		next, cmd := m.handleCoCreateDoneMsg(msg)
		return next, cmd, true
	case steerResultMsg:
		return m, tea.Batch(fetchSnapshot(m.runtime), listenDone(m.runtime)), true
	case continueResultMsg:
		if msg.err != nil {
			m.err = msg.err
			m.applyEvent(host.Event{
				Time: time.Now(), Category: "ERROR", Summary: msg.err.Error(), Level: "error",
			})
			m.refreshEventViewport()
			return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus()), true
		}
		m.err = nil
		m.textarea.Placeholder = defaultSteerPlaceholder()
		return m, tea.Batch(fetchSnapshot(m.runtime), listenDone(m.runtime), m.textarea.Focus()), true
	case spinnerTickMsg:
		m.spinnerIdx = (m.spinnerIdx + 1) % len(spinnerFrames)
		if m.snapshot.IsRunning {
			// Visual refreshes for the star / top-bar spinner go through here (350ms)
			m.refreshEventViewport()
		}
		return m, tickSpinner(), true
	case toolSpinnerTickMsg:
		m.toolSpinnerIdx = (m.toolSpinnerIdx + 1) % len(toolSpinnerFrames)
		// Spinner refresh for the event-stream "running" row (150ms, independent cadence).
		// Arbiter can handle Continue/queries while the Engine is stopped, so we cannot use snapshot.IsRunning
		// as the animation prerequisite; refresh whenever any call-like running event exists. If none exist, skip the full rerender.
		if m.hasRunningEvent() {
			m.refreshEventViewport()
		}
		return m, tickToolSpinner(), true
	case cursorTickMsg:
		m.cursorIdx++
		if m.snapshot.IsRunning {
			// Cursor blinking needs a full rerender of the stream panel (the cursor sits at the end of content);
			// clear dirty at the same time so the following flush tick does not rerender again.
			m.refreshStreamViewport()
			m.streamDirty = false
		}
		if s := m.importer; s != nil && !s.done && !s.paused {
			// Import running: the trailing star and retry countdown are both in the viewport content and are recomputed on each tick.
			// Tied to the cursor tick (120ms) so it stays in sync with the stream-panel cursor — matching stars should not move at different speeds.
			s.frame = m.cursorIdx
			boxW, _ := reportModalSize(m.width, m.height)
			s.refresh(paddedModalContentWidth(boxW))
		}
		return m, tickCursor(), true
	case streamDeltaMsg:
		if len(m.streamRounds) == 0 {
			m.streamRounds = append(m.streamRounds, "")
		}
		m.streamRounds[len(m.streamRounds)-1] += string(msg)
		// Do not refreshStreamViewport immediately; streamFlushTick merges updates at 60fps.
		// During fast LLM streaming, there can be dozens of tokens per second; refreshing each one would mean dozens of full rerenders of 32 segments per second.
		m.streamDirty = true
		return m, listenStream(m.runtime), true
	case streamClearMsg:
		// Round boundary: flush the accumulated delta first so the new round aligns visually
		if m.flushStreamIfDirty() && m.streamScroll {
			m.streamVP.GotoBottom()
		}
		if len(m.streamRounds) == 0 {
			m.streamRounds = append(m.streamRounds, "")
		} else if strings.TrimSpace(m.streamRounds[len(m.streamRounds)-1]) != "" {
			m.streamRounds = append(m.streamRounds, "")
		}
		m.trimStreamRounds()
		m.streamRound = len(m.streamRounds)
		m.refreshStreamViewport()
		if m.streamScroll {
			m.streamVP.GotoBottom()
		}
		return m, listenStream(m.runtime), true
	case streamFlushTickMsg:
		if m.flushStreamIfDirty() && m.streamScroll {
			m.streamVP.GotoBottom()
		}
		return m, tickStreamFlush(), true
	case quitResetMsg:
		m.quitPending = false
		return m, nil, true
	default:
		return m, nil, false
	}
}

func (m Model) handleStartResultMsg(msg startResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err
		wasStarting := m.starting
		m.starting = false
		if m.mode != modeNew {
			m.applyEvent(host.Event{
				Time: time.Now(), Category: "ERROR", Summary: msg.err.Error(), Level: "error",
			})
			m.refreshEventViewport()
		}
		if m.cocreate != nil {
			m.cocreate.awaiting = false
			m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
			return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus())
		}
		if wasStarting {
			// After Enter already moved into the workbench, the LLM error during startup is shown in the current workbench,
			// and we no longer fall back to the welcome page.
			m.mode = modeRunning
			m.snapshot.IsRunning = false
			m.snapshot.RuntimeState = "idle"
			m.textarea.Placeholder = "Khởi động thất bại, hãy kiểm tra cấu hình mô hình hoặc dùng /model để chuyển mô hình"
			m.refreshStreamViewport()
			m.refreshStateViewport()
			return m, m.textarea.Focus()
		}
		if m.mode == modeNew {
			m.textarea.Placeholder = placeholderForNewMode(m.startupMode)
			return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus())
		}
		return m, fetchSnapshot(m.runtime)
	}
	m.starting = false

	if m.mode == modeNew {
		m.cocreate = nil
		enableMouse := m.enterRunning()
		m.resizeTextarea()
		m.textarea.Placeholder = defaultSteerPlaceholder()
		return m, tea.Batch(fetchSnapshot(m.runtime), m.textarea.Focus(), enableMouse)
	}

	return m, fetchSnapshot(m.runtime)
}

func (m *Model) enterStarting(rawPrompt string) tea.Cmd {
	m.cocreate = nil
	m.err = nil
	m.starting = true
	m.snapshot.IsRunning = true
	m.snapshot.RuntimeState = "running"
	enableMouse := m.enterRunning()
	m.resetOutputPanels()
	m.resizeTextarea()
	m.textarea.Placeholder = "Đang khởi tạo sáng tác..."
	m.applyStartupPromptEvent(rawPrompt)
	m.applyEvent(host.Event{
		Time: time.Now(), Category: "SYSTEM", Summary: "Đang khởi tạo sáng tác", Level: "info",
	})
	m.refreshEventViewport()
	m.refreshStreamViewport()
	m.refreshStateViewport()
	return tea.Batch(m.textarea.Focus(), enableMouse)
}

func (m *Model) applyStartupPromptEvent(rawPrompt string) {
	text := utils.CleanInputLine(rawPrompt)
	if text == "" {
		return
	}
	m.applyEvent(host.Event{
		Time:     time.Now(),
		Category: "USER",
		Summary:  "Yêu cầu sáng tác: " + truncate(text, maxPromptEventCols),
		Detail:   text,
		Level:    "info",
	})
}

func (m Model) handleCoCreateDoneMsg(msg cocreateDoneMsg) (tea.Model, tea.Cmd) {
	if m.cocreate == nil || msg.reqID != m.cocreate.reqID {
		return m, nil
	}
	if msg.err != nil {
		m.err = msg.err
		m.cocreate.awaiting = false
		m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
		return m, m.textarea.Focus()
	}
	m.err = nil
	m.cocreate.apply(msg.reply)
	m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
	return m, m.textarea.Focus()
}

func (m Model) handleTextareaMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.refitTextareaHeight()
	m.updateCommandPalette()
	return m, cmd
}

// applyEvent applies a single event to m.events:
// - If the event has an ID and already exists -> update in place (merge done-state fields while keeping the first Time / Summary)
// - New event -> append it and record it in eventIndex when needed
// - If maxEvents is exceeded, slide-trim and rebuild the index
func (m *Model) applyEvent(ev host.Event) {
	if ev.ID != "" {
		if idx, ok := m.eventIndex[ev.ID]; ok && idx >= 0 && idx < len(m.events) {
			existing := &m.events[idx]
			if !ev.FinishedAt.IsZero() {
				existing.FinishedAt = ev.FinishedAt
			}
			if ev.Duration > 0 {
				existing.Duration = ev.Duration
			}
			if ev.Failed {
				existing.Failed = true
			}
			if ev.Level != "" {
				existing.Level = ev.Level
			}
			// Allow Summary to override when non-empty (the final state may carry extra info); otherwise keep the first one.
			if ev.Summary != "" {
				existing.Summary = ev.Summary
			}
			// Retry events with the same ID update across attempts, and the new deadline must follow so the countdown resets correctly.
			if !ev.RetryAt.IsZero() {
				existing.RetryAt = ev.RetryAt
			}
			return
		}
	}

	m.events = append(m.events, ev)
	if ev.ID != "" {
		m.eventIndex[ev.ID] = len(m.events) - 1
	}
	if len(m.events) > maxEvents {
		drop := len(m.events) - maxEvents
		m.events = m.events[drop:]
		m.rebuildEventIndex()
	}
}

// trimStreamRounds truncates streamRounds to maxStreamRounds segments; overflow is dropped from the front.
// Call this after each new streamClear round starts and after replay has loaded all historical items.
func (m *Model) trimStreamRounds() {
	if len(m.streamRounds) <= maxStreamRounds {
		return
	}
	drop := len(m.streamRounds) - maxStreamRounds
	m.streamRounds = m.streamRounds[drop:]
}

func (m *Model) rebuildEventIndex() {
	m.eventIndex = make(map[string]int, len(m.events))
	for i, e := range m.events {
		if e.ID != "" {
			m.eventIndex[e.ID] = i
		}
	}
}

func (m *Model) resetOutputPanels() {
	m.events = nil
	m.eventIndex = make(map[string]int)
	m.viewport.SetContent("")
	m.viewport.GotoTop()
	m.streamBuf.Reset()
	m.streamRounds = nil
	m.streamVP.SetContent("")
	m.streamVP.GotoTop()
	m.streamRound = 0
}

func (m *Model) applyRuntimeReplay(items []domain.RuntimeQueueItem) {
	for _, item := range items {
		switch item.Kind {
		case domain.RuntimeQueueUIEvent:
			// the event stream is not replayed: the queue only contains done-state events, and Agent/Depth/Duration/Level
			// fields needed for rendering are not restored by replay, so the resulting rows are incomplete. Empty is better than half-baked data.
			continue
		case domain.RuntimeQueueStreamClear:
			if len(m.streamRounds) == 0 {
				m.streamRounds = append(m.streamRounds, "")
			} else if strings.TrimSpace(m.streamRounds[len(m.streamRounds)-1]) != "" {
				m.streamRounds = append(m.streamRounds, "")
			}
		case domain.RuntimeQueueStreamDelta:
			text := host.ReplayDeltaText(item)
			if text == "" {
				continue
			}
			if len(m.streamRounds) == 0 {
				m.streamRounds = append(m.streamRounds, "")
			}
			m.streamRounds[len(m.streamRounds)-1] += text
		}
	}
	m.trimStreamRounds()
	m.streamRound = len(m.streamRounds)
	m.refreshEventViewport()
	m.refreshStreamViewport()
}
