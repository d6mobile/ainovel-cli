package agents

import (
	"encoding/json"
	"testing"
)

func TestNormalizeCommitChapterArgsParsesStringifiedArrays(t *testing.T) {
	raw := json.RawMessage(`{"chapter":1,"summary":"s","characters":["A"],"key_events":["e"],"state_changes":"[{\"entity\":\"A\",\"field\":\"status\",\"new_value\":\"awake\"}]","timeline_events":"[{\"time\":\"morning\",\"event\":\"A wakes\",\"characters\":[\"A\"]}]"}`)
	got, changed, err := normalizeCommitChapterArgs(raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := obj["state_changes"].([]any); !ok {
		t.Fatalf("state_changes = %T, want []any", obj["state_changes"])
	}
	if _, ok := obj["timeline_events"].([]any); !ok {
		t.Fatalf("timeline_events = %T, want []any", obj["timeline_events"])
	}
}

func TestNormalizeCommitChapterArgsParsesDoubleEncodedRoot(t *testing.T) {
	raw := json.RawMessage(`"{\"chapter\":1,\"summary\":\"s\",\"characters\":[\"A\"],\"key_events\":[\"e\"],\"state_changes\":[{\"entity\":\"A\",\"field\":\"status\",\"new_value\":\"awake\"}],\"timeline_events\":[{\"time\":\"morning\",\"event\":\"A wakes\"}]}"`)
	got, changed, err := normalizeCommitChapterArgs(raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if !json.Valid(got) || len(got) == 0 || got[0] != '{' {
		t.Fatalf("got %s, want JSON object", got)
	}
}

func TestNormalizeCommitChapterArgsExtractsFencedJSON(t *testing.T) {
	raw := json.RawMessage("\"Here is the call:\\n```json\\n{\\\"chapter\\\":1,\\\"summary\\\":\\\"s\\\",\\\"characters\\\":[\\\"A\\\"],\\\"key_events\\\":[\\\"e\\\"],\\\"timeline_events\\\":\\\"[{\\\\\\\"time\\\\\\\":\\\\\\\"m\\\\\\\",\\\\\\\"event\\\\\\\":\\\\\\\"e\\\\\\\"}]\\\",\\\"state_changes\\\":[]}\\n```\"")
	got, changed, err := normalizeCommitChapterArgs(raw)
	if err != nil {
		t.Fatalf("normalize fenced: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var obj map[string]any
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := obj["timeline_events"].([]any); !ok {
		t.Fatalf("timeline_events = %T", obj["timeline_events"])
	}
}

func TestNormalizeCommitChapterArgsRejectsStringifiedObjectForArray(t *testing.T) {
	raw := json.RawMessage(`{"chapter":1,"summary":"s","characters":["A"],"key_events":["e"],"state_changes":"{\"entity\":\"A\"}"}`)
	if _, _, err := normalizeCommitChapterArgs(raw); err == nil {
		t.Fatal("expected error for object where array is required")
	}
}
