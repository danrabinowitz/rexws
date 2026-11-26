package rex

import "testing"

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
	data := []byte(`{"windows":[
		{"window_id":"window:1","label":"api"},
		{"window_id":"window:2","label":"tests"}
	]}`)
	labels, err := parseWindows(data)
	if err != nil {
		t.Fatalf("parseWindows: %v", err)
	}
	if labels["window:1"] != "api" || labels["window:2"] != "tests" {
		t.Errorf("labels = %#v", labels)
	}
}

func TestParseHelpersRejectMalformedJSON(t *testing.T) {
	if _, err := parseBlocks([]byte("{")); err == nil {
		t.Error("parseBlocks accepted invalid JSON")
	}
	if _, err := parseWindows([]byte("{")); err == nil {
		t.Error("parseWindows accepted invalid JSON")
	}
}
