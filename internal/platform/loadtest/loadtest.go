// Package loadtest provides a concurrent HTTP load generator and SLO verification engine.
package loadtest

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTargetSLO is the p95 latency target specified in ADR 0021 and architecture requirements.
const DefaultTargetSLO = 200 * time.Millisecond

// WeightedPath defines an HTTP route and its relative selection probability.
type WeightedPath struct {
	Path   string
	Method string // defaults to GET
	Weight int    // relative probability weight (must be > 0)
}

// Config controls the load test parameters.
type Config struct {
	BaseURL     string
	Paths       []WeightedPath
	Client      *http.Client
	Cookies     []*http.Cookie
	Headers     http.Header
	Concurrency int           // number of concurrent virtual users (workers)
	Duration    time.Duration // total run time
	MaxRequests int           // optional cap on total requests (0 for unlimited)
	TargetSLO   time.Duration // p95 latency SLO threshold (defaults to 200ms)
	Insecure    bool          // skip TLS certificate verification
}

// Sample records the outcome and duration of a single HTTP request.
type Sample struct {
	Path       string
	Method     string
	StatusCode int
	Duration   time.Duration
	Err        error
}

// PathStats holds latency and throughput statistics for an individual route.
type PathStats struct {
	Path         string
	Method       string
	TotalCount   int
	SuccessCount int
	ErrorCount   int
	StatusCounts map[int]int
	Min          time.Duration
	Mean         time.Duration
	P50          time.Duration
	P75          time.Duration
	P90          time.Duration
	P95          time.Duration
	P99          time.Duration
	Max          time.Duration
	RPS          float64
}

// Stats aggregates overall load test outcomes and verifies SLO compliance.
type Stats struct {
	BaseURL      string
	Concurrency  int
	Duration     time.Duration
	TotalCount   int
	SuccessCount int
	ErrorCount   int
	StatusCounts map[int]int
	RPS          float64
	Min          time.Duration
	Mean         time.Duration
	P50          time.Duration
	P75          time.Duration
	P90          time.Duration
	P95          time.Duration
	P99          time.Duration
	Max          time.Duration
	TargetSLO    time.Duration
	PassedSLO    bool
	PathStats    []PathStats
}

// Run executes concurrent load against the configured BaseURL and returns computed statistics.
func Run(ctx context.Context, cfg Config) (*Stats, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("loadtest: BaseURL is required")
	}
	baseURL, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("loadtest: invalid BaseURL %q: %w", cfg.BaseURL, err)
	}

	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	if cfg.Duration <= 0 && cfg.MaxRequests <= 0 {
		cfg.Duration = 10 * time.Second
	}
	if cfg.TargetSLO <= 0 {
		cfg.TargetSLO = DefaultTargetSLO
	}
	if len(cfg.Paths) == 0 {
		cfg.Paths = []WeightedPath{
			{Path: "/", Method: http.MethodGet, Weight: 1},
		}
	}

	client := cfg.Client
	if client == nil {
		tr := &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		}
		if cfg.Insecure {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // intentional for local dev testing
		}
		client = &http.Client{
			Transport: tr,
			Timeout:   15 * time.Second,
		}
	}

	// Prepare weighted selection
	totalWeight := 0
	for _, p := range cfg.Paths {
		w := p.Weight
		if w <= 0 {
			w = 1
		}
		totalWeight += w
	}

	selectPath := func() WeightedPath {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(totalWeight)))
		if err != nil {
			return cfg.Paths[0]
		}
		r := int(n.Int64())
		cum := 0
		for _, p := range cfg.Paths {
			w := p.Weight
			if w <= 0 {
				w = 1
			}
			cum += w
			if r < cum {
				return p
			}
		}
		return cfg.Paths[0]
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if cfg.Duration > 0 {
		runCtx, cancel = context.WithTimeout(ctx, cfg.Duration)
		defer cancel()
	}

	var reqCount atomic.Int64
	var mu sync.Mutex
	var samples []Sample

	start := time.Now()
	var wg sync.WaitGroup
	wg.Add(cfg.Concurrency)

	for i := 0; i < cfg.Concurrency; i++ {
		go func() {
			defer wg.Done()
			var localSamples []Sample

			for {
				select {
				case <-runCtx.Done():
					mu.Lock()
					samples = append(samples, localSamples...)
					mu.Unlock()
					return
				default:
				}

				if cfg.MaxRequests > 0 {
					current := reqCount.Add(1)
					if current > int64(cfg.MaxRequests) {
						mu.Lock()
						samples = append(samples, localSamples...)
						mu.Unlock()
						return
					}
				}

				wp := selectPath()
				method := wp.Method
				if method == "" {
					method = http.MethodGet
				}

				rel, err := url.Parse(wp.Path)
				if err != nil {
					localSamples = append(localSamples, Sample{
						Path:   wp.Path,
						Method: method,
						Err:    err,
					})
					continue
				}
				target := baseURL.ResolveReference(rel).String()

				req, err := http.NewRequestWithContext(runCtx, method, target, nil)
				if err != nil {
					localSamples = append(localSamples, Sample{
						Path:   wp.Path,
						Method: method,
						Err:    err,
					})
					continue
				}

				// Copy headers
				for k, vv := range cfg.Headers {
					for _, v := range vv {
						req.Header.Add(k, v)
					}
				}
				if req.Header.Get("User-Agent") == "" {
					req.Header.Set("User-Agent", "getafjob-loadtest/1.0")
				}
				if req.Header.Get("Sec-Fetch-Site") == "" {
					req.Header.Set("Sec-Fetch-Site", "same-origin")
				}

				// Add cookies
				for _, c := range cfg.Cookies {
					req.AddCookie(c)
				}

				reqStart := time.Now()
				resp, err := client.Do(req)
				took := time.Since(reqStart)

				// If the request was canceled because the test duration expired, ignore this truncated sample
				if runCtx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
					continue
				}

				s := Sample{
					Path:     wp.Path,
					Method:   method,
					Duration: took,
					Err:      err,
				}
				if err == nil {
					s.StatusCode = resp.StatusCode
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
				localSamples = append(localSamples, s)
			}
		}()
	}

	wg.Wait()
	totalElapsed := time.Since(start)

	return computeStats(cfg.BaseURL, cfg.Concurrency, cfg.TargetSLO, totalElapsed, samples), nil
}

