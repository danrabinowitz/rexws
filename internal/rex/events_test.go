package rex

import (
	"reflect"
	"strings"
	"testing"
)

// The fixtures below are copied from a live `rex events --json` stream
// (rex 0.1.0-preview.25) and the schema that `rex block inspect --json`
// publishes for program_status_changed and program_status_removed.
const (
	sessionCreatedLine = `{"type":"session_created","payload":{"session":{"session_id":"session:01a21681-4673-7f1f-943f-e819684bb363","label":"rexws-probe"}}}`

	sessionDestroyedLine = `{"type":"session_destroyed","payload":{"session_id":"session:01a21681-4673-7f1f-943f-e819684bb363"}}`

	statusChangedLine = `{"type":"session_event","payload":{"session_id":"session:01a21673-4bb3-7743-8035-e66865c056d5","event":{"type":"block_event","payload":{"block_id":"block:01a21673-4bb3-7877-a6e2-3a9831150978","creator_name":"com.superlogical.terminal","name":"program_status_changed","payload":{"id":"","record":{"state":"working","app":"claude-code","effective_app":"claude-code","msg":"Writing internal/config/state.go","updated_at":"2026-11-25T23:21:25.429572-07:00","owner":{"pid":15821,"name":"2.1.332"}},"reason":"report"}}}}}`

	statusRemovedLine = `{"type":"session_event","payload":{"session_id":"session:s1","event":{"type":"block_event","payload":{"block_id":"block:b1","creator_name":"com.superlogical.terminal","name":"program_status_removed","payload":{"ids":["","build"],"reason":"prompt"}}}}}`

	titleChangedLine = `{"type":"session_event","payload":{"session_id":"session:s1","event":{"type":"block_event","payload":{"block_id":"block:b1","creator_name":"com.superlogical.terminal","name":"title_changed","payload":{"title":"/tmp","owner":{"pid":1,"name":"zsh"}}}}}}`

	clientConnectedLine = `{"type":"session_event","payload":{"session_id":"session:s1","event":{"type":"client_connected","payload":{"client_id":"client:c1"}}}}`

	// Window and view fixtures below are copied from a scratch session probed
	// with rex 0.1.0-preview.25 (rex window new/rename/focus/close).
	sessionViewChangedLine = `{"type":"session_event","payload":{"session_id":"session:s1","event":{"type":"session_view_changed","payload":{"revision":3}}}}`

	activeWindowChangedLine = `{"type":"session_event","payload":{"session_id":"session:s1","event":{"type":"active_window_changed","payload":{"window_id":"window:w2"}}}}`

	windowCreatedLine = `{"type":"session_event","payload":{"session_id":"session:s1","event":{"type":"window_created","payload":{"window_id":"window:w2","label":"probe-two"}}}}`

	windowClosedLine = `{"type":"session_event","payload":{"session_id":"session:s1","event":{"type":"window_closed","payload":{"window_id":"window:w2"}}}}`

	windowLabelChangedLine = `{"type":"session_event","payload":{"session_id":"session:s1","event":{"type":"window_label_changed","payload":{"window_id":"window:w2","label":"renamed-two"}}}}`
)

func TestParseEventSessionLifecycle(t *testing.T) {
	event, ok, err := ParseEvent([]byte(sessionCreatedLine))
	if err != nil || !ok {
		t.Fatalf("session_created: ok=%v err=%v", ok, err)
	}
	if event.Type != EventSessionCreated || event.SessionID != "session:01a21681-4673-7f1f-943f-e819684bb363" || event.Label != "rexws-probe" {
		t.Errorf("session_created = %#v", event)
	}

	event, ok, err = ParseEvent([]byte(sessionDestroyedLine))
	if err != nil || !ok {
		t.Fatalf("session_destroyed: ok=%v err=%v", ok, err)
	}
	if event.Type != EventSessionDestroyed || event.SessionID != "session:01a21681-4673-7f1f-943f-e819684bb363" {
		t.Errorf("session_destroyed = %#v", event)
	}
}

func TestParseEventProgramStatusChanged(t *testing.T) {
	event, ok, err := ParseEvent([]byte(statusChangedLine))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if event.Type != EventProgramStatusChanged {
		t.Errorf("type = %q", event.Type)
	}
	if event.SessionID != "session:01a21673-4bb3-7743-8035-e66865c056d5" || event.BlockID != "block:01a21673-4bb3-7877-a6e2-3a9831150978" {
		t.Errorf("target = %#v", event)
	}
	if event.Reason != "report" {
		t.Errorf("reason = %q", event.Reason)
	}
	record := event.Record
	if record == nil {
		t.Fatal("record is nil")
	}
	if record.State != "working" || record.App != "claude-code" || record.EffectiveApp != "claude-code" {
		t.Errorf("record = %#v", record)
	}
	if record.Owner == nil || record.Owner.PID != 15821 || record.Owner.Name != "2.1.332" {
		t.Errorf("owner = %#v", record.Owner)
	}
	if record.UpdatedAt == "" {
		t.Error("updated_at is empty")
	}
}

