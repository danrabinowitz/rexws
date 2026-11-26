package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		{ID: "session:1", Label: "repo/main", Agents: []Agent{
			{BlockID: "block:main", WindowLabel: "main", App: "claude-code", State: AgentWorking},
		}},
		{ID: "session:2", Label: "repo/feat/auth", Agents: []Agent{
			{BlockID: "block:feat", WindowLabel: "api", App: "claude-code", State: AgentBlocked, Detail: "permission"},
		}},
		// Rex allows duplicate labels; the first session wins.
		{ID: "session:3", Label: "repo/feat/auth", Agents: []Agent{
			{BlockID: "block:other", State: AgentIdle},
		}},
	}

	got, err := Build(inputs, state, sessions, config.StatusReady)
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
	if len(main.Agents) != 1 || main.Agents[0].BlockID != "block:main" || main.Agents[0].App != "claude-code" {
		t.Errorf("main agents = %#v", main.Agents)
	}

	feat := byName["repo/feat/auth"]
	if feat.WorkflowStatus != config.StatusBlocked || feat.Note != "waiting on schema migration" {
		t.Errorf("feat workflow state = %#v", feat)
	}
	if feat.RexSessionID != "session:2" || feat.AgentState != AgentBlocked || feat.AgentDetail != "permission" {
		t.Errorf("feat rex state = %#v", feat)
	}
	if len(feat.Agents) != 1 || feat.Agents[0].BlockID != "block:feat" || feat.Agents[0].WindowLabel != "api" {
		t.Errorf("feat agents = %#v", feat.Agents)
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

	got, err := Build(inputs, config.NewState(), nil, config.StatusReady)
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
	if ws.Agents == nil || len(ws.Agents) != 0 {
		t.Errorf("agents = %#v, want an empty non-nil slice", ws.Agents)
	}
}

