// Package rex wraps the installed Rex CLI (verified against rex
// 0.1.0-preview.25). It lists sessions, reads OSC 7501 program status, and
// switches the Rex app to a session.
package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	// TerminalCreator is the block creator that exposes program_status.
	TerminalCreator = "com.superlogical.terminal"
	defaultTimeout  = 30 * time.Second
)

// Client runs the rex binary.
type Client struct {
	Bin     string
	Timeout time.Duration
}

// New returns a client that uses the rex binary on PATH.
func New() *Client {
	return &Client{Bin: "rex", Timeout: defaultTimeout}
}

// Session is one Rex session.
type Session struct {
	ID    string `json:"session_id"`
	Label string `json:"label"`
}

// StatusRecord is one OSC 7501 program status record.
type StatusRecord struct {
	ID           string       `json:"id,omitempty"`
	State        string       `json:"state"`
	Kind         string       `json:"kind,omitempty"`
	App          string       `json:"app,omitempty"`
	EffectiveApp string       `json:"effective_app,omitempty"`
	Title        string       `json:"title,omitempty"`
	Msg          string       `json:"msg,omitempty"`
	UpdatedAt    string       `json:"updated_at"`
	Owner        *StatusOwner `json:"owner,omitempty"`
}

// StatusOwner identifies the terminal's foreground process at report time.
type StatusOwner struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

// SessionStatus is a session plus the agent state read from its terminals.
type SessionStatus struct {
	Session
	State  string
	Detail string
	// Err is set when this session's agent state could not be read.
	Err error
}

// SwitchResult reports what Switch did.
type SwitchResult struct {
	SessionID string
	Created   bool
}

type block struct {
	ID          string `json:"block_id"`
	CreatorName string `json:"creator_name"`
}

// ListSessions returns live sessions via `rex session ls --json`.
func (c *Client) ListSessions(ctx context.Context) ([]Session, error) {
	out, err := c.run(ctx, "session", "ls", "--json")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Sessions []Session `json:"sessions"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("parse rex session ls: %w", err)
	}
	return payload.Sessions, nil
}

// Snapshot lists sessions and reads each one's agent state. A failure to read
// one session does not hide the others; the error is carried per session.
func (c *Client) Snapshot(ctx context.Context) ([]SessionStatus, error) {
	sessions, err := c.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SessionStatus, 0, len(sessions))
	for _, session := range sessions {
		state, detail, err := c.SessionAgent(ctx, session.ID)
		out = append(out, SessionStatus{Session: session, State: state, Detail: detail, Err: err})
	}
	return out, nil
}

// SessionAgent combines the program status of every terminal block in a
// session. A session with no reports is idle. The most urgent state wins:
// blocked, then error, working, done, and idle.
func (c *Client) SessionAgent(ctx context.Context, sessionID string) (state, detail string, err error) {
	out, err := c.run(ctx, "block", "ls", "-s", sessionID, "--json")
	if err != nil {
		return "", "", err
	}
	var payload struct {
		Blocks []block `json:"blocks"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", "", fmt.Errorf("parse rex block ls: %w", err)
	}

	var records []StatusRecord
	for _, b := range payload.Blocks {
		if b.CreatorName != TerminalCreator {
			continue
		}
		status, err := c.programStatus(ctx, sessionID, b.ID)
		if err != nil {
			return "", "", err
		}
		records = append(records, status...)
	}
	state, detail = combineRecords(records)
	return state, detail, nil
}

func (c *Client) programStatus(ctx context.Context, sessionID, blockID string) ([]StatusRecord, error) {
	// -s is required: block IDs from another session do not resolve without it.
	out, err := c.run(ctx, "block", "call", "-s", sessionID, "-b", blockID, "program_status")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Records []StatusRecord `json:"records"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("parse program_status for %s: %w", blockID, err)
	}
	return payload.Records, nil
}

var statePriority = map[string]int{
	"idle":    0,
	"done":    1,
	"working": 2,
	"error":   3,
	"blocked": 4,
}

func combineRecords(records []StatusRecord) (state, detail string) {
	best := "idle"
	bestRank := 0
	for _, record := range records {
		stateName := record.State
		rank, ok := statePriority[stateName]
		if !ok {
			// Rex documents unknown states as idle.
			stateName, rank = "idle", 0
		}
		if rank > bestRank {
			best, bestRank = stateName, rank
			detail = ""
			if stateName == "blocked" {
				detail = record.Kind
			}
		} else if rank == bestRank && stateName == "blocked" && detail == "" {
			detail = record.Kind
		}
	}
	return best, detail
}

// AppClient returns the client ID of the first connected Rex app.
func (c *Client) AppClient(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "client", "ls", "--json")
	if err != nil {
		return "", err
	}
	var payload struct {
		Clients []struct {
			ID   string `json:"client_id"`
			Info *struct {
				Kind string `json:"kind"`
			} `json:"info"`
		} `json:"clients"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return "", fmt.Errorf("parse rex client ls: %w", err)
	}
	for _, client := range payload.Clients {
		if client.Info != nil && client.Info.Kind == "app" {
			return client.ID, nil
		}
	}
	return "", errors.New("no Rex app client is connected; open Rex or pass --client")
}

// Switch selects the session labeled sessionName in the Rex app, creating it
// in dir first when it does not exist. client overrides the app client.
func (c *Client) Switch(ctx context.Context, sessionName, dir, client string) (SwitchResult, error) {
	sessions, err := c.ListSessions(ctx)
	if err != nil {
		return SwitchResult{}, err
	}

	result := SwitchResult{}
	for _, session := range sessions {
		if session.Label == sessionName {
			result.SessionID = session.ID
			break
		}
	}
	if result.SessionID == "" {
		out, err := c.run(ctx, "session", "new", sessionName, "-c", dir, "--json")
		if err != nil {
			return SwitchResult{}, err
		}
		var created struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(out, &created); err != nil {
			return SwitchResult{}, fmt.Errorf("parse rex session new: %w", err)
		}
		if created.SessionID == "" {
			return SwitchResult{}, errors.New("rex session new returned no session_id")
		}
		result.SessionID = created.SessionID
		result.Created = true
	}

	if client == "" {
		client, err = c.AppClient(ctx)
		if err != nil {
			return SwitchResult{}, err
		}
	}
	if _, err := c.run(ctx, "-C", client, "do", "session.select", "session_id="+result.SessionID); err != nil {
		if result.Created {
			return SwitchResult{}, fmt.Errorf("created session %s but could not select it: %w", result.SessionID, err)
		}
		return SwitchResult{}, err
	}
	return result, nil
}

func (c *Client) run(ctx context.Context, args ...string) ([]byte, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.bin(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		switch {
		case errors.Is(err, exec.ErrNotFound):
			return nil, errors.New("rex was not found on PATH")
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return nil, fmt.Errorf("rex %s timed out after %s", strings.Join(args, " "), timeout)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("rex %s: %w: %s", strings.Join(args, " "), err, msg)
		}
		return nil, fmt.Errorf("rex %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

func (c *Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "rex"
}
