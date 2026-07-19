package tui

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/term"
)

func TestIMECursorPositionPlacesTerminalCursorInsideInputLine(t *testing.T) {
	m := NewModel(nil, nil, "")
	m.mode = modeRunning
	m.width = 120
	m.height = 24
	m.resizeTextarea()
	m.textarea.SetValue("xin")
	m.textarea.CursorEnd()
	m.refitTextareaHeight()

	pos, ok := m.imeCursorPosition()
	if !ok {
		t.Fatal("expected IME cursor position to be available")
	}

	topH, _, bodyH := m.layoutHeights()
	wantRow := topH + bodyH + 2
	if pos.Row != wantRow {
		t.Fatalf("cursor row = %d, want %d (textarea content line, below the top border)", pos.Row, wantRow)
	}
	if pos.Col != 9 {
		t.Fatalf("cursor col = %d, want 9 (padding + app prompt + textarea prompt + typed text)", pos.Col)
	}
}

func TestViewClearsIMECursorPositionWhenInputIsNotRendered(t *testing.T) {
	sync := newIMECursorSync()
	m := NewModel(nil, nil, "")
	m.imeSync = sync
	m.mode = modeRunning
	m.width = 120
	m.height = 24
	m.resizeTextarea()
	m.textarea.SetValue("xin")
	m.textarea.CursorEnd()

	_ = m.View()
	if frame := sync.frameState(); !frame.HasInput {
		t.Fatal("expected wide workbench view to publish an input cursor")
	}

	m.width = 80
	_ = m.View()
	if frame := sync.frameState(); frame.HasInput {
		t.Fatalf("narrow fallback view kept stale input cursor: %+v", frame)
	}
}

type fakeTermFile struct {
	bytes.Buffer
	fd uintptr
}

func (f *fakeTermFile) Close() error { return nil }
func (f *fakeTermFile) Fd() uintptr  { return f.fd }

func TestIMECursorWriterPreservesTermFile(t *testing.T) {
	sync := newIMECursorSync()
	writer := newIMECursorWriter(&fakeTermFile{fd: 1}, sync)
	if _, ok := writer.(term.File); !ok {
		t.Fatal("IME cursor writer must preserve term.File so Bubble Tea can detect ttyOutput")
	}
}

func TestIMECursorWriterRestoresRendererCursorBeforeNextFrame(t *testing.T) {
	var out fakeTermFile
	sync := newIMECursorSync()
	writer := newIMECursorWriter(&out, sync)

	sync.setFrame(imeFrameState{
		RenderEnd: imeCursorPosition{Col: 1, Row: 24},
		Input:     imeCursorPosition{Col: 9, Row: 21},
		HasInput:  true,
	})
	if _, err := writer.Write([]byte("first-frame")); err != nil {
		t.Fatal(err)
	}
	out.Reset()

	sync.setFrame(imeFrameState{
		RenderEnd: imeCursorPosition{Col: 1, Row: 24},
		Input:     imeCursorPosition{Col: 10, Row: 21},
		HasInput:  true,
	})
	if _, err := writer.Write([]byte("next-frame")); err != nil {
		t.Fatal(err)
	}

	got := out.Buffer.String()
	if !strings.HasPrefix(got, "\x1b[24;1Hnext-frame") {
		t.Fatalf("writer did not restore Bubble Tea renderer cursor before frame: %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[21;10H\x1b[?25h") {
		t.Fatalf("writer did not move and show terminal cursor at input after frame: %q", got)
	}
}
