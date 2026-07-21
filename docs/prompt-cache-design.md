# Thiết kế cache prompt: phối hợp ba tầng litellm / agentcore / ainovel

> Tài liệu này là một bài giải thích: giới thiệu cách chúng tôi thiết kế cache prompt LLM end-to-end
> (prompt caching) trong ba kho mã phối hợp, bao gồm nguyên lý thiết kế, các ca truy vết thực tế
> và vị trí mã nguồn có thể đối chiếu.
>
> - **litellm** —— Cổng LLM: dịch giao thức và khai báo năng lực
> - **agentcore** —— Khung Agent: vị trí cache và danh tính cache
> - **ainovel-cli** —— Tầng ứng dụng: tích hợp bằng một dòng cấu hình (codebot cũng tương tự)

---

## 1. Vì sao đáng làm: mô hình chi phí và một ca thực tế

Hệ thống Agent có một đặc điểm cấu trúc: **mỗi vòng request đều mang theo toàn bộ lịch sử**. Một vòng lặp công cụ 30 lượt thì
payload ở lượt 30 sẽ chứa toàn bộ tin nhắn của 29 lượt trước. Nếu không cache, cùng một tiền tố byte sẽ bị tính phí lặp đi lặp lại.

Định giá cache của hai nhà cung cấp lớn (lấy Anthropic làm ví dụ):

| Hạng mục | So với giá input thông thường |
|---|---|
| Ghi cache (TTL 5 phút) | 1.25x |
| Ghi cache (TTL 1 giờ) | 2x |
| **Đọc cache** | **0.1x (tiết kiệm 90%)** |

Ca thực tế: một lần sinh tiểu thuyết dài 33 chương tiêu tốn $58, sau phân tích `meta/usage.json` phát hiện
**tỷ lệ cache hit tổng thể chỉ 8.5%** (coordinator chỉ 2.7%, architect là 0). Sau khi đối chiếu
chuỗi usage theo từng request (input vs cache_read), chúng tôi xác định ba nguyên nhân gốc:

1. **tools dao động theo byte**: Description/Schema của công cụ subagent được tái dựng trực tiếp từ vòng lặp Go map
   ở mỗi lượt, thứ tự ngẫu nhiên → payload từ byte đầu tiên đã khác lượt trước → cache tiền tố mất tác dụng hoàn toàn;
2. **không có affinity định tuyến**: phía OpenAI không truyền `prompt_cache_key`, nên các request có byte hoàn toàn giống nhau
   vẫn có thể bị load balancing sang instance không có cache (bằng chứng rõ ràng: trong 33 session, request đầu tiên có byte giống nhau chỉ trúng 12 lần);
3. **Claude không có breakpoint thì không có cache**: Anthropic là cache tường minh, không gắn `cache_control` = hoàn toàn không cache.

Ba nguyên nhân này lần lượt tương ứng với ba mảng thiết kế ở phần dưới: **kỷ luật ổn định tiền tố**, **danh tính cache**, **dàn xếp breakpoint**.

---

## 2. Kiến thức nền: mô hình tinh thần của hai giao thức cache

### 2.1 OpenAI: cache tiền tố tự động (ngầm định)

- Server tự động cache tiền tố **≥1024 tokens**, client không cần khai báo;
- Mức hit tăng theo bước căn 128-token;
- Request có thể mang `prompt_cache_key` (field chính thức) để tạo **affinity định tuyến** — các request cùng key
  sẽ cố gắng rơi vào cùng một phân mảnh cache;
- `cached_tokens` trong usage báo cáo lượng hit; **ghi cache không bao giờ được báo cáo** (`cache_write` luôn bằng 0)
  là hiện tượng bình thường, không phải bug.

### 2.2 Anthropic: breakpoint tường minh (`cache_control`)

- Client gắn breakpoint `cache_control` lên các content block, **breakpoint sẽ bao phủ mọi thứ trước nó**
  (thứ tự cố định là tools → system → messages);
