package netguard

import (
	"net/netip"
	"strings"
	"testing"
)

// FuzzPublicURL: never panics, and a URL counted as public never resolves to a literal non-public address.
func FuzzPublicURL(f *testing.F) {
	for _, s := range []string{
		"https://jobs.example.com/x", "http://127.0.0.1/", "http://0x7f.1/", "http://[::ffff:127.0.0.1]/",
		"http://x.ⓛocalhost/", "http://１２７.０.０.１/", "http://127.0.0.1:1\\@a.b/", "https://user@a.b:443/x?y#z",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		host, ok := Host(raw)
		if ok != PublicURL(raw) {
			t.Fatal("Host and PublicURL disagree")
		}
		if !ok {
			return
		}
		if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") {
			t.Fatalf("public host %q", host)
		}
		if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil && !PublicIP(ip) {
			t.Fatalf("public URL with private literal %q", host)
		}
		if ip, kind := browserIPv4(host); kind == ipV4 && !PublicIP(ip) {
			t.Fatalf("public URL with private IPv4 %q", host)
		}
	})
}

// FuzzBrowserIPv4 compares dotted-decimal input with the standard parser.
func FuzzBrowserIPv4(f *testing.F) {
	for _, s := range []string{"1.2.3.4", "127.0.0.1", "255.255.255.255", "0.0.0.0", "10.0.0.256"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		std, err := netip.ParseAddr(s)
		if err != nil || !std.Is4() {
			_, _ = browserIPv4(s) // must not panic
			return
		}
		got, kind := browserIPv4(s)
		if kind != ipV4 || got != std {
			t.Fatalf("browserIPv4(%q) = %v %v, want %v", s, got, kind, std)
		}
	})
}
