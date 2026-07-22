package tools

import (
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
)

var premiseHeadingAliases = map[string]string{
	"Định vị thể loại":           "Định vị thể loại",
	"Thể loại và tông giọng":     "Thể loại và tông giọng",
	"Xung đột cốt lõi":           "Xung đột cốt lõi",
	"Mục tiêu nhân vật chính":    "Mục tiêu nhân vật chính",
	"Hướng kết cục":              "Hướng kết cục",
	"Vùng cấm khi viết":          "Vùng cấm khi viết",
	"Điểm bán khác biệt":         "Điểm bán khác biệt",
	"Móc câu khác biệt":          "Móc câu khác biệt",
	"Cam kết thực hiện cốt lõi":  "Cam kết thực hiện cốt lõi",
	"Động cơ câu chuyện":         "Động cơ câu chuyện",
	"Tuyến quan hệ/trưởng thành": "Tuyến quan hệ/trưởng thành",
	"Lộ trình nâng cấp":          "Lộ trình nâng cấp",
	"Bước ngoặt giữa truyện":     "Bước ngoặt giữa truyện",
	"Mệnh đề chung cuộc":         "Mệnh đề chung cuộc",
	"Tính phù hợp truyện ngắn":   "Tính phù hợp truyện ngắn",
	"题材定位":                       "Định vị thể loại",
	"题材和基调":                      "Thể loại và tông giọng",
	"核心冲突":                       "Xung đột cốt lõi",
	"主角目标":                       "Mục tiêu nhân vật chính",
	"结局方向":                       "Hướng kết cục",
	"终局方向":                       "Hướng kết cục",
	"写作禁区":                       "Vùng cấm khi viết",
	"差异化卖点":                      "Điểm bán khác biệt",
	"差异化钩子":                      "Móc câu khác biệt",
	"核心兑现承诺":                     "Cam kết thực hiện cốt lõi",
	"故事引擎":                       "Động cơ câu chuyện",
	"关系/成长主线":                    "Tuyến quan hệ/trưởng thành",
	"升级路径":                       "Lộ trình nâng cấp",
	"中段转折":                       "Bước ngoặt giữa truyện",
	"中期转向":                       "Bước ngoặt giữa truyện",
	"终局命题":                       "Mệnh đề chung cuộc",
	"短篇适配性":                      "Tính phù hợp truyện ngắn",
	"本作为什么适合短篇/单卷收束":             "Tính phù hợp truyện ngắn",
}

func parsePremiseSections(premise string) map[string]string {
	lines := strings.Split(premise, "\n")
	sections := make(map[string]string)
	var current string
	var body []string

	flush := func() {
		if current == "" {
			return
		}
		text := strings.TrimSpace(strings.Join(body, "\n"))
		if text != "" {
			sections[current] = text
		}
		body = body[:0]
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if heading, ok := canonicalPremiseHeading(trimmed); ok {
			flush()
			current = heading
			continue
		}
		if current != "" {
			body = append(body, line)
		}
	}
	flush()
	return sections
}

func canonicalPremiseHeading(line string) (string, bool) {
	if !strings.HasPrefix(line, "#") {
		return "", false
	}
	title := strings.TrimSpace(strings.TrimLeft(line, "#"))
	if title == "" {
		return "", false
	}
	canonical, ok := premiseHeadingAliases[title]
	return canonical, ok
}

func premiseStructure(premise string, tier domain.PlanningTier) map[string]any {
	sections := parsePremiseSections(premise)
	required := requiredPremiseHeadings(tier)
	found := make([]string, 0, len(required))
	var missing []string
	for _, heading := range required {
		if _, ok := sections[heading]; ok {
			found = append(found, heading)
			continue
		}
		missing = append(missing, heading)
	}

	structure := map[string]any{
		"template_ready": len(missing) == 0,
		"found":          found,
		"missing":        missing,
	}
	if len(sections) > 0 {
		structure["section_count"] = len(sections)
	}
	return structure
}

func requiredPremiseHeadings(tier domain.PlanningTier) []string {
	common := []string{
		"Thể loại và tông giọng",
		"Định vị thể loại",
		"Xung đột cốt lõi",
		"Mục tiêu nhân vật chính",
		"Hướng kết cục",
		"Vùng cấm khi viết",
		"Điểm bán khác biệt",
		"Móc câu khác biệt",
		"Cam kết thực hiện cốt lõi",
	}

	switch tier {
	case domain.PlanningTierLong:
		return append(common,
			"Động cơ câu chuyện",
			"Tuyến quan hệ/trưởng thành",
			"Lộ trình nâng cấp",
			"Bước ngoặt giữa truyện",
			"Mệnh đề chung cuộc",
		)
	case domain.PlanningTierMid:
		return append(common,
			"Động cơ câu chuyện",
			"Bước ngoặt giữa truyện",
		)
	case domain.PlanningTierShort:
		return append(common,
			"Tính phù hợp truyện ngắn",
		)
	default:
		return common
	}
}