- Mỗi request **tối đa 4 breakpoint**;
- Giá ghi là 1.25x (5m) / 2x (1h), giá đọc là 0.1x;
- `cache_control` **không được gắn lên thinking block** (sẽ bị 400 từ chối).

### 2.3 Tiền đề chung

Dù là cache ngầm hay tường minh, cache đều chỉ nhận **tiền tố byte phải giống hệt nhau**. Vì vậy, nền tảng của mọi thiết kế chỉ có một câu:

> **Sắp xếp toàn bộ request theo thứ tự từ thay đổi chậm đến thay đổi nhanh: cái tĩnh để trước, cái động để sau,
> và mọi lịch sử đã gửi đi không được thay đổi dù chỉ một byte.**

---

## 3. Kiến trúc tổng thể: phân công ba tầng

```
┌────────────────────────────────────────────────────────┐
│ Tầng ứng dụng (ainovel-cli / codebot)                   │
│   Quyết định giá trị của "danh tính cache":              │
│   một cuốn sách một base, một vai một tên               │
│   Chi phí tích hợp = mỗi agent hai dòng cấu hình         │
├────────────────────────────────────────────────────────┤
│ agentcore (khung Agent)                                 │
│   Quyết định "đặt breakpoint ở đâu, key được dẫn xuất khi nào":│
│   system floor + rolling tip của tin nhắn cuối; spawn thêm #seq; │
│   gate theo năng lực provider, không hỗ trợ thì lặng lẽ bỏ qua    │
├────────────────────────────────────────────────────────┤
│ litellm (cổng LLM)                                      │
│   Chỉ dịch giao thức: cache_control ↔ field của từng nhà cung cấp,│
│   truyền thẳng prompt_cache_key, khai báo năng lực Capabilities   │
│   Không đưa ra bất kỳ quyết định "có nên cache không" nào        │
└────────────────────────────────────────────────────────┘
```

Nguyên tắc phân tách: **litellm chỉ trả lời "endpoint này hỗ trợ gì", agentcore chỉ trả lời "đặt breakpoint ở đâu",
ứng dụng chỉ trả lời "danh tính là gì"**. Mỗi tầng có thể kiểm thử riêng; đổi ứng dụng khác (codebot tái dùng cùng bộ
agentcore/litellm) mà không phải viết lại logic cache.

---

## 4. Nền tảng: ba kỷ luật ổn định byte tiền tố

Lợi ích của cache phụ thuộc vào việc byte tiền tố ổn định. Ba kỷ luật dưới đây mỗi cái đều ứng với một sự cố thực tế.

### Kỷ luật 1: chuỗi tools phải được serialize theo byte xác định

Sự cố: công cụ `subagent` nhúng danh sách agent đã đăng ký vào Description/Schema của chính nó, trong khi danh sách này lấy từ
việc lặp Go map — mỗi lần gọi thứ tự lại ngẫu nhiên, byte của tools đổi ở từng lượt, khiến tỷ lệ hit của coordinator chỉ còn 2.7%.
(Đội Claude Code cũng từng dính đúng một vấn đề như vậy: cả fleet của họ từng phải trả thêm 10.2% chi phí ghi cache vì nguyên nhân này.)

Khắc phục (agentcore `subagent/subagent.go`):

```go
// sortedAgentNames returns registered agent names in deterministic order.
// Description and Schema are rebuilt on every LLM call; iterating the map
// directly would shuffle their bytes across requests and defeat provider
// prefix caching (tools serialize into the cached prompt prefix).
func (t *Tool) sortedAgentNames() []string {
	return slices.Sorted(maps.Keys(t.agents))
}
```

> Bài học tổng quát: **mọi tập hợp đi vào payload request đều phải được sắp xếp trước khi serialize**.
> Random hóa khi lặp map của Go sẽ giấu bug này rất sâu — chức năng hoàn toàn bình thường, chỉ hóa đơn là bất thường.

### Kỷ luật 2: lịch sử phải append-only (nén phải được "commit")

