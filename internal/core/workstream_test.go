package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/git"
	"github.com/danrabinowitz/rexws/internal/pathutil"
)

func TestBuildMergesWorktreesStateAndSessions(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "repo")
	linkedDir := filepath.Join(root, "wt-feat")
	for _, dir := range []string{mainDir, linkedDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mainPath, err := pathutil.Canonical(mainDir)
	if err != nil {
		t.Fatal(err)
	}
	linkedPath, err := pathutil.Canonical(linkedDir)
	if err != nil {
		t.Fatal(err)
	}

	inputs := []Input{{
		Project: config.Project{Name: "repo", Path: mainPath},
		Worktrees: []git.Worktree{
			{Path: mainDir, Head: "0123456789abcdef", Branch: "main"},
			{Path: linkedDir, Head: "fedcba9876543210", Branch: "feat/auth"},
			{Path: filepath.Join(root, "bare"), Bare: true},
			{Path: filepath.Join(root, "gone"), Branch: "gone", Prunable: true},
		},
	}}
	state := config.State{Workstreams: map[string]config.WorkstreamState{
		linkedPath: {Status: config.StatusBlocked, Note: "waiting on schema migration"},
	}}
	sessions := []Session{
		{ID: "session:1", Label: "repo/main", AgentState: AgentWorking},
		{ID: "session:2", Label: "repo/feat/auth", AgentState: AgentBlocked, AgentDetail: "permission"},
		// Rex allows duplicate labels; the first session wins.
		{ID: "session:3", Label: "repo/feat/auth", AgentState: AgentIdle},
	}

	got, err := Build(inputs, state, sessions)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d workstreams, want 2: %#v", len(got), got)
	}
	if got[0].SessionName != "repo/feat/auth" || got[1].SessionName != "repo/main" {
		t.Fatalf("unexpected order: %q, %q", got[0].SessionName, got[1].SessionName)
	}

	byName := map[string]Workstream{got[0].SessionName: got[0], got[1].SessionName: got[1]}

	main := byName["repo/main"]
	if main.Project != "repo" || main.Branch != "main" || main.Path != mainPath {
		t.Errorf("main identity = %#v", main)
	}
	if main.WorkflowStatus != config.StatusReady {
		t.Errorf("main status = %q, want ready", main.WorkflowStatus)
	}
	if main.RexSessionID != "session:1" || main.AgentState != AgentWorking || main.AgentDetail != "" {
		t.Errorf("main rex state = %#v", main)
	}

	feat := byName["repo/feat/auth"]
	if feat.WorkflowStatus != config.StatusBlocked || feat.Note != "waiting on schema migration" {
		t.Errorf("feat workflow state = %#v", feat)
	}
	if feat.RexSessionID != "session:2" || feat.AgentState != AgentBlocked || feat.AgentDetail != "permission" {
		t.Errorf("feat rex state = %#v", feat)
	}
}

func TestBuildDetachedWorktree(t *testing.T) {
	root := t.TempDir()
	wtDir := filepath.Join(root, "detached")
	if err := os.MkdirAll(wtDir, 0o755); err != nil {
		t.Fatal(err)
	}
	inputs := []Input{{
		Project:   config.Project{Name: "repo", Path: root},
		Worktrees: []git.Worktree{{Path: wtDir, Head: "0123456789abcdef", Detached: true}},
	}}

	got, err := Build(inputs, config.NewState(), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d workstreams", len(got))
	}
	ws := got[0]
	if ws.Branch != "0123456" || ws.SessionName != "repo/0123456" || !ws.Detached {
		t.Errorf("detached workstream = %#v", ws)
	}
	if ws.AgentState != AgentStopped {
		t.Errorf("agent state = %q, want stopped", ws.AgentState)
	}
}

func TestBuildFailsForMissingWorktree(t *testing.T) {
	inputs := []Input{{
		Project:   config.Project{Name: "repo", Path: "/code/repo"},
		Worktrees: []git.Worktree{{Path: filepath.Join(t.TempDir(), "missing"), Branch: "main"}},
	}}
	if _, err := Build(inputs, config.NewState(), nil); err == nil {
		t.Fatal("expected an error for a missing worktree path")
	}
}

func TestFilterStatuses(t *testing.T) {
	workstreams := []Workstream{
		{SessionName: "a/main", WorkflowStatus: config.StatusInProgress},
		{SessionName: "b/main", WorkflowStatus: config.StatusBlocked},
		{SessionName: "c/main", WorkflowStatus: config.StatusReady},
	}
	if got := FilterStatuses(workstreams, nil); len(got) != 3 {
		t.Errorf("empty filter kept %d, want 3", len(got))
	}
	got := FilterStatuses(workstreams, []config.Status{config.StatusBlocked})
	if len(got) != 1 || got[0].SessionName != "b/main" {
		t.Errorf("filtered = %#v", got)
	}
}
