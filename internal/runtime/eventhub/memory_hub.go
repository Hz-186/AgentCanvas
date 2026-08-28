package eventhub

import (
	"sort"
	"sync"
	"time"
)

type subscriber struct {
	ch     chan StreamEvent
	closed bool
}

type runState struct {
	mu sync.Mutex

	nextSeq       uint64
	lastPublished uint64
	events        []StreamEvent
	eventBytes    int64

	nextSubscriber uint64
	subscribers    map[uint64]*subscriber

	closed   bool
	closedAt time.Time
	terminal *StreamEvent
	snapshot *StreamEvent
}

func newRunState() *runState {
	return &runState{
		nextSeq:     1,
		subscribers: make(map[uint64]*subscriber),
	}
}

// MemoryHub is a single-process, per-run event hub.  It intentionally does
// not persist events: callers should publish an event only after their audit
// repository transaction has committed, then rely on replay/snapshot for
// reconnects.
type MemoryHub struct {
	mu     sync.RWMutex
	runs   map[int64]*runState
	config Config
}

// NewMemoryHub constructs a hub with the protocol defaults.  Passing a Config
// is optional and is useful for tests (small replay windows/short retention).
func NewMemoryHub(config ...Config) *MemoryHub {
	cfg := Config{}
	if len(config) > 0 {
		cfg = config[0]
	}
	return &MemoryHub{runs: make(map[int64]*runState), config: cfg.withDefaults()}
}

// Config returns a copy of the effective hub configuration.
func (h *MemoryHub) Config() Config {
	if h == nil {
		return (Config{}).withDefaults()
	}
	h.mu.RLock()
	config := h.config
	h.mu.RUnlock()
	return config.withDefaults()
}

func (h *MemoryHub) now() time.Time {
	h.mu.RLock()
	now := h.config.Now
	h.mu.RUnlock()
	if now == nil {
		return time.Now()
	}
	return now().UTC()
}

func (h *MemoryHub) getRun(runID int64, create bool) *runState {
	if h == nil || runID == 0 {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runs == nil {
		h.runs = make(map[int64]*runState)
	}
	if h.config.MaxEvents <= 0 || h.config.MaxBytes <= 0 || h.config.TerminalRetention <= 0 || h.config.SubscriberBuffer <= 0 || h.config.Now == nil {
		h.config = h.config.withDefaults()
	}
	if state, ok := h.runs[runID]; ok {
		return state
	}
	if !create {
		return nil
	}
	state := newRunState()
	h.runs[runID] = state
	return state
}

func (h *MemoryHub) removeExpiredRuns(now time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.runs == nil {
		h.runs = make(map[int64]*runState)
	}
	if h.config.MaxEvents <= 0 || h.config.MaxBytes <= 0 || h.config.TerminalRetention <= 0 || h.config.SubscriberBuffer <= 0 || h.config.Now == nil {
		h.config = h.config.withDefaults()
	}
	retention := h.config.TerminalRetention
	for runID, state := range h.runs {
		state.mu.Lock()
		expired := state.closed && !state.closedAt.IsZero() && !now.Before(state.closedAt.Add(retention))
		if expired {
			for id, sub := range state.subscribers {
				closeSubscriberLocked(state, id, sub)
			}
			state.events = nil
			state.terminal = nil
			state.snapshot = nil
		}
		state.mu.Unlock()
		if expired {
			delete(h.runs, runID)
		}
	}
}

// Prepare allocates the next per-run sequence number.  The returned event is
// not visible to subscribers until PublishPrepared is called.
func (h *MemoryHub) Prepare(runID int64, event StreamEvent) StreamEvent {
	if h == nil || runID == 0 {
		return event
	}
	now := h.now()
	state := h.getRun(runID, true)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		// A closed run cannot be reused.  Preserve the event's caller data so a
		// failed publisher can report it, but do not allocate a new sequence.
		return normalizeEvent(event, runID, now)
	}
	event = normalizeEvent(event, runID, now)
	if state.nextSeq == 0 {
		state.nextSeq = 1
	}
	event.Seq = state.nextSeq
	state.nextSeq++
	return cloneEvent(event)
}

