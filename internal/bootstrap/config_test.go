package bootstrap

import (
	"errors"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/notify"
)

func TestConfigResolveReasoningEffort(t *testing.T) {
	cfg := Config{
		ReasoningEffort: "low", // top-level default
		Roles: map[string]RoleConfig{
			"writer":    {Provider: "p", Model: "m", ReasoningEffort: "high"}, // role override
			"architect": {Provider: "p", Model: "m"},                          // no reasoning_effort, should fall back to default
		},
	}

	cases := []struct {
		role string
		want string
	}{
		{"writer", "high"},   // role override takes precedence
		{"architect", "low"}, // role unset -> fall back to top-level default
		{"editor", "low"},    // role missing -> top-level default
		{"", "low"},          // empty -> top-level default
		{"default", "low"},   // default -> top-level default
		{"arbiter", "low"},   // non-configured role (arbiter follows top-level default)
	}
	for _, c := range cases {
		if got := cfg.ResolveReasoningEffort(c.role); got != c.want {
			t.Errorf("ResolveReasoningEffort(%q) = %q, want %q", c.role, got, c.want)
		}
	}

	// When the top-level default is also empty, roles without overrides return "" (no override).
	empty := Config{Roles: map[string]RoleConfig{"writer": {ReasoningEffort: "xhigh"}}}
	if got := empty.ResolveReasoningEffort("editor"); got != "" {
		t.Errorf("khi mặc định rỗng, editor phải trả \"\", got %q", got)
	}
	if got := empty.ResolveReasoningEffort("writer"); got != "xhigh" {
		t.Errorf("khi mặc định rỗng, override writer phải có hiệu lực, got %q", got)
	}
}

func TestValidateBaseRejectsNonConfigurableRoles(t *testing.T) {
	for _, role := range []string{"coordinator", "arbiter"} {
		t.Run(role, func(t *testing.T) {
			cfg := Config{
				Provider:  "openrouter",
				ModelName: "test-model",
				Providers: map[string]ProviderConfig{
					"openrouter": {APIKey: "sk-test-123456"},
				},
				Roles: map[string]RoleConfig{
					role: {Provider: "openrouter", Model: "test-model"},
				},
			}

			err := cfg.ValidateBase()
			if err == nil {
				t.Fatalf("roles.%s phải bị từ chối", role)
			}
			if !errors.Is(err, errs.ErrConfig) {
				t.Fatalf("phải wrap errs.ErrConfig, got: %v", err)
			}
		})
	}
}

func TestValidateBaseNotifyEventsMatchRuntimeContract(t *testing.T) {
	validConfig := func(events []string) Config {
		return Config{
			Provider:  "openrouter",
			ModelName: "test-model",
			Providers: map[string]ProviderConfig{
				"openrouter": {APIKey: "sk-test-123456"},
			},
			Notify: NotifyConfig{Events: events},
		}
	}

	cfg := validConfig(notify.Kinds())
	if err := cfg.ValidateBase(); err != nil {
		t.Fatalf("hợp đồng event notify hiện tại phải qua toàn bộ kiểm tra cấu hình: %v", err)
	}

	cfg = validConfig([]string{"repeat"})
	if err := cfg.ValidateBase(); !errors.Is(err, errs.ErrConfig) {
		t.Fatalf("event repeat cũ phải bị từ chối, got: %v", err)
	}
}

func TestProviderStreamIdleTimeoutValue(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", defaultStreamIdleTimeout, false},
		{"900s", 15 * time.Minute, false},
		{"15m", 15 * time.Minute, false},
		{"abc", 0, true},
		{"-5s", 0, true},
		{"0", 0, true}, // not providing a "disable watchdog" mode — real dead streams need a finite bound
	}
	for _, c := range cases {
		got, err := ProviderConfig{StreamIdleTimeout: c.in}.StreamIdleTimeoutValue()
		if c.wantErr {
			if err == nil {
				t.Errorf("%q phải báo lỗi", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q = (%v, %v), want %v", c.in, got, err, c.want)
		}
	}
}

func TestValidateBaseRejectsBadStreamIdleTimeout(t *testing.T) {
	cfg := Config{
		Provider:  "openrouter",
		ModelName: "test-model",
		Providers: map[string]ProviderConfig{
			"openrouter": {APIKey: "sk-test-123456", StreamIdleTimeout: "fast"},
		},
	}
	if err := cfg.ValidateBase(); !errors.Is(err, errs.ErrConfig) {
		t.Fatalf("stream_idle_timeout không hợp lệ phải bị từ chối và wrap ErrConfig, got: %v", err)
	}
}
