# Production Localization Pass Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove remaining untranslated Chinese/Cyrillic prose from production user-facing and model-facing string literals while preserving domain data and persisted formats.

**Architecture:** Use a scan-driven workflow: extract production string literals containing Chinese/Cyrillic, classify each hit, translate only user/model-facing prose, then verify with targeted package tests and the same scan. Keep identifiers, JSON keys, command names, role names, tool names, regexes, and domain data unchanged.

**Tech Stack:** Go, Docker-based Go 1.25 commands, Python 3 scan scripts, existing Go test suite.

## Global Constraints

- Preserve Vietnamese localization for user-facing UI, prompts, docs, and examples.
- Do not modify story workspace data under story directories.
- Do not change public APIs, persisted data formats, JSON keys, command names, role names, tool names, package names, function names, or config schemas.
- Do not translate regex patterns and parser literals for Chinese source novels, such as chapter-title matching.
- Do not translate domain examples and default rule/style-stat data, such as forbidden phrases and Chinese fatigue-word patterns.
- Prefer Docker-based Go commands: `docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test ./...`.
- Keep changes mechanical and scoped; do not refactor control flow.

---

## File Map

**Scan and verification**
- Use inline Python scan commands only; do not add repo scripts for this temporary audit.

**Expected production clusters**
- Modify: `assets/load.go`
- Modify: `internal/agents/build.go`
- Modify: `internal/agents/ctxpack/restore.go`
- Modify: `internal/agents/guard/subagent_guards.go`
- Modify: `internal/arbiter/*.go`
- Modify: `internal/diag/*.go`
- Modify: `internal/eval/*.go`
- Modify: `internal/flow/*.go`
- Modify: `internal/host/*.go`
- Modify: `internal/host/imp/*.go`
- Modify: `internal/host/exp/*.go`
- Modify: `internal/rules/*.go`
- Modify: `internal/store/*.go`
- Modify: `internal/tools/*.go`
- Modify: `internal/version/*.go`

**Tests likely to need updates**
- Existing tests in the same packages may assert old Chinese strings. Update expected strings only when the production output changed language.
- Add small localization guard tests only for high-risk prompt/message packages if needed: `internal/arbiter`, `internal/agents/guard`, `internal/host/imp`, `internal/store`, `internal/tools`.

---

### Task 1: Establish scan baseline and classification rules

**Files:**
- Read-only: production Go files under `cmd/`, `internal/`, `assets/`
- Read-only: public scripts/docs as needed

**Interfaces:**
- Consumes: current `main` state plus spec `docs/superpowers/specs/2026-07-21-production-localization-pass-design.md`
- Produces: a working classification of hits into `translate`, `keep-domain-data`, and `ignore-code/comment`

- [ ] **Step 1: Confirm branch and clean status**

Run:

```bash
git status --short --branch
```

Expected: branch is `chore/production-localization-pass` and no uncommitted code changes except the spec/plan files if already created.

- [ ] **Step 2: Run the production string literal scan**

Run:

```bash
python3 - <<'PY'
from pathlib import Path
import re
root=Path('/mnt/Data/AI/ainovel-cli')
han=re.compile(r'[一-鿿]')
cyr=re.compile(r'[Ѐ-ӿ]')

def extract_strings(text):
    i=0; line=1; n=len(text)
    while i<n:
        c=text[i]
        if c=='\n': line+=1; i+=1; continue
        if c=='/' and i+1<n and text[i+1]=='/':
            j=text.find('\n',i); i=n if j==-1 else j; continue
        if c=='/' and i+1<n and text[i+1]=='*':
            j=text.find('*/',i+2); seg=text[i:n if j==-1 else j+2]
            line+=seg.count('\n'); i=n if j==-1 else j+2; continue
        if c in ('"','`'):
            quote=c; start=line; i+=1; buf=[]; esc=False
            if quote=='`':
                while i<n and text[i]!='`':
                    if text[i]=='\n': line+=1
                    buf.append(text[i]); i+=1
                i+=1
            else:
                while i<n:
                    ch=text[i]
                    if ch=='\n': line+=1
                    if esc:
                        buf.append(ch); esc=False; i+=1; continue
                    if ch=='\\': esc=True; i+=1; continue
                    if ch=='"': i+=1; break
                    buf.append(ch); i+=1
            s=''.join(buf)
            if han.search(s) or cyr.search(s):
                yield start,s.replace('\n','\\n')[:240]
            continue
        i+=1

