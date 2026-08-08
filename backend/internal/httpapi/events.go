package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The event stream (CONTRACT §7.2): one per project, every event carrying
// `{ id, type, project_id, at, payload }`.
//
// **Reconnect is honoured where it can be, and says so when it cannot.** The
// hub keeps a bounded ring of recent events per project; a client returning
// with `Last-Event-ID` inside that window is replayed exactly, and one that
// fell further behind gets a `resync` event and refetches. The alternative —
// pretending a gap did not happen — is a canvas that is quietly wrong, which is
// worse than one that admits it is stale.
//
// In memory rather than in Postgres, deliberately. These events are
// notifications about facts that are already durable; the database is the
// record, and persisting a second copy of it would create a second thing that
// can disagree. A restart drops the ring, and every client gets `resync`, which
// is the correct answer after a restart.

// ringSize is how many events per project are replayable.
//
// Enough to cover a browser tab reconnecting after a laptop lid closes for a
// minute during a busy turn; not so many that an idle deployment holds a
// megabyte per project forever.
const ringSize = 256

// Event is one thing that happened, as the wire carries it.
type Event struct {
	ID        uint64         `json:"id"`
	Type      string         `json:"type"`
	ProjectID uuid.UUID      `json:"project_id"`
	At        time.Time      `json:"at"`
	Payload   map[string]any `json:"payload"`
}

// EventHub fans events out to the subscribers of one project each.
type EventHub struct {
	mu       sync.Mutex
	nextID   uint64
	rings    map[uuid.UUID]*ring
	channels map[uuid.UUID]map[chan Event]struct{}
}

// NewEventHub builds the hub.
func NewEventHub() *EventHub {
	return &EventHub{
		rings:    map[uuid.UUID]*ring{},
		channels: map[uuid.UUID]map[chan Event]struct{}{},
	}
}

// Publish records an event and delivers it to every live subscriber.
//
// Never blocks. A subscriber whose buffer is full is one whose client stopped
// reading, and holding a turn's progress hostage to a wedged browser tab is not
// a trade anybody wants: the slow reader misses events and finds out on its
// next reconnect, because the id it sends back will be older than the ring.
func (h *EventHub) Publish(projectID uuid.UUID, eventType string, payload map[string]any) {
	h.mu.Lock()
	h.nextID++
	e := Event{ID: h.nextID, Type: eventType, ProjectID: projectID, At: time.Now(), Payload: payload}

	r, ok := h.rings[projectID]
	if !ok {
		r = newRing(ringSize)
		h.rings[projectID] = r
	}
	r.add(e)

	subscribers := make([]chan Event, 0, len(h.channels[projectID]))
	for ch := range h.channels[projectID] {
		subscribers = append(subscribers, ch)
	}
	h.mu.Unlock()

	for _, ch := range subscribers {
		select {
		case ch <- e:
		default:
		}
	}
}

// subscribe returns a channel of future events plus whatever the client missed.
//
// replayed is false when `since` is older than the ring, which is the caller's
// signal to emit `resync` before anything else.
func (h *EventHub) subscribe(projectID uuid.UUID, since uint64, resuming bool) (ch chan Event, missed []Event, replayed bool, cancel func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch = make(chan Event, 64)
	if h.channels[projectID] == nil {
		h.channels[projectID] = map[chan Event]struct{}{}
	}
	h.channels[projectID][ch] = struct{}{}

	if resuming {
		replayed = true
		if r, ok := h.rings[projectID]; ok {
			missed, replayed = r.since(since)
		}
	} else {
		// A fresh subscriber has missed nothing by definition.
		replayed = true
	}

	cancel = func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if set, ok := h.channels[projectID]; ok {
			delete(set, ch)
			if len(set) == 0 {
				delete(h.channels, projectID)
			}
		}
		close(ch)
	}
	return ch, missed, replayed, cancel
}

// ring is a fixed-size window of recent events.
type ring struct {
	items []Event
	next  int
	full  bool
}

func newRing(n int) *ring { return &ring{items: make([]Event, n)} }

func (r *ring) add(e Event) {
	r.items[r.next] = e
	r.next = (r.next + 1) % len(r.items)
	if r.next == 0 {
		r.full = true
	}
}

