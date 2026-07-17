package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/imp"
)

// importState  /import 。
//
// ，Tiến hành；Hoàn tất Esc Tắt。
// Esc Đang chạy（ctx.Cancel）， runner 。
type importState struct {
	reqID      int
	source     string
	stage      imp.Stage
	current    int
	total      int
	startedAt  time.Time
	finishedAt time.Time
	history    []importLine
	totalLines int // （history  importHistoryMax ）
	err        error
	done       bool // （Hoàn tất/）
	paused     bool //  awaiting 、Tắt：Tắt，
	frame      int  // cursor tick（120ms，）： tick
	cancel     context.CancelFunc
	viewport   viewport.Model
}

type importLine struct {
	at      time.Time
	stage   imp.Stage
	current int
	total   int
	message string
	level   string    // "warn" /
	key     string    //  key （ ID ）
	retryAt time.Time //  = ，
	err     error

	rendered  string //  renderedW Cache；， tick
	renderedW int
}

// importHistoryMax Trung bình： +
// ，。（logs/import.log）。
const importHistoryMax = 1000

func newImportState(reqID int, source string, width, height int, cancel context.CancelFunc) *importState {
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	vp := viewport.New(contentW, boxH-4)
	s := &importState{
		reqID:     reqID,
		source:    source,
		startedAt: time.Now(),
		stage:     imp.StageIngesting,
		cancel:    cancel,
		viewport:  vp,
	}
	s.refresh(contentW)
	return s
}

func (s *importState) appendEvent(ev imp.Event, contentW int) {
	s.stage = ev.Stage
	s.current = ev.Current
	s.total = ev.Total
	if ev.Err != nil {
		s.err = ev.Err
	}
	line := importLine{
		at: ev.Time, stage: ev.Stage, current: ev.Current, total: ev.Total,
		message: ev.Message, level: ev.Level, key: ev.Key, retryAt: ev.RetryAt, err: ev.Err,
	}
	//  Key  → （7 ）；Tiến độ，。
	if ev.Key != "" && len(s.history) > 0 && s.history[len(s.history)-1].key == ev.Key {
		s.history[len(s.history)-1] = line
	} else {
		s.totalLines++
		s.history = append(s.history, line)
		if len(s.history) > importHistoryMax {
			s.history = append(s.history[:0], s.history[len(s.history)-importHistoryMax:]...)
		}
	}
	if ev.Stage == imp.StageDone || ev.Stage == imp.StageError {
		s.done = true
		s.finishedAt = ev.Time
	}
	s.refresh(contentW)
}

