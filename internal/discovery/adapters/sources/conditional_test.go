package sources_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
)

// etagServer answers 304 when If-None-Match carries its ETag, otherwise 200 with body and the ETag.
type etagServer struct {
	etag, lastModified, body string
	got                      []http.Header
}

func (s *etagServer) RoundTrip(req *http.Request) (*http.Response, error) {
	s.got = append(s.got, req.Header.Clone())
	h := http.Header{}
	if s.etag != "" {
		h.Set("ETag", s.etag)
	}
	if s.lastModified != "" {
		h.Set("Last-Modified", s.lastModified)
	}
	code := http.StatusOK
	if inm := req.Header.Get("If-None-Match"); inm != "" && inm == s.etag {
		code, h = http.StatusNotModified, http.Header{} // some servers repeat no validators on 304
	}
	return &http.Response{StatusCode: code, Header: h, Body: io.NopCloser(strings.NewReader(s.body)), Request: req}, nil
}

func TestConditionalFetch(t *testing.T) {
	srv := &etagServer{etag: `W/"v1"`, lastModified: "Wed, 04 Nov 2026 10:00:00 GMT",
		body: `{"jobs":[{"id":1,"title":"Go Developer","absolute_url":"https://boards.greenhouse.io/acme/jobs/1"}]}`}
	c := sources.NewClient(&http.Client{Transport: srv}).WithoutWaiting()
	src, err := sources.FromBoard(c, sources.Board{ATS: "greenhouse", Slug: "acme", Company: "ACME"})
	if err != nil {
		t.Fatal(err)
	}

	first, err := src.Fetch(t.Context())
	if err != nil || first.NotModified || len(first.Postings) != 1 || first.Validators.ETag != `W/"v1"` ||
		first.Validators.LastModified != "Wed, 04 Nov 2026 10:00:00 GMT" {
		t.Fatalf("first = %+v, %v", first, err)
	}
	if h := srv.got[0]; h.Get("If-None-Match") != "" || h.Get("If-Modified-Since") != "" {
		t.Errorf("unconditional request sent validators: %v", h)
	}

	src.(discovery.Conditional).SetValidators(first.Validators)
	second, err := src.Fetch(t.Context())
	if err != nil || !second.NotModified || len(second.Postings) != 0 || second.Validators != first.Validators {
		t.Fatalf("second = %+v, %v (304 keeps the validators)", second, err)
	}
	if h := srv.got[1]; h.Get("If-None-Match") != `W/"v1"` || h.Get("If-Modified-Since") != first.Validators.LastModified {
		t.Errorf("conditional headers = %v", h)
	}

	srv.etag = `W/"v2"`
	third, err := src.Fetch(t.Context())
	if err != nil || third.NotModified || len(third.Postings) != 1 || third.Validators.ETag != `W/"v2"` {
		t.Fatalf("changed = %+v, %v", third, err)
	}

	// An oversized validator is not kept.
	srv.etag = strings.Repeat("e", 300)
	if _, v, _, err := c.GetConditional(t.Context(), "x", "https://boards-api.greenhouse.io/x", nil, "*/*", discovery.Validators{}); err != nil || v.ETag != "" {
		t.Errorf("long etag kept: %q, %v", v.ETag, err)
	}
}

func TestConditionalSources(t *testing.T) {
	for _, b := range []sources.Board{
		{ATS: "greenhouse", Slug: "acme", Company: "ACME"}, {ATS: "ashby", Slug: "acme"}, {ATS: "lever", Slug: "acme"},
		{ATS: "personio", Slug: "acme"},
	} {
		srv := &etagServer{etag: `"x"`}
		c := sources.NewClient(&http.Client{Transport: srv}).WithoutWaiting()
		src, err := sources.FromBoard(c, b)
		if err != nil {
			t.Fatal(err)
		}
		cs, ok := src.(discovery.Conditional)
		if !ok {
			t.Fatalf("%s is not conditional", b.ATS)
		}
		cs.SetValidators(discovery.Validators{ETag: `"x"`})
		if got, err := src.Fetch(t.Context()); err != nil || !got.NotModified {
			t.Errorf("%s: %+v, %v", b.ATS, got, err)
		}
	}
}
