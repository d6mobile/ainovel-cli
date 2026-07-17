package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/tools"
	"github.com/voocel/ainovel-cli/internal/utils"
)

const maxEvents = 500

// maxStreamRounds 。 LLM call  streamClear
// ， writer  3~5 （agent header /  / draft / commit），32
//
//	6~10 Đầu ra。 commit  store/drafts，
//	token delta  O() 。 512KB，Thấp。
const maxStreamRounds = 32

type focusPane int

const (
	focusEvents focusPane = iota
	focusStream
	focusDetail
	focusState // （）

	focusPaneCount // ，Tab
)

type appMode int

const (
	modeNew     appMode = iota // ChờĐầu vào
	modeRunning                // （，Đầu vàoKhôi phục）
	modeDone                   // Hoàn tất
)

// /  spinner （bubbles.Spinner.MiniDot）。
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// " Đang chạy" spinner （bubbles.Spinner.Dot）。
// 7  + 1  3×3 ，。
//   - tick，。
var toolSpinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

// Model  TUI 。
type Model struct {
	runtime        *host.Host
	askBridge      *askUserBridge
	askState       *askUserState
	cocreate       *cocreateState
	help           *helpState
	modelSwitch    *modelSwitchState
	modelConfig    *modelConfigState
	report         *reportState
	version        string
	importer       *importState
	importSeq      int
	simulator      *simulationState
	simSeq         int
	compItems      []commandPaletteItem
	compIdx        int
	compActive     bool
	snapshot       host.UISnapshot
	events         []host.Event
	eventIndex     map[string]int   // event.ID → m.events ；
	viewport       viewport.Model   //  viewport
	streamVP       viewport.Model   // Đầu ra viewport
	detailVP       viewport.Model   //  viewport
	stateVP        viewport.Model   //  viewport（）
	streamBuf      *strings.Builder //
	streamRounds   []string
	textarea       textarea.Model
	width          int
	height         int
	autoScroll     bool
	streamScroll   bool      // Tự động
	streamDirty    bool      // streamRounds  delta； streamFlushTick 60fps
	lastKeyAt      time.Time //  Enter ；KeyEnter  \n
	inputHistory   []string  // Đã nộpĐầu vào（：）
	historyIdx     int       // Hiện tại；== len(inputHistory) "，"
	historyDraft   string    // ，Khôi phục
	focusPane      focusPane
	hoverPane      focusPane
	hoverActive    bool
	mode           appMode
	starting       bool // UI ，Host Khởi tạo
	startupMode    startupMode
	importHint     string // Hoàn tất（；）
	cocreateSeq    int
	reportSeq      int
	err            error
	spinnerIdx     int
	toolSpinnerIdx int  //  Đang chạy（150ms tick，/）
	cursorIdx      int  // （ tick）
	streamRound    int  // Đầu ra
	quitPending    bool //  Ctrl+C
	abortPending   bool // Chờ Done
	mouseOff       bool // true ，Trung bình；Khôi phục
}

// NewModel  TUI Model。
func NewModel(rt *host.Host, bridge *askUserBridge, version string) Model {
	ta := textarea.New()
	ta.Placeholder = placeholderForNewMode(startupModeQuick)
	ta.CharLimit = 5000
	ta.SetHeight(1)
	// MaxHeight=6 Đầu vàorộngTự động wrap （ 6 ）。
	ta.MaxHeight = 6
	ta.ShowLineNumbers = false
	ta.Focus()

	// Mặc định Enter （ handleEnterKey ）；
	//  ctrl+j（unix \n） alt+enter（GUI ）。
	// Giao thức Shift+Enter  Enter， Shift+Enter。
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "alt+enter")

	vp := viewport.New(80, 20)
	vp.SetContent("")

	svp := viewport.New(80, 10)
	svp.SetContent("")

	dvp := viewport.New(40, 20)
	dvp.SetContent("")

	stvp := viewport.New(32, 20)
	stvp.SetContent("")

	// Hoàn tất（LoadState  digest，）；
	// ，（RFC §18.2）。
	importHint := ""
	if rt != nil {
		importHint = rt.ImportResumeHint()
	}

	return Model{
		runtime:      rt,
		askBridge:    bridge,
		version:      strings.TrimSpace(version),
		autoScroll:   true,
		streamScroll: true,
		mode:         modeNew,
		startupMode:  startupModeQuick,
		importHint:   importHint,
		textarea:     ta,
		viewport:     vp,
		streamVP:     svp,
		detailVP:     dvp,
		stateVP:      stvp,
		streamBuf:    &strings.Builder{},
		eventIndex:   make(map[string]int),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		listenEvents(m.runtime),
		listenAskUser(m.askBridge),
		listenDone(m.runtime),
		listenStream(m.runtime),
		tickSnapshot(m.runtime),
		bootstrapRuntime(m.runtime),
		tickSpinner(),
		tickToolSpinner(),
		tickCursor(),
		tickStreamFlush(),
	)
}