Sự cố: chiến lược nén context của writer là kiểu "projection" (mỗi lần gọi chỉ tạm thời sửa lại khung nhìn lịch sử, nhưng không ghi ngược
về baseline). Một khi vượt ngưỡng, **mỗi lượt đều đang viết lại toàn bộ tiền tố** → toàn bộ miss ở mọi lượt.

Khắc phục: sau khi projection thì commit (`CommitOnProject: true`), để việc sửa chỉ xảy ra một lần, rồi quay lại
append-only cho đến lần vượt ngưỡng tiếp theo.

> Dạng tổng quát: nén context là **một lần đứt có kế hoạch** (reset tiền tố, trả giá đầy đủ một lần),
> điều đó không sao; điều không chấp nhận được là **lượt nào cũng đứt**. Nén thì hoặc không làm, hoặc đã làm xong thì phải cố định.

### Kỷ luật 3: nội dung động phải đi ở phần đuôi

Những thứ thay đổi mỗi lượt (bao thư trạng thái thế giới, lời nhắc mỗi vòng, kết quả công cụ mới nhất) chỉ được phép **append ở cuối tin nhắn**,
không bao giờ quay lại sửa đoạn ở giữa. Bao thư `novel_context` của ainovel là thiết kế append ở đuôi — nó thay đổi mỗi chương,
nhưng sự thay đổi đó không ảnh hưởng đến hàng chục vạn token phía trước trong cache.

---

## 5. Danh tính cache: một cuốn sách một base, một vai một tên, một session một key

`prompt_cache_key` của hệ OpenAI giải quyết bài toán **định tuyến**: nếu các request có byte giống nhau nhưng bị load balance sang
những instance khác nhau thì vẫn miss. Mục tiêu thiết kế của key là: "những request cùng một dòng cache sẽ luôn mang cùng một key".

Bộ ba danh tính của chúng tôi (ainovel `internal/agents/build.go`):

```go
// promptCacheBase derives a stable short hash from the book directory and uses it as the prefix
// of the prompt cache identity: the same book shares routing buckets across process restarts,
// without leaking the local path to the provider. The role suffix is appended by the caller,
// and subagent appends "#seq" again on each spawn (one key per session).
func promptCacheBase(bookDir string) string {
	sum := sha256.Sum256([]byte(bookDir))
	return "nvl-" + hex.EncodeToString(sum[:6])
}
```

Việc tích hợp ở tầng ứng dụng chỉ cần mỗi agent hai dòng:

```go
writer := subagent.Config{
	// ...
	CacheLastMessage: "ephemeral",                // Claude breakpoint switch (see §6)
	PromptCacheKey:   cacheBase + "-writer",      // OpenAI routing identity (role-level)
}
// coordinator (top-level Agent) cũng tương tự:
agentcore.WithCacheLastMessage("ephemeral"),
agentcore.WithPromptCacheKey(cacheBase+"-coordinator"),
```

Cấp thứ ba (cấp session) do agentcore tự dẫn xuất — mỗi lần spawn một session mới là một dòng cache lineage mới
(agentcore `subagent/subagent.go`):

```go
runSeq := t.runSeq.Add(1)

// One conversation, one cache key: suffix the per-run sequence so each
// spawn forms its own cache lineage instead of piling every run of this
// agent into a single routing bucket.
promptCacheKey := cfg.PromptCacheKey
if promptCacheKey != "" {
	promptCacheKey = fmt.Sprintf("%s#%d", promptCacheKey, runSeq)
}
```

Hình thái cuối cùng: `nvl-a1b2c3-writer#17` = cuốn sách này, vai writer, session spawn lần thứ 17.

> Vì sao không dùng một key toàn cục? Vì các session khác nhau có tiền tố khác nhau, trộn chung vào một bucket định tuyến sẽ làm loãng tỷ lệ hit.
> Vì sao không gắn timestamp/random? Vì key phải **ổn định qua các request**, trong cùng session thì mọi lượt đều phải giống nhau.

Thiết kế tương ứng của codebot: ngữ nghĩa của key = SessionID (đổi session = đổi lineage), teammate nối thêm hậu tố tên,
khi host tái dùng cùng một Agent instance để chuyển session thì gọi `Agent.SetPromptCacheKey` để đặt lại danh tính.

