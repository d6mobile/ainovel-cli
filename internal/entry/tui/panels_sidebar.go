package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/voocel/ainovel-cli/internal/host"
)

// renderStateContent (/)， stateVP.SetContent 。
func renderStateContent(snap host.UISnapshot, contentW int) string {
	contentW = max(12, contentW)
	agents := sidebarAgents(snap.Agents)
	idleAgents := sidebarIdleAgents(snap.Agents)
	var sections []string

	if snap.RecoveryLabel != "" {
		sections = append(sections, lipgloss.NewStyle().Foreground(colorMuted).Italic(true).
			Render(truncate(snap.RecoveryLabel, contentW)))
	}

	var overview strings.Builder
	overview.WriteString(renderField("Trạng thái chạy", snapshotRuntimeStateLabel(snap.RuntimeState)))
	overview.WriteString(renderField("Giai đoạn", snapshotPhaseLabel(snap.Phase)))
	overview.WriteString(renderField("Luồng", snapshotFlowLabel(snap.Flow)))
	if snap.AdvanceMode == "review" {
		advance := "Nghiệm thu từng chương"
		if snap.AdvancePermitChapter > 0 {
			advance = fmt.Sprintf("Đã cho phép chương %d", snap.AdvancePermitChapter)
		}
		overview.WriteString(renderField("Tiến hành", advance))
	} else if snap.AdvanceMode == "auto" {
		overview.WriteString(renderField("Tiến hành", "Tự động"))
	}
	if snap.Layered {
		overview.WriteString(renderField("Đã hoàn thành", fmt.Sprintf("%d chương", snap.CompletedCount)))
		// ：Hiện tại，"Đã lập kế hoạch"，
		//  EstimatedChapters （ 92），Dàn ý。
		// progress.TotalChapters  ContextProfile ， UI。
		if planned := len(snap.Outline); planned > 0 {
			overview.WriteString(renderField("Đã lập kế hoạch", fmt.Sprintf("%d chương", planned)))
		}
	} else {
		switch {
		case snap.TotalChapters > 0:
			overview.WriteString(renderField("Tiến độ", fmt.Sprintf("%d / %d chương", snap.CompletedCount, snap.TotalChapters)))
		default:
			overview.WriteString(renderField("Đã hoàn thành", fmt.Sprintf("%d chương", snap.CompletedCount)))
		}
	}
	overview.WriteString(renderField("Số chữ", formatNumber(snap.TotalWordCount)))
	if label, ch := inProgressDisplay(snap); label != "" {
		overview.WriteString(renderField(label, fmt.Sprintf("Chương %d", ch)))
	}
	if headline := snapshotHeadline(snap); headline != "" {
		label := "Hiện tại"
		if !snap.IsRunning {
			label = "Chờ khôi phục"
		}
		overview.WriteString(renderHighlightField(label, truncate(headline, contentW-10)))
	}
	sections = append(sections, renderSidebarSection("Tổng quan", overview.String(), contentW))

	if len(agents) > 0 {
		var agentBody strings.Builder
		for _, agent := range agents {
			agentBody.WriteString(renderAgentLine(agent, contentW))
			agentBody.WriteString("\n")
		}
		if len(idleAgents) > 0 {
			agentBody.WriteString(lipgloss.NewStyle().Foreground(colorDim).Render("Chờ: " + truncate(strings.Join(idleAgents, " · "), max(8, contentW-2))))
			agentBody.WriteString("\n")
		}
		sections = append(sections, renderSidebarSection("Vai trò đang chạy", agentBody.String(), contentW))
	}

	if len(snap.PendingRewrites) > 0 {
		var rewrite strings.Builder
		rewrite.WriteString(renderHighlightField("Hàng đợi", fmt.Sprintf("%v", snap.PendingRewrites)))
		if snap.RewriteReason != "" {
			rewrite.WriteString(renderField("Lý do", truncate(snap.RewriteReason, contentW-10)))
		}
		sections = append(sections, renderSidebarSection("Làm lại", rewrite.String(), contentW))
	}

	if snap.PendingSteer != "" {
		sections = append(sections, renderSidebarSection("Can thiệp",
			renderHighlightField("Chờ xử lý", truncate(snap.PendingSteer, contentW-10)), contentW))
	}
	if snap.HasAdvanceHold {
		sections = append(sections, renderSidebarSection("Dừng nghiệm thu",
			renderHighlightField("Chờ", truncate(snap.AdvanceHoldReason, contentW-10)), contentW))
	}

	if body := renderUsageSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("Mức dùng", body, contentW))
	}

	if body := renderCacheSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("Cache", body, contentW))
	}

	if body := renderContextSidebar(snap, contentW); body != "" {
		sections = append(sections, renderSidebarSection("Ngữ cảnh", body, contentW))
	}

	return strings.Join(sections, "\n\n")
}