func (m *Model) paneAtMouse(x, y int) (focusPane, bool) {
	if m.width == 0 || m.height == 0 {
		return focusEvents, false
	}

	topH, _, bodyH := m.layoutHeights()
	if bodyH < 1 {
		return focusEvents, false
	}

	bodyStartY := topH
	bodyEndY := topH + bodyH
	if y < bodyStartY || y >= bodyEndY {
		return focusEvents, false
	}

	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	centerStartX := leftW
	rightStartX := m.width - rightW

	if x >= rightStartX {
		return focusDetail, true
	}
	if x < centerStartX {
		return focusState, true
	}

	eventH, _ := m.splitHeights(bodyH)
	if y-bodyStartY < eventH {
		return focusEvents, true
	}
	return focusStream, true
}

func (m *Model) paneHighlighted(pane focusPane) bool {
	if m.focusPane == pane {
		return true
	}
	return m.hoverActive && m.hoverPane == pane
}

// hasRunningEvent Hoàn tất（spinner ）。
// toolSpinnerTick ： running  spinner Đầu ra，
//
//	refreshEventViewport 。
func (m *Model) hasRunningEvent() bool {
	for i := range m.events {
		if m.events[i].Running() {
			return true
		}
	}
	return false
}

// flushStreamIfDirty  streamRounds  viewport；mark 。
// ， GotoBottom。
func (m *Model) flushStreamIfDirty() bool {
	if !m.streamDirty {
		return false
	}
	m.refreshStreamViewport()
	m.streamDirty = false
	return true
}

// refreshEventViewport x viewport。
func (m *Model) refreshEventViewport() {
	centerW := m.eventFlowWidth()
	content := renderEventContent(m.events, centerW, m.toolSpinnerIdx)
	snap := m.snapshot
	if m.starting {
		snap.IsRunning = true
	}
	if activity := renderEventActivity(snap, m.spinnerIdx, centerW); activity != "" {
		if strings.TrimSpace(content) != "" {
			content += "\n" + activity
		} else {
			content = activity
		}
	}
	m.viewport.SetContent(content)
	if m.autoScroll {
		m.viewport.GotoBottom()
	}
}

func (m *Model) refreshStreamViewport() {
	cursor := ""
	if m.snapshot.IsRunning {
		cursor = renderStreamCursor(m.cursorIdx)
	}
	m.streamVP.SetContent(renderStreamContent(m.streamRounds, m.streamVP.Width, cursor))
}

func (m *Model) refreshDetailViewport() {
	rightW := m.detailWidth()
	if rightW <= 4 {
		return
	}
	m.detailVP.SetContent(renderDetailContent(m.snapshot, rightW-4))
}

// refreshStateViewport  viewport。
//
//	snapshot ，。
func (m *Model) refreshStateViewport() {
	leftW := m.sidebarWidth()
	if leftW <= 4 {
		return
	}
	m.stateVP.SetContent(renderStateContent(m.snapshot, leftW-4))
}

