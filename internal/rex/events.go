package rex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
)

// Event is a normalized Rex event.
type Event struct {
	Type       EventType
	SessionID  string
	Label      string // session_created only
	BlockID    string // program status events only
	Record     *StatusRecord
	RemovedIDs []string // program_status_removed only
	Reason     string
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
		if session.Event.Type != "block_event" {
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
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("rex was not found on PATH")
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
