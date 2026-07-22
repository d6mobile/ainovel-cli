// Package userrules 是用户规则归一化的服务层：把各来源的自然语言规则经 LLM 单次调用
// 归一化成候选结构化字段，再由 rules.BuildSnapshot 确定性合并成本书快照。
//
// 分层职责：
//   - rules 包：纯数据 + 确定性合并（Snapshot / Candidate / BuildSnapshot / SystemDefaults）
//   - 本包：LLM 归一化 + 编排 + 落盘（依赖 agentcore + store + rules）
//
// 归一化是增强路径，不是主创作的前置条件：任何来源失败都降级为 raw preferences，主创作必须继续。
package userrules

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/rules"
)

// normalizeMaxTokens 单次归一化的输出上限（思考 token 与 JSON 输出共享这一预算）。
// 归一化 JSON 本身很小（通常 <1k），这里留大头是给"无法关闭思考的推理模型"的思考预算——
// 留窄了思考会挤占 JSON 导致截断、解析失败。max_tokens 是上限不是计费量，调大不增成本。
const normalizeMaxTokens = 8192

// normalizeMaxAttempts 归一化总尝试次数（最多 2 次重试后降级，不做无界重试，见设计 §失败与降级）。
// LLM 输出有随机性，解析失败再试常能拿到合法 JSON；瞬时网络抖动同理。
const normalizeMaxAttempts = 3

// Normalizer 把单个来源的自然语言规则归一化成 rules.Candidate（单次 LLM 调用）。
type Normalizer struct {
	model agentcore.ChatModel
}

// NewNormalizer 用一个 ChatModel 构造归一化器。归一化是一次性启动工具，
// 应传入能力较强的模型（如 ModelSet 的默认模型），不必跟随写作的弱模型。
//
// 归一化不覆盖 thinking：显式 off 本身也是只有部分模型支持的推理参数，
// 普通 chat 模型会拒绝它。沿用 provider/model 默认，由 normalizeMaxTokens
// 为不可关闭思考的模型预留输出预算。
func NewNormalizer(model agentcore.ChatModel) *Normalizer {
	return &Normalizer{model: model}
}

// Normalize 归一化一个来源。永不返回 error——失败时返回 degraded Candidate
// （原文作 raw preferences、不产 structured），由调用方继续合并。
func (n *Normalizer) Normalize(ctx context.Context, source, text string) rules.Candidate {
	text = strings.TrimSpace(text)
	if text == "" {
		return rules.Candidate{Source: source}
	}
	if n == nil || n.model == nil {
		return degraded(source, text)
	}

	messages := []agentcore.Message{
		{Role: agentcore.RoleSystem, Content: []agentcore.ContentBlock{agentcore.TextBlock(normalizerSystemPrompt)}},
		{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock(text)}},
	}

	// 有限重试后降级：技术错误（网络/模型/非法 JSON）进日志、不进快照，
	// 快照只留 status=degraded + 来源标注（见设计 §失败与降级 / §回显）。
	var lastErr string
	for attempt := 1; attempt <= normalizeMaxAttempts; attempt++ {
		resp, err := n.model.Generate(ctx, messages, nil,
			agentcore.WithMaxTokens(normalizeMaxTokens))
		switch {
		case err != nil:
			lastErr = err.Error()
		case resp == nil:
			lastErr = "Model trả về phản hồi rỗng"
		default:
			raw := resp.Message.TextContent()
			if out, ok := parseNormalizerJSON(raw); ok {
				return rules.Candidate{
					Source:      source,
					Structured:  out.Structured,
					Preferences: strings.TrimSpace(out.Preferences),
					Uncertain:   coerceUncertain(out.Uncertain),
				}
			}
			lastErr = "Trả về JSON không hợp lệ"
			// 反馈式重试：把上次的非法输出与纠正提示并入对话，让下一轮带着错误针对性
			// 重出 JSON，而非原样盲重试。只对"格式坏"有意义——网络错误 / 空响应那两支
			// 没有可反馈的上次输出，仍是盲重试。
			messages = append(messages,
				agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock(raw)}},
				agentcore.Message{Role: agentcore.RoleUser, Content: []agentcore.ContentBlock{agentcore.TextBlock(normalizerRetryHint)}},
			)
		}
		slog.Warn("Chuẩn hóa quy tắc thất bại",
			"module", "rules", "source", source, "attempt", attempt, "err", lastErr)
		if ctx.Err() != nil {
			break // ctx 取消则重试也必失败，直接降级
		}
	}
	return degraded(source, text)
}

// degraded 构造一个降级候选：归一化失败时把原文当作风格偏好，不提炼任何机械规则。
// uncertain 标注来源（便于回显"哪些来源未能解析"），但不含技术错误细节——技术错误只进日志。
func degraded(source, text string) rules.Candidate {
	return rules.Candidate{
		Source:      source,
		Preferences: text,
		Uncertain:   []string{source + ": chuẩn hóa thất bại, đã dùng nguyên văn làm tùy chọn phong cách (chưa trích xuất quy tắc cơ học)"},
		Degraded:    true,
	}
}

