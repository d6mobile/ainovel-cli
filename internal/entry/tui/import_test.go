package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/voocel/ainovel-cli/internal/host/imp"
)

// TestImportHistoryCoalescesRetryLines guards in-place retry-line updates: consecutive events with the same key occupy one line
// ("N/7" animates on one line); after a normal progress line, the next retry starts a new line, preserving order.
func TestImportHistoryCoalescesRetryLines(t *testing.T) {
	s := newImportState(1, "book.txt", 100, 40, nil)
	base := len(s.history)
	retry := func(msg string) imp.Event {
		return imp.Event{Time: time.Now(), Stage: imp.StageSegmenting, Message: msg, Level: "warn", Key: "retry:segmenting"}
	}
	s.appendEvent(retry("Thử lại sau 1s (lần 1/7)"), 80)
	s.appendEvent(retry("Thử lại sau 2s (lần 2/7)"), 80)
	s.appendEvent(retry("Thử lại sau 4s (lần 3/7)"), 80)
	if got := len(s.history) - base; got != 1 {
		t.Fatalf("các lần retry liên tiếp cùng Key phải gộp thành 1 dòng, got %d", got)
	}
	if last := s.history[len(s.history)-1]; last.message != "Thử lại sau 4s (lần 3/7)" {
		t.Fatalf("dòng gộp phải cập nhật thành thông điệp mới nhất, got %q", last.message)
	}
	// A normal progress line breaks the retry coalescing, so the next retry starts a new line.
	s.appendEvent(imp.Event{Time: time.Now(), Stage: imp.StageAnalyzing, Message: "Phân tích các lô liên tiếp từ chương 1..."}, 80)
	s.appendEvent(retry("Thử lại sau 1s (lần 1/7)"), 80)
	if got := len(s.history) - base; got != 3 {
		t.Fatalf("retry sau khi bị ngắt phải tạo dòng mới, tổng 3 dòng, got %d", got)
	}
}

// TestRenderImportLineWrapsWithoutClipping guards full visibility for error details: the body wraps after subtracting the prefix width
// the remaining width wraps with aligned continuations, and no line may exceed contentW — the viewport hard-clips over-wide lines,
// and the HTTP status/provider/model details in errors are exactly what we need for debugging, so clipping them would hide the failure.
func TestRenderImportLineWrapsWithoutClipping(t *testing.T) {
	ln := importLine{
		at:      time.Now(),
		stage:   imp.StageSegmenting,
		message: "Khoảng chia L1..L171",
		err: errors.New("imp: gọi mô hình thất bại (tham số yêu cầu không hợp lệ, HTTP 400, openrouter, deepseek/deepseek-chat)：" +
			"Provider returned error: invalid request payload with a very long gateway message tail"),
	}
	const contentW = 80
	out := renderImportLine(ln, contentW, time.Now())
	// Wrapping may break at any character; strip whitespace before comparing so only content is asserted.
	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' {
				return -1
			}
			return r
		}, s)
	}
	for _, want := range []string{"HTTP 400", "openrouter", "gateway message tail"} {
		if !strings.Contains(norm(out), norm(want)) {
			t.Fatalf("nội dung dòng thiếu %q: %q", want, out)
		}
	}
	for i, line := range strings.Split(out, "\n") {
		if w := lipgloss.Width(line); w > contentW {
			t.Fatalf("dòng %d rộng %d vượt quá %d, sẽ bị viewport cắt: %q", i, w, contentW, line)
		}
	}
	// Narrow terminals: the prefix (timestamp + icon + long stage name) can consume most of the width, so the body must wrap instead of being forced onto an over-wide line.
	ln.stage = imp.StageAwaitingConfirmation
	const narrowW = 40
	for i, line := range strings.Split(renderImportLine(ln, narrowW, time.Now()), "\n") {
		if w := lipgloss.Width(line); w > narrowW {
			t.Fatalf("terminal hẹp: dòng %d rộng %d vượt quá %d: %q", i, w, narrowW, line)
		}
	}
}

// TestRenderImportLineMultilineBlock guards multiline block layout (the split-confirmation preview): wrapped lines should stay together
// with a shallow indent (2 columns), not aligned to the full prefix width — a 40+ column prefix would shove the chapter list into the right half of the panel and leave the left half empty.
func TestRenderImportLineMultilineBlock(t *testing.T) {
	ln := importLine{
		at:      time.Now(),
		stage:   imp.StageAwaitingConfirmation,
		current: 157, total: 157,
		message: "Đã chia 157 chương, hãy kiểm tra:\n  Chương 1 Mở đầu\n  Chương 2 Tôi cố ý\n",
	}
	const contentW = 100
	out := strings.Split(renderImportLine(ln, contentW, time.Now()), "\n")
	if len(out) != 3 {
		t.Fatalf("phải là dòng tiền tố + 2 dòng nội dung, got %d dòng: %q", len(out), out)
	}
	for i, line := range out[1:] {
		if w := lipgloss.Width(line); w > contentW {
			t.Fatalf("dòng %d quá rộng %d: %q", i+1, w, line)
		}
		if strings.HasPrefix(line, strings.Repeat(" ", 20)) {
			t.Fatalf("dòng tiếp của block nhiều dòng không được căn theo độ rộng tiền tố: %q", line)
		}
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("dòng tiếp của block nhiều dòng phải thụt nhẹ 2 cột: %q", line)
		}
	}
}

