package sources_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
)

type portalRoundTripper func(*http.Request) (*http.Response, error)

func (f portalRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestPortalsConditional(t *testing.T) {
	for _, portalName := range []string{"linkedin", "indeed", "glassdoor"} {
		t.Run(portalName, func(t *testing.T) {
			var gotHeader http.Header
			rt := portalRoundTripper(func(r *http.Request) (*http.Response, error) {
				gotHeader = r.Header.Clone()
				return &http.Response{
					StatusCode: http.StatusNotModified,
					Header:     http.Header{"ETag": {`"etag-123"`}},
					Body:       io.NopCloser(strings.NewReader("")),
					Request:    r,
				}, nil
			})
			c := sources.NewClient(&http.Client{Transport: rt}).WithoutWaiting()
			var src discovery.Source
			switch portalName {
			case "linkedin":
				s := sources.NewLinkedIn(c, []string{"golang"}, "Germany")
				s.MaxPages = 1
				src = s
			case "indeed":
				s := sources.NewIndeed(c, []string{"golang"}, "Germany")
				s.MaxPages = 1
				src = s
			case "glassdoor":
				s := sources.NewGlassdoor(c, []string{"golang"}, "Germany")
				s.MaxPages = 1
				src = s
			}

			cond, ok := src.(discovery.Conditional)
			if !ok {
				t.Fatalf("%s does not implement discovery.Conditional", portalName)
			}
			cond.SetValidators(discovery.Validators{ETag: `"etag-123"`})

			batch, err := src.Fetch(context.Background())
			if err != nil {
				t.Fatalf("fetch: %v", err)
			}
			if !batch.NotModified {
				t.Errorf("expected NotModified=true, got %+v", batch)
			}
			if batch.Validators.ETag != `"etag-123"` {
				t.Errorf("expected ETag preserved, got %q", batch.Validators.ETag)
			}
			if gotHeader.Get("If-None-Match") != `"etag-123"` {
				t.Errorf("expected If-None-Match header, got %q", gotHeader.Get("If-None-Match"))
			}
		})
	}
}

func TestPortalsThrottled(t *testing.T) {
	// First request succeeds with 1 item, second request returns 429 -> should keep the first item.
	callCount := 0
	rt := portalRoundTripper(func(r *http.Request) (*http.Response, error) {
		callCount++
		if callCount == 1 {
			body := `<ul class="jobs-search__results-list">
				<li><div data-entity-urn="urn:li:jobPosting:9001"><h3 class="base-search-card__title">Go Dev</h3></div></li>
			</ul>`
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader(body)),
				Request:    r,
			}, nil
		}
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader("too many requests")),
			Request:    r,
		}, nil
	})
	c := sources.NewClient(&http.Client{Transport: rt}).WithoutWaiting()
	s := sources.NewLinkedIn(c, []string{"golang"}, "Germany")
	s.MaxPages = 2
	s.Pause = 0

	batch, err := s.Fetch(context.Background())
	if err != nil {
		t.Fatalf("expected graceful 429 recovery, got err: %v", err)
	}
	if len(batch.Postings) != 1 || batch.Postings[0].ExternalID != "9001" {
		t.Errorf("expected 1 posting from first page, got %+v", batch.Postings)
	}
}

func TestPortalsEmptyQueries(t *testing.T) {
	c := sources.NewClient(http.DefaultClient)
	for _, src := range []discovery.Source{
		sources.NewLinkedIn(c, nil, ""),
		sources.NewIndeed(c, []string{}, ""),
		sources.NewGlassdoor(c, nil, ""),
	} {
		batch, err := src.Fetch(context.Background())
		if err != nil || len(batch.Postings) != 0 {
			t.Errorf("%s empty queries: batch=%+v, err=%v", src.Name(), batch, err)
		}
	}
}

func TestPortalsDirectJSON(t *testing.T) {
	// Test Indeed GraphQL/API direct JSON response
	indeedJSON := `{
		"results": [
			{
				"jobkey": "ind_json_1",
				"title": "Backend Lead",
				"company": "Komet AG",
				"formattedLocation": "Hamburg, Germany",
				"date": "2026-11-03",
				"jobDescriptionText": "Distributed backend systems in Go."
			}
		]
	}`
	rtIndeed := portalRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(indeedJSON)),
			Request:    r,
		}, nil
	})
	cIndeed := sources.NewClient(&http.Client{Transport: rtIndeed}).WithoutWaiting()
	sIndeed := sources.NewIndeed(cIndeed, []string{"golang"}, "Germany")
	sIndeed.MaxPages = 1
	batchIndeed, err := sIndeed.Fetch(context.Background())
	if err != nil || len(batchIndeed.Postings) != 1 {
		t.Fatalf("indeed direct json: %+v, %v", batchIndeed, err)
	}
	if p := batchIndeed.Postings[0]; p.ExternalID != "ind_json_1" || p.Company != "Komet AG" || p.Title != "Backend Lead" {
		t.Errorf("unexpected indeed posting: %+v", p)
	}

	// Test Glassdoor direct JSON response
	glassdoorJSON := `{
		"data": {
			"jobListings": [
				{
					"listingId": 2009000000001,
					"jobTitle": "Security Engineer (Remote)",
					"employerName": "Shield GmbH",
					"location": "Berlin, Germany",
					"datePosted": "2026-11-03",
					"description": "Cloud security infrastructure."
				}
			]
		}
	}`
	rtGlassdoor := portalRoundTripper(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(glassdoorJSON)),
			Request:    r,
		}, nil
	})
	cGlassdoor := sources.NewClient(&http.Client{Transport: rtGlassdoor}).WithoutWaiting()
	sGlassdoor := sources.NewGlassdoor(cGlassdoor, []string{"golang"}, "Germany")
	sGlassdoor.MaxPages = 1
	batchGlassdoor, err := sGlassdoor.Fetch(context.Background())
	if err != nil || len(batchGlassdoor.Postings) != 1 {
		t.Fatalf("glassdoor direct json: %+v, %v", batchGlassdoor, err)
	}
	if p := batchGlassdoor.Postings[0]; p.ExternalID != "2009000000001" || p.Company != "Shield GmbH" || p.Workplace != discovery.WorkplaceRemote {
		t.Errorf("unexpected glassdoor posting: %+v", p)
	}
}