// PublishPrepared makes a prepared event visible.  The method deliberately
// has no error return to keep it safe for emitters that are already in a
// best-effort live path; invalid/duplicate events are ignored.
func (h *MemoryHub) PublishPrepared(event StreamEvent) {
	if h == nil || event.RunID == 0 {
		return
	}
	now := h.now()
	state := h.getRun(event.RunID, true)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	event = normalizeEvent(event, event.RunID, now)
	if event.Seq == 0 {
		// Be forgiving for legacy callers while keeping the normal Prepare →
		// PublishPrepared contract.  This also makes migration wrappers simple.
		if state.nextSeq == 0 {
			state.nextSeq = 1
		}
		event.Seq = state.nextSeq
		state.nextSeq++
	}
	if event.Seq <= state.lastPublished {
		return
	}
	if event.Seq >= state.nextSeq {
		state.nextSeq = event.Seq + 1
	}
	// Publishing out of order is not expected when the caller uses a per-run
	// lane.  Sorting here keeps replay deterministic if a bad caller violates
	// that requirement; a sequence already published is still de-duplicated.
	event = cloneEvent(event)
	state.events = append(state.events, event)
	state.eventBytes += eventSize(event)
	if event.Seq > state.lastPublished {
		state.lastPublished = event.Seq
	}
	if event.Kind == StreamSnapshot {
		snapshot := cloneEvent(event)
		state.snapshot = &snapshot
	}
	if isTerminalKind(event.Kind) {
		terminal := cloneEvent(event)
		state.terminal = &terminal
	}
	trimEventsLocked(state, h.config.MaxEvents, h.config.MaxBytes)
	notifySubscribersLocked(state, event)
}

// Subscribe atomically determines the replay boundary and registers the live
// channel.  This is the important no-gap property: a publish cannot occur
// between those two operations.
func (h *MemoryHub) Subscribe(runID int64, afterSeq uint64) (replay []StreamEvent, live <-chan StreamEvent, cancel func()) {
	if h == nil || runID == 0 {
		closed := make(chan StreamEvent)
		close(closed)
		return nil, closed, func() {}
	}
	now := h.now()
	h.removeExpiredRuns(now)
	state := h.getRun(runID, true)
	state.mu.Lock()
	defer state.mu.Unlock()

	// Subscribers receive a snapshot when their cursor has fallen behind the
	// oldest retained event.  The snapshot is inserted in the returned replay
	// slice, never published into the live ring, so it cannot perturb sequence
	// allocation or another subscriber's view.
	needsSnapshot := replayHasGap(state.events, afterSeq)
	if needsSnapshot {
		if snapshot, ok := h.snapshotLocked(runID, state, now); ok {
			replay = append(replay, snapshot)
		}
	} else {
		for _, event := range state.events {
			if event.Seq > afterSeq {
				replay = append(replay, cloneEvent(event))
			}
		}
	}

	buffer := h.config.SubscriberBuffer
	if buffer <= 0 {
		buffer = defaultSubscriberBuffer
	}
	if state.closed {
		closed := make(chan StreamEvent)
		close(closed)
		return cloneEvents(replay), closed, func() {}
	}
	state.nextSubscriber++
	id := state.nextSubscriber
	sub := &subscriber{ch: make(chan StreamEvent, buffer)}
	state.subscribers[id] = sub
	return cloneEvents(replay), sub.ch, h.cancelFunc(state, id, sub)
}

func replayHasGap(events []StreamEvent, afterSeq uint64) bool {
	if len(events) == 0 {
		return false
	}
	earliest := events[0].Seq
	for _, event := range events[1:] {
		if event.Seq < earliest {
			earliest = event.Seq
		}
	}
	return earliest > 0 && afterSeq < earliest-1
}

func cloneEvents(events []StreamEvent) []StreamEvent {
	if len(events) == 0 {
		return nil
	}
	cloned := make([]StreamEvent, len(events))
	for i, event := range events {
		cloned[i] = cloneEvent(event)
	}
	return cloned
}

func (h *MemoryHub) cancelFunc(state *runState, id uint64, sub *subscriber) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			state.mu.Lock()
			defer state.mu.Unlock()
			if current, ok := state.subscribers[id]; ok && current == sub {
				closeSubscriberLocked(state, id, sub)
			}
		})
	}
}

func closeSubscriberLocked(state *runState, id uint64, sub *subscriber) {
	if sub == nil || sub.closed {
		delete(state.subscribers, id)
		return
	}
	sub.closed = true
	delete(state.subscribers, id)
	close(sub.ch)
}

func notifySubscribersLocked(state *runState, event StreamEvent) {
	for id, sub := range state.subscribers {
		if sub == nil || sub.closed {
			delete(state.subscribers, id)
			continue
		}
		select {
		case sub.ch <- cloneEvent(event):
		default:
			// A slow consumer must not stall the runner.  It can reconnect with
			// Last-Event-ID and use replay/snapshot to recover.
			closeSubscriberLocked(state, id, sub)
		}
	}
}