---

## 6. Bố trí breakpoint Claude: floor + rolling tip

Anthropic không gắn breakpoint = không có cache. Phân bổ ngân sách của chúng ta (giới hạn 4 breakpoint/request):

```
[tools][system ←breakpoint ① "floor"][... lịch sử messages ...][latest message ←breakpoint ② "rolling tip"]
```

### 6.1 Floor: ghim tiền tố tĩnh

System prompt là khối tĩnh lớn nhất. Gắn cho nó một breakpoint riêng để đảm bảo **khi session mới khởi chạy / cache ở đuôi bị đẩy ra,
ít nhất tiền tố system+tools vẫn được đọc từ cache** (agentcore `loop.go`):

```go
} else if agentCtx.SystemPrompt != "" {
	m := SystemMsg(agentCtx.SystemPrompt)
	if config.CacheLastMessage != "" {
		// Cache floor: pin the static system prompt with its own
		// breakpoint so a fresh session — or a turn whose tail entry was
		// evicted — still reads the system+tools prefix from cache.
		m.Metadata = map[string]any{"cache_control": config.CacheLastMessage}
	}
	prefix = append(prefix, m)
}
```

### 6.2 Rolling tip: đẩy phạm vi bao phủ theo từng vòng

Đặt một breakpoint lên **tin nhắn không phải system cuối cùng**. Trong vòng lặp công cụ, mỗi lần LLM gọi sẽ ghi một cache
bao phủ tới `tool_use+tool_result` mới nhất; lượt tiếp theo đọc thẳng từ cache, không cần gửi lại.

```go
// markLastMessageForCache returns a copy of messages with cache_control attached
// to the metadata of the last non-system message. System messages are skipped so
// trailing per-turn reminders (which change every turn) don't end up carrying
// the breakpoint.
func markLastMessageForCache(messages []Message, cacheControl string) []Message {
	idx := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != RoleSystem {
			idx = i
			break
		}
	}
	// ...
}
```

Lưu ý bỏ qua reminder system ở cuối: nó thay đổi mỗi lượt, gắn breakpoint lên nó đồng nghĩa với việc mỗi lượt sẽ ghi một cache
không bao giờ được tái dùng.

### 6.3 Semantics của block cuối: một message chỉ đốt một breakpoint

Semantics của `cache_control` ở cấp message là "sau message này, viết một breakpoint". Khi dịch sang cấp block thì chỉ được phép
rơi vào **khối cuối cùng có thể cache** — gắn dấu cho từng block sẽ đốt vượt ngân sách 4 breakpoint; Anthropic còn
từ chối `cache_control` trên thinking block, nên phải quét từ cuối lên và bỏ qua reasoning
(agentcore `llm/litellm.go`):

```go
if cache != nil {
	// Anthropic rejects cache_control on thinking blocks — land the
	// breakpoint on the last cacheable block instead.
	for i := len(blocks) - 1; i >= 0; i-- {
		if _, isReasoning := blocks[i].(litellm.ReasoningBlock); isReasoning {
			continue
		}
		blocks[i] = withBlockCache(blocks[i], cache)
		break
	}
}
```

### 6.4 Pipeline TTL

Quy ước giá trị cấu hình là chuỗi dạng `"type[:ttl]"`, ví dụ `"ephemeral"` (mặc định 5m) hoặc `"ephemeral:1h"`:

```go
func cacheControlFromMetadata(metadata map[string]any) *litellm.CacheControl {
	value, _ := metadata["cache_control"].(string)
	if value == "" {
		return nil
	}
	if typ, ttl, ok := strings.Cut(value, ":"); ok {
		return &litellm.CacheControl{Type: typ, TTL: ttl}
	}
	return &litellm.CacheControl{Type: value}
}
```

Có nên nâng lên 1h hay không thì phải dựa trên dữ liệu: giá ghi tăng từ 1.25x lên 2x, chỉ đáng làm nếu khoảng cách giữa các lần gọi đo được
thường xuyên vượt quá 5 phút (chúng tôi đo được median khoảng cách của coordinator là 172s, nên không nâng).

