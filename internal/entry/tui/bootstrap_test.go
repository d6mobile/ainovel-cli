package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
)

// TestBootstrapCompletedBookLandsOnDoneWorkbench ：resumeLabel
// complete ，——，，
//
//	/reopen、/export、Làm lạiĐầu vàoHoàn tất。
func TestBootstrapCompletedBookLandsOnDoneWorkbench(t *testing.T) {
	m := Model{mode: modeNew, textarea: textarea.New()}
	next, cmd, handled := m.handleRuntimeMsg(bootstrapMsg{completed: true})
	if !handled || cmd == nil {
		t.Fatal("completed bootstrap phải được xử lý và trả về command")
	}
	got := next.(Model)
	if got.mode != modeDone {
		t.Fatalf("sách hoàn tất phải vào workbench trạng thái hoàn tất, got mode=%v", got.mode)
	}
	if got.textarea.Placeholder != donePlaceholder {
		t.Fatalf("phải có hướng dẫn trạng thái hoàn tất (gồm /reopen), got %q", got.textarea.Placeholder)
	}

	// （ bootstrap）。
	m = Model{mode: modeRunning, textarea: textarea.New()}
	next, _, _ = m.handleRuntimeMsg(bootstrapMsg{completed: true})
	if next.(Model).mode != modeRunning {
		t.Fatal("completed bootstrap không được đổi trạng thái khi không ở trang chào mừng")
	}
}
