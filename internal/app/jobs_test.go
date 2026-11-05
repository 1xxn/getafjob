package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
)

type fakeBoards []discovery.Board

func (f fakeBoards) ImportBoards(context.Context, []discovery.Board) (int, int, error) {
	return 0, 0, nil
}

func (f fakeBoards) EnabledBoards(context.Context) ([]discovery.Board, error) { return f, nil }

func names(srcs []discovery.Source) string {
	out := make([]string, len(srcs))
	for i, s := range srcs {
		out[i] = s.Name()
	}
	return strings.Join(out, " ")
}

func TestCrawlSources(t *testing.T) {
	c := sources.NewClient(http.DefaultClient)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	boards := fakeBoards{
		{ID: 1, ATS: "greenhouse", Slug: "acme", Company: "ACME"},
		{ID: 2, ATS: "personio", Slug: "evil.example.org/x", Company: "Bad"}, // skipped
		{ID: 3, ATS: "workday", Slug: "muster", Company: "Muster", Config: []byte(`{"host":"muster.wd3.myworkdayjobs.com","site":"External"}`)},
	}
	cases := []struct {
		cfg  config.Config
		want string
	}{
		{config.Config{}, "google apple:germany-DEU arbeitnow greenhouse:acme workday:muster/External"},
		{config.Config{CrawlQueries: []string{"Go"}}, "google apple:germany-DEU arbeitnow himalayas amazon greenhouse:acme workday:muster/External"},
		{config.Config{CrawlQueries: []string{"Go"}, CrawlCities: []string{"Berlin"}},
			"google apple:germany-DEU arbeitnow himalayas amazon arbeitsagentur greenhouse:acme workday:muster/External"},
	}
	for _, tc := range cases {
		got, err := crawlSources(context.Background(), c, boards, tc.cfg, logger)
		if err != nil || names(got) != tc.want {
			t.Errorf("sources = %q, %v; want %q", names(got), err, tc.want)
		}
	}
}

func TestAdminCrawl(t *testing.T) {
	url, pw := dbtest.AppURL(t)
	vars := map[string]string{
		"GETAFJOB_DATABASE_URL":      url.Reveal(),
		"GETAFJOB_DATABASE_PASSWORD": pw.Reveal(),
	}
	for i, want := range []string{"crawl queued (job ", "a manual crawl is already queued or running"} {
		env, out, errb := testEnv(vars)
		if code := Main(t.Context(), []string{"admin", "crawl"}, env); code != ExitOK || !strings.HasPrefix(out.String(), want) {
			t.Fatalf("run %d: code %d, out %q, err %q", i+1, code, out.String(), errb.String())
		}
	}
}
