//go:build live

package rex

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/danrabinowitz/rexws/internal/agentstate"
)

// TestLiveSessionAgents reads a real session's per-block program status. Run
// it with:
//
//	REXWS_LIVE_SESSION=<session-id> go test -tags live ./internal/rex -run TestLive -v
func TestLiveSessionAgents(t *testing.T) {
	sessionID := os.Getenv("REXWS_LIVE_SESSION")
	if sessionID == "" {
		t.Skip("set REXWS_LIVE_SESSION to a live Rex session ID")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	blocks, err := New("").SessionAgents(ctx, sessionID)
	if err != nil {
		t.Fatalf("SessionAgents: %v", err)
	}
	for _, block := range blocks {
		switch block.State {
		case agentstate.Idle, agentstate.Working, agentstate.Blocked, agentstate.Error,
			agentstate.Done, agentstate.Unknown:
		default:
			t.Errorf("block %s has unexpected state %q", block.BlockID, block.State)
		}
		t.Logf("%s/%s app=%s state=%s detail=%q", block.WindowLabel, block.BlockLabel, block.App, block.State, block.Detail)
	}
	t.Logf("%d reporting terminal blocks", len(blocks))
}
