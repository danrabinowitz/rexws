package tui

import (
	"reflect"
	"testing"

	"github.com/danrabinowitz/rexws/internal/config"
	"github.com/danrabinowitz/rexws/internal/core"
)

func sampleWorkstreams() []core.Workstream {
	return []core.Workstream{
		{
			Project: "rexws", Branch: "m1-m2-core-rex", Path: "/wt/rexws", SessionName: "rexws/m1-m2-core-rex",
			WorkflowStatus: config.StatusInProgress, WorkflowStatusLabel: "In Progress",
			RexSessionID: "session:rexws", AgentState: core.AgentBlocked, AgentDetail: "permission",
			Agents: []core.Agent{
				{BlockID: "block:agent", WindowID: "window:agent", App: "claude-code", State: core.AgentWorking},
				{BlockID: "block:tests", WindowID: "window:tests", App: "", State: core.AgentBlocked, Detail: "permission"},
			},
			Windows: []core.Window{
				{ID: "window:agent", Label: "agent", Active: true, FocusedBlockID: "block:agent"},
				{ID: "window:tests", Label: "tests", FocusedBlockID: "block:tests"},
			},
		},
		{
			Project: "do-si-do", Branch: "refactor-sync", Path: "/wt/dosido", SessionName: "do-si-do/refactor-sync",
			WorkflowStatus: config.StatusInProgress, WorkflowStatusLabel: "In Progress",
			RexSessionID: "session:dosido", AgentState: core.AgentBlocked, AgentDetail: "permission",
			Agents: []core.Agent{
				{BlockID: "block:d", WindowID: "window:d", App: "claude-code", State: core.AgentBlocked, Detail: "permission"},
			},
			Windows: []core.Window{
				{ID: "window:d", Label: "shell", Active: true, FocusedBlockID: "block:d"},
			},
		},
		{
			Project: "monorepo", Branch: "sparse-api-v2", Path: "/wt/mono", SessionName: "monorepo/sparse-api-v2",
			WorkflowStatus: config.StatusBlocked, WorkflowStatusLabel: "Blocked",
			RexSessionID: "session:mono", AgentState: core.AgentIdle,
			Windows: []core.Window{
				{ID: "window:m1", Label: "api", Active: true},
				{ID: "window:m2", Label: "logs"},
				{ID: "window:m3", Label: "shell"},
			},
		},
		{
			Project: ".ai", Branch: "main", Path: "/wt/ai", SessionName: ".ai/main",
			WorkflowStatus: config.StatusReady, WorkflowStatusLabel: "Ready",
			AgentState: core.AgentStopped,
		},
	}
}

func buildFor(t *testing.T, workstreams []core.Workstream, expanded, forced map[string]bool, query string) BuildResult {
	t.Helper()
	return BuildRows(BuildOptions{
		Workstreams: workstreams,
		Statuses:    config.DefaultStatuses(),
		Expanded:    expanded,
		Forced:      forced,
		Query:       query,
	})
}

func rowByKey(rows []Row, key RowKey) (Row, bool) {
	for _, row := range rows {
		if row.Key == key {
			return row, true
		}
	}
	return Row{}, false
}