func TestBuildFailsForMissingWorktree(t *testing.T) {
	inputs := []Input{{
		Project:   config.Project{Name: "repo", Path: "/code/repo"},
		Worktrees: []git.Worktree{{Path: filepath.Join(t.TempDir(), "missing"), Branch: "main"}},
	}}
	if _, err := Build(inputs, config.NewState(), nil, config.StatusReady); err == nil {
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

func TestBuildMultipleAgents(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := pathutil.Canonical(dir)
	if err != nil {
		t.Fatal(err)
	}
	inputs := []Input{{
		Project:   config.Project{Name: "repo", Path: repo},
		Worktrees: []git.Worktree{{Path: dir, Head: "abc", Branch: "main"}},
	}}
	sessions := []Session{{
		ID:    "session:1",
		Label: "repo/main",
		Agents: []Agent{
			{BlockID: "block:1", WindowLabel: "api", App: "claude-code", State: AgentBlocked, Detail: "question"},
			{BlockID: "block:2", WindowLabel: "tests", App: "claude-code", State: AgentWorking},
			{BlockID: "block:3", WindowLabel: "logs", App: "cargo", State: AgentIdle},
		},
	}}

	got, err := Build(inputs, config.NewState(), sessions, config.StatusReady)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d workstreams", len(got))
	}
	ws := got[0]
	if ws.AgentState != AgentBlocked || ws.AgentDetail != "question" {
		t.Errorf("aggregate = (%q, %q), want (blocked, question)", ws.AgentState, ws.AgentDetail)
	}
	if len(ws.Agents) != 3 {
		t.Fatalf("got %d agents, want 3", len(ws.Agents))
	}
	if ws.Agents[1].WindowLabel != "tests" || ws.Agents[1].State != AgentWorking {
		t.Errorf("second agent = %#v", ws.Agents[1])
	}
}

func TestBuildSessionWithoutAgentsIsIdle(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	inputs := []Input{{
		Project:   config.Project{Name: "repo", Path: root},
		Worktrees: []git.Worktree{{Path: dir, Head: "abc", Branch: "main"}},
	}}
	sessions := []Session{{ID: "session:1", Label: "repo/main"}}

	got, err := Build(inputs, config.NewState(), sessions, config.StatusReady)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got[0].AgentState != AgentIdle {
		t.Errorf("agent state = %q, want idle", got[0].AgentState)
	}
	if got[0].RexSessionID != "session:1" {
		t.Errorf("session id = %q", got[0].RexSessionID)
	}
	if got[0].Agents == nil || len(got[0].Agents) != 0 {
		t.Errorf("agents = %#v", got[0].Agents)
	}
}

func TestBuildUnreadableSession(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	inputs := []Input{{
		Project:   config.Project{Name: "repo", Path: root},
		Worktrees: []git.Worktree{{Path: dir, Head: "abc", Branch: "main"}},
	}}
	sessions := []Session{{ID: "session:1", Label: "repo/main", Unreadable: true}}

	got, err := Build(inputs, config.NewState(), sessions, config.StatusReady)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got[0].AgentState != AgentUnknown {
		t.Errorf("agent state = %q, want unknown", got[0].AgentState)
	}
	if got[0].Agents == nil || len(got[0].Agents) != 0 {
		t.Errorf("agents = %#v", got[0].Agents)
	}
}

func TestSummarize(t *testing.T) {
	tests := []struct {
		name   string
		agents []Agent
		want   AgentState
		detail string
	}{
		{"no agents", nil, AgentIdle, ""},
		{"one working", []Agent{{State: AgentWorking}}, AgentWorking, ""},
		{"blocked beats working", []Agent{{State: AgentWorking}, {State: AgentBlocked, Detail: "permission"}}, AgentBlocked, "permission"},
		{"error beats working", []Agent{{State: AgentWorking}, {State: AgentError}}, AgentError, ""},
		{"done beats idle", []Agent{{State: AgentIdle}, {State: AgentDone}}, AgentDone, ""},
		{"unknown does not outrank known", []Agent{{State: AgentUnknown}, {State: AgentWorking}}, AgentWorking, ""},
		{"all unknown", []Agent{{State: AgentUnknown}, {State: AgentUnknown}}, AgentUnknown, ""},
		{"empty state is unknown", []Agent{{State: ""}}, AgentUnknown, ""},
		{"another blocked fills the kind", []Agent{{State: AgentBlocked}, {State: AgentBlocked, Detail: "permission"}}, AgentBlocked, "permission"},
		{"first blocked kind wins", []Agent{{State: AgentBlocked, Detail: "question"}, {State: AgentBlocked, Detail: "permission"}}, AgentBlocked, "question"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, detail := summarize(test.agents)
			if state != test.want || detail != test.detail {
				t.Errorf("summarize = (%q, %q), want (%q, %q)", state, detail, test.want, test.detail)
			}
		})
	}
}

func TestWorkstreamJSONIncludesAgents(t *testing.T) {
	ws := Workstream{
		Project:        "repo",
		Branch:         "main",
		Path:           "/repo",
		SessionName:    "repo/main",
		WorkflowStatus: config.StatusReady,
		AgentState:     AgentWorking,
		Agents: []Agent{{
			BlockID:     "block:1",
			WindowID:    "window:1",
			WindowLabel: "api",
			App:         "claude-code",
			State:       AgentWorking,
			Msg:         "Running tests",
		}},
	}
	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded struct {
		AgentState string `json:"agent_state"`
		Agents     []struct {
			BlockID     string `json:"block_id"`
			WindowLabel string `json:"window_label"`
			State       string `json:"state"`
			Msg         string `json:"msg"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.AgentState != "working" {
		t.Errorf("agent_state = %q", decoded.AgentState)
	}
	if len(decoded.Agents) != 1 {
		t.Fatalf("agents = %#v", decoded.Agents)
	}
	agent := decoded.Agents[0]
	if agent.BlockID != "block:1" || agent.WindowLabel != "api" || agent.State != "working" || agent.Msg != "Running tests" {
		t.Errorf("agent = %#v", agent)
	}

	empty, err := json.Marshal(Workstream{Agents: []Agent{}})
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}
	if !strings.Contains(string(empty), `"agents":[]`) {
		t.Errorf("empty agents should encode as [], got %s", empty)
	}
}
