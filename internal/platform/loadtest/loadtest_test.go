package loadtest_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/platform/loadtest"
)

func TestRunBasic(t *testing.T) {
	var jobsCount, profileCount atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs":
			jobsCount.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("jobs list"))
		case "/profile":
			profileCount.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("profile details"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfg := loadtest.Config{
		BaseURL: srv.URL,
		Paths: []loadtest.WeightedPath{
			{Path: "/jobs", Weight: 3},
			{Path: "/profile", Weight: 1},
		},
		Concurrency: 4,
		Duration:    500 * time.Millisecond,
		TargetSLO:   200 * time.Millisecond,
	}

	stats, err := loadtest.Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if stats.TotalCount == 0 {
		t.Fatal("expected > 0 requests")
	}
	if stats.SuccessCount != stats.TotalCount {
		t.Errorf("expected 100%% success, got %d ok, %d err", stats.SuccessCount, stats.ErrorCount)
	}
	if !stats.PassedSLO {
		t.Errorf("expected SLO to pass, got p95 %v > %v", stats.P95, stats.TargetSLO)
	}
	if jobsCount.Load() == 0 || profileCount.Load() == 0 {
		t.Errorf("expected both paths to be hit, got jobs=%d, profile=%d", jobsCount.Load(), profileCount.Load())
	}

	summary := stats.Summary()
	if !strings.Contains(summary, "GETAFJOB LOAD TEST REPORT") || !strings.Contains(summary, "[ PASS ]") {
		t.Errorf("unexpected summary output:\n%s", summary)
	}
}

func TestRunSLOFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := loadtest.Config{
		BaseURL: srv.URL,
		Paths: []loadtest.WeightedPath{
			{Path: "/slow", Weight: 1},
		},
		Concurrency: 2,
		MaxRequests: 10,
		TargetSLO:   10 * time.Millisecond, // intentionally low to trigger SLO failure
	}

	stats, err := loadtest.Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if stats.PassedSLO {
		t.Errorf("expected SLO to fail with p95 %v > target %v", stats.P95, stats.TargetSLO)
	}
	if !strings.Contains(stats.Summary(), "[ FAIL ]") {
		t.Errorf("expected summary to indicate [ FAIL ], got:\n%s", stats.Summary())
	}
}

func TestRunErrorRate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfg := loadtest.Config{
		BaseURL:     srv.URL,
		Concurrency: 2,
		MaxRequests: 10,
		TargetSLO:   200 * time.Millisecond,
	}

	stats, err := loadtest.Run(t.Context(), cfg)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if stats.ErrorCount != stats.TotalCount {
		t.Errorf("expected all requests to be counted as errors, got %d ok, %d err", stats.SuccessCount, stats.ErrorCount)
	}
	if stats.PassedSLO {
		t.Errorf("expected SLO to fail on 100%% error rate")
	}
}

func TestRunContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)

	cfg := loadtest.Config{
		BaseURL:     srv.URL,
		Concurrency: 3,
		Duration:    10 * time.Second, // Long duration, but context will cancel early
	}

	start := time.Now()
	stats, err := loadtest.Run(ctx, cfg)
	took := time.Since(start)

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if took > 2*time.Second {
		t.Errorf("Run did not cancel promptly, took %v", took)
	}
	if stats.TotalCount == 0 {
		t.Errorf("expected some requests before cancellation")
	}
}