// updateViewportSize Hiện tại viewport 。
func (m *Model) updateViewportSize() {
	centerW := m.eventFlowWidth()
	rightW := m.detailWidth()
	bodyH := m.bodyHeight()
	eventH, streamH := m.splitHeights(bodyH)
	m.viewport.Width = centerW - 2
	m.viewport.Height = eventH - 1 // -1  event panel header
	m.streamVP.Width = centerW - 2
	m.streamVP.Height = streamH - 1 // -1  stream panel header
	m.detailVP.Width = rightW - 2
	m.detailVP.Height = bodyH
	leftW := m.sidebarWidth()
	m.stateVP.Width = max(1, leftW-2)
	m.stateVP.Height = max(1, bodyH-1) // -1 ，
	// Cao，（bubbles
	// SetContent ），viewport 。SetYOffset 。
	m.stateVP.SetYOffset(m.stateVP.YOffset)
	m.detailVP.SetYOffset(m.detailVP.YOffset)
}

// splitHeights Đầu raCao。
func (m *Model) splitHeights(bodyH int) (eventH, streamH int) {
	eventH = bodyH * 40 / 100
	if eventH < 3 {
		eventH = 3
	}
	streamH = bodyH - eventH - 1 // -1
	if streamH < 3 {
		streamH = 3
	}
	return
}

func (m *Model) inputWidth() int {
	if m.width == 0 {
		return 60
	}
	return m.width - 6 // border + padding +  "❯ "
}

func (m *Model) currentInputWidth() int {
	if m.cocreate != nil {
		return coCreateInputWidth(m.width, m.height)
	}
	return m.inputWidth()
}

// refitTextareaHeight Hiện tại， SetHeight。
//
//	= （\n ）rộng wrap 。 MaxHeight=6
//
// "/Tự động， 6 "。
func (m *Model) refitTextareaHeight() {
	w := m.textarea.Width()
	if w <= 0 {
		return
	}
	//  input  1  dòng: textarea  textarea
	// 。 inputBox Cao， conversation 、
	// input ，。
	if m.cocreate != nil {
		m.textarea.SetHeight(1)
		return
	}
	text := m.textarea.Value()
	if text == "" {
		m.textarea.SetHeight(1)
		return
	}
	//  2 （textarea  prompt symbol + cursor）， 1 。
	contentW := w - 2
	if contentW < 1 {
		contentW = 1
	}
	total := 0
	for line := range strings.SplitSeq(text, "\n") {
		lw := lipgloss.Width(line)
		if lw == 0 {
			total++
			continue
		}
		total += (lw + contentW - 1) / contentW
	}
	if total < 1 {
		total = 1
	}
	m.textarea.SetHeight(total) // SetHeight  MaxHeight clamp
}

// resizeTextarea xrộngCao。
//
//	SetWidth(currentInputWidth()) ，rộngCao。
func (m *Model) resizeTextarea() {
	m.textarea.SetWidth(m.currentInputWidth())
	m.refitTextareaHeight()
}

// maxInputHistory ，。
const maxInputHistory = 200