for p in sorted(list(root.glob('cmd/**/*.go'))+list(root.glob('internal/**/*.go'))+list(root.glob('assets/**/*.go'))):
    if p.name.endswith('_test.go'):
        continue
    rel=str(p.relative_to(root))
    hits=list(extract_strings(p.read_text(errors='ignore')))
    if hits:
        print(f'## {rel}')
        for ln,s in hits[:80]:
            print(f'{ln}: {s}')
        if len(hits)>80:
            print('...')
PY
```

Expected: output lists remaining production strings containing Chinese/Cyrillic.

- [ ] **Step 3: Classify keep-domain-data examples before editing**

Keep these categories unchanged:

```text
- Chinese chapter regexes and generated chapter numbering patterns: 第...章, 第 %d 章, ^#+\s+第.+?章
- Chinese novel import/source examples and title parsing literals
- rules defaults and style-stat data: 某种程度上, 仿佛, 值得注意的是, 不禁, 宛如, fatigue word patterns
- Chinese prose inside tests or fixtures
- code comments only, unless the same phrase appears in a string literal
```

Expected: only prose messages/prompts/errors/headings outside those categories are edited.

---

### Task 2: Localize agent, arbiter, and asset production prompts

**Files:**
- Modify: `assets/load.go`
- Modify: `internal/agents/build.go`
- Modify: `internal/agents/ctxpack/restore.go`
- Modify: `internal/agents/guard/subagent_guards.go`
- Modify: `internal/arbiter/arbiter.go`
- Modify: `internal/arbiter/failure.go`
- Modify: `internal/arbiter/intervention.go`
- Modify: `internal/arbiter/plan_start.go`
- Test: existing tests in `assets`, `internal/agents`, `internal/arbiter`

**Interfaces:**
- Consumes: existing agent/arbiter behavior and tool names
- Produces: same behavior with Vietnamese user/model-facing text

- [ ] **Step 1: Add or update a focused test where practical**

If a package already has a test around the changed text, update expected Vietnamese strings. For prompt constants without public access, add a package-local test only if it can assert a stable contract without brittle full snapshots. Example acceptable assertion in `internal/arbiter/arbiter_test.go`:

```go
func TestArbiterRetryHintIsVietnamese(t *testing.T) {
    if strings.Contains(jsonRetryHint, "请") || strings.Contains(jsonRetryHint, "不要") {
        t.Fatalf("retry hint còn tiếng Trung: %q", jsonRetryHint)
    }
    if !strings.Contains(jsonRetryHint, "Chỉ xuất") {
        t.Fatalf("retry hint chưa có hướng dẫn tiếng Việt: %q", jsonRetryHint)
    }
}
```

Expected before implementation: fails if the constant is still Chinese.

- [ ] **Step 2: Translate agent/arbiter prose only**

Translate messages such as:

```text
不支持覆盖的 prompt 文件 → Không hỗ trợ ghi đè file prompt
忽略非法风格文件名 → Bỏ qua tên file phong cách không hợp lệ
无效推理强度 → Mức suy luận không hợp lệ
审阅者：阅读原文... → Người đánh giá: đọc nguyên văn...
禁止结束... → Cấm kết thúc...
上面的输出不是合法的 JSON... → Đầu ra phía trên không phải JSON hợp lệ...
reason 不能为空 → reason không được để trống
```

Do not translate identifiers inside the text such as `prompt`, `JSON`, `Markdown`, `save_foundation`, `commit_chapter`, role names, or enum values.

- [ ] **Step 3: Run targeted tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test ./assets ./internal/agents ./internal/agents/ctxpack ./internal/agents/guard ./internal/arbiter
```

Expected: all listed packages pass.

---

### Task 3: Localize host, flow, and import pipeline production text

**Files:**
- Modify: `internal/flow/advance.go`
- Modify: `internal/flow/router.go`
- Modify: `internal/host/*.go`
- Modify: `internal/host/imp/*.go`
- Modify: `internal/host/sim/*.go` if scan finds untranslated strings
- Modify: `internal/host/exp/*.go` only for generated headings/labels, not regexes
- Test: existing tests in `internal/flow`, `internal/host`, `internal/host/imp`, `internal/host/sim`, `internal/host/exp`

**Interfaces:**
- Consumes: existing import/engine flow, JSON schemas, action names, file layout
- Produces: Vietnamese runtime status, import prompts, retry guidance, generated summaries, and errors

- [ ] **Step 1: Translate runtime status/errors and import prompts**

Translate prose messages such as:

```text
读取源文件 → Đọc file nguồn
导入完成，等待验收后续写 → Nhập xong, chờ nghiệm thu rồi viết tiếp
模型返回空响应 → Model trả về phản hồi rỗng
输出校验未通过 → Đầu ra chưa qua kiểm tra hợp lệ
请分析第...章 → Hãy phân tích chương...
以下是全书... → Dưới đây là...
```

