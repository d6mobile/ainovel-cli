package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

var commitArrayFields = map[string]bool{
	"timeline_events":      true,
	"foreshadow_updates":   true,
	"relationship_changes": true,
	"state_changes":        true,
	"cast_intros":          true,
}

var commitObjectFields = map[string]bool{
	"feedback": true,
}

type commitArgsNormalizingModel struct {
	inner agentcore.ChatModel
}

type capabilityCommitArgsNormalizingModel struct {
	*commitArgsNormalizingModel
	capabilities llm.CapabilityProvider
}

func withCommitChapterArgNormalizer(model agentcore.ChatModel) agentcore.ChatModel {
	if model == nil {
		return nil
	}
	wrapped := &commitArgsNormalizingModel{inner: model}
	if capabilities, ok := model.(llm.CapabilityProvider); ok {
		return &capabilityCommitArgsNormalizingModel{commitArgsNormalizingModel: wrapped, capabilities: capabilities}
	}
	return wrapped
}

func (m *capabilityCommitArgsNormalizingModel) Capabilities() llm.Capabilities {
	return m.capabilities.Capabilities()
}

func (m *commitArgsNormalizingModel) SupportsTools() bool { return m.inner.SupportsTools() }

func (m *commitArgsNormalizingModel) Generate(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	resp, err := m.inner.Generate(ctx, msgs, tools, opts...)
	if err != nil || resp == nil {
		return resp, err
	}
	resp.Message = normalizeCommitToolCalls(resp.Message)
	return resp, nil
}

func (m *commitArgsNormalizingModel) GenerateStream(ctx context.Context, msgs []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	in, err := m.inner.GenerateStream(ctx, msgs, tools, opts...)
	if err != nil || in == nil {
		return in, err
	}
	out := make(chan agentcore.StreamEvent)
	go func() {
		defer close(out)
		for ev := range in {
			if ev.Type == agentcore.StreamEventDone {
				ev.Message = normalizeCommitToolCalls(ev.Message)
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func normalizeCommitToolCalls(msg agentcore.Message) agentcore.Message {
	if len(msg.Content) == 0 {
		return msg
	}
	blocks := make([]agentcore.ContentBlock, len(msg.Content))
	copy(blocks, msg.Content)
	changed := false
	for i := range blocks {
		if blocks[i].Type != agentcore.ContentToolCall || blocks[i].ToolCall == nil || blocks[i].ToolCall.Name != "commit_chapter" {
			continue
		}
		normalized, ok, err := normalizeCommitChapterArgs(blocks[i].ToolCall.Args)
		if err != nil || !ok {
			continue
		}
		clone := *blocks[i].ToolCall
		clone.Args = normalized
		blocks[i].ToolCall = &clone
		changed = true
	}
	if changed {
		msg.Content = blocks
	}
	return msg
}

func normalizeCommitChapterArgs(raw json.RawMessage) (json.RawMessage, bool, error) {
	obj, rootChanged, err := decodeCommitArgsObject(raw)
	if err != nil {
		return nil, false, err
	}
	changed := rootChanged
	for field := range commitArrayFields {
		v, ok := obj[field]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		parsed, err := parseJSONStringValue(s)
		if err != nil {
			return nil, false, fmt.Errorf("%s: parse JSON string: %w", field, err)
		}
		arr, ok := parsed.([]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: expected JSON array inside string, got %T", field, parsed)
		}
		for i, item := range arr {
			if _, ok := item.(map[string]any); !ok {
				return nil, false, fmt.Errorf("%s[%d]: expected object, got %T", field, i, item)
			}
		}
		obj[field] = arr
		changed = true
	}
	for field := range commitObjectFields {
		v, ok := obj[field]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		parsed, err := parseJSONStringValue(s)
		if err != nil {
			return nil, false, fmt.Errorf("%s: parse JSON string: %w", field, err)
		}
		m, ok := parsed.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: expected JSON object inside string, got %T", field, parsed)
		}
		obj[field] = m
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}

func decodeCommitArgsObject(raw json.RawMessage) (map[string]any, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, false, fmt.Errorf("empty arguments")
	}
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			return nil, false, err
		}
		return obj, false, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return nil, false, err
	}
	objectText := strings.TrimSpace(text)
	if !strings.HasPrefix(objectText, "{") {
		extracted := extractFirstJSONObject(objectText)
		if extracted == "" {
			return nil, false, fmt.Errorf("JSON object not found")
		}
		objectText = extracted
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(objectText), &obj); err != nil {
		return nil, false, err
	}
	return obj, true, nil
}

func parseJSONStringValue(s string) (any, error) {
	var parsed any
	if err := json.Unmarshal([]byte(strings.TrimSpace(s)), &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func extractFirstJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inString {
			if escape {
				escape = false
				continue
			}
			switch c {
			case '\\':
				escape = true
			case '"':
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}