// pushInputHistory ，。。
func (m *Model) pushInputHistory(text string) {
	if text == "" {
		return
	}
	if n := len(m.inputHistory); n == 0 || m.inputHistory[n-1] != text {
		m.inputHistory = append(m.inputHistory, text)
		if len(m.inputHistory) > maxInputHistory {
			m.inputHistory = m.inputHistory[len(m.inputHistory)-maxInputHistory:]
		}
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""
}

// tryHistoryUp ；。
// Hiện tại textarea  draft，Khôi phục。
// （ textarea ）。
func (m *Model) tryHistoryUp() bool {
	if len(m.inputHistory) == 0 || m.historyIdx <= 0 {
		return false
	}
	if m.historyIdx == len(m.inputHistory) {
		m.historyDraft = m.textarea.Value()
	}
	m.historyIdx--
	m.textarea.SetValue(m.inputHistory[m.historyIdx])
	m.textarea.CursorEnd()
	m.refitTextareaHeight()
	return true
}

// tryHistoryDown ；Khôi phục draft。
func (m *Model) tryHistoryDown() bool {
	if m.historyIdx >= len(m.inputHistory) {
		return false
	}
	m.historyIdx++
	if m.historyIdx == len(m.inputHistory) {
		m.textarea.SetValue(m.historyDraft)
		m.historyDraft = ""
	} else {
		m.textarea.SetValue(m.inputHistory[m.historyIdx])
	}
	m.textarea.CursorEnd()
	m.refitTextareaHeight()
	return true
}

// textareaIsMultiline Hiện tại textarea ； ↑↓ 。
func (m *Model) textareaIsMultiline() bool {
	return strings.Contains(m.textarea.Value(), "\n")
}

// inputHints Hiện tại。
//
//	copySuffix，Trung bình；
//
// ，Khôi phục。
func (m *Model) inputHints() string {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	if m.quitPending {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Bold(true).Render("Press Ctrl+C again to exit")
	}
	limitHint := m.inputLimitHint()
	// (modeNew)，， Ctrl+R ；
	// ， Ctrl+R Tắt。
	suffix := limitHint + " · Ctrl+R chuyển sang chế độ chọn để sao chép"
	if m.mode == modeNew {
		suffix = limitHint
	}
	if m.mouseOff && m.mode != modeNew {
		// Trung bình：Hiện tại"Trung bình"， Ctrl+R Khôi phục
		return lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render("✂ Chế độ chọn để sao chép: kéo để chọn văn bản và sao chép · Ctrl+R thoát và khôi phục tương tác chuột")
	}
	if m.cocreate != nil {
		scrollHint := " · Tab cuộn: hội thoại"
		if m.cocreate.focusPrompt {
			scrollHint = " · Tab cuộn: chỉ dẫn sáng tác"
		}
		switch {
		case m.cocreate.awaiting:
			return dimStyle.Render("Chờ AI phản hồi · Esc thoát đồng sáng tác" + scrollHint + suffix)
		case m.cocreate.canStart():
			startLabel := "Ctrl+S bắt đầu sáng tác"
			if m.cocreate.stage {
				startLabel = "Ctrl+S áp dụng và tiếp tục"
			}
			return dimStyle.Render("Enter Gửi · " + startLabel + " · Esc thoát đồng sáng tác" + scrollHint + suffix)
		default:
			return dimStyle.Render("Enter Gửi · Esc thoát đồng sáng tác" + scrollHint + suffix)
		}
	}
	if m.mode == modeNew {
		if m.startupMode == startupModeQuick {
			return dimStyle.Render("Tab đổi chế độ khởi động · Nhập / để tìm lệnh · Enter bắt đầu sáng tác ngay · Esc xóa nhập liệu" + suffix)
		}
		return dimStyle.Render("Tab đổi chế độ khởi động · Nhập / để tìm lệnh · Enter bắt đầu hội thoại đồng sáng tác · Esc xóa nhập liệu" + suffix)
	}
	switch m.snapshot.RuntimeState {
	case "pausing":
		return dimStyle.Render("Đang tạm dừng sáng tác · Vui lòng chờ lượt hiện tại kết thúc" + suffix)
	case "paused":
		return dimStyle.Render("Nhập / để tìm lệnh · Enter tiếp tục sáng tác · Esc xóa nhập liệu" + suffix)
	}
	return dimStyle.Render("Nhập / để tìm lệnh · Nhấp/Tab đổi bảng · ↑↓ cuộn · End xuống cuối · Ctrl+L xóa màn hình · Esc tạm dừng · Enter gửi" + suffix)
}

func (m *Model) inputLimitHint() string {
	limit := m.textarea.CharLimit
	if limit <= 0 {
		return ""
	}
	used := m.textarea.Length()
	if used < limit*4/5 {
		return ""
	}
	return fmt.Sprintf(" · Đã nhập %d/%d", used, limit)
}

func (m *Model) eventFlowWidth() int {
	if m.width == 0 {
		return 80
	}
	leftW := m.sidebarWidth()
	rightW := m.detailWidth()
	return m.width - leftW - rightW
}

func (m *Model) sidebarWidth() int {
	if m.width == 0 {
		return 32
	}
	return m.width * 23 / 100
}

func (m *Model) detailWidth() int {
	if m.width == 0 {
		return 40
	}
	return m.width * 27 / 100
}

func (m *Model) bodyHeight() int {
	_, _, bodyH := m.layoutHeights()
	return bodyH
}

func (m *Model) currentSpinnerFrame() string {
	if !m.snapshot.IsRunning && !m.starting {
		return ""
	}
	return spinnerFrames[m.spinnerIdx%len(spinnerFrames)]
}

func (m *Model) outputDir() string {
	if m.runtime == nil {
		return ""
	}
	return m.runtime.Dir()
}

func defaultSteerPlaceholder() string {
	return "Nhập can thiệp cốt truyện, ví dụ: đưa tuyến tình cảm lên chương 4"
}

func (m *Model) syncRuntimePlaceholder() {
	if m.mode != modeRunning || m.cocreate != nil {
		return
	}
	if m.starting {
		m.textarea.Placeholder = "Đang khởi tạo sáng tác..."
		return
	}
	switch m.snapshot.RuntimeState {
	case "completed":
		m.textarea.Placeholder = donePlaceholder
	case "pausing":
		m.textarea.Placeholder = "Đang tạm dừng sáng tác..."
	case "paused":
		if m.snapshot.AdvanceMode == "review" && m.snapshot.Phase == "writing" {
			m.textarea.Placeholder = "Đang chờ nghiệm thu từng chương: nhập ý kiến chỉnh sửa, hoặc /next để cho phép chương tiếp theo"
		} else {
			m.textarea.Placeholder = "Sáng tác đã tạm dừng, nhập bất kỳ nội dung nào để tiếp tục"
		}
	default:
		if !m.snapshot.IsRunning {
			if m.snapshot.AdvanceMode == "review" && m.snapshot.Phase == "writing" {
				m.textarea.Placeholder = "Đang chờ nghiệm thu từng chương: nhập ý kiến chỉnh sửa, hoặc /next để cho phép chương tiếp theo"
			} else {
				m.textarea.Placeholder = "Phiên chạy bị gián đoạn, nhập bất kỳ nội dung nào để khôi phục sáng tác"
			}
		} else {
			m.textarea.Placeholder = defaultSteerPlaceholder()
		}
	}
}

func (m *Model) renderBottomBar() string {
	inputBox := renderInputBox(
		m.textarea.View(),
		m.inputHints(),
		m.snapshot,
		m.outputDir(),
		m.width,
	)
	if m.mode != modeNew || m.cocreate != nil {
		return inputBox
	}
	return renderStartupModeBar(m.width, m.startupMode) + "\n" + inputBox
}

func (m *Model) layoutHeights() (topH, inputH, bodyH int) {
	if m.width == 0 || m.height == 0 {
		return 1, 4, 20
	}
	topH = lipgloss.Height(renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version))
	inputH = lipgloss.Height(m.renderBottomBar())
	bodyH = m.height - topH - inputH
	if bodyH < 3 {
		bodyH = 3
	}
	return
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Đang tải..."
	}
	if m.width < 100 {
		return lipgloss.NewStyle().
			Width(m.width).Height(m.height).
			AlignHorizontal(lipgloss.Center).
			AlignVertical(lipgloss.Center).
			Render("Terminal quá hẹp, hãy mở rộng ít nhất đến 100 cột")
	}
	if m.askState != nil {
		return renderAskUserModal(m.width, m.height, m.askState)
	}
	if m.cocreate != nil {
		return renderCoCreateModal(m.width, m.height, m.cocreate, errorText(m.err), m.textarea.View(), m.spinnerIdx, m.quitPending)
	}
	if m.help != nil {
		return renderHelpModal(m.width, m.height, m.help)
	}
	if m.report != nil {
		return renderReportModal(m.width, m.height, m.report)
	}
	if m.importer != nil {
		//  Engine Trạng thái chạy， spinnerIdx（currentSpinnerFrame ）。
		return renderImportModal(m.width, m.height, m.importer, m.spinnerIdx)
	}
	if m.simulator != nil {
		return renderSimulationModal(m.width, m.height, m.simulator)
	}

	topBar := renderTopBar(m.snapshot, m.width, m.currentSpinnerFrame(), m.version)
	inputBox := m.renderBottomBar()
	_, inputH, bodyH := m.layoutHeights()

	var body string
	if m.mode == modeNew {
		errMsg := ""
		if m.err != nil {
			errMsg = m.err.Error()
		}
		body = renderWelcome(m.width, bodyH, errMsg, m.startupMode, m.importHint)
	} else {
		leftW := m.sidebarWidth()
		rightW := m.detailWidth()
		centerW := m.width - leftW - rightW
		eventH, streamH := m.splitHeights(bodyH)

		if m.viewport.Width != centerW-2 || m.viewport.Height != eventH-1 {
			m.viewport.Width = centerW - 2
			m.viewport.Height = eventH - 1 // -1  event panel header
		}
		if m.streamVP.Width != centerW-2 || m.streamVP.Height != streamH-1 {
			m.streamVP.Width = centerW - 2
			m.streamVP.Height = streamH - 1 // -1  stream panel header
		}

		eventFlow := renderEventFlowViewport(m.viewport, centerW, eventH, m.paneHighlighted(focusEvents))
		streamPanel := renderStreamPanel(m.streamVP, centerW, streamH, m.paneHighlighted(focusStream), m.snapshot.IsRunning || m.starting, m.spinnerIdx)
		center := lipgloss.JoinVertical(lipgloss.Left, eventFlow, streamPanel)

		left := renderStatePanel(m.stateVP, leftW, bodyH, m.paneHighlighted(focusState))
		right := renderDetailPanel(m.detailVP, rightW, bodyH, m.paneHighlighted(focusDetail))
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, center, right)
	}

	view := lipgloss.JoinVertical(lipgloss.Left, topBar, body, inputBox)

	// ： body ，
	if m.modelSwitch != nil {
		commandBar := renderModelSwitchBar(m.width, m.modelSwitch)
		view = overlayAboveInput(view, commandBar, inputH)
	} else if m.modelConfig != nil {
		view = overlayAboveInput(view, renderModelConfigModal(m.width, m.modelConfig), inputH)
	} else if m.compActive {
		commandBar := renderCommandPalette(m.width, m.compItems, m.compIdx)
		view = overlayAboveInput(view, commandBar, inputH)
	}
	return view
}