// TestWrapTextResetsAtNewlines guards multiline wrapping: when '\n' is hit, the line-width counter must reset, otherwise
// once any line wraps, every later line is misdetected as over-wide and gets fake wraps + indent, breaking the whole confirmation preview.
func TestWrapTextResetsAtNewlines(t *testing.T) {
	in := strings.Repeat("rộng", 30) + "\nDòng ngắn một\nDòng ngắn hai"
	out := wrapText(in, 20)
	for i, l := range strings.Split(out, "\n") {
		if w := lipgloss.Width(l); w > 20 {
			t.Fatalf("dòng %d rộng %d vượt quá 20: %q", i, w, l)
		}
	}
	if !strings.Contains(out, "\nDòng ngắn một\nDòng ngắn hai") {
		t.Fatalf("dòng ngắn sẵn có không được bị tách vụn: %q", out)
	}
}

// TestImportEscResumeGate guards the Esc landing point for the import panel: after a successful import started from the welcome page,
// closing the panel must also trigger one recovery pass (bootstrap Resume only runs at startup), otherwise the user stays on a page with no
// continuation entry; error terminal states and the workbench only close the panel; while running, Esc still cancels instead of closing.
func TestImportEscResumeGate(t *testing.T) {
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	// tea.Batch returns a BatchMsg after execution (subcommands are not run), which lets us distinguish "focus + resume" from focus-only.
	isBatch := func(cmd tea.Cmd) bool {
		_, ok := cmd().(tea.BatchMsg)
		return ok
	}
	newM := func(mode appMode, st *importState) Model {
		return Model{mode: mode, importer: st, textarea: textarea.New()}
	}

	m := newM(modeNew, &importState{done: true, stage: imp.StageDone})
	next, cmd := m.handleImportKey(esc)
	if next.(Model).importer != nil {
		t.Fatal("Esc ở trạng thái cuối phải đóng panel")
	}
	if !isBatch(cmd) {
		t.Fatal("đóng panel sau khi nhập thành công ở trang chào mừng phải kèm lệnh khôi phục")
	}

	m = newM(modeNew, &importState{done: true, stage: imp.StageError, err: errors.New("boom")})
	if _, cmd := m.handleImportKey(esc); isBatch(cmd) {
		t.Fatal("trạng thái cuối lỗi không được kích hoạt khôi phục (sách có thể chưa nhập thành công)")
	}

	m = newM(modeRunning, &importState{done: true, stage: imp.StageDone})
	if _, cmd := m.handleImportKey(esc); isBatch(cmd) {
		t.Fatal("workbench có cổng riêng, không được kích hoạt khôi phục lặp")
	}

	canceled := false
	m = newM(modeNew, &importState{cancel: func() { canceled = true }})
	next, _ = m.handleImportKey(esc)
	if !canceled || next.(Model).importer == nil {
		t.Fatal("Esc khi đang chạy phải hủy nhập và giữ panel chờ runner kết thúc")
	}
}

// TestRetryCountdown guards the countdown rendering contract (shared by the event and import panels):
// when no deadline is set or the deadline has passed, return empty (the request is already in flight); otherwise round up to whole seconds, count down once per second, and never show 0s.
func TestRetryCountdown(t *testing.T) {
	now := time.Now()
	if got := retryCountdown(time.Time{}, now); got != "" {
		t.Fatalf("deadline zero phải trả rỗng, got %q", got)
	}
	if got := retryCountdown(now.Add(-time.Second), now); got != "" {
		t.Fatalf("đã tới hạn phải trả rỗng, got %q", got)
	}
	if got := retryCountdown(now.Add(7500*time.Millisecond), now); got != "Thử lại sau 8s" {
		t.Fatalf("7.5s phải làm tròn lên thành 8s, got %q", got)
	}
	if got := retryCountdown(now.Add(300*time.Millisecond), now); got != "Thử lại sau 1s" {
		t.Fatalf("dưới 1s phải hiển thị 1s, got %q", got)
	}
}

// TestParseImportArgsGuide guards --guide parsing: natural-language guidance may include spaces (all following tokens are folded into it),
// it can be combined with other options (when placed last), and empty input is an error.
func TestParseImportArgsGuide(t *testing.T) {
	opts, err := parseImportArgs([]string{"--guide=Hồi xen kẽ X", "cũng là", "chương độc lập"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Guidance != "Hồi xen kẽ X cũng là chương độc lập" {
		t.Fatalf("hướng dẫn có khoảng trắng phải được gộp toàn bộ, got %q", opts.Guidance)
	}
	opts, err = parseImportArgs([]string{"book.txt", "--yes", "--guide=Gộp lời mở đầu vào chương một"})
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AutoConfirm || opts.SourcePath != "book.txt" || opts.Guidance != "Gộp lời mở đầu vào chương một" {
		t.Fatalf("phân tích kết hợp với tùy chọn khác không khớp: %+v", opts)
	}
	if _, err := parseImportArgs([]string{"--guide="}); err == nil {
		t.Fatal("--guide rỗng phải báo lỗi")
	}
}