func renderAgentLine(agent host.AgentSnapshot, width int) string {
	stateColor := taskStatusColor(agent.State)
	icon := lipgloss.NewStyle().Foreground(stateColor).Render(agentStateIcon(agent.State))
	badge := lipgloss.NewStyle().Foreground(stateColor).Render(agentStateLabel(agent.State))
	name := lipgloss.NewStyle().Bold(true).Foreground(bodyTextColor).Render(agentDisplayName(agent.Name))
	line := icon + " " + name + " " + badge

	taskLine := agentTaskLine(agent)
	if taskLine != "" {
		line += "\n" + lipgloss.NewStyle().Foreground(colorDim).Render("  "+truncate(taskLine, max(8, width-2)))
	}

	detail := agent.Summary
	if agent.Tool != "" {
		detail = agent.Tool
	}
	if agent.State == "idle" && detail == "Chờ" {
		detail = ""
	}
	if detail != "" && detail != taskLine {
		line += "\n" + lipgloss.NewStyle().Foreground(colorMuted).Render("  "+truncate(detail, max(8, width-2)))
	}
	if ctx := agentContextLine(agent); ctx != "" {
		line += "\n" + lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("  "+truncate(ctx, max(8, width-2)))
	}
	return line
}

func renderSidebarSection(title, body string, width int) string {
	body = strings.TrimRight(body, "\n")
	if body == "" {
		return ""
	}
	lineW := max(0, width-lipgloss.Width(title)-1)
	header := panelTitleStyle.Render(title) + " " +
		lipgloss.NewStyle().Foreground(colorDim).Render(strings.Repeat("─", lineW))
	card := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(colorDim).
		PaddingLeft(1).
		Render(body)
	return header + "\n" + card
}

func sidebarAgents(agents []host.AgentSnapshot) []host.AgentSnapshot {
	var out []host.AgentSnapshot
	for _, agent := range agents {
		if agent.State == "idle" {
			continue
		}
		out = append(out, agent)
	}
	if len(out) == 0 {
		out = append(out, agents...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := out[i], out[j]
		if agentStateRank(li.State) != agentStateRank(lj.State) {
			return agentStateRank(li.State) < agentStateRank(lj.State)
		}
		return agentOrder(li.Name) < agentOrder(lj.Name)
	})
	return out
}

func sidebarIdleAgents(agents []host.AgentSnapshot) []string {
	var names []string
	hasActive := false
	for _, agent := range agents {
		if agent.State != "idle" {
			hasActive = true
			continue
		}
		names = append(names, agentDisplayName(agent.Name))
	}
	if !hasActive {
		return nil
	}
	sort.Strings(names)
	return names
}

// inProgressDisplay " Đang chạy"。
//
//	flow （Trau chuốt/Viết lại/Viết）；in_progress_chapter  flow  stale：
//	 - polishing/rewriting  pending_rewrites Trung bình → Hàng đợi
//	 -  0
func inProgressDisplay(snap host.UISnapshot) (label string, chapter int) {
	ch := snap.InProgressChapter
	switch snap.Flow {
	case "polishing":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "Đang trau chuốt", ch
	case "rewriting":
		if ch <= 0 || !slices.Contains(snap.PendingRewrites, ch) {
			if len(snap.PendingRewrites) == 0 {
				return "", 0
			}
			ch = snap.PendingRewrites[0]
		}
		return "Đang viết lại", ch
	default:
		if ch <= 0 {
			return "", 0
		}
		return "Đang viết", ch
	}
}