func (s *importState) refresh(contentW int) {
	titleStyle := lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	okStyle := lipgloss.NewStyle().Foreground(colorSuccess)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	stageStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	var b strings.Builder
	b.WriteString(titleStyle.Render("Nhập tiểu thuyết ngoài"))
	b.WriteString("\n\n")
	b.WriteString(dimStyle.Render("Tệp nguồn "))
	b.WriteString(s.source)
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Bắt đầu "))
	b.WriteString(formatReportTime(s.startedAt))
	if !s.finishedAt.IsZero() {
		b.WriteString(dimStyle.Render("  Hoàn tất "))
		b.WriteString(formatReportTime(s.finishedAt))
	}
	b.WriteString("\n\n")

	// Hiện tạiGiai đoạn
	b.WriteString(mutedStyle.Render("Giai đoạn "))
	b.WriteString(stageStyle.Render(string(s.stage)))
	if s.total > 0 {
		b.WriteString(mutedStyle.Render("  Tiến độ "))
		if s.current > 0 {
			b.WriteString(fmt.Sprintf("%d/%d", s.current, s.total))
		} else {
			b.WriteString(fmt.Sprintf("0/%d", s.total))
		}
	}
	b.WriteString("\n\n")

	// 。（）：
	// ✗ = · ↻ =/（） · ✓ =Hoàn tất · · =Tiến độ。
	b.WriteString(titleStyle.Render("Nhật ký quy trình"))
	b.WriteString(" ")
	if s.totalLines > len(s.history) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d dòng, chỉ hiển thị %d dòng gần nhất, đầy đủ xem logs/import.log)", s.totalLines, len(s.history))))
	} else {
		b.WriteString(dimStyle.Render(fmt.Sprintf("(%d dòng)", s.totalLines)))
	}
	b.WriteString("\n")
	now := time.Now()
	for i := range s.history {
		ln := &s.history[i]
		// rộngCache：refresh  120ms tick ，
		// （wrapText+），publish Giai đoạn。
		//  tick （ 2s ）。
		live := !ln.retryAt.IsZero() && now.Before(ln.retryAt.Add(2*time.Second))
		if ln.rendered == "" || ln.renderedW != contentW || live {
			ln.rendered = renderImportLine(*ln, contentW, now)
			ln.renderedW = contentW
		}
		b.WriteString("\n")
		b.WriteString(ln.rendered)
	}

	running := !s.done && !s.paused
	if running {
		// ：，cursor tick ，
		// " Đang chạy"——，Chờ。
		b.WriteString("\n\n")
		b.WriteString(lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render(streamCursorFrames[s.frame%len(streamCursorFrames)]))
	}

	//
	b.WriteString("\n\n")
	switch {
	case s.err != nil:
		b.WriteString(errStyle.Render("Nhập thất bại"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc Đóng bảng"))
	case s.paused && s.stage == imp.StageAwaitingConfirmation:
		b.WriteString(okStyle.Render("Chia đoạn hoàn tất, đang chờ bạn kiểm tra"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("y Xác nhận chia đoạn và tiếp tục; nếu cần chỉnh chia đoạn, nhấn Esc rồi dùng /import --guide=<mô tả tự nhiên>; Esc Đóng bảng"))
	case s.paused:
		// Chờ，Tắt： Esc Tắt。
		b.WriteString(okStyle.Render("Đã tạm dừng nhập, đang chờ thao tác của bạn"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Tiếp tục theo gợi ý phía trên (ví dụ /import --story=open|closed); Esc Đóng bảng"))
	case s.done:
		b.WriteString(okStyle.Render("Nhập hoàn tất, Foundation và các chương đã sẵn sàng"))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("Esc Đóng bảng và nối cổng viết tiếp (engine dừng ở ranh giới chương tiếp theo, chờ bạn nghiệm thu cho phép)"))
	default:
		b.WriteString(dimStyle.Render("Esc Hủy nhập"))
	}

	// ：refresh  tick （/），
	//  GotoBottom Đang chạy 350ms 。
	atBottom := s.viewport.AtBottom()
	s.viewport.SetContent(b.String())
	if running && atBottom {
		s.viewport.GotoBottom()
	}
}

// renderImportLine Nhật ký quy trình dòng:  +  + Giai đoạn（+Tiến độ）+ 。
// rộng，；rộng——
// viewport rộng， HTTP /provider/Mô hình，。
func renderImportLine(ln importLine, contentW int, now time.Time) string {
	dimStyle := lipgloss.NewStyle().Foreground(colorDim)
	mutedStyle := lipgloss.NewStyle().Foreground(colorMuted)
	okStyle := lipgloss.NewStyle().Foreground(colorSuccess)
	errStyle := lipgloss.NewStyle().Foreground(colorError)
	warnStyle := lipgloss.NewStyle().Foreground(colorReview)
	stageStyle := lipgloss.NewStyle().Foreground(colorAccent2)

	var p strings.Builder
	p.WriteString(dimStyle.Render(ln.at.Format("15:04:05")))
	p.WriteString(" ")
	switch {
	case ln.err != nil:
		p.WriteString(errStyle.Bold(true).Render("✗"))
	case ln.level == "warn":
		p.WriteString(warnStyle.Bold(true).Render("↻"))
	case ln.stage == imp.StageDone:
		p.WriteString(okStyle.Bold(true).Render("✓"))
	default:
		p.WriteString(dimStyle.Render("·"))
	}
	p.WriteString(" ")
	p.WriteString(stageStyle.Render(string(ln.stage)))
	if ln.total > 0 && ln.current > 0 {
		p.WriteString(mutedStyle.Render(fmt.Sprintf(" %d/%d", ln.current, ln.total)))
	}
	p.WriteString(" ")
	prefix := p.String()

	var text string
	style := lipgloss.NewStyle()
	switch {
	case ln.err != nil:
		text = ln.message + " — " + ln.err.Error()
		style = errStyle
	case ln.level == "warn":
		text = ln.message
		if cd := retryCountdown(ln.retryAt, now); cd != "" {
			text += " · " + cd
		}
		style = warnStyle
	default:
		text = ln.message
	}
	// ：lipgloss rộng，
	// ， contentW  viewport 。
	prefixW := lipgloss.Width(prefix)
	wrapW := contentW - prefixW
	if wrapW < 20 {
		// （++Giai đoạn+Tiến độ）rộng：，
		// rộng contentW —— 20 rộng viewport ，
		//  HTTP /provider 。
		var out strings.Builder
		out.WriteString(prefix)
		for _, l := range strings.Split(wrapText(text, max(10, contentW-4)), "\n") {
			out.WriteString("\n    ")
			out.WriteString(style.Render(l))
		}
		return out.String()
	}
	// （）：，——rộng
	// ，40+ ，。
	head, body := text, ""
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		head, body = text[:i], strings.TrimRight(text[i+1:], "\n")
	}
	lines := strings.Split(wrapText(head, wrapW), "\n")
	var out strings.Builder
	out.WriteString(prefix)
	out.WriteString(style.Render(lines[0]))
	pad := strings.Repeat(" ", prefixW)
	for _, l := range lines[1:] {
		out.WriteString("\n")
		out.WriteString(pad)
		out.WriteString(style.Render(l))
	}
	if body != "" {
		for _, l := range strings.Split(wrapText(body, contentW-2), "\n") {
			out.WriteString("\n  ")
			out.WriteString(style.Render(l))
		}
	}
	return out.String()
}

func renderImportModal(width, height int, s *importState, frame int) string {
	if s == nil {
		return ""
	}
	boxW, boxH := reportModalSize(width, height)
	contentW := paddedModalContentWidth(boxW)
	running := !s.done && !s.paused
	if s.viewport.Width != contentW {
		s.viewport.Width = contentW
		s.refresh(contentW)
	}
	vpH := boxH - 4
	if running {
		vpH -= 2 //  +
	}
	if s.viewport.Height != vpH {
		s.viewport.Height = vpH
	}

	hint := "  ↑↓ Cuộn · Esc Hủy/Tắt"
	switch {
	case s.paused && s.stage == imp.StageAwaitingConfirmation:
		hint = "  ↑↓ Cuộn · y Xác nhận chia đoạn · Esc Đóng"
	case running:
		hint = "  ↑↓ Cuộn · Esc Hủy"
	}

	body := strings.Split(s.viewport.View(), "\n")
	if running {
		// Đang chạy： + 。 spinner （350ms）——
		//  120ms ，，。
		//  viewport ——viewport ，；
		// ，Mô hình/，。
		star := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).
			Render(streamCursorFrames[frame%len(streamCursorFrames)])
		status := lipgloss.NewStyle().Foreground(colorMuted).
			Render(fmt.Sprintf(" Đang chạy · Đã dùng %s", formatElapsed(time.Since(s.startedAt))))
		body = append([]string{star + status, ""}, body...)
	}
	modal := renderPaddedModalFrame(boxW, boxH, "Nhập tiểu thuyết ngoài", hint, body)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, modal)
}

