package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// outlineGridThreshold Dàn ý。
// short tier  25 ，20 、" Đang chạy"；
//
//	layered  n  20，。
const outlineGridThreshold = 20

// renderOutlineSection ：（" Đang chạy"），。
func renderOutlineSection(snap host.UISnapshot, contentW int) string {
	if len(snap.Outline) < outlineGridThreshold {
		return renderOutlineList(snap, contentW)
	}
	return renderOutlineGrid(snap, contentW)
}

// renderOutlineList （）。" Đang chạy"，。
func renderOutlineList(snap host.UISnapshot, contentW int) string {
	var b strings.Builder
	for _, e := range snap.Outline {
		ch := fmt.Sprintf("%2d", e.Chapter)
		var marker, chStyle string
		titleStyle := cardContentStyle
		switch {
		case snap.CompletedCount >= e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
		case snap.InProgressChapter == e.Chapter:
			marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
			chStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
		default:
			marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
			chStyle = lipgloss.NewStyle().Foreground(colorDim).Render(ch)
			titleStyle = lipgloss.NewStyle().Foreground(colorMuted)
		}
		title := truncate(e.Title, contentW-6)
		line := marker + chStyle + " " + titleStyle.Render(title)
		if snap.InProgressChapter == e.Chapter {
			line += lipgloss.NewStyle().Foreground(colorAccent).Italic(true).Render("  Đang chạy")
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineGrid Dàn ý""，rộng。
//
//	contentW （1-4），（""）。
//
// ："  Đang chạy"——，
//
//	▸  +  + Tổng quan"Đang viết  N " Đang chạy。
func renderOutlineGrid(snap host.UISnapshot, contentW int) string {
	n := len(snap.Outline)
	if n == 0 {
		return ""
	}
	chNumW := 2
	titleW := 0
	for _, e := range snap.Outline {
		if w := len(strconv.Itoa(e.Chapter)); w > chNumW {
			chNumW = w
		}
		if w := lipgloss.Width(e.Title); w > titleW {
			titleW = w
		}
	}
	// rộng 14（ 7 ）；， cell
	if titleW > 14 {
		titleW = 14
	} else if titleW < 4 {
		titleW = 4
	}
	cellW := 3 + chNumW + titleW // marker(1) + (1) +  + (1) +
	gutter := 4
	cols := (contentW + gutter) / (cellW + gutter)
	if cols < 1 {
		cols = 1
	} else if cols > 4 {
		cols = 4
	}
	rows := (n + cols - 1) / cols

	var b strings.Builder
	cellStyle := lipgloss.NewStyle().Width(cellW)
	gutterStr := strings.Repeat(" ", gutter)
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			idx := c*rows + r
			if idx >= n {
				break
			}
			cell := renderOutlineCell(snap.Outline[idx], snap, chNumW, titleW)
			//  cell  cellW  + gutter；Hiện tại cell
			if c < cols-1 && (c+1)*rows+r < n {
				b.WriteString(cellStyle.Render(cell))
				b.WriteString(gutterStr)
			} else {
				b.WriteString(cell)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// renderOutlineCell  cell：Hoàn tất（●）/  Đang chạy（▸）/ （○）。
func renderOutlineCell(e host.OutlineSnapshot, snap host.UISnapshot, chNumW, titleW int) string {
	chStr := fmt.Sprintf("%*d", chNumW, e.Chapter)
	title := truncateWidth(e.Title, titleW)
	var marker, chRendered, titleRendered string
	switch {
	case snap.CompletedCount >= e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorSuccess).Render("●")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = cardContentStyle.Render(title)
	case snap.InProgressChapter == e.Chapter:
		marker = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("▸")
		chRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render(title)
	default:
		marker = lipgloss.NewStyle().Foreground(colorDim).Render("○")
		chRendered = lipgloss.NewStyle().Foreground(colorDim).Render(chStr)
		titleRendered = lipgloss.NewStyle().Foreground(colorMuted).Render(title)
	}
	return marker + " " + chRendered + " " + titleRendered
}

// truncateWidth "rộng"（Trung bình 2 ）， lipgloss.Width 。
// ， cell  truncate 。
func truncateWidth(s string, maxW int) string {
	if lipgloss.Width(s) <= maxW {
		return s
	}
	var b strings.Builder
	cur := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if cur+rw > maxW {
			break
		}
		b.WriteRune(r)
		cur += rw
	}
	return b.String()
}

// renderDetailContent 。
// x（Dàn ý、Vai trò），（、）。
func renderDetailContent(snap host.UISnapshot, contentW int) string {
	var b strings.Builder

	// Dàn ý
	if len(snap.Outline) > 0 {
		outlineHeader := ":: Dàn ý"
		if snap.Layered {
			outlineHeader = fmt.Sprintf(":: Dàn ý（%s · dàn ý lập kế hoạch động）", snap.CurrentVolumeArc)
		}
		b.WriteString(panelTitleStyle.Render(outlineHeader))
		b.WriteString("\n")
		b.WriteString(renderOutlineSection(snap, contentW))
		//
		compassStyle := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
		if snap.Layered {
			if snap.NextVolumeTitle != "" {
				b.WriteString(compassStyle.Render("  ┄ Quyển tiếp theo: " + snap.NextVolumeTitle))
				b.WriteString("\n")
			}
			b.WriteString(compassStyle.Render("  ··· Các chương sau sẽ tự động tạo khi sáng tác tiến triển"))
			b.WriteString("\n")
			if snap.CompassDirection != "" {
				direction := fmt.Sprintf("  → Kết cục: %s", snap.CompassDirection)
				if snap.CompassScale != "" {
					direction += "（" + snap.CompassScale + "）"
				}
				b.WriteString(compassStyle.Render(truncate(direction, contentW)))
				b.WriteString("\n")
			}
		}
		b.WriteString("\n")
	}

	// Vai trò
	if len(snap.Characters) > 0 {
		b.WriteString(panelTitleStyle.Render(":: Vai trò"))
		b.WriteString("\n")
		for _, c := range snap.Characters {
			writeBulletWrapped(&b, c, contentW, cardContentStyle)
		}
		b.WriteString("\n")
	}

	// ：Vai trò +  5
	if snap.SupportingCount > 0 {
		b.WriteString(panelTitleStyle.Render(":: Hệ sinh thái nhân vật phụ"))
		b.WriteString("\n")
		b.WriteString(cardContentStyle.Render(truncate(fmt.Sprintf("Đã xuất hiện: %d nhân vật", snap.SupportingCount), contentW)))
		b.WriteString("\n")
		for _, name := range snap.RecentSupporting {
			writeBulletWrapped(&b, name, contentW, cardContentStyle)
		}
		b.WriteString("\n")
	}

	// Tiền đề
	if snap.Premise != "" {
		b.WriteString(panelTitleStyle.Render(":: Tiền đề"))
		b.WriteString("\n")
		for _, line := range wrapStreamText(snap.Premise, contentW) {
			b.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render(line))
			b.WriteString("\n")
		}
		b.WriteString("\n\n")
	}

	if snap.LastCommitSummary != "" {
		b.WriteString(cardTitleStyle.Render("~ Lần nộp gần nhất ~"))
		b.WriteString("\n")
		writeWrapped(&b, snap.LastCommitSummary, contentW, cardContentStyle)
		b.WriteString("\n")
	}

	if snap.LastReviewSummary != "" {
		b.WriteString(cardTitleStyle.Render("~ Lần thẩm định gần nhất ~"))
		b.WriteString("\n")
		writeWrapped(&b, snap.LastReviewSummary, contentW, cardContentStyle)
		b.WriteString("\n")
	}

	if len(snap.RecentSummaries) > 0 {
		b.WriteString(cardTitleStyle.Render("~ Tóm tắt ~"))
		b.WriteString("\n")
		for _, s := range snap.RecentSummaries {
			writeWrapped(&b, s, contentW, cardContentStyle)
		}
	}

	return b.String()
}

// writeWrapped rộng，。
func writeWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for _, line := range wrapStreamText(text, max(8, contentW)) {
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}
}

// writeBulletWrapped "· "：rộng，。
func writeBulletWrapped(b *strings.Builder, text string, contentW int, style lipgloss.Style) {
	for i, line := range wrapStreamText(text, max(8, contentW-2)) {
		prefix := "· "
		if i > 0 {
			prefix = "  "
		}
		b.WriteString(style.Render(prefix + line))
		b.WriteString("\n")
	}
}