func computeStats(baseURL string, concurrency int, targetSLO time.Duration, elapsed time.Duration, samples []Sample) *Stats {
	stats := &Stats{
		BaseURL:      baseURL,
		Concurrency:  concurrency,
		Duration:     elapsed,
		TotalCount:   len(samples),
		StatusCounts: make(map[int]int),
		TargetSLO:    targetSLO,
	}

	if len(samples) == 0 {
		return stats
	}

	if elapsed > 0 {
		stats.RPS = float64(stats.TotalCount) / elapsed.Seconds()
	}

	durations := make([]time.Duration, 0, len(samples))
	pathSamples := make(map[string][]Sample)

	var totalDuration time.Duration
	for _, s := range samples {
		pathKey := s.Method + " " + s.Path
		pathSamples[pathKey] = append(pathSamples[pathKey], s)

		if s.Err != nil || s.StatusCode >= 400 || s.StatusCode == 0 {
			stats.ErrorCount++
		} else {
			stats.SuccessCount++
		}
		stats.StatusCounts[s.StatusCode]++
		durations = append(durations, s.Duration)
		totalDuration += s.Duration
	}

	slices.Sort(durations)
	stats.Min = durations[0]
	stats.Max = durations[len(durations)-1]
	stats.Mean = totalDuration / time.Duration(len(durations))
	stats.P50 = percentile(durations, 50)
	stats.P75 = percentile(durations, 75)
	stats.P90 = percentile(durations, 90)
	stats.P95 = percentile(durations, 95)
	stats.P99 = percentile(durations, 99)

	// SLO passes if p95 latency is within target threshold and error rate < 1%
	errRate := float64(stats.ErrorCount) / float64(stats.TotalCount)
	stats.PassedSLO = stats.P95 <= stats.TargetSLO && errRate < 0.01

	// Per-path breakdown
	for key, ps := range pathSamples {
		parts := strings.SplitN(key, " ", 2)
		method, path := parts[0], parts[1]

		pDurations := make([]time.Duration, 0, len(ps))
		pStatuses := make(map[int]int)
		var pTotalDur time.Duration
		pSuccess := 0
		pErrors := 0

		for _, s := range ps {
			pDurations = append(pDurations, s.Duration)
			pStatuses[s.StatusCode]++
			pTotalDur += s.Duration
			if s.Err != nil || s.StatusCode >= 400 || s.StatusCode == 0 {
				pErrors++
			} else {
				pSuccess++
			}
		}

		slices.Sort(pDurations)
		pRPS := 0.0
		if elapsed > 0 {
			pRPS = float64(len(ps)) / elapsed.Seconds()
		}

		stats.PathStats = append(stats.PathStats, PathStats{
			Path:         path,
			Method:       method,
			TotalCount:   len(ps),
			SuccessCount: pSuccess,
			ErrorCount:   pErrors,
			StatusCounts: pStatuses,
			Min:          pDurations[0],
			Mean:         pTotalDur / time.Duration(len(pDurations)),
			P50:          percentile(pDurations, 50),
			P75:          percentile(pDurations, 75),
			P90:          percentile(pDurations, 90),
			P95:          percentile(pDurations, 95),
			P99:          percentile(pDurations, 99),
			Max:          pDurations[len(pDurations)-1],
			RPS:          pRPS,
		})
	}

	slices.SortFunc(stats.PathStats, func(a, b PathStats) int {
		if a.TotalCount != b.TotalCount {
			return b.TotalCount - a.TotalCount // descending by count
		}
		return strings.Compare(a.Path, b.Path)
	})

	return stats
}

