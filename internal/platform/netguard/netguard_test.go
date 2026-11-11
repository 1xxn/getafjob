package netguard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestNonPublicURLs(t *testing.T) {
	for _, u := range []string{
		"http://localhost/", "http://127.0.0.1/", "http://10.0.0.1/", "http://172.16.0.1/", "http://192.168.1.1/",
		"http://100.64.0.1/", "http://169.254.169.254/latest/meta-data/", "http://0.0.0.0/",
		"http://[::1]/", "http://[::ffff:127.0.0.1]/", "http://[fe80::1]/", "http://[fd00::1]/",
		"http://[64:ff9b::7f00:1]/", "http://[2002:7f00:1::]/",
		"http://2130706433/", "http://127.1/", "http://0x7f.0.0.1/", "http://0177.0.0.1/",
		"http://router/", "http://printer.local/", "http://wiki.internal/",
		"javascript:alert(1)", "file:///etc/passwd", "ftp://example.com/", "http://256.1.1.1/", "",
		// read like Chromium (WHATWG URL, UTS 46)
		"http://127.0.0.1:8765\\@example.com/", "http:\\\\127.0.0.1\\", "http://127.0.0%2e1/",
		"http://x.localhos%74/", "http://x.ⓛocalhost:8765/", "http://foo.ｌｏｃａｌｈｏｓｔ/", "http://１２７.０.０.１/",
		"http://127.0.0.1．/", "http://127。0。0。1/", "http://127.0.0.⒈/", "http://x.local\u00adhost/",
		"\x00http://127.0.0.1/", "http://127.0.0.1%00.example.com/",
		"http://-1.0.0.1/", "http://0x-1.0.0.1/", "http://+127.0.0.1/", "http://1_27.0.0.1/",
	} {
		if PublicURL(u) {
			t.Errorf("PublicURL(%q) = true", u)
		}
	}
}

func TestPublicURLs(t *testing.T) {
	for _, u := range []string{
		"https://boards.greenhouse.io/acme/jobs/1", "http://8.8.8.8/", "https://[2606:4700::1111]/",
		"https://jobs.example.de/x", "https://bücher.example/jobs", "https://jobs.example.com/a\\b?next=\\@127.0.0.1",
		"https://jobs.example.com/%41", "https://user:pw@127.0.0.1@jobs.example.com/", " https://jobs.example.org ",
	} {
		if !PublicURL(u) {
			t.Errorf("PublicURL(%q) = false", u)
		}
	}
}

func TestHostIsWhatTheBrowserOpens(t *testing.T) {
	for raw, want := range map[string]string{
		"https://jobs.ｅｘａｍｐｌｅ.org/apply": "jobs.example.org",
		"https://Jobs.Example.org./x":    "jobs.example.org",
		"https://bücher.example/":        "xn--bcher-kva.example",
	} {
		if got, ok := Host(raw); !ok || got != want {
			t.Errorf("Host(%q) = %q, %v; want %q", raw, got, ok, want)
		}
	}
}

func TestPublicIP(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2606:4700::1111": true, "93.184.215.14": true,
		"127.0.0.1": false, "::1": false, "169.254.169.254": false, "224.0.0.1": false, "fe80::1%eth0": false,
		"::ffff:10.0.0.1": false, "64:ff9b::808:808": true, "64:ff9b::a00:1": false,
	} {
		if got := PublicIP(netip.MustParseAddr(addr)); got != want {
			t.Errorf("PublicIP(%s) = %v", addr, got)
		}
	}
}

func TestClientRefusesPrivateTargetsPerConnection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	defer srv.Close()

	// The dialer checks the address it connects to, also when the URL check is skipped (redirect, rebinding).
	c := NewClient(ClientOptions{})
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if _, err := c.Do(req); err == nil || !errors.Is(err, ErrBlocked) {
		t.Fatalf("private connection: err = %v", err)
	}
	if _, err := Get(t.Context(), c, srv.URL, false); !errors.Is(err, ErrBlocked) {
		t.Fatalf("Get private URL: err = %v", err)
	}

	// Tests with local servers opt out explicitly.
	resp, err := Get(t.Context(), NewClient(ClientOptions{AllowPrivate: true}), srv.URL, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if b, _ := ReadLimited(resp.Body, 10); string(b) != "ok" {
		t.Fatalf("body = %q", b)
	}
}

func TestRedirectToPrivateIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.RedirectHandler("http://169.254.169.254/latest/meta-data/", http.StatusFound))
	defer srv.Close()
	c := NewClient(ClientOptions{AllowPrivate: true})
	c.CheckRedirect = NewClient(ClientOptions{}).CheckRedirect
	_, err := Get(t.Context(), c, srv.URL, true)
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v", err)
	}
}

func TestReadLimited(t *testing.T) {
	if _, err := ReadLimited(strings.NewReader("12345"), 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
	if b, err := ReadLimited(strings.NewReader("1234"), 4); err != nil || string(b) != "1234" {
		t.Fatalf("b = %q, err = %v", b, err)
	}
}

func TestCanonicalURL(t *testing.T) {
	for _, tc := range [][2]string{
		{"https://Jobs.Example.com/a?b=1#top", "https://jobs.example.com/a?b=1"},
		{"https://user:pw@jobs.example.com/x", "https://jobs.example.com/x"},
		{"http://jobs.example.com:80/x", "http://jobs.example.com/x"},
		{"https://jobs.example.com:443", "https://jobs.example.com"},
		{"https://bücher.example/jobs", "https://xn--bcher-kva.example/jobs"},
		{"http://0x08080808/", "http://8.8.8.8/"},
		{" https://jobs.example.org/a b ", "https://jobs.example.org/a%20b"},
		{`https://jobs.example.com/a\b?next=x`, "https://jobs.example.com/a/b?next=x"},
	} {
		raw, want := tc[0], tc[1]
		got, err := CanonicalURL(raw)
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", raw, got, err, want)
		}
		if !canonicalURL.MatchString(got) {
			t.Errorf("%q: canonical form %q does not match the request pattern", raw, got)
		}
	}
	for _, raw := range []string{
		"https://jobs.example.com:8443/", "http://jobs.example.com:443/", "https://jobs.example.com:22/",
		"https://[2606:4700:4700::1111]/", "ftp://jobs.example.com/", "http://127.0.0.1/", "http://localhost/",
		"http://169.254.169.254/latest/meta-data/", "http://2130706433/", "https://jobs.example.com:0/",
	} {
		if got, err := CanonicalURL(raw); !errors.Is(err, ErrBlocked) {
			t.Errorf("%q: %q %v, want ErrBlocked", raw, got, err)
		}
	}
}

func TestGetRefusesOtherPortsAndRedirectsThere(t *testing.T) {
	if _, err := Get(t.Context(), NewClient(ClientOptions{}), "https://jobs.example.com:8443/", false); !errors.Is(err, ErrBlocked) {
		t.Fatalf("other port: %v", err)
	}
	if _, err := Get(t.Context(), NewClient(ClientOptions{AllowPrivate: true}), "http://10.0.0.1:8080/", true); !errors.Is(err, ErrBlocked) {
		t.Fatalf("test mode admits only loopback: %v", err)
	}
	check := NewClient(ClientOptions{}).CheckRedirect
	for target, blocked := range map[string]bool{
		"https://jobs.example.com:8443/": true, "https://jobs.example.com/careers": false, "http://10.0.0.1/": true,
	} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		if err := check(req, nil); errors.Is(err, ErrBlocked) != blocked {
			t.Errorf("redirect to %s: %v", target, err)
		}
	}
}
