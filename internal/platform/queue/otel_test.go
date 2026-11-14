package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river/rivertype"
	gotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestTelemetryMiddleware(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	prev := gotel.GetTracerProvider()
	gotel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)))
	t.Cleanup(func() { gotel.SetTracerProvider(prev) })

	mw, err := newTelemetry()
	if err != nil {
		t.Fatal(err)
	}
	job := &rivertype.JobRow{Kind: "crawl", Queue: Crawl, Attempt: 1}
	inner := func(ctx context.Context) error {
		if !trace.SpanContextFromContext(ctx).IsValid() {
			t.Error("job context has no span")
		}
		return nil
	}
	if err := mw.Work(context.Background(), job, inner); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("secret detail")
	if err := mw.Work(context.Background(), job, func(context.Context) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	spans := rec.Ended()
	if len(spans) != 2 || spans[0].Name() != "job crawl" || spans[1].Status().Code != codes.Error ||
		spans[1].Status().Description == boom.Error() {
		t.Errorf("spans = %+v", spans)
	}
}

func TestPauseCancelsJobs(t *testing.T) {
	paused := true
	mw := &pause{paused: func(context.Context) bool { return paused }}
	ran := 0
	inner := func(context.Context) error { ran++; return nil }
	job := &rivertype.JobRow{Kind: "crawl"}
	var cancel *rivertype.JobCancelError
	if err := mw.Work(context.Background(), job, inner); !errors.As(err, &cancel) || !errors.Is(err, ErrPaused) || ran != 0 {
		t.Fatalf("paused: err = %v, ran = %d", err, ran)
	}
	paused = false
	if err := mw.Work(context.Background(), job, inner); err != nil || ran != 1 {
		t.Fatalf("running: err = %v, ran = %d", err, ran)
	}
}
