package tui

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/danrabinowitz/rexws/internal/rex"
)

// streamEventMsg is one normalized Rex event from any stream.
type streamEventMsg struct {
	event      rex.Event
	generation int
}

// streamStateMsg reports a stream connecting or dropping. An empty SessionID
// means the server-wide lifecycle stream. A connecting message means the
// process started but has not yet survived the grace period.
type streamStateMsg struct {
	sessionID  string
	connecting bool
	connected  bool
	err        error
	generation int
}

// hubGeneration is monotonic across hubs, so replacing the hub (for example
// after a fatal load) cannot reuse a generation the model still considers
// current.
var hubGeneration atomic.Int64

// maxQueuedMessages bounds the hub's delivery queue. Coalescing keeps it small
// even during a burst; the cap is a last-resort guard so an event scan can
// never block on a slow model.
const maxQueuedMessages = 512

// streamGrace is how long a started stream process must stay up before it
// counts as connected. A process that fails immediately must never look
// healthy, even for an instant.
const streamGrace = 500 * time.Millisecond

// openStreamFunc opens one `rex events` stream. started fires only after the
// process exists. It is a seam so stream lifecycle tests do not need a real
// Rex binary.
type openStreamFunc func(ctx context.Context, bin, sessionID string, started func(), handle func(rex.Event)) error

// waitFunc waits d, returning false when ctx is canceled first.
type waitFunc func(ctx context.Context, d time.Duration) bool

// streamHub owns the server-wide and per-session `rex events --json` streams.
// Streams push messages into a bounded queue guarded by a mutex; the event
// scanner never blocks on the model.
type streamHub struct {
	mu         sync.Mutex
	bin        string
	queue      []tea.Msg
	signal     chan struct{}
	done       chan struct{}
	generation int
	started    bool
	root       context.Context
	cancel     context.CancelFunc
	sessions   map[string]context.CancelFunc

	openStream openStreamFunc
	wait       waitFunc
	grace      time.Duration
}

func newStreamHub(bin string) *streamHub {
	return &streamHub{
		bin:        bin,
		signal:     make(chan struct{}, 1),
		done:       make(chan struct{}),
		sessions:   map[string]context.CancelFunc{},
		openStream: defaultOpenStream,
		wait:       waitOrCancel,
		grace:      streamGrace,
	}
}

func defaultOpenStream(ctx context.Context, bin, sessionID string, started func(), handle func(rex.Event)) error {
	return rex.New(bin).StreamWithStart(ctx, sessionID, started, handle)
}

func waitOrCancel(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
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
	// A restart invalidates every queued message from the old streams.
	h.generation = int(hubGeneration.Add(1))
	h.queue = nil
	h.root, h.cancel = context.WithCancel(context.Background())
	go h.run(h.root, "", h.generation)
}

// Generation identifies the current stream generation. The model drops
// messages from earlier generations.
func (h *streamHub) Generation() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.generation
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
		generation := h.generation
		ctx, cancel := context.WithCancel(h.root)
		h.sessions[sessionID] = cancel
		go h.run(ctx, sessionID, generation)
	}
}

// Close stops every stream and unblocks any waiting reader.
func (h *streamHub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	h.sessions = map[string]context.CancelFunc{}
	if h.done != nil {
		close(h.done)
		h.done = nil
	}
}

// run keeps one Rex event stream alive with exponential backoff, reporting
// connect/drop transitions into the hub queue. A stream counts as connected
// only after its process started and stayed up through the grace period, and a
// successful connect resets the backoff.
func (h *streamHub) run(ctx context.Context, sessionID string, generation int) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		connected, err := h.runOnce(ctx, sessionID, generation)
		if ctx.Err() != nil {
			return
		}
		h.post(ctx, streamStateMsg{sessionID: sessionID, connected: false, err: err, generation: generation})
		if connected {
			backoff = time.Second
		}
		if !h.wait(ctx, backoff) {
			return
		}
		backoff = nextBackoff(backoff, connected)
	}
}