// sendCoCreate ， reqID、textarea、placeholder。
func (m *Model) sendCoCreate() tea.Cmd {
	m.cocreateSeq++
	m.cocreate.reqID = m.cocreateSeq
	m.cocreate.awaiting = true
	m.resizeTextarea()
	m.textarea.Placeholder = placeholderForCoCreate(m.cocreate)
	m.textarea.Blur()
	return runCoCreate(m.runtime, m.cocreate)
}

func (m Model) handleCoCreateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.cocreate == nil {
		return m, nil
	}
	state := m.cocreate

	//  ↑↓/PgUp/PgDn/Home/End ；Tab  ↔
	// （Mặc định，）。， Tab
	// 。： follow， follow（）。
	switch msg.Type {
	case tea.KeyTab:
		state.focusPrompt = !state.focusPrompt
		return m, nil
	case tea.KeyUp, tea.KeyPgUp:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		state.convFollow = false
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		return m, cmd
	case tea.KeyDown, tea.KeyPgDown:
		if state.focusPrompt {
			var cmd tea.Cmd
			state.promptVP, cmd = state.promptVP.Update(msg)
			return m, cmd
		}
		var cmd tea.Cmd
		state.convVP, cmd = state.convVP.Update(msg)
		if state.convVP.AtBottom() {
			state.convFollow = true
		}
		return m, cmd
	case tea.KeyHome:
		if state.focusPrompt {
			state.promptVP.GotoTop()
			return m, nil
		}
		state.convFollow = false
		state.convVP.GotoTop()
		return m, nil
	case tea.KeyEnd:
		if state.focusPrompt {
			state.promptVP.GotoBottom()
			return m, nil
		}
		state.convFollow = true
		state.convVP.GotoBottom()
		return m, nil
	case tea.KeyEsc:
		return m.exitCoCreate()
	}

	// Chờ AI （Đầu vào///Ctrl+U/）——
	//  AI Đầu vào。 case ，
	//  Enter  awaiting —— \n 。

	switch msg.Type {
	case tea.KeyCtrlS:
		if state.awaiting {
			return m, nil
		}
		if !state.canStart() {
			return m, nil
		}
		// Giai đoạn：" brief"Khôi phục，。
		if state.stage {
			draft := state.draftPrompt()
			m.cocreate = nil
			m.err = nil
			m.resizeTextarea()
			m.textarea.Placeholder = defaultSteerPlaceholder()
			return m, tea.Batch(resumeFromCoCreate(m.runtime, draft), m.textarea.Focus())
		}
		// ：。
		plan, err := state.buildPlan()
		if err != nil {
			m.err = err
			return m, nil
		}
		cmd := m.enterStarting(plan.RawPrompt)
		return m, tea.Batch(startRuntime(m.runtime, plan), cmd)
	case tea.KeyEnter:
		// Alt+Enter → ， textarea.Update （KeyMap.InsertNewline ）
		if msg.Alt {
			break
		}
		//  →  \n ：。
		//  awaiting —— awaiting  \n ，
		//  "abc\ndef"  "abcdef"， base 。
		if !m.lastKeyAt.IsZero() && time.Since(m.lastKeyAt) < 50*time.Millisecond {
			var cmd tea.Cmd
			m.textarea, cmd = m.textarea.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
			m.refitTextareaHeight()
			return m, cmd
		}
		// ：awaiting （）
		if state.awaiting {
			return m, nil
		}
		text := utils.CleanInputLine(m.textarea.Value())
		if text == "" {
			return m, nil
		}
		m.err = nil
		state.appendUser(text)
		m.textarea.Reset()
		m.refitTextareaHeight()
		cmd := m.sendCoCreate()
		return m, cmd
	case tea.KeyCtrlU:
		m.textarea.Reset()
		m.refitTextareaHeight()
		return m, nil
	}

	//  1/2/3  textarea  → （，）。
	// Đầu vào，。awaiting ，
	// （state.suggestions ）。
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && !state.awaiting {
		if r := msg.Runes[0]; r >= '1' && r <= '3' {
			if strings.TrimSpace(m.textarea.Value()) == "" {
				if sugs := state.suggestions(); int(r-'0') <= len(sugs) {
					m.textarea.SetValue(sugs[r-'1'])
					m.refitTextareaHeight()
					return m, nil
				}
			}
		}
	}

	// Đầu vào textarea
	if msg.Type == tea.KeyRunes && (containsSGRFragment(string(msg.Runes)) || isCSILeak(msg.Runes)) {
		return m, nil
	}
	var ok bool
	if msg, ok = cleanHumanKeyRunes(msg); !ok {
		return m, nil
	}
	if msg.Type == tea.KeyRunes {
		m.lastKeyAt = time.Now()
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.refitTextareaHeight()
	return m, cmd
}

