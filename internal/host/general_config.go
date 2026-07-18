package host

import (
	"fmt"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/notify"
)

type GeneralSettingsDraft struct {
	Style  string
	Budget bootstrap.BudgetConfig
	Notify bootstrap.NotifyConfig
}

func (h *Host) ConfigureGeneralSettings(draft GeneralSettingsDraft) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	candidate := bootstrap.CloneConfig(h.cfg)
	candidate.Style = strings.TrimSpace(draft.Style)
	candidate.Budget = draft.Budget
	candidate.Notify = draft.Notify
	candidate.FillDefaults()
	if err := candidate.ValidateBase(); err != nil {
		return err
	}
	if h.configPath == "" {
		return fmt.Errorf("无法定位配置文件路径")
	}
	if err := bootstrap.SaveConfig(h.configPath, candidate); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}

	h.cfg = candidate
	h.rebindGeneralSettingsRuntimeLocked(candidate)
	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: fmt.Sprintf("Cài đặt chung đã được lưu: %s", h.configPath), Level: "info"})
	return nil
}

func (h *Host) rebindGeneralSettingsRuntimeLocked(cfg bootstrap.Config) {
	if cfg.Notify.IsEnabled() {
		h.notifier = notify.New(cfg.Notify.Command, cfg.Notify.Events)
	} else {
		h.notifier = nil
	}

	if sentinel := NewBudgetSentinel(cfg.Budget,
		func() float64 { c, _, _, _, _ := h.usage.Totals(); return c },
		func(reason string) { h.abortWithEvent(reason, "error") },
		func(level, summary string) {
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: summary, Level: level})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: level, Title: "ainovel: 预算", Body: summary})
		},
	); sentinel != nil {
		h.budget = sentinel
		h.usage.SetOnCost(sentinel.OnCost)
		h.usage.SetOnMissingUsage(func() {
			const blind = "预算盲区: 模型未返回 usage 数据，成本统计为 0，预算上限不会触发（自定义模型请确认注册表价格或上游 include_usage）"
			h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: blind, Level: "warn"})
			h.notifier.Send(notify.Notification{Kind: notify.KindBudget, Level: "warn", Title: "ainovel: 预算", Body: blind})
		})
	} else {
		h.budget = nil
		h.usage.SetOnCost(nil)
		h.usage.SetOnMissingUsage(nil)
	}
	if h.engine != nil {
		h.engine.budget = h.budget
	}
}