// formatElapsed  mm:ss （ 1  h:mm:ss）。
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	sec := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%02d:%02d", m, sec)
}

func (m Model) handleImportKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.importer == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		// （、）→ Esc ， runner ； awaiting
		// （Tắt）→ Esc Đóng bảng。 paused  awaiting Tắt（）。
		if !m.importer.done && !m.importer.paused && m.importer.cancel != nil {
			m.importer.cancel()
			return m, nil
		}
		succeeded := m.importer.stage == imp.StageDone && m.importer.err == nil
		m.importer = nil
		// ：（bootstrap  Resume
		// ），Khôi phục，Hoàn tất Hold ，
		//  Enter ""。
		if succeeded && m.mode == modeNew {
			return m, tea.Batch(m.textarea.Focus(), resumeBook(m.runtime))
		}
		return m, m.textarea.Focus()
	case tea.KeyUp:
		m.importer.viewport.ScrollUp(1)
	case tea.KeyDown:
		m.importer.viewport.ScrollDown(1)
	case tea.KeyPgUp:
		m.importer.viewport.HalfPageUp()
	case tea.KeyPgDown:
		m.importer.viewport.HalfPageDown()
	case tea.KeyRunes:
		//  y =  /import --yes（Khôi phục），Hiện tại。
		if len(msg.Runes) == 1 && (msg.Runes[0] == 'y' || msg.Runes[0] == 'Y') &&
			m.importer.paused && m.importer.stage == imp.StageAwaitingConfirmation {
			return m.confirmImportSegmentation()
		}
	}
	return m, nil
}

