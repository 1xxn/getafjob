package app

import (
	"bufio"
	"context"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

// The probe counts any HTTP answer to its CONNECT as a running proxy, also a refusal; a closed port is not one.
func TestProbeProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		line, _ := bufio.NewReader(c).ReadString('\n')
		got <- line
		_, _ = c.Write([]byte("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n"))
	}()
	if !probeProxy(t.Context(), &url.URL{Scheme: "http", Host: ln.Addr().String()}) {
		t.Error("a proxy that refuses counts as reachable")
	}
	if line := <-got; line != "CONNECT probe.invalid:443 HTTP/1.1\r\n" {
		t.Errorf("request line %q", line)
	}

	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := closed.Addr().String()
	_ = closed.Close()
	start := time.Now()
	if probeProxy(t.Context(), &url.URL{Scheme: "http", Host: addr}) {
		t.Error("closed port counts as reachable")
	}
	if time.Since(start) > probeTimeout+time.Second {
		t.Error("probe ignored its timeout")
	}
}

// A recorded backup shows as the last one; the proxy is only probed when one is configured.
func TestSystemFactsBackupAndProxy(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	base, _ := url.Parse("https://jobs.example.org")
	f := systemFacts{pool: pool, cfg: config.Config{PublicURL: base}}
	got, err := f.Facts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastBackup.IsZero() || got.EgressReachable != nil {
		t.Fatalf("empty system: %+v", got)
	}
	if err := recordBackup(ctx, pool, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := recordBackup(ctx, pool, 2<<20); err != nil {
		t.Fatal(err)
	}
	proxy, _ := url.Parse("http://egress:4750")
	f.cfg.EgressProxy = proxy
	f.probe = func(context.Context, *url.URL) bool { return true }
	if got, err = f.Facts(ctx); err != nil {
		t.Fatal(err)
	}
	if got.LastBackupBytes != 2<<20 || time.Since(got.LastBackup) > time.Minute {
		t.Errorf("last backup %v, %d bytes", got.LastBackup, got.LastBackupBytes)
	}
	if got.EgressReachable == nil || !*got.EgressReachable {
		t.Error("proxy not probed")
	}
}
