package tui

import (
	"context"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/danrabinowitz/rexws/internal/rex"
)

// streamEventMsg is one normalized Rex event from any stream.
type streamEventMsg struct {
	event rex.Event
}

// streamStateMsg reports a stream connecting or dropping. An empty SessionID
// means the server-wide lifecycle stream.
type streamStateMsg struct {
	sessionID string
	connected bool
	err       error
}

// streamHub owns the server-wide and per-session `rex events --json` streams.
// Streams push messages into a buffered channel; a slow model cannot block the
// event scanner indefinitely.
type streamHub struct {
	mu       sync.Mutex
	bin      string
	events   chan tea.Msg
	started  bool
	root     context.Context
	cancel   context.CancelFunc
	sessions map[string]context.CancelFunc
}

func newStreamHub(bin string) *streamHub {
	return &streamHub{
		bin:      bin,
		events:   make(chan tea.Msg, 256),
		sessions: map[string]context.CancelFunc{},
	}
}

// SetBin updates the Rex binary; a changed value restarts the streams.
func (h *streamHub) SetBin(bin string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.bin == bin {
		return
	}
	h.bin = bin
	if h.started {
		h.restartLocked()
	}
}

// Start begins the server-wide stream.
func (h *streamHub) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.started {
		return
	}
	h.started = true
	h.restartLocked()
}

// Reconnect drops every stream and starts the server-wide stream again. The
// next session sync restarts the per-session streams.
func (h *streamHub) Reconnect() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.started {
		h.started = true
	}
	h.restartLocked()
}

func (h *streamHub) restartLocked() {
	if h.cancel != nil {
		h.cancel()
	}
	for _, cancel := range h.sessions {
		cancel()
	}
	h.sessions = map[string]context.CancelFunc{}
	h.root, h.cancel = context.WithCancel(context.Background())
	go h.run(h.root, "")
}

// SyncSessions starts streams for new live sessions and stops streams for
// sessions that disappeared.
func (h *streamHub) SyncSessions(sessionIDs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.started {
		return
	}
	wanted := make(map[string]bool, len(sessionIDs))
	for _, id := range sessionIDs {
		wanted[id] = true
	}
	for id, cancel := range h.sessions {
		if !wanted[id] {
			cancel()
			delete(h.sessions, id)
		}
	}
	for id := range wanted {
		if _, ok := h.sessions[id]; ok {
			continue
		}
		sessionID := id
		ctx, cancel := context.WithCancel(h.root)
		h.sessions[sessionID] = cancel
		go h.run(ctx, sessionID)
	}
}

// Close stops every stream.
func (h *streamHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	h.sessions = map[string]context.CancelFunc{}
}

// run keeps one Rex event stream alive with exponential backoff, reporting
// connect/drop transitions into the hub channel.
func (h *streamHub) run(ctx context.Context, sessionID string) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		client := rex.New(h.binValue())
		h.send(ctx, streamStateMsg{sessionID: sessionID, connected: true})
		err := client.Stream(ctx, sessionID, func(event rex.Event) {
			h.send(ctx, streamEventMsg{event: event})
		})
		if ctx.Err() != nil {
			return
		}
		h.send(ctx, streamStateMsg{sessionID: sessionID, connected: false, err: err})
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (h *streamHub) binValue() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bin
}

func (h *streamHub) send(ctx context.Context, msg tea.Msg) {
	select {
	case h.events <- msg:
	case <-ctx.Done():
	}
}

// waitForStream reads one hub message, then the model re-issues it.
func waitForStream(hub *streamHub) tea.Cmd {
	return func() tea.Msg {
		return <-hub.events
	}
}
