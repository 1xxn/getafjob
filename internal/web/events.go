package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/events"
)

// eventHeartbeat keeps proxies from closing an idle stream.
const eventHeartbeat = 20 * time.Second

// eventStream sends the user's application changes as server-sent events (#166). A reconnecting page passes
// Last-Event-ID and gets what it missed from the hub's short replay.
func (s *Server) eventStream(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.Events == nil {
		http.Error(w, "events unavailable", http.StatusServiceUnavailable)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // a stream outlives the server's write timeout
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	last, _ := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	past, ch, cancel := s.Events.Subscribe(u.ID, last)
	defer cancel()
	send := func(id uint64, data events.Event) bool {
		b, err := json.Marshal(data)
		if err != nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, eventName(data), b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	_, _ = fmt.Fprint(w, "retry: 3000\n\n")
	if rc.Flush() != nil {
		return
	}
	for _, e := range past {
		if !send(e.ID, e) {
			return
		}
	}
	ping := time.NewTicker(eventHeartbeat)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			if !send(e.ID, e) {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// eventName is the SSE event type: "jobs" for the crawl and new ratings (job lists, the dashboard), "app" for
// application changes and mailbox suggestions.
func eventName(e events.Event) string {
	if e.Topic == events.TopicCrawl || e.Topic == events.TopicMatches {
		return "jobs"
	}
	return "app"
}