func percentile(sorted []time.Duration, p int) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted) * p) / 100
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Summary returns a formatted report string summarizing the load test results.
func (s *Stats) Summary() string {
	var b strings.Builder

	sloStatus := "[ FAIL ]"
	if s.PassedSLO {
		sloStatus = "[ PASS ]"
	}

	successRate := 0.0
	if s.TotalCount > 0 {
		successRate = (float64(s.SuccessCount) / float64(s.TotalCount)) * 100.0
	}

	b.WriteString("================================================================================\n")
	b.WriteString("                        GETAFJOB LOAD TEST REPORT\n")
	b.WriteString("================================================================================\n")
	fmt.Fprintf(&b, "Target URL:     %s\n", s.BaseURL)
	fmt.Fprintf(&b, "Concurrency:    %d workers\n", s.Concurrency)
	fmt.Fprintf(&b, "Duration:       %s\n", s.Duration.Round(10*time.Millisecond))
	fmt.Fprintf(&b, "Total Requests: %d (%.1f req/s)\n", s.TotalCount, s.RPS)
	fmt.Fprintf(&b, "Success Rate:   %.1f%% (%d ok, %d err)\n", successRate, s.SuccessCount, s.ErrorCount)
	fmt.Fprintf(&b, "SLO Target:     p95 < %s -> %s (measured p95 = %s)\n",
		s.TargetSLO, sloStatus, s.P95.Round(100*time.Microsecond))

	b.WriteString("\nLATENCY PERCENTILES:\n")
	fmt.Fprintf(&b, "  Min:  %10s\n", s.Min.Round(100*time.Microsecond))
	fmt.Fprintf(&b, "  p50:  %10s\n", s.P50.Round(100*time.Microsecond))
	fmt.Fprintf(&b, "  p75:  %10s\n", s.P75.Round(100*time.Microsecond))
	fmt.Fprintf(&b, "  p90:  %10s\n", s.P90.Round(100*time.Microsecond))
	fmt.Fprintf(&b, "  p95:  %10s\n", s.P95.Round(100*time.Microsecond))
	fmt.Fprintf(&b, "  p99:  %10s\n", s.P99.Round(100*time.Microsecond))
	fmt.Fprintf(&b, "  Max:  %10s\n", s.Max.Round(100*time.Microsecond))

	if len(s.PathStats) > 0 {
		b.WriteString("\nBREAKDOWN BY ROUTE:\n")
		b.WriteString("  ROUTE                                  COUNT    RPS   p50      p95      STATUS\n")
		for _, ps := range s.PathStats {
			route := ps.Method + " " + ps.Path
			if len(route) > 37 {
				route = route[:34] + "..."
			}

			var statuses []string
			for code, cnt := range ps.StatusCounts {
				statuses = append(statuses, fmt.Sprintf("%d (%d)", code, cnt))
			}
			slices.Sort(statuses)

			fmt.Fprintf(&b, "  %-37s %6d %6.1f %8s %8s   %s\n",
				route, ps.TotalCount, ps.RPS,
				ps.P50.Round(100*time.Microsecond),
				ps.P95.Round(100*time.Microsecond),
				strings.Join(statuses, ", "),
			)
		}
	}
	b.WriteString("================================================================================\n")

	return b.String()
}
