package tui

import (
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/host"
)

type fakeModelRuntime struct {
	providers   []string
	models      map[string][]host.ConfiguredModel
	curProvider string
	curModel    string
	thinking    map[string]string // role ->
	available   []agentcore.ThinkingLevel
	setCalls    []struct{ role, level string }
	switchCalls int
}

func (f *fakeModelRuntime) ConfiguredProviders() []string { return f.providers }
func (f *fakeModelRuntime) ConfiguredModelOptions(provider string) []host.ConfiguredModel {
	return f.models[provider]
}
func (f *fakeModelRuntime) CurrentModelSelection(role string) (string, string, bool) {
	return f.curProvider, f.curModel, true
}
func (f *fakeModelRuntime) AvailableThinking(role string) []agentcore.ThinkingLevel {
	return f.available
}
func (f *fakeModelRuntime) CurrentThinking(role string) string { return f.thinking[role] }
func (f *fakeModelRuntime) SwitchModel(role, provider, model string) error {
	f.switchCalls++
	f.curProvider, f.curModel = provider, model
	return nil
}
func (f *fakeModelRuntime) SetRoleThinking(role, level string) error {
	f.setCalls = append(f.setCalls, struct{ role, level string }{role, level})
	if f.thinking == nil {
		f.thinking = map[string]string{}
	}
	f.thinking[role] = level
	return nil
}

// CaoHiện tạiMô hình、，，
// Mặc định。
func TestModelSwitchKeepsUnrepresentableThinkingIntent(t *testing.T) {
	rt := &fakeModelRuntime{
		providers:   []string{"proxy"},
		models:      map[string][]host.ConfiguredModel{"proxy": {{Name: "chat-only"}}},
		curProvider: "proxy", curModel: "chat-only",
		thinking:  map[string]string{"writer": "high"},
		available: nil, // Hiện tạiMô hình“”
	}
	st := newModelSwitchState(rt, "writer")
	if st.thinkingKey() != "" {
		t.Fatalf("khi không hiển thị được high, panel phải rơi về mức kế thừa, got %q", st.thinkingKey())
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.setCalls) != 0 {
		t.Fatalf("không đổi cường độ thì không được ghi lại: %+v", rt.setCalls)
	}
	if rt.thinking["writer"] != "high" {
		t.Fatalf("ý định bị xóa thành %q, phải giữ high", rt.thinking["writer"])
	}
}

// ，。
func TestModelSwitchAppliesExplicitThinkingChange(t *testing.T) {
	rt := &fakeModelRuntime{
		providers:   []string{"proxy"},
		models:      map[string][]host.ConfiguredModel{"proxy": {{Name: "m"}}},
		curProvider: "proxy", curModel: "m",
		thinking:  map[string]string{"writer": ""},
		available: []agentcore.ThinkingLevel{"low", "high"},
	}
	st := newModelSwitchState(rt, "writer")
	st.focus = modelFocusThinking
	st.cycle(1, rt) //
	want := st.thinkingKey()
	if want == "" {
		t.Fatal("tiền điều kiện test: phải đã chuyển tới một mức cường độ không rỗng")
	}
	if err := st.apply(rt); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(rt.setCalls) != 1 || rt.setCalls[0].level != want {
		t.Fatalf("đổi rõ ràng phải ghi lại %q, got %+v", want, rt.setCalls)
	}
}
