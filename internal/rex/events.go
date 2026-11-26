package rex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"strings"
)

// EventType is the subset of Rex events rexws consumes.
type EventType string

const (
	EventSessionCreated       EventType = "session_created"
	EventSessionDestroyed     EventType = "session_destroyed"
	EventProgramStatusChanged EventType = "program_status_changed"
	EventProgramStatusRemoved EventType = "program_status_removed"
	EventSessionViewChanged   EventType = "session_view_changed"
	EventActiveWindowChanged  EventType = "active_window_changed"
	EventWindowCreated        EventType = "window_created"
	EventWindowClosed         EventType = "window_closed"
	EventWindowLabelChanged   EventType = "window_label_changed"
)

// Event is a normalized Rex event.
type Event struct {
	Type        EventType
	SessionID   string
	Label       string // session_created only
	BlockID     string // program status events only
	WindowID    string // window events and active_window_changed
	WindowLabel string // window_created and window_label_changed
	Revision    int    // session_view_changed only
	Record      *StatusRecord
	RemovedIDs  []string // program_status_removed only
	Reason      string
}

type rawEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type rawSessionEvent struct {
	SessionID string   `json:"session_id"`
	Event     rawEvent `json:"event"`
}

type rawBlockEvent struct {
	BlockID     string          `json:"block_id"`
	CreatorName string          `json:"creator_name"`
	Name        string          `json:"name"`
	Payload     json.RawMessage `json:"payload"`
}

// ParseEvent parses one newline-delimited `rex events --json` object. It
// returns ok=false for events rexws does not use.
func ParseEvent(line []byte) (event Event, ok bool, err error) {
	var raw rawEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return Event{}, false, fmt.Errorf("parse rex event: %w", err)
	}

	switch raw.Type {
	case "session_created":
		var payload struct {
			Session Session `json:"session"`
		}
		if err := json.Unmarshal(raw.Payload, &payload); err != nil {
			return Event{}, false, fmt.Errorf("parse session_created: %w", err)
		}
		if payload.Session.ID == "" {
			return Event{}, false, errors.New("session_created has no session_id")
		}
		return Event{Type: EventSessionCreated, SessionID: payload.Session.ID, Label: payload.Session.Label}, true, nil

	case "session_destroyed":
		var payload struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(raw.Payload, &payload); err != nil {
			return Event{}, false, fmt.Errorf("parse session_destroyed: %w", err)
		}
		if payload.SessionID == "" {
			return Event{}, false, errors.New("session_destroyed has no session_id")
		}
		return Event{Type: EventSessionDestroyed, SessionID: payload.SessionID}, true, nil

	case "session_event":
		var session rawSessionEvent
		if err := json.Unmarshal(raw.Payload, &session); err != nil {
			return Event{}, false, fmt.Errorf("parse session_event: %w", err)
		}
		switch session.Event.Type {
		case string(EventSessionViewChanged):
			var payload struct {
				Revision int `json:"revision"`
			}
			if err := json.Unmarshal(session.Event.Payload, &payload); err != nil {
				return Event{}, false, fmt.Errorf("parse session_view_changed: %w", err)
			}
			return Event{Type: EventSessionViewChanged, SessionID: session.SessionID, Revision: payload.Revision}, true, nil

		case string(EventActiveWindowChanged):
			var payload struct {
				WindowID string `json:"window_id"`
			}
			if err := json.Unmarshal(session.Event.Payload, &payload); err != nil {
				return Event{}, false, fmt.Errorf("parse active_window_changed: %w", err)
			}
			return Event{Type: EventActiveWindowChanged, SessionID: session.SessionID, WindowID: payload.WindowID}, true, nil

		case string(EventWindowCreated), string(EventWindowLabelChanged):
			var payload struct {
				WindowID string `json:"window_id"`
				Label    string `json:"label"`
			}
			if err := json.Unmarshal(session.Event.Payload, &payload); err != nil {
				return Event{}, false, fmt.Errorf("parse %s: %w", session.Event.Type, err)
			}
			if payload.WindowID == "" {
				return Event{}, false, fmt.Errorf("%s has no window_id", session.Event.Type)
			}
			return Event{Type: EventType(session.Event.Type), SessionID: session.SessionID, WindowID: payload.WindowID, WindowLabel: payload.Label}, true, nil

		case string(EventWindowClosed):
			var payload struct {
				WindowID string `json:"window_id"`
			}
			if err := json.Unmarshal(session.Event.Payload, &payload); err != nil {
				return Event{}, false, fmt.Errorf("parse window_closed: %w", err)
			}
			if payload.WindowID == "" {
				return Event{}, false, errors.New("window_closed has no window_id")
			}
			return Event{Type: EventWindowClosed, SessionID: session.SessionID, WindowID: payload.WindowID}, true, nil

		case "block_event":
			// Handled below.
		default:
			return Event{}, false, nil
		}
		var block rawBlockEvent
		if err := json.Unmarshal(session.Event.Payload, &block); err != nil {
			return Event{}, false, fmt.Errorf("parse block_event: %w", err)
		}
		if block.CreatorName != TerminalCreator {
			return Event{}, false, nil
		}
		event := Event{SessionID: session.SessionID, BlockID: block.BlockID}
		switch block.Name {
		case string(EventProgramStatusChanged):
			var payload struct {
				ID     string       `json:"id"`
				Record StatusRecord `json:"record"`
				Reason string       `json:"reason"`
			}
			if err := json.Unmarshal(block.Payload, &payload); err != nil {
				return Event{}, false, fmt.Errorf("parse program_status_changed: %w", err)
			}
			if payload.Record.State == "" {
				return Event{}, false, errors.New("program_status_changed record has no state")
			}
			event.Type = EventProgramStatusChanged
			event.Record = &payload.Record
			event.Reason = payload.Reason
			return event, true, nil

		case string(EventProgramStatusRemoved):
			var payload struct {
				IDs    []string `json:"ids"`
				Reason string   `json:"reason"`
			}
			if err := json.Unmarshal(block.Payload, &payload); err != nil {
				return Event{}, false, fmt.Errorf("parse program_status_removed: %w", err)
			}
			if len(payload.IDs) == 0 {
				return Event{}, false, errors.New("program_status_removed has no ids")
			}
			event.Type = EventProgramStatusRemoved
			event.RemovedIDs = payload.IDs
			event.Reason = payload.Reason
			return event, true, nil
		}
	}
	return Event{}, false, nil
}

// Stream runs `rex events --json` and calls handle for every event rexws
// uses. With an empty sessionID it follows server-wide lifecycle events; with
// a session it also receives that session's block events. Stream returns when
// the command ends or ctx is canceled.
func (c *Client) Stream(ctx context.Context, sessionID string, handle func(Event)) error {
	if handle == nil {
		return errors.New("rex event handler is nil")
	}
	args := []string{"events", "--json"}
	if sessionID != "" {
		args = append(args, "-s", sessionID)
	}

	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("rex binary %q was not found", c.bin())
		}
		return fmt.Errorf("start rex events: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		event, ok, err := ParseEvent(line)
		if err != nil {
			cmd.Process.Kill()
			cmd.Wait()
			return err
		}
		if ok {
			handle(event)
		}
	}
	scanErr := scanner.Err()
	waitErr := cmd.Wait()

	if ctx.Err() != nil {
		return ctx.Err()
	}
	if scanErr != nil {
		return fmt.Errorf("read rex events: %w", scanErr)
	}
	if waitErr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("rex events: %w: %s", waitErr, msg)
		}
		return fmt.Errorf("rex events: %w", waitErr)
	}
	return nil
}
