package host

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/domain"
)

func testObserver(events *[]Event) *observer {
	return &observer{
		emitEv: func(ev Event) {
			*events = append(*events, ev)
		},
		emitD:               func(string) {},
		emitC:               func() {},
		agents:              make(map[string]*agentState),
		lastThinkingByAgent: make(map[string]string),
		dispatchStarts:      make(map[string]*activeCall),
		toolStarts:          make(map[string]*activeCall),
		streamExtractors:    make(map[string]*agentExtractor),
		streamArgPrefixes:   make(map[string]string),
		streamArgLabels:     make(map[string]string),
		retryEvents:         make(map[string]string),
	}
}

func TestObserverSubagentRetryEventsUpdateSameLinePerAgent(t *testing.T) {
	var events []Event
	o := testObserver(&events)

	for i := 1; i <= 2; i++ {
		o.handleToolUpdate(agentcore.Event{
			Type: agentcore.EventToolExecUpdate,
			Progress: &agentcore.ProgressPayload{
				Kind:       agentcore.ProgressRetry,
				Agent:      "writer",
				Attempt:    i,
				MaxRetries: 7,
				Message:    "stream failed",
			},
		})
	}

	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 raw update events", len(events))
	}
	if events[0].ID == "" || events[1].ID != events[0].ID {
		t.Fatalf("writer retry events should share ID: %+v", events)
	}
	// Summary 不嵌静态延时（UI 依 RetryAt 倒计时）；延时以截止时刻形式携带，静态快照留在 Detail 供日志。
	if events[1].Agent != "writer" || !strings.Contains(events[1].Summary, "重试 (2/7)") {
		t.Fatalf("event = %+v, want writer retry 2/7 without inline delay", events[1])
	}
	if events[1].RetryAt.IsZero() || !strings.Contains(events[1].Detail, "重试 (2/7，2s后)") {
		t.Fatalf("event = %+v, want RetryAt deadline + static delay in Detail", events[1])
	}
}

func TestDisplayToolNameVietnameseChapterLabels(t *testing.T) {
	cases := []struct {
		tool string
		args string
	}{
		{"commit_chapter", `{"chapter":1}`},
		{"novel_context", `{"chapter":1}`},
		{"read_chapter", `{"chapter":1,"source":"draft"}`},
	}
	for _, tc := range cases {
		got := displayToolName(tc.tool, json.RawMessage(tc.args))
		if strings.Contains(got, "第") || strings.Contains(got, "草稿") || strings.Contains(got, "对话") {
			t.Fatalf("displayToolName(%s) = %q contains Chinese label", tc.tool, got)
		}
		if !strings.Contains(got, "chương 1") {
			t.Fatalf("displayToolName(%s) = %q, want Vietnamese chapter label", tc.tool, got)
		}
	}
}

func TestSanitizeRuntimeQueueItemLegacyChineseLabels(t *testing.T) {
	item := domain.RuntimeQueueItem{
		Kind:    domain.RuntimeQueueUIEvent,
		Summary: "commit_chapter(第1章)",
		Payload: map[string]any{
			"Summary": "commit_chapter 错误: bad",
			"Detail":  "恢复创作: 恢复：第 1 章进行中",
		},
	}
	sanitizeRuntimeQueueItem(&item)
	if item.Summary != "commit_chapter(chương 1)" {
		t.Fatalf("summary = %q", item.Summary)
	}
	payload := item.Payload.(map[string]any)
	if payload["Summary"] != "commit_chapter lỗi: bad" {
		t.Fatalf("payload summary = %q", payload["Summary"])
	}
	if got := payload["Detail"].(string); strings.Contains(got, "恢复") || strings.Contains(got, "第") {
		t.Fatalf("payload detail not sanitized: %q", got)
	}
}

func TestObserverSubagentToolDeltaUpdatesSaveFoundationType(t *testing.T) {
	var events []Event
	o := testObserver(&events)

	o.handleSubagentDelta(&agentcore.ProgressPayload{
		Kind:      agentcore.ProgressToolDelta,
		Agent:     "architect_long",
		Tool:      "save_foundation",
		DeltaKind: agentcore.DeltaToolCall,
		Delta:     `{"type":"premise","content":"# 书名`,
	})

	if len(events) < 2 {
		t.Fatalf("events = %d, want start + summary update", len(events))
	}
	if events[0].Category != "TOOL" || events[0].Summary != "save_foundation" || events[0].Depth != 1 {
		t.Fatalf("start event = %+v", events[0])
	}
	if events[1].ID != events[0].ID || events[1].Summary != "save_foundation[premise]" {
		t.Fatalf("summary update = %+v, start = %+v", events[1], events[0])
	}
}

func TestObserverSubagentToolDeltaUpdatesSaveFoundationTypeAcrossChunks(t *testing.T) {
	var events []Event
	o := testObserver(&events)

	for _, delta := range []string{`{"ty`, `pe":"premise","content":"# 书名`} {
		o.handleSubagentDelta(&agentcore.ProgressPayload{
			Kind:      agentcore.ProgressToolDelta,
			Agent:     "architect_long",
			Tool:      "save_foundation",
			DeltaKind: agentcore.DeltaToolCall,
			Delta:     delta,
		})
	}

	var summaries []string
	for _, ev := range events {
		summaries = append(summaries, ev.Summary)
	}
	if !strings.Contains(strings.Join(summaries, "\n"), "save_foundation[premise]") {
		t.Fatalf("summaries = %v, want save_foundation[premise]", summaries)
	}
}
