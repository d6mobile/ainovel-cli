package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAllowsFilter(t *testing.T) {
	if New("", nil).allows(KindDeadlock) != true {
		t.Error("mặc định events phải cho qua toàn bộ")
	}
	n := New("", []string{KindRunEnd, KindBudget})
	if !n.allows(KindRunEnd) || !n.allows(KindBudget) {
		t.Error("kind được liệt kê phải được cho qua")
	}
	if n.allows(KindDeadlock) {
		t.Error("kind không được liệt kê phải bị chặn")
	}
	var nilN *Notifier
	if nilN.allows(KindRunEnd) {
		t.Error("Notifier nil phải chặn mọi thứ")
	}
	nilN.Send(Notification{Kind: KindRunEnd})
}

func TestKindsAreUniqueAndKnown(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range Kinds() {
		if kind == "" || seen[kind] {
			t.Fatalf("tên sự kiện thông báo phải không rỗng và duy nhất: %q", kind)
		}
		seen[kind] = true
		if !IsKnownKind(kind) {
			t.Fatalf("Kinds và IsKnownKind không nhất quán: %q", kind)
		}
	}
	if IsKnownKind("repeat") {
		t.Fatal("sự kiện repeat cũ không được tiếp tục xuất hiện trong hợp đồng mới")
	}
}

func TestCommandChannelEnvAndStdin(t *testing.T) {
	dir := t.TempDir()
	envFile := filepath.Join(dir, "env.txt")
	jsonFile := filepath.Join(dir, "stdin.json")

	command := `echo "$NOTIFY_KIND|$NOTIFY_LEVEL|$NOTIFY_TITLE|$NOTIFY_BODY" > ` + shellQuote(envFile) + ` && cat > ` + shellQuote(jsonFile)
	if runtime.GOOS == "windows" {
		// Explicit UTF-8 (no BOM) so Chinese title/body survive PowerShell's default code page.
		command = `$utf8 = New-Object System.Text.UTF8Encoding $false; ` +
			`$line = "$env:NOTIFY_KIND|$env:NOTIFY_LEVEL|$env:NOTIFY_TITLE|$env:NOTIFY_BODY"; ` +
			`[System.IO.File]::WriteAllText(` + powerShellQuote(envFile) + `, $line, $utf8); ` +
			`$reader = New-Object System.IO.StreamReader([Console]::OpenStandardInput(), $utf8); ` +
			`$payload = $reader.ReadToEnd(); ` +
			`[System.IO.File]::WriteAllText(` + powerShellQuote(jsonFile) + `, $payload, $utf8)`
	}
	n := New(command, nil)
	nt := Notification{Kind: KindBudget, Level: "warn", Title: "ainovel: Ngân sách", Body: "Đã dùng $8.00"}
	n.deliver(nt)

	env, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("command chưa được thực thi: %v", err)
	}
	if got := strings.TrimSpace(string(env)); got != "budget|warn|ainovel: Ngân sách|Đã dùng $8.00" {
		t.Errorf("truyền biến môi trường không khớp: %q", got)
	}

	raw, err := os.ReadFile(jsonFile)
	if err != nil {
		t.Fatalf("stdin chưa được truyền: %v", err)
	}
	var decoded Notification
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("stdin không hợp lệ JSON: %v", err)
	}
	if decoded != nt {
		t.Errorf("JSON trong stdin không khớp: %+v", decoded)
	}
}

func TestCommandChannelTimeoutKill(t *testing.T) {
	command := "sleep 30"
	if runtime.GOOS == "windows" {
		command = "Start-Sleep -Seconds 30"
	}
	n := New(command, nil)
	n.timeout = 200 * time.Millisecond

	start := time.Now()
	n.deliver(Notification{Kind: KindRunEnd})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("hết thời gian mà không bị kill, bị chặn %v", elapsed)
	}
}

func TestWindowsNotificationScriptUsesEnvironmentWithoutInterpolation(t *testing.T) {
	for _, want := range []string{"$env:NOTIFY_TITLE", "$env:NOTIFY_BODY", "$env:NOTIFY_LEVEL", "ShowBalloonTip"} {
		if !strings.Contains(windowsNotificationScript, want) {
			t.Fatalf("Windows notification script missing %q", want)
		}
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func powerShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