// since returns the events after id, and whether the whole gap was covered.
func (r *ring) since(id uint64) ([]Event, bool) {
	var (
		out    []Event
		oldest uint64
		hasAny bool
	)
	visit := func(e Event) {
		if e.ID == 0 {
			return
		}
		if !hasAny || e.ID < oldest {
			oldest, hasAny = e.ID, true
		}
		if e.ID > id {
			out = append(out, e)
		}
	}
	if r.full {
		for i := r.next; i < len(r.items); i++ {
			visit(r.items[i])
		}
	}
	for i := 0; i < r.next; i++ {
		visit(r.items[i])
	}
	// Covered when the client's last id is at or after the oldest we still
	// hold, or when we hold nothing at all and therefore nothing was lost.
	return out, !hasAny || id+1 >= oldest
}

// ── the endpoint ─────────────────────────────────────────────────────────────

// registerEvents wires GET /api/v1/projects/{pid}/events.
//
// Written against net/http directly rather than through huma: a long-lived
// text/event-stream is not a request/response operation, and describing it as
// one in the OpenAPI document would generate a client method that hangs.
func (s *Server) registerEvents(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/projects/{pid}/events", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		p, ok := principalFromRequest(ctx)
		if !ok {
			writeStreamError(w, http.StatusUnauthorized, "You are not signed in. Sign in again to continue.")
			return
		}
		projectID, err := uuid.Parse(r.PathValue("pid"))
		if err != nil {
			writeStreamError(w, http.StatusBadRequest, "That is not a project id.")
			return
		}
		if _, err := s.deps.Projects.Get(ctx, projectID, p); err != nil {
			writeStreamError(w, http.StatusNotFound, "That project was not found.")
			return
		}

		flusher, canFlush := w.(http.Flusher)
		if !canFlush {
			writeStreamError(w, http.StatusInternalServerError, "This server cannot stream.")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		// Nginx and friends buffer text/event-stream by default, which turns a
		// live stream into a long silence followed by everything at once.
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		// One comment before anything else, so the response has a body byte
		// immediately.
		//
		// X-Accel-Buffering persuades nginx; it persuades nothing else, and an
		// intermediary that withholds headers until the first byte leaves a
		// client showing "connecting" for as long as this stream stays quiet —
		// up to a full keepalive interval on a project where nothing is
		// happening. That is indistinguishable from a broken stream, which is
		// the state §7.2 exists to make visible rather than to produce. It is
		// a comment, so it carries no id and cannot disturb Last-Event-ID.
		fmt.Fprint(w, ": connected\n\n")
		flusher.Flush()

		since, resuming := lastEventID(r)
		ch, missed, replayed, cancel := s.events.subscribe(projectID, since, resuming)
		defer cancel()

		if !replayed {
			// The gap is wider than the ring. Say so rather than silently
			// resuming from a hole: a client that thinks it is caught up and
			// is not renders a canvas that is quietly wrong.
			writeEvent(w, flusher, Event{
				ID: since, Type: "resync", ProjectID: projectID, At: time.Now(),
				Payload: map[string]any{
					"reason": "more events happened than this server keeps for replay; refetch the project",
				},
			})
		}
		for _, e := range missed {
			writeEvent(w, flusher, e)
		}
		flusher.Flush()

		// A comment line every 25 seconds keeps proxies from closing an idle
		// stream. It is not an event and carries no id, so it cannot disturb
		// the client's Last-Event-ID.
		keepalive := time.NewTicker(25 * time.Second)
		defer keepalive.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.shutdown:
				return
			case <-keepalive.C:
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
			case e, open := <-ch:
				if !open {
					return
				}
				writeEvent(w, flusher, e)
				flusher.Flush()
			}
		}
	})
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, e Event) {
	body, err := json.Marshal(e)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.ID, e.Type, body)
	_ = flusher
}

// lastEventID reads the resume point, and reports whether the client sent one.
//
// The two are different facts. A client that sends `0` has seen nothing and
// wants everything the ring holds; a client that sends no header at all is new
// and has missed nothing by definition. Collapsing them into one number makes
// the first indistinguishable from the second, and a reconnecting client
// silently loses whatever happened while it was away.
func lastEventID(r *http.Request) (uint64, bool) {
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("last_event_id")
	}
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// writeStreamError answers a failed handshake in the contract's envelope, so a
// client that could not open the stream reads the same shape as everywhere else.
func writeStreamError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": codeForStatus(status), "message": message},
	})
}