func TestBuildRowsGroupsAndExpansion(t *testing.T) {
	workstreams := sampleWorkstreams()
	got := buildFor(t, workstreams, nil, nil, "")
	if got.Workstreams != 4 || got.Live != 3 || got.Blocked != 2 {
		t.Errorf("counts = %+v", got)
	}
	// Three group headers plus four workstreams; no children are expanded.
	if len(got.Rows) != 7 {
		t.Fatalf("got %d rows: %#v", len(got.Rows), got.Rows)
	}
	if got.Rows[0].Kind != RowGroup || got.Rows[0].GroupLabel != "IN PROGRESS" || got.Rows[0].GroupCount != "(2)" {
		t.Errorf("first row = %#v", got.Rows[0])
	}
	// BuildRows preserves the workstream order it is given.
	if got.Rows[1].Project != "rexws" || got.Rows[2].Project != "do-si-do" {
		t.Errorf("workstream order = %q, %q", got.Rows[1].Project, got.Rows[2].Project)
	}

	expanded := buildFor(t, workstreams, map[string]bool{"/wt/rexws": true}, nil, "")
	var windows []Row
	for _, row := range expanded.Rows {
		if row.Kind == RowWindow {
			windows = append(windows, row)
		}
	}
	if len(windows) != 2 || windows[0].WindowLabel != "agent" || windows[1].WindowLabel != "tests" {
		t.Fatalf("windows = %#v", windows)
	}
	if !windows[0].WindowActive || windows[1].WindowActive {
		t.Errorf("active markers = %v, %v", windows[0].WindowActive, windows[1].WindowActive)
	}
	if windows[0].WindowApp != "claude-code" {
		t.Errorf("window app = %q", windows[0].WindowApp)
	}
	if windows[1].Badge != "blocked (permission)" {
		t.Errorf("window badge = %q", windows[1].Badge)
	}
}

func TestBuildRowsParentOnlyMatch(t *testing.T) {
	// "rexws" matches the project of one workstream only.
	got := buildFor(t, sampleWorkstreams(), nil, nil, "rexws")
	if got.Workstreams != 1 {
		t.Fatalf("workstreams = %d, rows = %#v", got.Workstreams, got.Rows)
	}
	// Group header + parent only; parent-only matches do not auto-expand.
	if len(got.Rows) != 2 || got.Rows[1].Kind != RowWorkstream || got.Rows[1].Expanded {
		t.Fatalf("rows = %#v", got.Rows)
	}
	parent := got.Rows[1]
	if !parent.Matched || parent.ContextMatch || parent.WindowCount != "2 tabs" {
		t.Errorf("parent = %#v", parent)
	}
	if len(parent.ProjectSpans) != 1 || parent.ProjectSpans[0] != (Span{0, 5}) {
		t.Errorf("project spans = %#v", parent.ProjectSpans)
	}

	// When expanded, a parent-only match shows all its children.
	expanded := buildFor(t, sampleWorkstreams(), map[string]bool{"/wt/rexws": true}, nil, "rexws")
	windows := 0
	for _, row := range expanded.Rows {
		if row.Kind == RowWindow {
			windows++
		}
	}
	if windows != 2 {
		t.Errorf("parent-only match showed %d windows, want 2", windows)
	}
}