func trimEventsLocked(state *runState, maxEvents int, maxBytes int64) {
	if maxEvents <= 0 {
		maxEvents = defaultMaxEvents
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	for len(state.events) > maxEvents || state.eventBytes > maxBytes {
		if len(state.events) == 0 {
			state.eventBytes = 0
			return
		}
		// Keep a terminal event regardless of size.  It may be the only event
		// left after a very small byte budget.
		if isTerminalKind(state.events[0].Kind) {
			if len(state.events) == 1 {
				return
			}
			// Move terminal to the end and evict the next oldest transient event.
			state.events = append(state.events[1:], state.events[0])
			continue
		}
		removed := state.events[0]
		state.events = state.events[1:]
		state.eventBytes -= eventSize(removed)
	}
	// Keep replay ordered by seq even if a caller published prepared events out
	// of order.  Normal publishers use a per-run lane and do not pay this cost.
	sort.SliceStable(state.events, func(i, j int) bool { return state.events[i].Seq < state.events[j].Seq })
}

func (h *MemoryHub) snapshotLocked(runID int64, state *runState, now time.Time) (StreamEvent, bool) {
	if state.snapshot != nil {
		snapshot := cloneEvent(*state.snapshot)
		if snapshot.Seq == 0 {
			snapshot.Seq = state.lastPublished
		}
		return normalizeSnapshot(snapshot, runID, state.lastPublished, now), true
	}
	if h.config.SnapshotProvider != nil {
		// See Config.SnapshotProvider's contract: this callback is invoked under
		// the run lock to make the snapshot and replay boundary atomic.
		snapshot, err := h.config.SnapshotProvider(runID)
		if err == nil {
			return normalizeSnapshot(snapshot, runID, state.lastPublished, now), true
		}
	}
	if state.terminal != nil {
		return normalizeSnapshot(*state.terminal, runID, state.lastPublished, now), true
	}
	return StreamEvent{}, false
}

func normalizeSnapshot(snapshot StreamEvent, runID int64, seq uint64, now time.Time) StreamEvent {
	snapshot = normalizeEvent(snapshot, runID, now)
	snapshot.Kind = StreamSnapshot
	snapshot.Type = StreamSnapshot
	if seq > 0 {
		snapshot.Seq = seq
	}
	return cloneEvent(snapshot)
}

// Snapshot returns the latest authoritative snapshot available to the hub.
// It does not publish the snapshot into the replay ring.
func (h *MemoryHub) Snapshot(runID int64) (StreamEvent, error) {
	if h == nil || runID == 0 {
		return StreamEvent{}, ErrRunNotFound
	}
	now := h.now()
	h.removeExpiredRuns(now)
	state := h.getRun(runID, false)
	if state == nil {
		return StreamEvent{}, ErrRunNotFound
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	snapshot, ok := h.snapshotLocked(runID, state, now)
	if !ok {
		return StreamEvent{}, ErrSnapshotUnavailable
	}
	return snapshot, nil
}

// CloseRun publishes the terminal event once and closes all current live
// subscribers after enqueueing it.  The terminal event remains replayable for
// TerminalRetention.
func (h *MemoryHub) CloseRun(runID int64, terminal StreamEvent) {
	if h == nil || runID == 0 {
		return
	}
	now := h.now()
	state := h.getRun(runID, true)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	terminal = normalizeEvent(terminal, runID, now)
	if terminal.Seq == 0 {
		if state.nextSeq == 0 {
			state.nextSeq = 1
		}
		terminal.Seq = state.nextSeq
		state.nextSeq++
	}
	if terminal.Seq > state.lastPublished {
		terminal = cloneEvent(terminal)
		state.events = append(state.events, terminal)
		state.eventBytes += eventSize(terminal)
		state.lastPublished = terminal.Seq
		if terminal.Seq >= state.nextSeq {
			state.nextSeq = terminal.Seq + 1
		}
		state.terminal = cloneEventPtr(terminal)
		trimEventsLocked(state, h.config.MaxEvents, h.config.MaxBytes)
		notifySubscribersLocked(state, terminal)
	}
	state.closed = true
	state.closedAt = now
	for id, sub := range state.subscribers {
		closeSubscriberLocked(state, id, sub)
	}
}

func cloneEventPtr(event StreamEvent) *StreamEvent {
	cloned := cloneEvent(event)
	return &cloned
}

// Close immediately tears down all live subscribers and forgets all runs.  It
// is useful for process shutdown and deterministic tests.
func (h *MemoryHub) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for runID, state := range h.runs {
		state.mu.Lock()
		for id, sub := range state.subscribers {
			closeSubscriberLocked(state, id, sub)
		}
		state.mu.Unlock()
		delete(h.runs, runID)
	}
}

// PurgeExpired removes terminal runs older than the configured retention.  It
// is intentionally explicit so applications can call it from an existing
// housekeeping loop instead of creating one goroutine per hub.
func (h *MemoryHub) PurgeExpired() {
	if h == nil {
		return
	}
	h.removeExpiredRuns(h.now())
}
