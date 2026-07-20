package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/voocel/ainovel-cli/internal/host"
)

func TestRenderTopBarShowsVersion(t *testing.T) {
	out := renderTopBar(host.UISnapshot{
		Provider:  "openrouter",
		ModelName: "test-model",
		NovelName: "Tiểu thuyết kiểm thử",
	}, 120, "", "v1.2.3")
	if !strings.Contains(out, "ainovel-cli v1.2.3") {
		t.Fatalf("top bar missing version: %q", out)
	}
}

// TestRenderStatusBar guards the bottom status bar contract: model identity (window + thinking), session tokens,
// spending/budget, and book contents must all be present (asserted as plain text after stripping styles).
func TestRenderStatusBar(t *testing.T) {
	out := ansi.Strip(renderStatusBar(host.UISnapshot{
		Provider:           "openrouter",
		ModelName:          "test-model",
		ModelContextWindow: 200000,
		ThinkingLevel:      "medium",
		TotalInputTokens:   1_234_000,
		TotalOutputTokens:  89_300,
		TotalCostUSD:       0.31,
		BudgetLimitUSD:     5,
		TotalSavedUSD:      0.12,
	}, "/tmp/output", 120))
	for _, want := range []string{"test-model(200K,trung bình)", "↑1.2M", "↓89.3k", "$0.31/$5.00", "tiết kiệm $0.12", "./output"} {
		if !strings.Contains(out, want) {
			t.Fatalf("status bar thiếu %q: %q", want, out)
		}
	}
}

func TestRenderStatusBarAutoThinkingAndEmpty(t *testing.T) {
	out := ansi.Strip(renderStatusBar(host.UISnapshot{
		ModelName:          "test-model",
		ModelContextWindow: 128000,
	}, "", 120))
	if !strings.Contains(out, "test-model(128K,tự động)") {
		t.Fatalf("thiếu chú thích auto cho mức thinking: %q", out)
	}
	if out := ansi.Strip(renderStatusBar(host.UISnapshot{}, "", 120)); out != "Sẵn sàng" {
		t.Fatalf("snapshot rỗng phải fallback Sẵn sàng, got %q", out)
	}
}

func TestRenderUsageLineSeparatesFullWidthNameAndTokens(t *testing.T) {
	out := renderUsageLine("gpt-5.6-sol", bodyTextColor, 5300, 0, 0.23, 32)
	if !strings.Contains(out, "gpt-5.6-sol 5.3k") {
		t.Fatalf("model name and tokens should have a visible gap: %q", out)
	}
}

func TestTruncateByDisplayWidth(t *testing.T) {
	// Pure Chinese text should be truncated by display width: a 10-column budget = 3 CJK chars (6 columns) + "..." (3 columns), and rune-based truncation would overflow to 17 columns
	got := truncate("kiểm toán viên đạo đức thuật toán công tại Lâm Cảng", 10)
	if w := lipgloss.Width(got); w > 10 {
		t.Errorf("truncate vượt độ rộng cột: %d > 10 (%q)", w, got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("cắt chuỗi quá rộng phải có dấu ba chấm: %q", got)
	}
	// ASCII behavior stays the same as the old implementation
	if got := truncate("abcdef", 6); got != "abcdef" {
		t.Errorf("không quá rộng thì không được cắt: %q", got)
	}
	if got := truncate("abcdefgh", 6); got != "abc..." {
		t.Errorf("cắt ASCII: got %q want %q", got, "abc...")
	}
}

func TestRenderDetailContentWrapsCJK(t *testing.T) {
	long := "Thẩm Nghiên (nhân vật chính; kiểm toán viên đạo đức thuật toán công tại Lâm Cảng, phụ trách điều tra sự cố đêm bão, kiên trì công lý thủ tục)"
	const contentW = 40
	out := renderDetailContent(host.UISnapshot{
		Characters:       []string{long},
		SupportingCount:  1,
		RecentSupporting: []string{long},
		RecentSummaries:  []string{"Chương 6: " + long},
	}, contentW)
	for line := range strings.SplitSeq(out, "\n") {
		if w := lipgloss.Width(line); w > contentW {
			t.Errorf("dòng tràn chiều rộng panel: %d > %d (%q)", w, contentW, line)
		}
	}
	// Long descriptions should wrap to multiple lines with hanging indentation, not be truncated and lose information
	joined := strings.ReplaceAll(strings.ReplaceAll(out, "\n", ""), " ", "")
	if !strings.Contains(joined, strings.ReplaceAll("kiên trì công lý thủ tục", " ", "")) {
		t.Errorf("sau khi xuống dòng phải giữ mô tả đầy đủ, output thực tế:\n%s", out)
	}
}
