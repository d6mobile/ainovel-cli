package tui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
)

type imeCursorPosition struct {
	Col int
	Row int
}

type imeFrameState struct {
	RenderEnd imeCursorPosition
	Input     imeCursorPosition
	HasInput  bool
}

type imeCursorSync struct {
	mu    sync.Mutex
	frame imeFrameState
}

func newIMECursorSync() *imeCursorSync {
	return &imeCursorSync{}
}

func (s *imeCursorSync) setFrame(frame imeFrameState) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.frame = frame
	s.mu.Unlock()
}

func (s *imeCursorSync) frameState() imeFrameState {
	if s == nil {
		return imeFrameState{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.frame
}

type imeCursorWriter struct {
	out           term.File
	sync          *imeCursorSync
	lastRenderEnd imeCursorPosition
	hasLast       bool
}

func newIMECursorWriter(out term.File, sync *imeCursorSync) term.File {
	return &imeCursorWriter{out: out, sync: sync}
}

func (w *imeCursorWriter) Read(p []byte) (int, error) {
	return w.out.Read(p)
}

func (w *imeCursorWriter) Close() error {
	return nil
}

func (w *imeCursorWriter) Fd() uintptr {
	return w.out.Fd()
}

func (w *imeCursorWriter) Write(p []byte) (int, error) {
	frame := w.sync.frameState()
	var b strings.Builder
	if w.hasLast {
		b.WriteString(cursorPositionSeq(w.lastRenderEnd))
	}
	b.Write(p)
	if frame.HasInput {
		b.WriteString(cursorPositionSeq(frame.Input))
		b.WriteString("\x1b[?25h")
	}
	if _, err := io.WriteString(w.out, b.String()); err != nil {
		return 0, err
	}
	w.lastRenderEnd = frame.RenderEnd
	w.hasLast = frame.RenderEnd.Col > 0 && frame.RenderEnd.Row > 0
	return len(p), nil
}

func cursorPositionSeq(pos imeCursorPosition) string {
	col := pos.Col
	row := pos.Row
	if col < 1 {
		col = 1
	}
	if row < 1 {
		row = 1
	}
	return fmt.Sprintf("\x1b[%d;%dH", row, col)
}

func (m Model) imeCursorPosition() (imeCursorPosition, bool) {
	if m.width <= 0 || m.height <= 0 || !m.textarea.Focused() || m.cocreate != nil {
		return imeCursorPosition{}, false
	}

	topH, _, bodyH := m.layoutHeights()
	startupH := 0
	if m.mode == modeNew {
		startupH = lipgloss.Height(renderStartupModeBar(m.width, m.startupMode))
	}

	info := m.textarea.LineInfo()
	visualRow := rowsBeforeTextareaCursor(m.textarea.Value(), m.textarea.Line(), m.textarea.Width()) + info.RowOffset
	height := m.textarea.Height()
	if height < 1 {
		height = 1
	}
	if visualRow < 0 {
		visualRow = 0
	}
	if visualRow >= height {
		visualRow = height - 1
	}

	col := 1 + 1 + lipgloss.Width("❯ ") + lipgloss.Width(m.textarea.Prompt) + info.ColumnOffset
	row := topH + bodyH + startupH + 2 + visualRow
	return imeCursorPosition{Col: col, Row: row}, true
}

func rowsBeforeTextareaCursor(value string, cursorLine, contentWidth int) int {
	if cursorLine <= 0 || value == "" {
		return 0
	}
	if contentWidth < 1 {
		contentWidth = 1
	}
	lines := strings.Split(value, "\n")
	if cursorLine > len(lines) {
		cursorLine = len(lines)
	}
	rows := 0
	for _, line := range lines[:cursorLine] {
		w := lipgloss.Width(line)
		if w == 0 {
			rows++
			continue
		}
		rows += (w + contentWidth - 1) / contentWidth
	}
	return rows
}

func (m Model) syncIMECursorFrame(view string, hasInput bool, input imeCursorPosition) string {
	if m.imeSync == nil {
		return view
	}
	height := lipgloss.Height(view)
	if height < 1 {
		height = 1
	}
	m.imeSync.setFrame(imeFrameState{
		RenderEnd: imeCursorPosition{Col: 1, Row: height},
		Input:     input,
		HasInput:  hasInput,
	})
	return view
}