func snapshotHeadline(snap host.UISnapshot) string {
	if snap.PendingSteer != "" {
		if !snap.IsRunning {
			return "Chờ khôi phục: xử lý can thiệp người dùng"
		}
		return "Chờ xử lý can thiệp người dùng"
	}
	if len(snap.PendingRewrites) > 0 {
		if !snap.IsRunning {
			return "Chờ khôi phục: xử lý làm lại"
		}
		return "Chờ xử lý làm lại"
	}
	if snap.AdvanceMode == "review" && !snap.IsRunning && snap.Phase == "writing" {
		return "Nghiệm thu từng chương: chờ cho phép chương tiếp theo"
	}
	return ""
}

func snapshotPhaseLabel(phase string) string {
	switch phase {
	case "premise":
		return "Tiền đề"
	case "outline":
		return "Dàn ý"
	case "writing":
		return "Viết"
	case "complete":
		return "Hoàn tất"
	case "init":
		return "Khởi tạo"
	default:
		if phase == "" {
			return "-"
		}
		return phase
	}
}

func snapshotRuntimeStateLabel(state string) string {
	switch state {
	case "running":
		return "Đang chạy"
	case "pausing":
		return "Đang tạm dừng"
	case "paused":
		return "Đã tạm dừng"
	case "completed":
		return "Đã hoàn thành"
	default:
		return "Rảnh"
	}
}

func snapshotFlowLabel(flow string) string {
	switch flow {
	case "":
		return "-"
	case "writing":
		return "Viết"
	case "reviewing":
		return "Thẩm định"
	case "rewriting":
		return "Viết lại"
	case "polishing":
		return "Trau chuốt"
	case "steering":
		return "Can thiệp"
	default:
		return flow
	}
}

