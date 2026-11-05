// Package otel sets up OpenTelemetry (ADR 0021): traces and metrics over OTLP/HTTP to a collector. Without an
// endpoint nothing is installed and the API stays a no-op, so code can always create spans and instruments.
package otel

import (
	"context"
	"errors"
	"net/url"
	"time"

	gotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"

	"github.com/1xxn/getafjob/internal/platform/buildinfo"
)

// MetricInterval is how often metrics are exported.
const MetricInterval = 30 * time.Second

// Setup installs the global tracer and meter providers for service (getafjob-serve, getafjob-worker). shutdown
// flushes and stops them; with a nil endpoint it does nothing.
func Setup(ctx context.Context, endpoint *url.URL, service string) (shutdown func(context.Context) error, err error) {
	if endpoint == nil {
		return func(context.Context) error { return nil }, nil
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName(service), semconv.ServiceVersion(buildinfo.Get().Version)))
	if err != nil {
		return nil, err
	}
	base := endpoint.String()
	traces, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(base+"/v1/traces"))
	if err != nil {
		return nil, err
	}
	metrics, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(base+"/v1/metrics"))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traces), sdktrace.WithResource(res))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics, sdkmetric.WithInterval(MetricInterval))))
	gotel.SetTracerProvider(tp)
	gotel.SetMeterProvider(mp)
	gotel.SetTextMapPropagator(propagation.TraceContext{})
	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}
