package httpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Check probes a dependency (database, browser …). Errors go to the log only, never into the response.
type Check func(ctx context.Context) error

// Health serves /healthz (process is alive) and /readyz (all checks pass).
type Health struct {
	mu     sync.RWMutex
	checks map[string]Check
	logger *slog.Logger
	// Timeout bounds every single check.
	Timeout time.Duration
}

// NewHealth creates a health handler without checks.
func NewHealth(logger *slog.Logger) *Health {
	return &Health{checks: map[string]Check{}, logger: logger, Timeout: 2 * time.Second}
}

// Add registers a readiness check.
func (h *Health) Add(name string, c Check) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = c
}

// Register adds /healthz and /readyz to mux.
func (h *Health) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", h.live)
	mux.HandleFunc("GET /readyz", h.ready)
}

type readiness struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

func (h *Health) live(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte("ok\n"))
}

func (h *Health) ready(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	names := make([]string, 0, len(h.checks))
	for n := range h.checks {
		names = append(names, n)
	}
	checks := make(map[string]Check, len(h.checks))
	for n, c := range h.checks {
		checks[n] = c
	}
	h.mu.RUnlock()
	sort.Strings(names)

	res := h.runChecks(r.Context(), names, checks)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if res.Status != "ok" {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	_ = json.NewEncoder(w).Encode(res)
}

// runChecks runs all checks in parallel, each with its own timeout.
func (h *Health) runChecks(ctx context.Context, names []string, checks map[string]Check) readiness {
	states := make([]string, len(names))
	var wg sync.WaitGroup
	for i, n := range names {
		wg.Go(func() { states[i] = h.probe(ctx, n, checks[n]) })
	}
	wg.Wait()
	res := readiness{Status: "ok", Checks: make(map[string]string, len(names))}
	for i, n := range names {
		res.Checks[n] = states[i]
		if states[i] != "ok" {
			res.Status = "fail"
		}
	}
	return res
}

// probe runs one check and logs a failure (the response only says "fail").
func (h *Health) probe(ctx context.Context, name string, check Check) string {
	ctx, cancel := context.WithTimeout(ctx, h.Timeout)
	defer cancel()
	if err := check(ctx); err != nil {
		h.logger.WarnContext(ctx, "readiness check failed", "check", name, "err", err)
		return "fail"
	}
	return "ok"
}
