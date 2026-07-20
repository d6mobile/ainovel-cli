package tui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderStatusBar Mức dùng，Đầu vào（Cao）：
//
//	◆ provider model(,) │ ↑Đầu vào ↓Đầu ra ⚡CacheTrung bình │ (/Ngân sách) tiết kiệm X    ./
//
// ""：Mô hình、、Ngân sách。
//
//	3s  UISnapshot（Mô hìnhHoàn tất usage ）；
//
// per-role/per-model Cache，。
func renderStatusBar(snap host.UISnapshot, outputDir string, width int) string {
	dim := lipgloss.NewStyle().Foreground(colorDim)
	val := lipgloss.NewStyle().Foreground(colorMuted)

	var segs []string
	if snap.ModelName != "" {
		s := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("◆") + " "
		if snap.Provider != "" {
			s += dim.Render(snap.Provider) + " "
		}
		s += val.Render(snap.ModelName)
		if suffix := modelInfoSuffix(snap); suffix != "" {
			s += dim.Render("(" + suffix + ")")
		}
		segs = append(segs, s)
	}
	if snap.TotalInputTokens > 0 || snap.TotalOutputTokens > 0 {
		s := dim.Render("↑") + val.Render(formatTokensCompact(snap.TotalInputTokens)) +
			" " + dim.Render("↓") + val.Render(formatTokensCompact(snap.TotalOutputTokens))
		// Trung bìnhMô hình prompt cache ，"0% "。
		if snap.OverallCacheCapable && snap.OverallRecentSamples > 0 && snap.OverallRecentInput > 0 {
			rate := cacheHitRate(snap.OverallRecentCacheRead, snap.OverallRecentInput)
			s += " " + lipgloss.NewStyle().Foreground(cacheHitColor(rate)).Render("⚡"+formatPercent(rate))
		}
		segs = append(segs, s)
	}
	if snap.TotalCostUSD > 0 || snap.BudgetLimitUSD > 0 {
		cost := formatCostUSD(snap.TotalCostUSD)
		if cost == "" {
			cost = "$0"
		}
		style := val
		if snap.BudgetLimitUSD > 0 {
			// Ngân sách/——，Ngân sách。
			switch ratio := snap.TotalCostUSD / snap.BudgetLimitUSD; {
			case ratio >= 1:
				style = lipgloss.NewStyle().Foreground(colorError).Bold(true)
			case ratio >= 0.8:
				style = lipgloss.NewStyle().Foreground(colorReview)
			}
		}
		s := style.Render(cost)
		if snap.BudgetLimitUSD > 0 {
			s += dim.Render("/" + formatCostUSD(snap.BudgetLimitUSD))
		}
		if saved := formatCostUSD(snap.TotalSavedUSD); saved != "" {
			s += dim.Render(" tiết kiệm " + saved)
		}
		segs = append(segs, s)
	}

	left := strings.Join(segs, dim.Render(" │ "))
	var right string
	if outputDir != "" {
		right = dim.Render("./" + filepath.Base(outputDir))
	}
	if left == "" && right == "" {
		return dim.Render("Sẵn sàng")
	}
	return joinInlineSides(left, right, width)
}

// modelInfoSuffix Mô hình：Cửa sổ ngữ cảnh + ， "200K,med"。
func modelInfoSuffix(snap host.UISnapshot) string {
	var parts []string
	if w := formatContextWindow(snap.ModelContextWindow); w != "" {
		parts = append(parts, w)
	}
	if t := formatThinkingLevel(snap.ThinkingLevel); t != "" {
		parts = append(parts, t)
	}
	return strings.Join(parts, ",")
}

func formatThinkingLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "":
		return "tự động"
	case "medium":
		return "trung bình"
	case "off":
		return "tắt"
	case "low":
		return "thấp"
	case "high":
		return "cao"
	case "xhigh":
		return "rất cao"
	case "max":
		return "cao nhất"
	default:
		return strings.ToLower(strings.TrimSpace(level))
	}
}
