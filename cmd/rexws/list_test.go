package main

import (
	"errors"
	"testing"

	"github.com/danrabinowitz/rexws/internal/agentstate"
	"github.com/danrabinowitz/rexws/internal/core"
	"github.com/danrabinowitz/rexws/internal/rex"
)

func TestCoreSessions(t *testing.T) {
	snapshot := []rex.SessionStatus{
		{
			Session: rex.Session{ID: "session:1", Label: "repo/main"},
			Blocks: []rex.BlockStatus{
				{
					BlockID: "block:1", BlockLabel: "shell", WindowID: "window:1", WindowLabel: "api",
					State: agentstate.Working, App: "claude-code", EffectiveApp: "claude-code",
					Msg: "Running tests", UpdatedAt: "2026-11-25T23:00:00Z",
				},
				{
					BlockID: "block:2", BlockLabel: "logs", WindowID: "window:2", WindowLabel: "tests",
					State: agentstate.Unknown, Err: errors.New("status unavailable"),
				},
			},
		},
		{
			Session: rex.Session{ID: "session:2", Label: "repo/feat"},
			Err:     errors.New("block list failed"),
		},
	}

	sessions, warnings := coreSessions(snapshot)
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions", len(sessions))
	}

	main := sessions[0]
	if main.ID != "session:1" || main.Unreadable {
		t.Errorf("main session = %#v", main)
	}
	if len(main.Agents) != 2 {
		t.Fatalf("main agents = %#v", main.Agents)
	}
	working := main.Agents[0]
	if working.WindowLabel != "api" || working.State != core.AgentWorking || working.Msg != "Running tests" {
		t.Errorf("working agent = %#v", working)
	}
	unknown := main.Agents[1]
	if unknown.State != core.AgentUnknown || unknown.Error != "status unavailable" {
		t.Errorf("unknown agent = %#v", unknown)
	}

	feat := sessions[1]
	if !feat.Unreadable || feat.Agents != nil {
		t.Errorf("feat session = %#v", feat)
	}

	if len(warnings) != 2 {
		t.Fatalf("got %d warnings: %#v", len(warnings), warnings)
	}
}
