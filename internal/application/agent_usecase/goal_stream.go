package agent_usecase

import (
	"encoding/json"
	"sync"

	"agentcanvas/internal/domain/goal"
	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/eventhub"
)

// goalStreamHub is conversation-scoped rather than run-scoped. Goal mutations
// are valid while no run exists, so reusing the run hub would either invent a
// run ID or silently drop the event.
type goalStreamHub struct {
	mu    sync.Mutex
	items map[int64]*goalStreamState
}

type goalStreamState struct {
	next   uint64
	events []eventhub.StreamEvent
	subs   map[uint64]chan eventhub.StreamEvent
	subID  uint64
}

func newGoalStreamHub() *goalStreamHub {
	return &goalStreamHub{items: make(map[int64]*goalStreamState)}
}

func (h *goalStreamHub) state(conversationID int64) *goalStreamState {
	item := h.items[conversationID]
	if item == nil {
		item = &goalStreamState{next: 1, subs: make(map[uint64]chan eventhub.StreamEvent)}
		h.items[conversationID] = item
	}
	return item
}

func (h *goalStreamHub) publish(conversationID int64, item *goal.ThreadGoal) {
	if h == nil || conversationID <= 0 {
		return
	}
	payload := map[string]any{"conversation_id": conversationID, "goal": item}
	data, _ := json.Marshal(payload)
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.state(conversationID)
	kind := runtimeevent.GoalUpdated
	if item == nil {
		kind = runtimeevent.GoalCleared
	}
	event := eventhub.StreamEvent{Version: eventhub.RunStreamVersion, RunID: 0, ConversationID: &conversationID, Seq: state.next, Kind: kind, Data: data}
	state.next++
	state.events = append(state.events, event)
	if len(state.events) > 256 {
		state.events = state.events[len(state.events)-256:]
	}
	for id, ch := range state.subs {
		select {
		case ch <- event:
		default:
			close(ch)
			delete(state.subs, id)
		}
	}
}

func (h *goalStreamHub) subscribe(conversationID int64, after uint64) ([]eventhub.StreamEvent, <-chan eventhub.StreamEvent, func()) {
	if h == nil || conversationID <= 0 {
		ch := make(chan eventhub.StreamEvent)
		close(ch)
		return nil, ch, func() {}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.state(conversationID)
	replay := make([]eventhub.StreamEvent, 0)
	for _, event := range state.events {
		if event.Seq > after {
			replay = append(replay, event)
		}
	}
	state.subID++
	id := state.subID
	ch := make(chan eventhub.StreamEvent, 32)
	state.subs[id] = ch
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if current, ok := state.subs[id]; ok && current == ch {
				delete(state.subs, id)
				close(ch)
			}
		})
	}
	return replay, ch, cancel
}