func TestParseEventProgramStatusRemoved(t *testing.T) {
	event, ok, err := ParseEvent([]byte(statusRemovedLine))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if event.Type != EventProgramStatusRemoved || event.Reason != "prompt" {
		t.Errorf("event = %#v", event)
	}
	if len(event.RemovedIDs) != 2 || event.RemovedIDs[0] != "" || event.RemovedIDs[1] != "build" {
		t.Errorf("removed ids = %#v", event.RemovedIDs)
	}
}

func TestParseEventWindowAndViewEvents(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Event
	}{
		{"view changed", sessionViewChangedLine, Event{Type: EventSessionViewChanged, SessionID: "session:s1", Revision: 3}},
		{"active window", activeWindowChangedLine, Event{Type: EventActiveWindowChanged, SessionID: "session:s1", WindowID: "window:w2"}},
		{"window created", windowCreatedLine, Event{Type: EventWindowCreated, SessionID: "session:s1", WindowID: "window:w2", WindowLabel: "probe-two"}},
		{"window closed", windowClosedLine, Event{Type: EventWindowClosed, SessionID: "session:s1", WindowID: "window:w2"}},
		{"window label changed", windowLabelChangedLine, Event{Type: EventWindowLabelChanged, SessionID: "session:s1", WindowID: "window:w2", WindowLabel: "renamed-two"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, ok, err := ParseEvent([]byte(test.line))
			if err != nil || !ok {
				t.Fatalf("ok=%v err=%v", ok, err)
			}
			if !reflect.DeepEqual(event, test.want) {
				t.Errorf("event = %#v, want %#v", event, test.want)
			}
		})
	}
}

func TestParseEventRejectsMalformedWindowEvents(t *testing.T) {
	for name, line := range map[string]string{
		"created without id": `{"type":"session_event","payload":{"session_id":"s","event":{"type":"window_created","payload":{"label":"x"}}}}`,
		"closed without id":  `{"type":"session_event","payload":{"session_id":"s","event":{"type":"window_closed","payload":{}}}}`,
		"label without id":   `{"type":"session_event","payload":{"session_id":"s","event":{"type":"window_label_changed","payload":{"label":"x"}}}}`,
		"active window bad":  `{"type":"session_event","payload":{"session_id":"s","event":{"type":"active_window_changed","payload":[]}}}`,
		"view changed bad":   `{"type":"session_event","payload":{"session_id":"s","event":{"type":"session_view_changed","payload":[]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok, err := ParseEvent([]byte(line)); err == nil {
				t.Errorf("expected an error, ok=%v", ok)
			}
		})
	}
}

func TestParseEventIgnoresUnusedEvents(t *testing.T) {
	for name, line := range map[string]string{
		"title_changed":    titleChangedLine,
		"client_connected": clientConnectedLine,
		"empty":            `{"type":"server_connected","payload":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if event, ok, err := ParseEvent([]byte(line)); err != nil || ok {
				t.Errorf("ok=%v err=%v event=%#v", ok, err, event)
			}
		})
	}
}

func TestParseEventIgnoresOtherBlockCreators(t *testing.T) {
	line := strings.Replace(statusChangedLine, TerminalCreator, "com.example.other", 1)
	if _, ok, err := ParseEvent([]byte(line)); err != nil || ok {
		t.Errorf("ok=%v err=%v", ok, err)
	}
}

func TestParseEventRejectsMalformedInput(t *testing.T) {
	for name, line := range map[string]string{
		"invalid json":           `{`,
		"no session id":          `{"type":"session_created","payload":{"session":{"label":"x"}}}`,
		"no destroyed session":   `{"type":"session_destroyed","payload":{}}`,
		"status without state":   `{"type":"session_event","payload":{"session_id":"s","event":{"type":"block_event","payload":{"creator_name":"com.superlogical.terminal","name":"program_status_changed","payload":{"record":{"updated_at":"x"}}}}}}`,
		"removed without ids":    `{"type":"session_event","payload":{"session_id":"s","event":{"type":"block_event","payload":{"creator_name":"com.superlogical.terminal","name":"program_status_removed","payload":{"reason":"prompt"}}}}}`,
		"session_event bad host": `{"type":"session_event","payload":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok, err := ParseEvent([]byte(line)); err == nil {
				t.Errorf("expected an error, ok=%v", ok)
			}
		})
	}
}
