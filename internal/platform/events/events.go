// Package events carries application changes from the database to open browser pages (#166): a trigger notifies
// the channel, Listen turns notifications into events, and the Hub hands them to each user's subscribers with a
// short replay for reconnects.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Channel is the Postgres notification channel of db/migrations/00032.
const Channel = "getafjob_events"

// replay is how many recent events per user a reconnecting page can catch up on.
const replay = 50

// Event is one application change; it carries ids and states only.
type Event struct {
	ID    uint64    `json:"id"`
	User  uuid.UUID `json:"-"`
	App   uuid.UUID `json:"app"`
	Event string    `json:"event"`
	From  string    `json:"from"`
	To    string    `json:"to"`
}

// Hub fans events out to the subscribers of each user.
type Hub struct {
	mu     sync.Mutex
	seq    uint64
	subs   map[uuid.UUID]map[chan Event]struct{}
	recent map[uuid.UUID][]Event
}

// NewHub returns an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[uuid.UUID]map[chan Event]struct{}{}, recent: map[uuid.UUID][]Event{}}
}

// Publish numbers e and delivers it to the user's subscribers; a subscriber that does not keep up loses events
// and catches up through the replay on reconnect.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// IDs grow across restarts (micro seconds), so a page's Last-Event-ID never skips new events.
	h.seq = max(h.seq+1, uint64(time.Now().UnixMicro())) //nolint:gosec // the clock is after 1970
	e.ID = h.seq
	r := append(h.recent[e.User], e)
	if len(r) > replay {
		r = r[len(r)-replay:]
	}
	h.recent[e.User] = r
	for ch := range h.subs[e.User] {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribe returns the user's events after lastID (0: none from the past) and a channel for new ones. Call the
// returned func to unsubscribe.
func (h *Hub) Subscribe(user uuid.UUID, lastID uint64) ([]Event, <-chan Event, func()) {
	ch := make(chan Event, 16)
	h.mu.Lock()
	defer h.mu.Unlock()
	var past []Event
	if lastID > 0 {
		for _, e := range h.recent[user] {
			if e.ID > lastID {
				past = append(past, e)
			}
		}
	}
	if h.subs[user] == nil {
		h.subs[user] = map[chan Event]struct{}{}
	}
	h.subs[user][ch] = struct{}{}
	return past, ch, func() {
		h.mu.Lock()
		delete(h.subs[user], ch)
		if len(h.subs[user]) == 0 {
			delete(h.subs, user)
		}
		h.mu.Unlock()
	}
}

type payload struct {
	User  uuid.UUID `json:"user"`
	App   uuid.UUID `json:"app"`
	Event string    `json:"event"`
	From  string    `json:"from"`
	To    string    `json:"to"`
}

// Listen forwards notifications to the hub until ctx ends; a lost connection is opened again after a pause.
func Listen(ctx context.Context, pool *pgxpool.Pool, hub *Hub, logger *slog.Logger) {
	for ctx.Err() == nil {
		if err := listenOnce(ctx, pool, hub); err != nil && ctx.Err() == nil {
			logger.WarnContext(ctx, "event listener stopped, retrying", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, hub *Hub) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "UNLISTEN "+Channel) }()
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		var p payload
		if json.Unmarshal([]byte(n.Payload), &p) != nil || p.User == uuid.Nil {
			continue
		}
		hub.Publish(Event{User: p.User, App: p.App, Event: p.Event, From: p.From, To: p.To})
	}
}
