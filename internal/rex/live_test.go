//go:build live

package rex

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLiveSessionAgent reads a real session's program status. Run it with:
//
//	REXWS_LIVE_SESSION=<session-id> go test -tags live ./internal/rex -run TestLive -v
func TestLiveSessionAgent(t *testing.T) {
	sessionID := os.Getenv("REXWS_LIVE_SESSION")
	if sessionID == "" {
		t.Skip("set REXWS_LIVE_SESSION to a live Rex session ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	state, detail, err := New().SessionAgent(ctx, sessionID)
	if err != nil {
		t.Fatalf("SessionAgent: %v", err)
	}
	switch state {
	case "idle", "working", "blocked", "error", "done":
	default:
		t.Fatalf("unexpected state %q", state)
	}
	t.Logf("state=%s detail=%q", state, detail)
}
