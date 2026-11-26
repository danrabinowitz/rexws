package rex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeSessionList = `{"sessions":[{"session_id":"session:fake","label":"repo/main"}]}`

const fakeWindowList = `{
	"active_window_id": "window:2",
	"windows": [
		{"window_id": "window:1", "label": "api", "focused_block_id": "block:1",
		 "layers": [{"blocks": [{"block_id": "block:1"}]}]},
		{"window_id": "window:2", "label": "tests", "focused_block_id": "block:2",
		 "layers": [{"blocks": [{"block_id": "block:2"}]}]}
	]
}`

// fakeRex writes a rex stub that returns the given session and window JSON and
// logs session.select calls. It returns the stub path and log path.
func fakeRex(t *testing.T, sessions, windows string) (*Client, string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	t.Setenv("FAKE_REX_LOG", logPath)

	script := `#!/bin/sh
log="$FAKE_REX_LOG"
case "$*" in
  "session ls --json") printf '%s\n' '` + sessions + `' ;;
  "window ls -s session:fake --json") printf '%s\n' '` + windows + `' ;;
  *"session.select"*) printf '%s\n' "$*" >> "$log" ;;
  *"session kill"*) printf '%s\n' "$*" >> "$log" ;;
  *) printf 'unexpected: %s\n' "$*" >&2; exit 1 ;;
esac
`
	binPath := filepath.Join(dir, "rex")
	if err := os.WriteFile(binPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return New(binPath), logPath
}

func TestSwitchWithWindow(t *testing.T) {
	client, logPath := fakeRex(t, fakeSessionList, fakeWindowList)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.Switch(ctx, "repo/main", "/tmp", "client:fake", "tests")
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if result.SessionID != "session:fake" || result.Created {
		t.Errorf("result = %#v", result)
	}
	if result.WindowID != "window:2" || result.WindowLabel != "tests" {
		t.Errorf("window = %q (%q)", result.WindowID, result.WindowLabel)
	}

	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read calls: %v", err)
	}
	want := "session.select session_id=session:fake window_id=window:2"
	if !strings.Contains(string(calls), want) {
		t.Errorf("select call = %q, want it to contain %q", calls, want)
	}
}

func TestSwitchWithoutWindowOmitsWindowID(t *testing.T) {
	client, logPath := fakeRex(t, fakeSessionList, fakeWindowList)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := client.Switch(ctx, "repo/main", "/tmp", "client:fake", "")
	if err != nil {
		t.Fatalf("Switch: %v", err)
	}
	if result.WindowID != "" {
		t.Errorf("window = %q, want empty", result.WindowID)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read calls: %v", err)
	}
	if !strings.Contains(string(calls), "session.select session_id=session:fake") {
		t.Errorf("select call = %q", calls)
	}
	if strings.Contains(string(calls), "window_id=") {
		t.Errorf("select call unexpectedly names a window: %q", calls)
	}
}

func TestSwitchWindowRequiresLiveSession(t *testing.T) {
	client, _ := fakeRex(t, `{"sessions":[]}`, fakeWindowList)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Switch(ctx, "repo/main", "/tmp", "client:fake", "tests")
	if err == nil || !strings.Contains(err.Error(), "no live session") {
		t.Fatalf("err = %v, want a no-live-session error", err)
	}
}

func TestKillSession(t *testing.T) {
	client, logPath := fakeRex(t, fakeSessionList, fakeWindowList)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.KillSession(ctx, "session:fake"); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	calls, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read calls: %v", err)
	}
	if !strings.Contains(string(calls), "session kill session:fake") {
		t.Errorf("kill call = %q", calls)
	}
}