func renderUsageSidebar(snap host.UISnapshot, width int) string {
	if snap.TotalInputTokens <= 0 && snap.TotalOutputTokens <= 0 && snap.TotalCostUSD <= 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderField("Đầu vào", formatTokensCompact(snap.TotalInputTokens)))
	b.WriteString(renderField("Đầu ra", formatTokensCompact(snap.TotalOutputTokens)))
	if cost := formatCostUSD(snap.TotalCostUSD); cost != "" {
		b.WriteString(renderField("Chi phí", cost))
	}
	if saved := formatCostUSD(snap.TotalSavedUSD); saved != "" {
		b.WriteString(renderField("Tiết kiệm", saved))
	}
	if snap.BudgetLimitUSD > 0 {
		pct := snap.TotalCostUSD / snap.BudgetLimitUSD * 100
		b.WriteString(renderField("Ngân sách", fmt.Sprintf("$%.2f/$%.2f (%.0f%%)", snap.TotalCostUSD, snap.BudgetLimitUSD, pct)))
	}

	agentStats := usageStatsByCost(snap.CachePerAgent)
	if len(agentStats) > 0 {
		b.WriteString(renderUsageGroupHeader("Vai trò", width))
		limit := min(len(agentStats), 4)
		for i := 0; i < limit; i++ {
			a := agentStats[i]
			b.WriteString(renderUsageLine(agentDisplayName(a.Role), eventAgentColor(a.Role), a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	modelStats := usageStatsByCost(snap.CachePerModel)
	if len(modelStats) > 0 {
		b.WriteString(renderUsageGroupHeader("Mô hình", width))
		limit := min(len(modelStats), 4)
		for i := 0; i < limit; i++ {
			a := modelStats[i]
			b.WriteString(renderUsageLine(modelDisplayName(a.Model), bodyTextColor, a.Input, a.Output, a.Cost, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func usageStatsByCost(in []host.AgentCacheStat) []host.AgentCacheStat {
	out := append([]host.AgentCacheStat(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Input+out[i].Output > out[j].Input+out[j].Output
	})
	return out
}

func renderUsageGroupHeader(label string, width int) string {
	line := lipgloss.NewStyle().Foreground(colorDim).
		Render(strings.Repeat("·", max(8, width-lipgloss.Width(label)-3)))
	return lipgloss.NewStyle().Foreground(colorMuted).Render(label+" ") + line + "\n"
}

func renderUsageLine(name string, color lipgloss.TerminalColor, input, output int, cost float64, width int) string {
	nameW := 11
	if width < 24 {
		nameW = 8
	}
	nameCell := lipgloss.NewStyle().Foreground(color).Width(nameW).
		Render(truncate(name, nameW))
	tokens := formatTokensCompact(input + output)
	right := tokens
	if costStr := formatCostUSD(cost); costStr != "" {
		right += " · " + costStr
	}
	// rộng，padding ；，
	// "gpt-5.6-sol5.3k" Mô hìnhMức dùng。
	return fitInlineLine(nameCell+" "+lipgloss.NewStyle().Foreground(colorDim).Render(right), width)
}

func modelDisplayName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "unknown"
	}
	parts := strings.Split(model, "/")
	if len(parts) >= 3 {
		return strings.Join(parts[1:], "/")
	}
	if len(parts) == 2 {
		return parts[1]
	}
	return model
}

// renderCacheSidebar "Cache"。
//
// ：
//  1. token：，section
//  2. Hiện tại role  prompt cache Mô hình："Chưa bật"
//  3. ："Trung bình/10 · Tiết kiệm · /"+  + per-role
//
// per-role  capable "/10%"； capable "Chưa bật"。
//
//	vs  N ""vs"ThấpTrung bình"。
func renderCacheSidebar(snap host.UISnapshot, width int) string {
	//  streaming  OpenAI  final usage chunk ——  0，
	// " cache""Mức dùngThấp"，，
	// Cache。Cao nhất。
	if snap.MissingAssistantUsage > 0 && snap.TotalInputTokens <= 0 {
		warn := lipgloss.NewStyle().Foreground(colorError).Bold(true).
			Render(fmt.Sprintf("⚠ Upstream chưa trả usage (%d lần)", snap.MissingAssistantUsage))
		hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("Kiểm tra provider stream_options.include_usage", max(8, width-2)))
		return warn + "\n" + hint + "\n"
	}

	if snap.TotalInputTokens <= 0 && snap.TotalCacheWriteTokens <= 0 {
		return ""
	}

	// Chưa bật → ，"0% Trung bình"
	if !snap.OverallCacheCapable && snap.TotalCacheReadTokens == 0 && snap.TotalCacheWriteTokens == 0 {
		return lipgloss.NewStyle().Foreground(colorDim).Italic(true).
			Render(truncate("Hiện tạiMô hìnhChưa bật prompt cache", max(8, width-2))) + "\n"
	}

	var b strings.Builder

	// ： +  N ，， "X% · N Y%"
	// （ / Trung bình / ）。
	overallHit := cacheHitRate(snap.TotalCacheReadTokens, snap.TotalInputTokens)
	b.WriteString(renderField("Trúng lũy kế", colorPercent(overallHit)))
	if snap.OverallRecentSamples > 0 && snap.OverallRecentInput > 0 {
		recent := cacheHitRate(snap.OverallRecentCacheRead, snap.OverallRecentInput)
		b.WriteString(renderField(fmt.Sprintf("Trúng %d gần đây", snap.OverallRecentSamples), colorPercent(recent)))
	}

	if savedStr := formatCostUSD(snap.TotalSavedUSD); savedStr != "" {
		b.WriteString(renderField("Tiết kiệm", savedStr))
	}

	// /。 0  OpenAI / Gemini Giao thức——
	// Tự động caching，cache （Trung bìnhĐầu vào，
	//  cache ），Giao thức cache_creation ，。
	//  Anthropic / Bedrock ，（5m +25%/1h +100%），
	// 。
	b.WriteString(renderField("Cache đọc", formatTokensCompact(snap.TotalCacheReadTokens)))
	if snap.TotalCacheWriteTokens > 0 {
		b.WriteString(renderField("Cache ghi", formatTokensCompact(snap.TotalCacheWriteTokens)))
	} else if snap.TotalCacheReadTokens > 0 {
		hint := lipgloss.NewStyle().Foreground(colorDim).Italic(true).Render("(cache tự động không phụ phí)")
		b.WriteString(renderField("Cache ghi", "0 "+hint))
	}

	//  = Trung bình（/）。
	// Trung bình， tui.log "Cache"warn。
	if snap.TotalCacheBreaks > 0 {
		v := lipgloss.NewStyle().Foreground(colorReview).Render(fmt.Sprintf("%d lần", snap.TotalCacheBreaks))
		b.WriteString(renderField("Đứt chuỗi", v))
	}

	// Arbiter x prompt cache（KB ，），
	// "Chưa bật""0%"；Mức dùng。
	var roles []host.AgentCacheStat
	for _, a := range snap.CachePerAgent {
		if a.Role != "arbiter" {
			roles = append(roles, a)
		}
	}
	if len(roles) > 0 {
		b.WriteString(lipgloss.NewStyle().Foreground(colorDim).
			Render(strings.Repeat("·", max(8, width-12))))
		b.WriteString("\n")
		for _, a := range roles {
			b.WriteString(renderCacheAgentLine(a, width))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// colorPercent Trung bình，。
func colorPercent(p float64) string {
	return lipgloss.NewStyle().Foreground(cacheHitColor(p)).Bold(true).
		Render(formatPercent(p))
}

// renderCacheAgentLine  role  dòng: role + Trung bình + Cache / Đầu vào。
//
// （cacheRead / input）Trung bình，
// "Cao"（ 100% / 1k Thấp 80% / 300k）。
//
// ；。 "/"，
// （：cache Trung bình / Đầu vào），。
//
// ：
//
//	Chưa bật     "WRITER        Chưa bật"
//	     "WRITER        85%  · 323k / 394k"
//	 cache  "Chưa bật"， 0/0
func renderCacheAgentLine(a host.AgentCacheStat, width int) string {
	// role "Vai trò đang chạy"；Width  12  ARCHITECT
	//  1 ， role Tự động。
	roleStyle := lipgloss.NewStyle().Foreground(eventAgentColor(a.Role)).Width(12)
	role := roleStyle.Render(agentDisplayName(a.Role))

	if !a.CacheCapable {
		dim := lipgloss.NewStyle().Foreground(colorDim).Italic(true)
		_ = width
		return role + dim.Render("Chưa bật")
	}

	// Trung bình；。
	hit := cacheHitRate(a.RecentCacheRead, a.RecentInput)
	if a.RecentSamples == 0 || a.RecentInput == 0 {
		hit = cacheHitRate(a.CacheRead, a.Input)
	}
	//  4 rộng（"100%"）， "5%"  "85%" 。
	pctCell := lipgloss.NewStyle().Width(4).
		Render(colorPercent(hit))

	//  / Đầu vào — ，，
	// ""；。
	tokens := lipgloss.NewStyle().Foreground(colorDim).Render(
		" · " + formatTokensCompact(a.CacheRead) + " / " + formatTokensCompact(a.Input))
	_ = width
	return role + pctCell + tokens
}

// cacheHitRate  input  cacheRead 。
// input == 0  0，Trung bình。
func cacheHitRate(cacheRead, input int) float64 {
	if input <= 0 {
		return 0
	}
	return float64(cacheRead) / float64(input) * 100
}

// cacheHitColor Trung bình：≥50%  / 20–50%  / <20% 。
// Ngữ cảnh：CacheTrung bìnhCao。
func cacheHitColor(percent float64) lipgloss.AdaptiveColor {
	switch {
	case percent >= 50:
		return colorSuccess
	case percent >= 20:
		return colorReview
	default:
		return colorError
	}
}

func formatPercent(p float64) string {
	if p <= 0 {
		return "0%"
	}
	if p < 10 {
		return fmt.Sprintf("%.1f%%", p)
	}
	return fmt.Sprintf("%.0f%%", p)
}

// formatTokensCompact  token  "8.2k" / "1.4M" 。
//
//	per-role ， formatNumber 。
func formatTokensCompact(n int) string {
	if n <= 0 {
		return "0"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func renderContextSidebar(snap host.UISnapshot, width int) string {
	if snap.ContextWindow <= 0 && snap.ContextStrategy == "" && snap.ContextScope == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString(renderContextUsageField("Ngữ cảnh chính", snap.ContextPercent, snap.ContextTokens, snap.ContextWindow))
	if strategy := contextStrategyLabel(snap.ContextStrategy); strategy != "" {
		b.WriteString(renderField("Chiến lược gần đây", truncate(strategy, max(8, width-12))))
	}
	if scope := contextScopeLabel(snap.ContextScope); scope != "" {
		b.WriteString(renderField("Khung nhìn hiện tại", scope))
	}
	if snap.ContextSummaryCount > 0 {
		b.WriteString(renderField("Tóm tắt", fmt.Sprintf("%d mục", snap.ContextSummaryCount)))
	}
	if snap.ContextActiveMessages > 0 {
		b.WriteString(renderField("Số tin nhắn", fmt.Sprintf("%d", snap.ContextActiveMessages)))
	}
	if snap.ContextCompactedCount > 0 || snap.ContextKeptCount > 0 {
		b.WriteString(renderField("Lần viết lại gần nhất", fmt.Sprintf("%d → %d", snap.ContextCompactedCount, snap.ContextKeptCount)))
	}
	return b.String()
}

func contextScopeLabel(scope string) string {
	switch scope {
	case "baseline":
		return "Cơ sở"
	case "projected":
		return "Dự phóng"
	case "recovered":
		return "Khôi phục"
	case "committed":
		return "Đã nộp"
	case "skipped":
		return "Bỏ qua do ngắt mạch"
	default:
		return scope
	}
}

func contextStrategyLabel(strategy string) string {
	switch strategy {
	case "":
		return ""
	case "tool_result_microcompact":
		return "Nén vi mô kết quả công cụ"
	case "light_trim":
		return "Cắt nhẹ"
	case "full_summary":
		return "Tóm tắt đầy đủ"
	default:
		return strategy
	}
}

func agentDisplayName(name string) string {
	return strings.ToUpper(name)
}

func agentTaskLine(agent host.AgentSnapshot) string {
	if agent.TaskKind != "" {
		return taskKindLabel(agent.TaskKind)
	}
	if agent.Summary != "" {
		return agent.Summary
	}
	return ""
}

func agentContextLine(agent host.AgentSnapshot) string {
	ctx := agent.Context
	if ctx.ContextWindow <= 0 || ctx.Tokens <= 0 {
		return ""
	}
	percentColor := contextPercentColor(ctx.Percent)
	percentStr := lipgloss.NewStyle().Foreground(percentColor).Render(fmt.Sprintf("ctx %.0f%%", ctx.Percent))
	parts := []string{percentStr}
	if scope := contextScopeLabel(ctx.Scope); scope != "" {
		parts = append(parts, scope)
	}
	if strategy := contextStrategyLabel(ctx.Strategy); strategy != "" {
		parts = append(parts, strategy)
	}
	return strings.Join(parts, " · ")
}

func agentStateRank(state string) int {
	switch state {
	case "running":
		return 0
	case "failed":
		return 1
	default:
		return 2
	}
}

func agentOrder(name string) int {
	switch {
	case strings.HasPrefix(name, "architect"):
		return 0
	case name == "editor":
		return 2
	case name == "writer":
		return 3
	default:
		return 9
	}
}

func agentStateLabel(state string) string {
	switch state {
	case "running":
		return "Đang chạy"
	case "failed":
		return "Bất thường"
	case "idle":
		return "Chờ"
	default:
		return state
	}
}

func agentStateIcon(state string) string {
	switch state {
	case "running":
		return "●"
	case "failed":
		return "×"
	default:
		return "·"
	}
}

func taskStatusColor(status string) lipgloss.AdaptiveColor {
	switch status {
	case "running":
		return colorSuccess
	case "queued":
		return colorMuted
	case "failed", "canceled":
		return colorError
	case "succeeded":
		return colorSuccess
	default:
		return colorDim
	}
}

func taskKindLabel(kind string) string {
	switch kind {
	case "foundation_plan":
		return "Lập kế hoạch nền tảng"
	case "chapter_write":
		return "Viết chương"
	case "chapter_review":
		return "Thẩm định chương"
	case "chapter_rewrite":
		return "Viết lại chương"
	case "chapter_polish":
		return "Trau chuốt chương"
	case "arc_expand":
		return "Mở rộng cung truyện"
	case "volume_append":
		return "Lập kế hoạch quyển tiếp theo"
	case "steer_apply":
		return "Xử lý can thiệp"
	default:
		return kind
	}
}
