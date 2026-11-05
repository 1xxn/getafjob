package app

import (
	"context"
	"testing"
	"time"

	gotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/1xxn/getafjob/internal/discovery"
)

// sums collects the sums of a counter by one attribute.
func sums(t *testing.T, rm metricdata.ResourceMetrics, name, key string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
				v, _ := dp.Attributes.Value(attribute.Key(key))
				out[v.AsString()] += dp.Value
			}
		}
	}
	return out
}

func TestCrawlMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := gotel.GetMeterProvider()
	gotel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { gotel.SetMeterProvider(prev) })

	m, err := newCrawlMetrics()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m.crawl(ctx, discovery.Report{
		Stats: discovery.Stats{Fetched: 10, Invalid: 1, Seen: 3, NotModified: 2, Closed: 1,
			Changes: map[discovery.Change]int{discovery.ChangeNew: 4, discovery.ChangeUpdated: 2}},
		Errors: map[string]string{"lever:a": "http_503", "ashby:b": "http_503", "greenhouse:c": "timeout"},
	}, 10, 90*time.Second)
	m.boards(ctx, discovery.DiscoveryStats{Checked: 5, Found: 2, Added: 1, Errors: 1})

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	check := func(name, key string, want map[string]int64) {
		t.Helper()
		got := sums(t, rm, name, key)
		for k, v := range want {
			if got[k] != v {
				t.Errorf("%s{%s=%s} = %d, want %d", name, key, k, got[k], v)
			}
		}
	}
	check("getafjob.crawl.postings", "step", map[string]int64{"fetched": 10, "invalid": 1, "seen": 3, "closed": 1})
	check("getafjob.crawl.changes", "change", map[string]int64{"new": 4, "updated": 2})
	check("getafjob.crawl.sources", "outcome", map[string]int64{"ok": 5, "not_modified": 2, "error": 3})
	check("getafjob.crawl.source_errors", "code", map[string]int64{"http_503": 2, "timeout": 1})
	check("getafjob.boards.checks", "result", map[string]int64{"found": 2, "none": 2, "error": 1})
	check("getafjob.boards.added", "", map[string]int64{"": 1})
}