Keep schema names and values unchanged: `BookSynthesis`, `RangeDigest`, `premise`, `characters`, `world_rules`, `structure`, `planning_tier`, `story_status`, `front_matter`, `back_matter`, `chapter`, `group`.

- [ ] **Step 2: Preserve Chinese parsing/output patterns**

Do not translate patterns/headings whose function is to parse or represent Chinese source novel structure:

```text
^#+\s+第.+?章
第 %d 章
第 %d 卷
第\s*(\d+)\s*章
```

Expected: import/export tests continue to pass and behavior does not change.

- [ ] **Step 3: Run targeted tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test ./internal/flow ./internal/host ./internal/host/imp ./internal/host/sim ./internal/host/exp
```

Expected: all listed packages pass.

---

### Task 4: Localize diagnostics, eval, rules, store, tools, and version text

**Files:**
- Modify: `internal/diag/*.go`
- Modify: `internal/eval/*.go`
- Modify: `internal/rules/*.go`
- Modify: `internal/store/*.go`
- Modify: `internal/tools/*.go`
- Modify: `internal/version/*.go`
- Test: existing tests in those packages

**Interfaces:**
- Consumes: existing generated markdown formats, error wrapping, store file names, tool schemas
- Produces: Vietnamese diagnostics, eval output, generated markdown labels, tool errors, and version/update errors

- [ ] **Step 1: Translate diagnostics and eval output**

Translate CLI/report/diagnostic messages while preserving field names and report structure:

```text
运行时错误 → Lỗi runtime
工件读取失败 → Đọc artifact thất bại
缺少 checkpoint → Thiếu checkpoint
variant 自身门禁失败 → variant tự thất bại ở cổng kiểm tra
```

Keep terms such as `baseline`, `variant`, `checkpoint`, `phase`, `flow`, `case`, `gate`, and JSON field names unchanged when they are part of protocol or reports.

- [ ] **Step 2: Translate generated markdown headings/labels**

Translate generated labels where users read output markdown:

```text
# 分层大纲 → # Dàn ý phân tầng
**主题** → **Chủ đề**
**目标** → **Mục tiêu**
# 时间线 → # Dòng thời gian
# 伏笔账本 → # Sổ cái phục bút
# 人物关系 → # Quan hệ nhân vật
# 世界观规则 → # Quy tắc thế giới quan
```

Keep chapter/volume numeric forms like `第 %d 章` or `第 %d 卷` only if tests or export format require Chinese-style headings for imported Chinese novels; otherwise use Vietnamese labels if the generated file is part of Vietnamese output.

- [ ] **Step 3: Translate tool and version errors**

Translate errors such as:

```text
store 不能为空 → store không được để trống
progress 未初始化 → progress chưa được khởi tạo
checksum 清单中未找到 → không tìm thấy trong manifest checksum
SHA256 校验失败 → kiểm tra SHA256 thất bại
```

Keep sentinel errors and wrapping `%w` unchanged.

- [ ] **Step 4: Run targeted tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test ./internal/diag ./internal/eval ./internal/rules ./internal/store ./internal/tools ./internal/version
```

Expected: all listed packages pass.

---

### Task 5: Final scan, classification report, and full verification

**Files:**
- Modify if needed: any package files still containing untranslated production prose
- Read-only: all production Go source

**Interfaces:**
- Consumes: all prior translated clusters
- Produces: final evidence that remaining Chinese/Cyrillic production strings are intentional domain-data or parser patterns

- [ ] **Step 1: Run gofmt on modified Go files**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.25 gofmt -w $(git diff --name-only -- '*.go')
```

Expected: no output and no formatting failures.

- [ ] **Step 2: Run full Docker tests**

Run:

```bash
docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test ./...
```

Expected: exit code 0; all packages pass.

- [ ] **Step 3: Run final production string scan**

Run the scan from Task 1 Step 2 again.

Expected: remaining hits are limited to classified keep-domain-data categories such as Chinese chapter regexes, Chinese examples, default forbidden phrases, fatigue-word patterns, and generated Chinese novel numbering if intentionally preserved.

- [ ] **Step 4: Check git diff**

Run:

```bash
git diff --stat
git diff --check
```

Expected: scoped localization changes only; `git diff --check` prints no whitespace errors.

- [ ] **Step 5: Commit**

Run:

```bash
git add docs/superpowers/specs/2026-07-21-production-localization-pass-design.md docs/superpowers/plans/2026-07-21-production-localization-pass.md .
git commit -m "chore: localize remaining production strings" -m "Co-Authored-By: Claude <noreply@anthropic.com>"
```

Expected: one commit containing the broader production localization pass.