// exitCoCreate ， Đang chạy LLM ，Khôi phụcĐầu vào。
func (m Model) exitCoCreate() (tea.Model, tea.Cmd) {
	if m.cocreate.cancel != nil {
		m.cocreate.cancel()
	}
	stage := m.cocreate.stage
	initial := m.cocreate.initialInput()
	m.cocreate = nil
	m.resizeTextarea()
	// Giai đoạn：、，Đầu vào（）。
	if stage {
		m.textarea.SetValue("")
		m.textarea.Placeholder = defaultSteerPlaceholder()
		return m, tea.Batch(cancelCoCreate(m.runtime), fetchSnapshot(m.runtime), m.textarea.Focus())
	}
	m.textarea.SetValue(initial)
	m.textarea.Placeholder = placeholderForNewMode(m.startupMode)
	return m, m.textarea.Focus()
}

func (m Model) handleAskUserKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.askState == nil {
		return m, nil
	}
	state := m.askState
	q := state.currentQuestion()

	if state.typing {
		switch msg.Type {
		case tea.KeyEsc:
			state.cancelCurrentTyping()
			return m, nil
		case tea.KeyEnter:
			if state.finishCurrentAnswer() {
				state.submit()
				m.askState = nil
				return m, m.textarea.Focus()
			}
			return m, nil
		case tea.KeyBackspace, tea.KeyCtrlH:
			if state.input != "" {
				_, size := utf8.DecodeLastRuneInString(state.input)
				state.input = state.input[:len(state.input)-size]
			}
			return m, nil
		default:
			if msg.Type == tea.KeyRunes {
				state.input += utils.CleanInputRunes(msg.Runes)
			}
			return m, nil
		}
	}

	switch msg.Type {
	case tea.KeyEsc:
		// Tắt，
		state.request.resultCh <- askUserResult{
			resp: &tools.AskUserResponse{
				Answers: make(map[string]string),
				Notes:   make(map[string]string),
			},
		}
		m.askState = nil
		return m, m.textarea.Focus()
	case tea.KeyUp:
		state.moveCursor(-1)
	case tea.KeyDown:
		state.moveCursor(1)
	case tea.KeySpace:
		if q.MultiSelect {
			state.toggleSelection()
			if state.cursor == len(q.Options) && !state.selected[state.cursor] {
				state.input = ""
			}
		}
	case tea.KeyEnter:
		if q.MultiSelect {
			if state.cursor == len(q.Options) {
				state.toggleSelection()
				if state.selected[state.cursor] {
					state.typing = true
				}
				return m, nil
			}
			if len(state.selected) == 0 {
				state.toggleSelection()
			}
		}
		if state.finishCurrentAnswer() {
			state.submit()
			m.askState = nil
			return m, m.textarea.Focus()
		}
	}
	return m, nil
}

