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
	h.budget = h.newBudgetSentinel(candidate.Budget)
	if candidate.Notify.IsEnabled() {
		h.notifier = notify.New(candidate.Notify.Command, candidate.Notify.Events)
	} else {
		h.notifier = nil
	}

	h.emitEvent(Event{Time: time.Now(), Category: "SYSTEM", Summary: fmt.Sprintf("Cài đặt chung đã được lưu: %s", h.configPath), Level: "info"})
	return nil
}
