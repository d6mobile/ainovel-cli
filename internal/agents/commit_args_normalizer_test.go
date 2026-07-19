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

func TestNormalizeCommitChapterArgsRejectsStringifiedObjectForRequiredArray(t *testing.T) {
	raw := json.RawMessage(`{"chapter":1,"summary":"s","characters":"{\"name\":\"A\"}","key_events":["e"]}`)
	if _, _, err := normalizeCommitChapterArgs(raw); err == nil {
		t.Fatal("expected error for object where required array is required")
	}
}

func TestNormalizeCommitChapterArgsParsesRequiredStringifiedArraysAndDropsMalformedOptionalArrays(t *testing.T) {
	raw := json.RawMessage(`{"chapter":4,"summary":"s","characters":"[\"Lâm Tịch\",\"Hàn Dục\"]","key_events":"[\"Mẹ qua đời\",\"Lâm Tịch rời đi Paris\"]","state_changes":"[{\"entity\":\"Tịch Trà Quán\",\"field\": tình trạng kinh doanh}]"}`)
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
	if _, ok := obj["characters"].([]any); !ok {
		t.Fatalf("characters = %T, want []any", obj["characters"])
	}
	if _, ok := obj["key_events"].([]any); !ok {
		t.Fatalf("key_events = %T, want []any", obj["key_events"])
	}
	if _, ok := obj["state_changes"]; ok {
		t.Fatal("malformed optional state_changes should be dropped")
	}
}

func TestNormalizeCommitChapterArgsRepairsObservedRelationshipKeyTypos(t *testing.T) {
	raw := json.RawMessage(`{"chapter":1,"summary":"s","characters":["A"],"key_events":["e"],"relationship_changes":[{"character_a":"A","charaacter_b":"B","relation":"r1"},{"character_a":"A","chraactor_b":"C","relation":"r2"}]}`)
	got, changed, err := normalizeCommitChapterArgs(raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var obj struct {
		RelationshipChanges []map[string]any `json:"relationship_changes"`
	}
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := obj.RelationshipChanges[0]["character_b"]; got != "B" {
		t.Fatalf("relationship_changes[0].character_b = %v, want B", got)
	}
	if _, ok := obj.RelationshipChanges[0]["charaacter_b"]; ok {
		t.Fatal("relationship_changes[0].charaacter_b should be removed")
	}
	if got := obj.RelationshipChanges[1]["character_b"]; got != "C" {
		t.Fatalf("relationship_changes[1].character_b = %v, want C", got)
	}
	if _, ok := obj.RelationshipChanges[1]["chraactor_b"]; ok {
		t.Fatal("relationship_changes[1].chraactor_b should be removed")
	}
}

func TestNormalizeCommitChapterArgsDropsObservedUnsupportedRootFields(t *testing.T) {
	raw := json.RawMessage(`{"chapter":1,"title":"unsupported","summary":"s","characters":["A"],"key_events":["e"]}`)
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
	if _, ok := obj["title"]; ok {
		t.Fatal("title should be removed")
	}
	if got := obj["summary"]; got != "s" {
		t.Fatalf("summary = %v, want s", got)
	}
}

func TestNormalizeCommitChapterArgsRepairsObservedStateChangeFieldsEmbeddedInEntity(t *testing.T) {
	raw := json.RawMessage(`{"chapter":1,"summary":"s","characters":["A"],"key_events":["e"],"state_changes":[{"entity":"Lâm Tịch, field: \"năng lực đặc biệt\", new_value: có thể đọc tâm thanh, reason: trọng sinh mang theo năng lực mới"}]}`)
	got, changed, err := normalizeCommitChapterArgs(raw)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	var obj struct {
		StateChanges []map[string]any `json:"state_changes"`
	}
	if err := json.Unmarshal(got, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	change := obj.StateChanges[0]
	if got := change["entity"]; got != "Lâm Tịch" {
		t.Fatalf("entity = %v, want Lâm Tịch", got)
	}
	if got := change["field"]; got != "năng lực đặc biệt" {
		t.Fatalf("field = %v, want năng lực đặc biệt", got)
	}
	if got := change["new_value"]; got != "có thể đọc tâm thanh" {
		t.Fatalf("new_value = %v, want có thể đọc tâm thanh", got)
	}
	if got := change["reason"]; got != "trọng sinh mang theo năng lực mới" {
		t.Fatalf("reason = %v, want trọng sinh mang theo năng lực mới", got)
	}
}
