package rex

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	if got := New("").Bin; got != "rex" {
		t.Errorf("New(\"\").Bin = %q, want rex", got)
	}
	if got := New("/opt/rex").Bin; got != "/opt/rex" {
		t.Errorf("New(path).Bin = %q", got)
	}
}

func TestChooseRecord(t *testing.T) {
	tests := []struct {
		name    string
		records []StatusRecord
		ok      bool
		state   string
		detail  string
	}{
		{"empty", nil, false, "", ""},
		{"working beats idle", []StatusRecord{{State: "idle"}, {State: "working"}}, true, "working", ""},
		{"blocked beats working", []StatusRecord{{State: "working"}, {State: "blocked", Kind: "permission"}}, true, "blocked", "permission"},
		{"error beats working", []StatusRecord{{State: "working"}, {State: "error"}}, true, "error", ""},
		{"done beats idle", []StatusRecord{{State: "idle"}, {State: "done"}}, true, "done", ""},
		{"unknown state is idle", []StatusRecord{{State: "teleporting"}}, true, "idle", ""},
		{"unknown then working", []StatusRecord{{State: "teleporting"}, {State: "working"}}, true, "working", ""},
		{"later blocked kind fills in", []StatusRecord{{State: "blocked"}, {State: "blocked", Kind: "permission"}}, true, "blocked", "permission"},
		{"first blocked kind wins", []StatusRecord{{State: "blocked", Kind: "question"}, {State: "blocked", Kind: "permission"}}, true, "blocked", "question"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record, ok := chooseRecord(test.records)
			if ok != test.ok {
				t.Fatalf("ok = %v, want %v", ok, test.ok)
			}
			if !ok {
				return
			}
			if record.State != test.state || record.Kind != test.detail {
				t.Errorf("record = (%q, %q), want (%q, %q)", record.State, record.Kind, test.state, test.detail)
			}
		})
	}
}

func TestParseBlocks(t *testing.T) {
	data := []byte(`{"blocks":[
		{"block_id":"block:1","label":"shell","creator_name":"com.superlogical.terminal","flavor":"com.superlogical.terminal.shell","window_id":"window:1"},
		{"block_id":"block:2","label":"logs","creator_name":"com.example.other","window_id":"window:2"}
	]}`)
	blocks, err := parseBlocks(data)
	if err != nil {
		t.Fatalf("parseBlocks: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("got %d blocks", len(blocks))
	}
	if blocks[0].ID != "block:1" || blocks[0].Label != "shell" || blocks[0].WindowID != "window:1" || blocks[0].CreatorName != TerminalCreator {
		t.Errorf("first block = %#v", blocks[0])
	}
	if blocks[1].ID != "block:2" || blocks[1].Label != "logs" || blocks[1].WindowID != "window:2" {
		t.Errorf("second block = %#v", blocks[1])
	}
}

func TestParseWindows(t *testing.T) {
	data := []byte(`{
		"active_window_id": "window:2",
		"windows": [
			{
				"window_id": "window:1",
				"label": "api",
				"focused_block_id": "block:1",
				"layers": [{"kind": "tiled", "blocks": [{"block_id": "block:1"}]}]
			},
			{
				"window_id": "window:2",
				"label": "tests",
				"focused_block_id": "block:2",
				"layers": [{"kind": "tiled", "blocks": [{"block_id": "block:2"}, {"block_id": "block:2"}]}]
			}
		]
	}`)
	windows, err := parseWindows(data)
	if err != nil {
		t.Fatalf("parseWindows: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("got %d windows", len(windows))
	}
	first := windows[0]
	if first.ID != "window:1" || first.Label != "api" || first.Active || first.FocusedBlockID != "block:1" {
		t.Errorf("first window = %#v", first)
	}
	if len(first.BlockIDs) != 1 || first.BlockIDs[0] != "block:1" {
		t.Errorf("first block ids = %#v", first.BlockIDs)
	}
	second := windows[1]
	if second.ID != "window:2" || second.Label != "tests" || !second.Active {
		t.Errorf("second window = %#v", second)
	}
	if len(second.BlockIDs) != 1 || second.BlockIDs[0] != "block:2" {
		t.Errorf("second block ids were not deduplicated: %#v", second.BlockIDs)
	}
}

func TestResolveWindow(t *testing.T) {
	windows := []Window{
		{ID: "window:01a216bd-0f30-71c1-8ebe-82c7fea10d6f", Label: "Window fea10d6f"},
		{ID: "window:01a216bd-0f5f-7052-bcca-d7c3d33c4c33", Label: "dup"},
		{ID: "window:01a216bd-0f8a-7802-936a-569124bf5ef7", Label: "dup"},
		{ID: "window:01a216bd-0f9b-7000-aaaa-111111111111", Label: "tests"},
	}

	tests := []struct {
		name     string
		selector string
		wantID   string
		wantErr  string
	}{
		{"exact id", "window:01a216bd-0f9b-7000-aaaa-111111111111", "window:01a216bd-0f9b-7000-aaaa-111111111111", ""},
		{"unique label", "tests", "window:01a216bd-0f9b-7000-aaaa-111111111111", ""},
		{"unique id suffix", "5ef7", "window:01a216bd-0f8a-7802-936a-569124bf5ef7", ""},
		{"duplicate label", "dup", "", "matches 2 windows"},
		{"unknown", "logs", "", "not found"},
		{"empty", "", "", "selector is empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			window, err := resolveWindow(windows, test.selector)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("err = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveWindow: %v", err)
			}
			if window.ID != test.wantID {
				t.Errorf("window = %#v, want ID %q", window, test.wantID)
			}
		})
	}
}

func TestResolveWindowRejectsAmbiguousSuffix(t *testing.T) {
	windows := []Window{
		{ID: "window:aaaa-1111", Label: "one"},
		{ID: "window:bbbb-1111", Label: "two"},
	}
	if _, err := resolveWindow(windows, "1111"); err == nil || !strings.Contains(err.Error(), "matches 2 windows") {
		t.Fatalf("err = %v, want an ambiguity error", err)
	}
}

func TestParseHelpersRejectMalformedJSON(t *testing.T) {
	if _, err := parseBlocks([]byte("{")); err == nil {
		t.Error("parseBlocks accepted invalid JSON")
	}
	if _, err := parseWindows([]byte("{")); err == nil {
		t.Error("parseWindows accepted invalid JSON")
	}
	if _, err := parseWindows([]byte(`{"windows":[{"label":"no id"}]}`)); err == nil {
		t.Error("parseWindows accepted a window without an ID")
	}
}
