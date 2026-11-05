package otel_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	gotel "go.opentelemetry.io/otel"

	"github.com/1xxn/getafjob/internal/platform/otel"
)

func TestSetupOff(t *testing.T) {
	shutdown, err := otel.Setup(t.Context(), nil, "getafjob-test")
	if err != nil || shutdown(t.Context()) != nil {
		t.Fatalf("setup without endpoint: %v", err)
	}
}

func TestSetupExports(t *testing.T) {
	var mu sync.Mutex
	got := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	shutdown, err := otel.Setup(t.Context(), u, "getafjob-test")
	if err != nil {
		t.Fatal(err)
	}
	_, span := gotel.Tracer("test").Start(context.Background(), "work")
	span.End()
	ctr, err := gotel.Meter("test").Int64Counter("test.count")
	if err != nil {
		t.Fatal(err)
	}
	ctr.Add(context.Background(), 1)
	if err := shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if got["/v1/traces"] == 0 || got["/v1/metrics"] == 0 {
		t.Errorf("exports = %v", got)
	}
}
