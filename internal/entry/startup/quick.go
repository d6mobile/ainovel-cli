package startup

import (
	"fmt"
	"strings"
)

// PrepareQuick Đầu vào Engine 。
func PrepareQuick(req Request) (Plan, error) {
	prompt := strings.TrimSpace(req.UserPrompt)
	if prompt == "" {
		return Plan{}, fmt.Errorf("prompt is required")
	}
	return Plan{
		Mode:        ModeQuick,
		DisplayName: "Bắt đầu nhanh",
		RawPrompt:   prompt,
	}, nil
}
