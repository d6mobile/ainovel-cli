---
name: verify
description: Verify ainovel-cli runtime changes by launching the TUI and driving the affected flow.
---

# ainovel-cli Runtime Verification

Use the actual CLI/TUI surface, not tests, for product-source changes.

## Build

The local host may not have Go installed. Build through Docker from the repo root:

```bash
tmp=$(mktemp -d)
docker run --rm -v "$PWD:/src" -v "$tmp:/out" -w /src golang:1.25 \
  go build -buildvcs=false -o /out/ainovel-cli ./cmd/ainovel-cli
```

`-buildvcs=false` avoids VCS stamping failures from the bind-mounted git repo inside Docker.

## Minimal safe config

Create a temp HOME with a local Ollama-style config so the app starts without real credentials:

```bash
mkdir -p "$tmp/home/.ainovel" "$tmp/work"
cat > "$tmp/home/.ainovel/config.json" <<'JSON'
{
  "provider": "ollama",
  "model": "dummy-model",
  "style": "default",
  "providers": {
    "ollama": {
      "base_url": "http://127.0.0.1:11434/v1",
      "models": [{"name": "dummy-model", "context_window": 128000}]
    }
  }
}
JSON
```

Add only the config fields needed by the change under verification.

## Launch and drive

Run in an isolated tmux server:

```bash
tmux -L ainovel-verify -f /dev/null new-session -d -s verify -x 120 -y 40 \
  "cd $tmp/work && HOME=$tmp/home TERM=xterm-256color $tmp/ainovel-cli"
sleep 1
tmux -L ainovel-verify capture-pane -t verify -p
```

Drive TUI commands with `tmux send-keys`, then capture the pane after each meaningful step:

```bash
tmux -L ainovel-verify send-keys -t verify '/config' Enter
sleep 0.3
tmux -L ainovel-verify capture-pane -t verify -p
```

Use stepwise `Escape` presses with short sleeps when backing out of nested modals; rapid key batches can leave the modal partially open.

Clean up when finished:

```bash
tmux -L ainovel-verify kill-session -t verify || true
```