---

## 7. Gửi an toàn: gate theo năng lực + phán định endpoint chính thức

### 7.1 Gate theo năng lực: field không được hỗ trợ thì không gửi ra ngoài

Các provider của litellm kiểm tra `ProviderOptions` **rất chặt** (key lạ sẽ báo lỗi ngay), nên
agentcore gate trước khi gửi theo khai báo năng lực (agentcore `llm/litellm.go`):

```go
// Prompt-cache routing identity. Capability-gated: litellm providers
// validate provider options strictly, so an unsupported key must be
// dropped here rather than rejected there.
if callCfg.PromptCacheKey != "" && caps.Cache.PromptKey == litellm.SupportYes {
	req.ProviderOptions["prompt_cache_key"] = callCfg.PromptCacheKey
}
```

### 7.2 Phán định endpoint chính thức: hệ sinh thái tương thích không có hợp đồng field lạ

`prompt_cache_key` là field chính thức của OpenAI, nhưng hành vi của các endpoint "OpenAI-compatible" không hề có một hợp đồng thống nhất nào.
Bằng chứng thực tế trên mạng (2026-07):

- **Endpoint strict từ chối thẳng**: Groq, Cerebras, Volcano Engine, Fireworks trả 400/422 cho field này
  (Zed #36215, OpenClaw #48155 đều đã đổi sang gửi có điều kiện vì lý do này);
- **Gateway kiểu re-bundle lặng lẽ bỏ qua**: các đường không passthrough của one-api/new-api/sub2api parse request body vào
  struct rồi re-marshal, field lạ biến mất không tiếng động (gửi như không gửi);
- **Endpoint rộng rãi thì bỏ qua**: Ollama, bản hiện tại của vLLM, MiniMax.

Vì vậy khai báo năng lực của litellm openai provider được phán định động theo BaseURL
(litellm `provider/openai/capabilities.go`):

```go
// promptCacheParamsSupport reports whether this endpoint is trusted to accept
// OpenAI's prompt cache params (prompt_cache_key / prompt_cache_retention).
// Only the official endpoint guarantees the field contract.
func (p *Provider) promptCacheParamsSupport() litellm.Support {
	if p.cfg.PromptCacheParams || isOfficialBaseURL(p.cfg.BaseURL) {
		return litellm.SupportYes
	}
	return litellm.SupportUnknown
}

func isOfficialBaseURL(baseURL string) bool {
	u, err := url.Parse(baseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "api.openai.com")
}
```

`api.openai.com` chính thức → `SupportYes` (gửi); BaseURL bên thứ ba → `SupportUnknown`
(guard ở §7.1 sẽ tự không gửi, **mặc định không bao giờ làm vỡ bất kỳ endpoint nào**); nếu người dùng đã xác nhận relay của mình
passthrough nguyên xi thì có thể opt-in rõ ràng trong cấu hình provider:

```jsonc
"my-relay": {
  "type": "openai",
  "base_url": "https://relay.example.com/v1",
  "extra": { "prompt_cache_params": true }   // Tôi xác nhận relay này passthrough request body
}
```

> Vì sao công tắc được đặt ở tầng năng lực litellm chứ không phải tầng cấu hình ứng dụng? Vì khi runtime `/model` đổi provider
> thì client cũng đổi; khai báo năng lực sẽ tự đổi theo client. Phán định ở thời điểm dựng ứng dụng không thể bao phủ việc đổi runtime.

---

## 8. Quan sát: phát hiện đứt chuỗi cache

Cache là một năng lực "không nhìn thấy được" — hỏng thì không báo lỗi, chỉ trở nên đắt hơn. Vì vậy cần quan sát (tham khảo
promptCacheBreakDetection của Claude Code, chúng tôi làm bản nhẹ).

Cách xác định (ainovel `internal/host/usage.go`):

```go
// Trong cùng một session (role+task): prefix không bị rút ngắn,
// nhưng lượng hit giảm >5% so với lần trước và mức giảm ≥2000 tokens
broke := prevPrefix > 0 && prefix >= prevPrefix &&
	float64(u.CacheRead) < float64(prevRead)*cacheBreakKeepRatio &&
	prevRead-u.CacheRead >= cacheBreakMinDropTokens
```

Bốn thiết kế then chốt, mỗi cái ứng với một loại báo động giả:

| Thiết kế | Chống loại báo động giả nào |
|---|---|
| **Hai ngưỡng** (tương đối 5% và tuyệt đối 2000) | Ngưỡng tương đối đơn lẻ bị nhiễu bởi prefix nhỏ; ngưỡng tuyệt đối đơn lẻ bỏ sót suy thoái của prefix lớn |
| **Baseline đi theo session (role+task)** | Chiều đo phát hiện phải đồng bộ với mức session của `prompt_cache_key` (`#seq`); nếu so sánh theo role xuyên session, sẽ báo giả khi "session trước rất ngắn, session mới request đầu lại có prefix dài hơn" (một lỗ hổng thực tế do Codex review bắt được) |
| **Prefix ngắn hơn = reset hợp lệ** | Nén context là đứt gãy theo kế hoạch, đặt lại baseline thì không cảnh báo |
| **replay không phát hiện** | Replaying lịch sử lúc khởi động sẽ biến các đứt gãy cũ thành cảnh báo mới |

Khi cảnh báo, chúng tôi đưa gợi ý quy nguyên theo khoảng thời gian: khoảng cách >1h → nghi TTL 1h hết hạn; >5m → nghi TTL 5m hết hạn;
rất ngắn → nghi bị server đẩy ra / drift định tuyến (**gateway quay vòng nhiều upstream account là nguyên nhân phổ biến nhất**). Số đếm được
lưu bền vào `usage.json` và hiển thị ở dòng "chain break" trong bảng cache của TUI.

---

## 9. Lằn ranh khóa chặt: nguyên tắc đơn điệu của session

Một ràng buộc mang tính hiến pháp cho các tính năng tương lai:

> **Mọi giá trị sẽ đi vào tiền tố cache (system prompt, tools, tham số thinking, tham số sampling),
> sau khi tính lần đầu trong session thì phải được khóa cứng — thà cũ còn hơn phá cache.**

Ví dụ: tính năng kiểu "điều chỉnh mức thinking lúc chạy" nếu cho giá trị mới tác động ngay vào session đang chạy,
thì mỗi lần chỉnh sẽ viết lại tiền tố, vô hiệu hóa toàn bộ cache. Cách làm đúng là chỉ để giá trị mới có hiệu lực với **session spawn mới**.
Khi đánh giá bất kỳ yêu cầu nào dạng "X có thể chỉnh lúc chạy", câu hỏi đầu tiên luôn là: X có nằm trong tiền tố cache không?

---

## 10. Ngộ nhận thường gặp và trần hiệu quả

1. **`cache_write` của OpenAI luôn bằng 0 là bình thường** — API không báo lượng ghi, đừng đi săn bug ở đó.
2. **Trần ở gateway**: nếu relay quay vòng nhiều upstream account, thì dù client có giữ byte ổn định đến đâu cũng sẽ miss
   (cache của upstream account A không nhìn thấy từ account B). Điều này giải thích bí ẩn "request hoàn toàn giống nhau nhưng chỉ hit 12/33".
   **Đây không phải vấn đề client có thể tự giải quyết** — dữ liệu của đội Claude Code cũng cho thấy khoảng 90% ca "client không đổi mà vẫn đứt"
   là do server.
3. **Cách xác minh**: JSONL của session không chứa system prompt và full request body, **chuỗi usage theo từng request
   (input vs cache_read) mới là chuẩn chẩn đoán vàng**. Một dấu vân tay thực dụng: nếu lượng hit đúng bằng
   "số token của system prompt được làm tròn xuống theo bội 128", thì chỉ có đoạn system hit, còn đoạn messages thì miss toàn bộ.
4. **Tính lợi ích**: giá đọc 0.1x, giá ghi 1.25x, nghĩa là một cache chỉ cần được đọc 1 lần là hòa vốn.
   Trong session agent nhiều vòng, breakpoint gần như luôn có lợi, nên `CacheLastMessage` không có công tắc tắt và mặc định bật.

---

## 11. Hướng dẫn tích hợp nhanh

**ainovel-cli** (đã tích hợp sẵn): mỗi agent cấu hình `CacheLastMessage: "ephemeral"` +
`PromptCacheKey: promptCacheBase(bookDir) + "-<role>"`, các phần còn lại tự động.

**codebot** (đã tích hợp sẵn): key = SessionID; khi `Reset`/`SwitchSession` thì
`agent.SetPromptCacheKey(newSessionID)`; teammate dùng `sessionID + "-" + name`.

**Danh sách tối thiểu khi nối app mới với agentcore**:

```go
agentcore.NewAgent(
	agentcore.WithCacheLastMessage("ephemeral"),   // Claude breakpoint: floor + rolling tip
	agentcore.WithPromptCacheKey(stableIdentity),   // OpenAI routing: ổn định, duy nhất theo mỗi session
	// ...
)
```

Kèm ba câu tự kiểm tra (ứng với ba kỷ luật):

1. Việc serialize tools của tôi có xác định theo byte không? (đã sắp xếp mọi tập hợp chưa)
2. Lịch sử của tôi có phải append-only không? (việc nén có được commit chưa)
3. Mọi nội dung thay đổi theo từng lượt có nằm ở cuối không?

---

## 12. Sổ tay kinh nghiệm cho người học

- Bản chất của tối ưu cache là **kỷ luật byte**, không phải chỉnh tham số: hãy đảm bảo tiền tố ổn định trước, rồi mới nói tới key và breakpoint.
- Chẩn đoán phải luôn bắt đầu từ **chuỗi usage theo từng request**, đừng đoán từ mã nguồn.
- Random hóa khi lặp Go map + serialize payload request = sát thủ cache khó phát hiện nhất, kiểm thử chức năng không bao giờ tự thấy được.
- "OpenAI-compatible" là một từ marketing chứ không phải hợp đồng: trước khi gửi field chính thức sang endpoint bên thứ ba, phải tìm bằng chứng nguồn một tay
  (source/issue/cách sửa đã được client cùng loại thực hiện), suy luận "thường sẽ bỏ qua" là rất nguy hiểm.
- Quan sát phải ưu tiên chống báo động giả: chiều đo phát hiện phải khớp với độ hạt của lineage cache; thà bỏ sót còn hơn báo sai,
  nếu không cảnh báo sẽ nhanh chóng bị bỏ qua.
- Tiêu chí kiểm tra của phân tầng: khi đổi sang một ứng dụng khác (codebot) để tích hợp, logic cache không phải viết lại dù chỉ một dòng.

---

### Phụ lục: chỉ mục mã nguồn

| Chủ đề | Vị trí |
|---|---|
| Sắp xếp tools theo tính xác định | agentcore `subagent/subagent.go` `sortedAgentNames` |
| Dẫn xuất key theo cấp session (`#seq`) | agentcore `subagent/subagent.go` `runAgent` |
| system floor + rolling tip | agentcore `loop.go` `callLLM` / `markLastMessageForCache` |
| breakpoint ở block cuối + bỏ thinking | agentcore `llm/litellm.go` `convertAgentBlocks` |
| Phân tích TTL (`"ephemeral:1h"`) | agentcore `llm/litellm.go` `cacheControlFromMetadata` |
| Gate theo năng lực | agentcore `llm/litellm.go` `applyCallConfig` |
| Phán định endpoint chính thức + opt-in | litellm `provider/openai/capabilities.go` / `provider.go Config` |
| Danh tính cache (một cuốn sách một base) | ainovel `internal/agents/build.go` `promptCacheBase` |
| Phát hiện đứt chuỗi | ainovel `internal/host/usage.go` `noteCacheBreak` |
| Vị trí kiến trúc | ainovel `docs/architecture.md` §6.6 |