// overlayAboveInput  overlay  base （inputBox ），
// Cao。 overlay rộng，。
func overlayAboveInput(base, overlay string, inputLineCount int) string {
	baseLines := strings.Split(base, "\n")
	overLines := strings.Split(strings.TrimRight(overlay, "\n"), "\n")

	endY := len(baseLines) - inputLineCount
	startY := endY - len(overLines)
	if startY < 0 {
		startY = 0
	}

	for i, ol := range overLines {
		y := startY + i
		if y >= 0 && y < endY {
			olW := lipgloss.Width(ol)
			// Cơ sở olW ， overlay +
			right := ansi.TruncateLeft(baseLines[y], olW, "")
			baseLines[y] = ol + right
		}
	}
	return strings.Join(baseLines, "\n")
}

// isCSILeak  KeyRunes  CSI 。
//
//	\x1b[A ，：
//
// \x1b  Escape，"["  "[A"  KeyRunes  textarea。
func isCSILeak(runes []rune) bool {
	if len(runes) == 0 || runes[0] != '[' {
		return false
	}
	for _, r := range runes[1:] {
		if (r >= '0' && r <= '9') || r == ';' ||
			(r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~' {
			continue
		}
		return false
	}
	return true
}

// containsSGRFragment  SGR （"<;;" ）。
func containsSGRFragment(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		j := i + 1
		if j >= len(s) || s[j] < '0' || s[j] > '9' {
			continue
		}
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j < len(s) && s[j] == ';' {
			return true
		}
	}
	return false
}

func cleanHumanKeyRunes(msg tea.KeyMsg) (tea.KeyMsg, bool) {
	if msg.Type != tea.KeyRunes {
		return msg, true
	}
	cleaned := utils.CleanInputRunes(msg.Runes)
	if cleaned == "" {
		return msg, false
	}
	msg.Runes = []rune(cleaned)
	return msg, true
}