// runOnce runs one stream attempt. It reports whether the attempt connected,
// which requires both a started process and the grace period passing before
// the process exits.
func (h *streamHub) runOnce(ctx context.Context, sessionID string, generation int) (bool, error) {
	attemptCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	started := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- h.openStream(attemptCtx, h.binValue(), sessionID, func() {
			// A started process is not yet healthy, but it does mean a
			// reconnect is underway, so the model can cancel a pending pause.
			h.post(attemptCtx, streamStateMsg{
				sessionID:  sessionID,
				connecting: true,
				generation: generation,
			})
			select {
			case started <- struct{}{}:
			default:
			}
		}, func(event rex.Event) {
			h.post(attemptCtx, streamEventMsg{event: event, generation: generation})
		})
	}()

	select {
	case err := <-done:
		return false, err
	case <-started:
	case <-ctx.Done():
		return false, ctx.Err()
	}

	timer := time.NewTimer(h.grace)
	defer timer.Stop()
	select {
	case <-timer.C:
		h.post(ctx, streamStateMsg{sessionID: sessionID, connected: true, generation: generation})
		select {
		case err := <-done:
			return true, err
		case <-ctx.Done():
			return true, ctx.Err()
		}
	case err := <-done:
		return false, err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// nextBackoff returns the delay before the next attempt. A successful connect
// resets it to 1s; consecutive failures double it up to 30s.
func nextBackoff(previous time.Duration, connected bool) time.Duration {
	if connected {
		return time.Second
	}
	next := previous * 2
	if next > 30*time.Second {
		next = 30 * time.Second
	}
	return next
}

func (h *streamHub) binValue() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.bin
}

// coalesceKey identifies messages that supersede each other. A newer program
// status for the same block, or a newer window/view event for the same
// session, replaces the queued one. The generation is part of the key so a
// stale event can never replace a current one.
func coalesceKey(msg tea.Msg) (string, bool) {
	envelope, ok := msg.(streamEventMsg)
	if !ok {
		return "", false
	}
	event := envelope.event
	generation := strconv.Itoa(envelope.generation)
	switch event.Type {
	case rex.EventProgramStatusChanged:
		return "status:" + generation + ":" + event.SessionID + ":" + event.BlockID, true
	case rex.EventSessionViewChanged,
		rex.EventActiveWindowChanged,
		rex.EventWindowCreated,
		rex.EventWindowClosed,
		rex.EventWindowLabelChanged:
		return "windows:" + generation + ":" + event.SessionID, true
	}
	return "", false
}

// post enqueues one message without blocking the scanner. Messages that
// coalesce replace their queued predecessor; an overfull queue drops the
// oldest event rather than stalling the stream.
func (h *streamHub) post(ctx context.Context, msg tea.Msg) {
	if ctx.Err() != nil {
		return
	}
	key, hasKey := coalesceKey(msg)
	h.mu.Lock()
	if hasKey {
		for i := len(h.queue) - 1; i >= 0; i-- {
			if queued, ok := coalesceKey(h.queue[i]); ok && queued == key {
				h.queue[i] = msg
				h.mu.Unlock()
				h.wake()
				return
			}
		}
	}
	if len(h.queue) >= maxQueuedMessages {
		h.dropOldestEventLocked()
	}
	h.queue = append(h.queue, msg)
	h.mu.Unlock()
	h.wake()
}

// dropOldestEventLocked makes room for a new message. Connection-state
// messages are kept in preference to events.
func (h *streamHub) dropOldestEventLocked() {
	for i, queued := range h.queue {
		if _, ok := queued.(streamEventMsg); ok {
			h.queue = append(h.queue[:i], h.queue[i+1:]...)
			return
		}
	}
	if len(h.queue) > 0 {
		h.queue = h.queue[1:]
	}
}

func (h *streamHub) wake() {
	select {
	case h.signal <- struct{}{}:
	default:
	}
}

// pop removes the oldest queued message, if any.
func (h *streamHub) pop() (tea.Msg, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.queue) == 0 {
		return nil, false
	}
	msg := h.queue[0]
	h.queue = h.queue[1:]
	return msg, true
}

// doneChan returns the close signal, or nil after Close.
func (h *streamHub) doneChan() <-chan struct{} {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.done
}

// waitForStream blocks until a message is queued, then returns it. The model
// re-issues it after every delivered message. Close unblocks the waiter with a
// nil message.
func waitForStream(hub *streamHub) tea.Cmd {
	return func() tea.Msg {
		done := hub.doneChan()
		if done == nil {
			return nil
		}
		for {
			if msg, ok := hub.pop(); ok {
				return msg
			}
			select {
			case <-hub.signal:
			case <-done:
				return nil
			}
		}
	}
}
