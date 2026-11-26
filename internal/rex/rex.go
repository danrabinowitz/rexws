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
	"io/fs"
	"os/exec"
	"strings"
	"time"

	"github.com/danrabinowitz/rexws/internal/agentstate"
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

// New returns a client for bin. An empty bin means "rex" from PATH.
func New(bin string) *Client {
	if bin == "" {
		bin = "rex"
	}
	return &Client{Bin: bin, Timeout: defaultTimeout}
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

// BlockStatus is one terminal block with the status of its foreground
// program. A block with no program-status records is not an agent and is
// omitted, so silent shell panes do not appear.
type BlockStatus struct {
	BlockID      string
	BlockLabel   string
	WindowID     string
	WindowLabel  string
	State        agentstate.State
	Detail       string
	App          string
	EffectiveApp string
	Msg          string
	Title        string
	UpdatedAt    string
	// Err is set when the block's program status could not be read. State is
	// then Unknown.
	Err error
}

// SessionStatus is a session plus its reporting terminal blocks.
type SessionStatus struct {
	Session
	Blocks []BlockStatus
	// Err is set when the session's blocks could not be listed.
	Err error
}

// SwitchResult reports what Switch did.
type SwitchResult struct {
	SessionID string
	Created   bool
}

type block struct {
	ID          string `json:"block_id"`
	Label       string `json:"label"`
	WindowID    string `json:"window_id"`
	CreatorName string `json:"creator_name"`
}

type window struct {
	ID    string `json:"window_id"`
	Label string `json:"label"`
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

// Snapshot lists sessions and reads each one's agents. A failure to read one
// session does not hide the others; the error is carried per session.
func (c *Client) Snapshot(ctx context.Context) ([]SessionStatus, error) {
	sessions, err := c.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SessionStatus, 0, len(sessions))
	for _, session := range sessions {
		blocks, err := c.SessionAgents(ctx, session.ID)
		out = append(out, SessionStatus{Session: session, Blocks: blocks, Err: err})
	}
	return out, nil
}

// SessionAgents returns one entry per terminal block in the session that has
// reported a program status, or an Unknown entry when its status could not be
// read. Blocks with no reports are omitted.
func (c *Client) SessionAgents(ctx context.Context, sessionID string) ([]BlockStatus, error) {
	blocks, err := c.listBlocks(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	windows, err := c.windowLabels(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	statuses := make([]BlockStatus, 0, len(blocks))
	for _, b := range blocks {
		if b.CreatorName != TerminalCreator {
			continue
		}
		status := BlockStatus{
			BlockID:     b.ID,
			BlockLabel:  b.Label,
			WindowID:    b.WindowID,
			WindowLabel: windows[b.WindowID],
		}
		records, err := c.programStatus(ctx, sessionID, b.ID)
		if err != nil {
			status.State = agentstate.Unknown
			status.Err = err
			statuses = append(statuses, status)
			continue
		}
		record, ok := chooseRecord(records)
		if !ok {
			continue
		}
		status.State = agentstate.Normalize(record.State)
		if status.State == agentstate.Blocked {
			status.Detail = record.Kind
		}
		status.App = record.App
		status.EffectiveApp = record.EffectiveApp
		status.Msg = record.Msg
		status.Title = record.Title
		status.UpdatedAt = record.UpdatedAt
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (c *Client) listBlocks(ctx context.Context, sessionID string) ([]block, error) {
	out, err := c.run(ctx, "block", "ls", "-s", sessionID, "--json")
	if err != nil {
		return nil, err
	}
	return parseBlocks(out)
}

func (c *Client) windowLabels(ctx context.Context, sessionID string) (map[string]string, error) {
	out, err := c.run(ctx, "window", "ls", "-s", sessionID, "--json")
	if err != nil {
		return nil, err
	}
	return parseWindows(out)
}

func parseBlocks(data []byte) ([]block, error) {
	var payload struct {
		Blocks []block `json:"blocks"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse rex block ls: %w", err)
	}
	return payload.Blocks, nil
}

func parseWindows(data []byte) (map[string]string, error) {
	var payload struct {
		Windows []window `json:"windows"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse rex window ls: %w", err)
	}
	labels := make(map[string]string, len(payload.Windows))
	for _, w := range payload.Windows {
		labels[w.ID] = w.Label
	}
	return labels, nil
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

// chooseRecord picks the most urgent record in one block. Records with a state
// rexws does not know are idle. Among equally urgent blocked records, one that
// names a kind wins.
func chooseRecord(records []StatusRecord) (StatusRecord, bool) {
	if len(records) == 0 {
		return StatusRecord{}, false
	}
	best := 0
	bestState := agentstate.Normalize(records[0].State)
	bestSeverity := bestState.Severity()
	for i := 1; i < len(records); i++ {
		state := agentstate.Normalize(records[i].State)
		severity := state.Severity()
		fillsKind := state == agentstate.Blocked && bestState == agentstate.Blocked &&
			records[best].Kind == "" && records[i].Kind != ""
		if severity > bestSeverity || (severity == bestSeverity && fillsKind) {
			best = i
			bestState = state
			bestSeverity = severity
		}
	}
	chosen := records[best]
	chosen.State = string(agentstate.Normalize(chosen.State))
	return chosen, true
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
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("rex binary %q was not found", c.bin())
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