// confirmImportSegmentation ""：
// AcceptSegmentation（Khôi phục， confirmation ）。 --yes
// ""——（Notes） --yes 、y ；
//
//	Options 、 intent.json， --guide 。
//
// Nhật ký quy trình，。
func (m Model) confirmImportSegmentation() (tea.Model, tea.Cmd) {
	prev := m.importer
	m.importSeq++
	state, listenCmd, err := startImportRun(m.runtime, m.importSeq, imp.Options{AcceptSegmentation: true}, m.width, m.height)
	if err != nil {
		m.applyEvent(host.Event{
			Time: time.Now(), Category: "ERROR", Summary: "Xác nhận chia đoạn thất bại: " + err.Error(), Level: "error",
		})
		return m, nil
	}
	state.source = prev.source
	state.history = append([]importLine(nil), prev.history...)
	state.totalLines = prev.totalLines
	boxW, _ := reportModalSize(m.width, m.height)
	state.refresh(paddedModalContentWidth(boxW))
	m.importer = state
	return m, listenCmd
}

// importEventMsg  imp.Event 。
type importEventMsg struct {
	reqID int
	ev    imp.Event
	ch    <-chan imp.Event //
}

// importClosedMsg Tắt（ goroutine ）。 awaiting ，
// TắtTắt， awaiting 。
type importClosedMsg struct {
	reqID int
}

// startImport Nhập tiểu thuyết ngoài： →  modal state → 。
func startImport(rt *host.Host, reqID int, args []string, width, height int) (*importState, tea.Cmd, error) {
	opts, err := parseImportArgs(args)
	if err != nil {
		return nil, nil, err
	}
	return startImportRun(rt, reqID, opts, width, height)
}

// startImportRun  Options （y ）。
// width/height Khởi tạo viewport；cancel  state  Esc 。
func startImportRun(rt *host.Host, reqID int, opts imp.Options, width, height int) (*importState, tea.Cmd, error) {
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := rt.ImportFrom(ctx, opts)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	state := newImportState(reqID, opts.SourcePath, width, height, cancel)
	return state, listenImportEvent(reqID, ch), nil
}

func listenImportEvent(reqID int, ch <-chan imp.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return importClosedMsg{reqID: reqID}
		}
		return importEventMsg{reqID: reqID, ev: ev, ch: ch}
	}
}

// parseImportArgs  `/import <path> [--yes] [--story=open|closed] [--continue] [--guide=<>]`。
// “Khôi phục”，Khôi phục（RFC §18）。
// --guide ，： --guide= ，。
func parseImportArgs(args []string) (imp.Options, error) {
	var opts imp.Options
	for i := range args {
		a := args[i]
		switch {
		case a == "--yes":
			opts.AutoConfirm = true
		case a == "--continue":
			opts.ContinueAfter = true
		case strings.HasPrefix(a, "--story="):
			v := strings.TrimPrefix(a, "--story=")
			if v != "open" && v != "closed" {
				return imp.Options{}, fmt.Errorf("--story chỉ có thể là open hoặc closed: %q", v)
			}
			opts.StoryResolution = v
		case strings.HasPrefix(a, "--guide="):
			parts := append([]string{strings.TrimPrefix(a, "--guide=")}, args[i+1:]...)
			g := strings.TrimSpace(strings.Join(parts, " "))
			if g == "" {
				return imp.Options{}, fmt.Errorf("--guide cần hướng dẫn chia đoạn bằng ngôn ngữ tự nhiên, ví dụ --guide=hồi xen kẽ X cũng là chương độc lập")
			}
			opts.Guidance = g
			return opts, nil
		case strings.HasPrefix(a, "--"):
			return imp.Options{}, fmt.Errorf("Tùy chọn không xác định %q (hỗ trợ: --yes / --story=open|closed / --continue / --guide=<hướng dẫn chia đoạn>)", a)
		default:
			if opts.SourcePath != "" {
				return imp.Options{}, fmt.Errorf("Chỉ nhận một đường dẫn tệp nguồn: thừa %q", a)
			}
			opts.SourcePath = a
		}
	}
	return opts, nil
}
