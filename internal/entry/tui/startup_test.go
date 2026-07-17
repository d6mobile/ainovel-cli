package tui

import (
	"errors"
	"strings"
	"testing"
)

func TestEnterStartingSwitchesToWorkbenchImmediately(t *testing.T) {
	m := NewModel(nil, nil, "")
	m.width = 120
	m.height = 40
	m.resizeTextarea()
	m.updateViewportSize()

	m.enterStarting("Viết một truyện dài huyền huyễn phương Đông")

	if m.mode != modeRunning {
		t.Fatalf("mode = %v, want modeRunning", m.mode)
	}
	if !m.starting {
		t.Fatal("starting should be true while host startup command is running")
	}
	if !m.snapshot.IsRunning {
		t.Fatal("snapshot should render as running during local startup")
	}
	if got := m.textarea.Placeholder; got != "Đang khởi tạo sáng tác..." {
		t.Fatalf("placeholder = %q", got)
	}
	if len(m.events) != 2 {
		t.Fatalf("events = %+v, want startup user + system events", m.events)
	}
	if m.events[0].Category != "USER" || !strings.HasPrefix(m.events[0].Summary, "Yêu cầu sáng tác: ") {
		t.Fatalf("first event = %+v, want USER prompt event", m.events[0])
	}
}

func TestStartupFailureStaysInWorkbench(t *testing.T) {
	m := NewModel(nil, nil, "")
	m.width = 120
	m.height = 40
	m.resizeTextarea()
	m.updateViewportSize()

	m.enterStarting("Viết một truyện dài huyền huyễn phương Đông")

	next, _ := m.handleStartResultMsg(startResultMsg{err: errors.New("Tài khoản mô hình chưa kích hoạt")})
	got := next.(Model)
	if got.mode != modeRunning {
		t.Fatalf("sau lỗi khởi động, mode = %v, want modeRunning", got.mode)
	}
	if got.starting {
		t.Fatal("sau lỗi khởi động, starting phải được reset")
	}
	if got.snapshot.IsRunning {
		t.Fatal("sau lỗi khởi động, snapshot không được vẫn hiển thị đang chạy")
	}
	if !strings.Contains(got.textarea.Placeholder, "Khởi động thất bại") {
		t.Fatalf("placeholder = %q", got.textarea.Placeholder)
	}
	if len(got.events) == 0 || got.events[len(got.events)-1].Category != "ERROR" {
		t.Fatalf("workbench phải giữ sự kiện lỗi khởi động: %+v", got.events)
	}
}

func TestApplyStartupPromptEventTruncatesSummaryButKeepsDetail(t *testing.T) {
	m := NewModel(nil, nil, "")
	prompt := strings.Repeat("x", maxPromptEventCols+50)

	m.applyStartupPromptEvent(prompt)

	if len(m.events) != 1 {
		t.Fatalf("events = %+v, want one event", m.events)
	}
	ev := m.events[0]
	if ev.Detail != prompt {
		t.Fatalf("detail should keep full prompt, got len=%d want=%d", len([]rune(ev.Detail)), len([]rune(prompt)))
	}
	maxSummaryRunes := len([]rune("Yêu cầu sáng tác: ")) + maxPromptEventCols
	if got := len([]rune(ev.Summary)); got > maxSummaryRunes {
		t.Fatalf("summary runes = %d, want <= %d", got, maxSummaryRunes)
	}
	if !strings.HasSuffix(ev.Summary, "...") {
		t.Fatalf("summary should be truncated with ellipsis, got %q", ev.Summary)
	}
}
