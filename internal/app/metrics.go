package app

import (
	"context"
	"time"

	gotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/1xxn/getafjob/internal/discovery"
)

const metricsScope = "github.com/1xxn/getafjob/internal/discovery"

// crawlMetrics turns crawl and board discovery reports into metrics (one sample per run).
type crawlMetrics struct {
	duration     metric.Float64Histogram
	postings     metric.Int64Counter // fetched, invalid, duplicate, seen, details, details_failed, closed
	changes      metric.Int64Counter // by change
	sources      metric.Int64Counter // by outcome: ok, not_modified, error
	sourceErrors metric.Int64Counter // by code
	boardChecks  metric.Int64Counter // by result
	boardsAdded  metric.Int64Counter
}

func newCrawlMetrics() (*crawlMetrics, error) {
	m := gotel.Meter(metricsScope)
	var c crawlMetrics
	var err error
	counter := func(name, desc string) metric.Int64Counter {
		if err != nil {
			return nil
		}
		var ctr metric.Int64Counter
		ctr, err = m.Int64Counter(name, metric.WithDescription(desc))
		return ctr
	}
	c.postings = counter("getafjob.crawl.postings", "Postings per crawl step.")
	c.changes = counter("getafjob.crawl.changes", "Index changes by kind.")
	c.sources = counter("getafjob.crawl.sources", "Fetched sources by outcome.")
	c.sourceErrors = counter("getafjob.crawl.source_errors", "Failed sources by error code.")
	c.boardChecks = counter("getafjob.boards.checks", "Board discovery checks by result.")
	c.boardsAdded = counter("getafjob.boards.added", "Boards added by discovery.")
	if err != nil {
		return nil, err
	}
	c.duration, err = m.Float64Histogram("getafjob.crawl.duration", metric.WithUnit("s"),
		metric.WithDescription("Duration of a crawl."))
	return &c, err
}

func step(name string) metric.AddOption { return metric.WithAttributes(attribute.String("step", name)) }

func (c *crawlMetrics) crawl(ctx context.Context, rep discovery.Report, sources int, took time.Duration) {
	c.duration.Record(ctx, took.Seconds())
	st := rep.Stats
	for name, n := range map[string]int{
		"fetched": st.Fetched, "invalid": st.Invalid, "duplicate": st.Duplicates, "seen": st.Seen,
		"details": st.Details, "details_failed": st.DetailsFailed, "closed": st.Closed,
	} {
		c.postings.Add(ctx, int64(n), step(name))
	}
	for ch, n := range st.Changes {
		c.changes.Add(ctx, int64(n), metric.WithAttributes(attribute.String("change", string(ch))))
	}
	outcome := func(o string) metric.AddOption { return metric.WithAttributes(attribute.String("outcome", o)) }
	c.sources.Add(ctx, int64(sources-len(rep.Errors)-st.NotModified), outcome("ok"))
	c.sources.Add(ctx, int64(st.NotModified), outcome("not_modified"))
	c.sources.Add(ctx, int64(len(rep.Errors)), outcome("error"))
	codes := map[string]int64{}
	for _, code := range rep.Errors {
		codes[code]++
	}
	for code, n := range codes {
		c.sourceErrors.Add(ctx, n, metric.WithAttributes(attribute.String("code", code)))
	}
}

func (c *crawlMetrics) boards(ctx context.Context, st discovery.DiscoveryStats) {
	result := func(r discovery.CheckResult) metric.AddOption {
		return metric.WithAttributes(attribute.String("result", string(r)))
	}
	c.boardChecks.Add(ctx, int64(st.Found), result(discovery.CheckFound))
	c.boardChecks.Add(ctx, int64(st.Errors), result(discovery.CheckError))
	c.boardChecks.Add(ctx, int64(st.Checked-st.Found-st.Errors), result(discovery.CheckNone))
	c.boardsAdded.Add(ctx, int64(st.Added))
}