// normalizerOutput 是归一化器约定的 JSON 形态。
// Structured 直接复用 rules.Structured（JSON 形状一致）；Uncertain 用 RawMessage 容忍
// 模型回的多种形态（string / []string / [{item,reason}]，原型实测均出现过）。
type normalizerOutput struct {
	Structured  rules.Structured `json:"structured"`
	Preferences string           `json:"preferences"`
	Uncertain   json.RawMessage  `json:"uncertain"`
}

func parseNormalizerJSON(raw string) (normalizerOutput, bool) {
	s := extractJSON(raw)
	if s == "" {
		return normalizerOutput{}, false
	}
	var out normalizerOutput
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return normalizerOutput{}, false
	}
	return out, true
}

// extractJSON 从模型回复里抠出 JSON 对象：剥 ```json 围栏，取首个 { 到末个 }。
func extractJSON(raw string) string {
	s := strings.TrimSpace(raw)
	if after, ok := strings.CutPrefix(s, "```"); ok {
		s = after
		s = strings.TrimPrefix(s, "json")
		s = strings.TrimPrefix(s, "JSON")
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < 0 || end < start {
		return ""
	}
	return s[start : end+1]
}

// coerceUncertain 把模型回的 uncertain 统一成 []string，容忍 string / []string / []object 三种形态。
func coerceUncertain(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return nonEmpty(list)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s = strings.TrimSpace(s); s != "" {
			return []string{s}
		}
		return nil
	}
	var objs []map[string]any
	if err := json.Unmarshal(raw, &objs); err == nil {
		var out []string
		for _, o := range objs {
			if str := stringifyUncertainObj(o); str != "" {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func stringifyUncertainObj(o map[string]any) string {
	item, _ := o["item"].(string)
	if item == "" {
		item, _ = o["field"].(string)
	}
	reason, _ := o["reason"].(string)
	switch {
	case item != "" && reason != "":
		return item + "：" + reason
	case item != "":
		return item
	case reason != "":
		return reason
	default:
		b, _ := json.Marshal(o)
		return string(b)
	}
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// normalizerSystemPrompt 是归一化器的系统提示词。
// 已用 10 条真实例子（含阈值发明陷阱）验证保守提升成立（10/10）。
const normalizerSystemPrompt = `Bạn là bộ chuẩn hoá quy tắc của hệ thống viết tiểu thuyết AI. Bạn đọc quy tắc viết dài hạn từ một nguồn của người dùng (ngôn ngữ tự nhiên) và trích xuất thành dạng có cấu trúc. Chỉ xuất một đối tượng JSON, không kèm giải thích.

JSON đầu ra gồm ba trường: structured / preferences / uncertain.

structured chỉ được phép có các trường sau (không thêm trường khác):
- genre: chuỗi (thể loại)
- forbidden_chars: [chuỗi] (ký tự cấm xuất hiện)
- forbidden_phrases: [chuỗi] (cụm từ cấm, khớp chữ nguyên văn)
- fatigue_words: {từ: số nguyên} (từ mệt mỏi → giới hạn số lần xuất hiện mỗi chương)

【Nâng cấp bảo thủ — quan trọng nhất】
- Chỉ ghi vào structured khi người dùng nói rõ ràng, không mơ hồ.
- forbidden_chars/forbidden_phrases là cấp error: chỉ nâng cấp khi có lệnh cấm rõ như “không xuất hiện X / cấm dùng X / đừng viết X”.
- fatigue_words: chỉ nâng cấp khi có cả “từ cụ thể” và “ngưỡng số lần cụ thể”; các yêu cầu như “ít dùng X / đừng dùng X mãi” nhưng không có số thì đưa vào preferences, tuyệt đối không tự bịa ngưỡng.
- Ý muốn về số chữ / độ dài (“mỗi chương 3000 chữ”, “viết ngắn hơn”) luôn đưa vào preferences: độ dài chương là vấn đề nhịp kể, để quá trình sáng tác tự cân bằng, không kiểm tra máy móc.
- Những gì không kiểm tra được bằng máy, thiếu ngưỡng rõ ràng, hoặc phụ thuộc ngữ cảnh thì luôn đưa vào preferences.
- Nguyên tắc: thà bỏ sót khỏi structured còn hơn nâng cấp sai (vì sẽ gây báo lỗi nhầm ở từng chương).

preferences: một đoạn ngôn ngữ tự nhiên dễ đọc về phong cách / nhân vật / thẩm mỹ.
uncertain: các mục bạn cố ý không nâng cấp vào structured + lý do (mảng chuỗi).`

// normalizerRetryHint 在归一化输出无法解析为 JSON 时追加给模型，引导其针对性重出
// （反馈式重试，见 Normalize 的"返回非合法 JSON"分支）。
const normalizerRetryHint = "Phần trả lời trên không phân tích được thành JSON. Chỉ xuất một đối tượng JSON, gồm ba trường structured / preferences / uncertain; không kèm giải thích hoặc code fence."