func TestBuildRowsChildOnlyMatchIsContext(t *testing.T) {
	// "tests" matches only a window label on the rexws workstream.
	got := buildFor(t, sampleWorkstreams(), nil, nil, "tests")
	if !got.Forced["/wt/rexws"] {
		t.Error("expected the rexws parent to be forced")
	}
	var windows []Row
	for _, row := range got.Rows {
		if row.Kind == RowWindow {
			windows = append(windows, row)
		}
	}
	if len(windows) != 1 || windows[0].WindowLabel != "tests" {
		t.Fatalf("windows = %#v", windows)
	}
	parent, ok := rowByKey(got.Rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !ok || !parent.ContextMatch || !parent.Expanded || parent.WindowCount != "1/2 tabs" {
		t.Errorf("context parent = %#v", parent)
	}
}

func TestBuildRowsParentAndChildBothMatch(t *testing.T) {
	// "rex" matches project "rexws" and window label "agent"? No. Use "tests":
	// add a workstream whose project matches and whose child matches.
	workstreams := sampleWorkstreams()
	workstreams[0].Project = "tests"
	got := buildFor(t, workstreams, nil, nil, "tests")
	var windows []Row
	for _, row := range got.Rows {
		if row.Kind == RowWindow {
			windows = append(windows, row)
		}
	}
	// Rule 2 shows all children of the matching parent, even non-matching ones.
	if len(windows) != 2 {
		t.Fatalf("windows = %#v", windows)
	}
	if windows[0].WindowMatched || !windows[1].WindowMatched {
		t.Errorf("child match flags = %v, %v", windows[0].WindowMatched, windows[1].WindowMatched)
	}
	parent, ok := rowByKey(got.Rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !ok || !parent.Matched || parent.ContextMatch || !parent.Expanded {
		t.Errorf("parent = %#v", parent)
	}
}

func TestBuildRowsGroupMatch(t *testing.T) {
	got := buildFor(t, sampleWorkstreams(), nil, nil, "blocked")
	// The BLOCKED group label matches; its workstream is visible. The agent
	// state "blocked" also matches per-agent in IN PROGRESS.
	if got.Workstreams != 3 {
		t.Fatalf("workstreams = %d, rows = %#v", got.Workstreams, got.Rows)
	}
	var group *Row
	for i := range got.Rows {
		if got.Rows[i].Kind == RowGroup && got.Rows[i].GroupID == config.StatusBlocked {
			group = &got.Rows[i]
		}
	}
	if group == nil || len(group.GroupSpans) == 0 {
		t.Fatalf("blocked group = %#v", group)
	}
	if group.GroupCount != "(1/1)" {
		t.Errorf("group count = %q", group.GroupCount)
	}
}

func TestBuildRowsPerAgentStateMatch(t *testing.T) {
	// The rexws aggregate is blocked (permission), but one agent is working.
	// Search must match the per-agent state, not only the aggregate.
	got := buildFor(t, sampleWorkstreams(), nil, nil, "working")
	if got.Workstreams != 1 {
		t.Fatalf("workstreams = %d, rows = %#v", got.Workstreams, got.Rows)
	}
	parent, ok := rowByKey(got.Rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !ok || !parent.Matched {
		t.Errorf("parent = %#v", parent)
	}
}

func TestBuildRowsStickyForcedExpansion(t *testing.T) {
	first := buildFor(t, sampleWorkstreams(), nil, nil, "tests")
	if !first.Forced["/wt/rexws"] {
		t.Fatal("tests should force the rexws parent")
	}
	// A later query that matches only the parent still uses the sticky set.
	second := buildFor(t, sampleWorkstreams(), nil, first.Forced, "rexws")
	parent, ok := rowByKey(second.Rows, RowKey{Kind: RowWorkstream, ID: "/wt/rexws"})
	if !ok || !parent.Expanded || parent.ContextMatch {
		t.Errorf("parent after query change = %#v", parent)
	}
	windows := 0
	for _, row := range second.Rows {
		if row.Kind == RowWindow {
			windows++
		}
	}
	// The parent now matches itself; the sticky forced set keeps it open and
	// shows all children.
	if windows != 2 {
		t.Errorf("windows after sticky query change = %d, want 2", windows)
	}
}

func TestBuildRowsHiddenGroupsAndCounts(t *testing.T) {
	got := buildFor(t, sampleWorkstreams(), nil, nil, "sparse")
	if got.Workstreams != 1 || got.Live != 1 {
		t.Errorf("counts = %+v", got)
	}
	for _, row := range got.Rows {
		if row.Kind == RowGroup && row.GroupID != config.StatusBlocked {
			t.Errorf("unexpected group %q", row.GroupID)
		}
	}
	if got.Rows[0].GroupCount != "(1/1)" {
		t.Errorf("count = %q", got.Rows[0].GroupCount)
	}
}

func TestBuildRowsDuplicateWindowLabels(t *testing.T) {
	ws := core.Workstream{
		Project: "repo", Branch: "main", Path: "/wt/dup", SessionName: "repo/main",
		WorkflowStatus: config.StatusReady,
		Windows: []core.Window{
			{ID: "window:aaaaaa76bc89", Label: "dup"},
			{ID: "window:bbbbbb111111", Label: "dup"},
			{ID: "window:cccccc222222", Label: "unique"},
		},
	}
	got := buildFor(t, []core.Workstream{ws}, map[string]bool{"/wt/dup": true}, nil, "")
	var labels []string
	for _, row := range got.Rows {
		if row.Kind == RowWindow {
			labels = append(labels, row.WindowLabel)
		}
	}
	want := []string{"dup (76bc89)", "dup (111111)", "unique"}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %#v, want %#v", labels, want)
	}
}

func TestMatchSpans(t *testing.T) {
	tests := []struct {
		text  string
		query string
		want  []Span
	}{
		{"working", "work", []Span{{0, 4}}},
		{"WORKING", "work", []Span{{0, 4}}},
		{"aXbXc", "x", []Span{{1, 2}, {3, 4}}},
		{"abc", "z", nil},
		{"abc", "", nil},
		{"ab", "abc", nil},
		{"café", "CAFÉ", []Span{{0, 4}}},
	}
	for _, test := range tests {
		if got := matchSpans(test.text, test.query); !reflect.DeepEqual(got, test.want) {
			t.Errorf("matchSpans(%q, %q) = %#v, want %#v", test.text, test.query, got, test.want)
		}
	}
}

func TestSilentWindowShowsDashes(t *testing.T) {
	ws := core.Workstream{
		Project: "repo", Branch: "main", Path: "/wt/silent", SessionName: "repo/main",
		WorkflowStatus: config.StatusReady, RexSessionID: "session:silent", AgentState: core.AgentIdle,
		Windows: []core.Window{{ID: "window:1", Label: "shell", Active: true, FocusedBlockID: "block:shell"}},
	}
	got := buildFor(t, []core.Workstream{ws}, map[string]bool{"/wt/silent": true}, nil, "")
	window, ok := rowByKey(got.Rows, RowKey{Kind: RowWindow, ID: "window:1"})
	if !ok {
		t.Fatalf("rows = %#v", got.Rows)
	}
	if window.WindowApp != "—" || window.Badge != "—" {
		t.Errorf("silent window = app %q badge %q", window.WindowApp, window.Badge)
	}

	// A live session with no windows has no chevron and no window count.
	nowin := core.Workstream{
		Project: "repo", Branch: "empty", Path: "/wt/empty", SessionName: "repo/empty",
		WorkflowStatus: config.StatusReady, RexSessionID: "session:empty",
	}
	got = buildFor(t, []core.Workstream{nowin}, nil, nil, "")
	row, ok := rowByKey(got.Rows, RowKey{Kind: RowWorkstream, ID: "/wt/empty"})
	if !ok || row.HasWindows || row.WindowCount != "" {
		t.Errorf("windowless workstream = %#v", row)
	}
}

func TestBuildRowsSanitizesDisplayStrings(t *testing.T) {
	ws := core.Workstream{
		Project: "repo\x1b[31m", Branch: "ma\tin", Path: "/wt/bad", SessionName: "repo/ma\tin",
		WorkflowStatus: config.StatusReady,
		Windows: []core.Window{
			{ID: "window:1", Label: "bad\nlabel", FocusedBlockID: "block:1"},
		},
		Agents: []core.Agent{
			{BlockID: "block:1", WindowID: "window:1", App: "cli\x00ent", State: core.AgentBlocked, Detail: "perm\x1b[0m"},
		},
	}
	got := buildFor(t, []core.Workstream{ws}, map[string]bool{"/wt/bad": true}, nil, "")
	for _, row := range got.Rows {
		for _, value := range []string{row.GroupLabel, row.Project, row.Branch, row.WindowLabel, row.WindowApp, row.Badge} {
			for _, r := range value {
				if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
					t.Fatalf("control character %q in %#v", r, row)
				}
			}
		}
	}
}

func TestBuildRowsIgnoresUnknownStatusGroups(t *testing.T) {
	ws := core.Workstream{Project: "repo", Branch: "main", Path: "/wt/x", WorkflowStatus: "weird"}
	got := buildFor(t, []core.Workstream{ws}, nil, nil, "")
	if len(got.Rows) != 0 {
		t.Errorf("rows = %#v", got.Rows)
	}
}
