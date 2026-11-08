package ats

import (
	"context"
	"sync"

	gotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const metricsScope = "github.com/1xxn/getafjob/internal/runner/ats"

// Outcome is the result of a run for maturity tracking.
type Outcome string

// Run outcomes.
const (
	OutcomeSubmitted Outcome = "submitted"
	OutcomeAsked     Outcome = "asked"
	OutcomeParked    Outcome = "parked"
	OutcomeGuard     Outcome = "guard"
	OutcomeExpired   Outcome = "expired"
)

// Stats holds run counts per outcome for one ATS adapter.
type Stats struct {
	Submitted int64
	Asked     int64
	Parked    int64
	Guard     int64
	Expired   int64
}

// Total returns the total number of recorded run outcomes.
func (s Stats) Total() int64 {
	return s.Submitted + s.Asked + s.Parked + s.Guard + s.Expired
}

// Maturity returns the measured success rate (submitted / total) in [0, 1].
func (s Stats) Maturity() float64 {
	total := s.Total()
	if total <= 0 {
		return 0
	}
	return float64(s.Submitted) / float64(total)
}

// Tracker records run outcomes per ATS adapter and exports maturity metrics.
type Tracker struct {
	mu       sync.RWMutex
	byATS    map[string]Stats
	runs     metric.Int64Counter
	maturity metric.Float64Gauge
}

// NewTracker creates an ATS maturity tracker backed by the global OpenTelemetry meter provider.
func NewTracker() (*Tracker, error) {
	m := gotel.Meter(metricsScope)
	runs, err := m.Int64Counter("getafjob.ats.runs",
		metric.WithDescription("Application runs by ATS adapter and outcome."))
	if err != nil {
		return nil, err
	}
	maturity, err := m.Float64Gauge("getafjob.ats.maturity",
		metric.WithUnit("1"),
		metric.WithDescription("Measured success rate (submitted / total runs) per ATS adapter."))
	if err != nil {
		return nil, err
	}
	return &Tracker{
		byATS:    make(map[string]Stats),
		runs:     runs,
		maturity: maturity,
	}, nil
}

// Record records a run outcome for an ATS adapter and updates its maturity metric.
func (t *Tracker) Record(ctx context.Context, adapter string, outcome Outcome) {
	if t == nil {
		return
	}
	name := Lookup(adapter).Name
	t.mu.Lock()
	st := t.byATS[name]
	switch outcome {
	case OutcomeSubmitted:
		st.Submitted++
	case OutcomeAsked:
		st.Asked++
	case OutcomeParked:
		st.Parked++
	case OutcomeGuard:
		st.Guard++
	case OutcomeExpired:
		st.Expired++
	default:
		t.mu.Unlock()
		return
	}
	t.byATS[name] = st
	rate := st.Maturity()
	t.mu.Unlock()

	t.runs.Add(ctx, 1, metric.WithAttributes(
		attribute.String("adapter", name),
		attribute.String("outcome", string(outcome)),
	))
	t.maturity.Record(ctx, rate, metric.WithAttributes(attribute.String("adapter", name)))
}

// Stats returns the current stats of an adapter.
func (t *Tracker) Stats(adapter string) Stats {
	if t == nil {
		return Stats{}
	}
	name := Lookup(adapter).Name
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.byATS[name]
}

// Maturity returns the measured success rate of an adapter in [0, 1].
func (t *Tracker) Maturity(adapter string) float64 {
	return t.Stats(adapter).Maturity()
}

// Snapshot returns a copy of all recorded adapter statistics.
func (t *Tracker) Snapshot() map[string]Stats {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]Stats, len(t.byATS))
	for k, v := range t.byATS {
		out[k] = v
	}
	return out
}
