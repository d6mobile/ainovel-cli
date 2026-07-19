package bootstrap

import (
	"context"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

type recordingChatModel struct {
	messages []agentcore.Message
}

func (m *recordingChatModel) Generate(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.messages = append([]agentcore.Message(nil), messages...)
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock("ok")},
		StopReason: agentcore.StopReasonStop,
	}}, nil
}

func (m *recordingChatModel) GenerateStream(_ context.Context, messages []agentcore.Message, _ []agentcore.ToolSpec, _ ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	m.messages = append([]agentcore.Message(nil), messages...)
	ch := make(chan agentcore.StreamEvent, 1)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock("ok")},
		StopReason: agentcore.StopReasonStop,
	}}
	close(ch)
	return ch, nil
}

func (m *recordingChatModel) SupportsTools() bool { return true }

func (m *recordingChatModel) Info() llm.ModelInfo {
	return llm.ModelInfo{Name: "recording", Provider: "test"}
}

func (m *recordingChatModel) ProviderName() string { return "test" }

func TestReplaySanitizerPreservesModelMetadata(t *testing.T) {
	model := withProviderReplaySanitizer(&recordingChatModel{})
	if got := ModelName(model); got != "recording" {
		t.Fatalf("ModelName = %q, want recording", got)
	}
	if got := ModelProvider(model); got != "test" {
		t.Fatalf("ModelProvider = %q, want test", got)
	}
}

func TestReplaySanitizerOmitsLengthStoppedAssistantFromProviderReplay(t *testing.T) {
	inner := &recordingChatModel{}
	model := withProviderReplaySanitizer(&failoverModel{primary: NewSwappableModel("test", "model", inner)})
	messages := lengthStopReplayMessages()

	if _, err := model.Generate(context.Background(), messages, nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertNoLengthStoppedReplay(t, inner.messages)
}

func TestModelSetForRoleWithFailoverSanitizesReplayWithoutFallbacks(t *testing.T) {
	inner := &recordingChatModel{}
	models := &ModelSet{
		Default:   NewSwappableModel("test", "default", &recordingChatModel{}),
		models:    map[string]*SwappableModel{"writer": NewSwappableModel("test", "writer", inner)},
		fallbacks: map[string][]modelTarget{},
	}

	if _, err := models.ForRoleWithFailover("writer", nil).Generate(context.Background(), lengthStopReplayMessages(), nil); err != nil {
		t.Fatalf("generate: %v", err)
	}
	assertNoLengthStoppedReplay(t, inner.messages)
}

func TestReplaySanitizerOmitsLengthStoppedAssistantFromStreamingProviderReplay(t *testing.T) {
	inner := &recordingChatModel{}
	model := withProviderReplaySanitizer(inner)

	stream, err := model.GenerateStream(context.Background(), lengthStopReplayMessages(), nil)
	if err != nil {
		t.Fatalf("generate stream: %v", err)
	}
	for range stream {
	}
	assertNoLengthStoppedReplay(t, inner.messages)
}

func lengthStopReplayMessages() []agentcore.Message {
	return []agentcore.Message{
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("write")}},
		{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock("partial")}, StopReason: agentcore.StopReasonLength},
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock("resume")}},
	}
}

func assertNoLengthStoppedReplay(t *testing.T, messages []agentcore.Message) {
	t.Helper()
	for _, msg := range messages {
		if msg.Role == agentcore.RoleAssistant && msg.StopReason == agentcore.StopReasonLength {
			t.Fatal("length-stopped assistant message should not be replayed into provider calls")
		}
	}
	if len(messages) != 2 {
		t.Fatalf("provider received %d messages, want 2", len(messages))
	}
}
