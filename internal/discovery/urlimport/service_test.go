package urlimport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/discovery"
)

type mockDiscovery struct {
	posted []discovery.Posting
	latest *discovery.CrawlRun
	active *discovery.ActiveRun
}

func (m *mockDiscovery) UpsertPosting(_ context.Context, p discovery.Posting, _ time.Time) (int64, discovery.Change, error) {
	m.posted = append(m.posted, p)
	return int64(len(m.posted)), discovery.ChangeNew, nil
}

func (m *mockDiscovery) LatestCrawlRun(_ context.Context) (*discovery.CrawlRun, error) {
	return m.latest, nil
}

func (m *mockDiscovery) ActiveCrawlRun(_ context.Context, _ time.Time) (*discovery.ActiveRun, error) {
	return m.active, nil
}

type mockBoards struct {
	imported []discovery.Board
}

func (m *mockBoards) ImportBoards(_ context.Context, boards []discovery.Board) (int, int, error) {
	m.imported = append(m.imported, boards...)
	return len(boards), 0, nil
}

func (m *mockBoards) EnabledBoards(_ context.Context) ([]discovery.Board, error) {
	return m.imported, nil
}

type mockMatcher struct {
	evaluated []discovery.Posting
}

func (m *mockMatcher) EvaluateAndSave(_ context.Context, _ uuid.UUID, _ int64, p discovery.Posting, _ time.Time) error {
	m.evaluated = append(m.evaluated, p)
	return nil
}

type mockQueue struct {
	queued bool
}

func (m *mockQueue) QueueCrawl(_ context.Context) (bool, error) {
	m.queued = true
	return true, nil
}

func (m *mockQueue) IsQueued(_ context.Context) (bool, error) {
	return m.queued, nil
}

func TestService_ImportJob(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
  <title>Senior Go Developer | ACME Robotics</title>
  <meta property="og:description" content="Design microservices in Go. €75,000 – €95,000 p.a." />
  <meta property="job:location" content="Berlin" />
</head>
<body>
  <h1>Senior Go Developer</h1>
</body>
</html>`))
	}))
	defer ts.Close()

	disc := &mockDiscovery{}
	matcher := &mockMatcher{}

	svc := NewService(Options{
		Client:       ts.Client(),
		AllowPrivate: true,
		Discovery:    disc,
		Matcher:      matcher,
	})

	uID := uuid.New()
	jobID, err := svc.ImportJob(t.Context(), uID, ts.URL+"/jobs/42")
	if err != nil {
		t.Fatalf("ImportJob failed: %v", err)
	}

	if jobID != 1 {
		t.Errorf("jobID = %d, want 1", jobID)
	}
	if len(disc.posted) != 1 {
		t.Fatalf("posted len = %d, want 1", len(disc.posted))
	}
	p := disc.posted[0]
	if p.Title != "Senior Go Developer" {
		t.Errorf("Title = %q, want 'Senior Go Developer'", p.Title)
	}
	if p.Company != "ACME Robotics" {
		t.Errorf("Company = %q, want 'ACME Robotics'", p.Company)
	}
	if p.SalaryMin != 75000 || p.SalaryMax != 95000 {
		t.Errorf("Salary = %d - %d, want 75000 - 95000", p.SalaryMin, p.SalaryMax)
	}
	if len(matcher.evaluated) != 1 {
		t.Fatalf("evaluated = %d, want 1", len(matcher.evaluated))
	}
}

func TestService_CrawlControls(t *testing.T) {
	now := time.Date(2026, 11, 9, 14, 0, 0, 0, time.UTC)
	lastRun := now.Add(-15 * time.Minute)
	disc := &mockDiscovery{
		latest: &discovery.CrawlRun{
			ID:         10,
			FinishedAt: &lastRun,
			Stats: discovery.Stats{
				Fetched: 50,
			},
		},
	}
	queue := &mockQueue{}

	svc := NewService(Options{
		Discovery: disc,
		Queue:     queue,
		Now:       func() time.Time { return now },
	})

	st, err := svc.CrawlStatus(t.Context())
	if err != nil {
		t.Fatalf("CrawlStatus error: %v", err)
	}
	if st.Running || st.Queued {
		t.Errorf("expected idle, got running=%v queued=%v", st.Running, st.Queued)
	}
	if st.LastRunAt == nil || *st.LastRunAt != lastRun {
		t.Errorf("LastRunAt = %v, want %v", st.LastRunAt, lastRun)
	}
	if st.LastStats.Fetched != 50 {
		t.Errorf("Fetched = %d, want 50", st.LastStats.Fetched)
	}

	queued, err := svc.TriggerCrawl(t.Context())
	if err != nil || !queued {
		t.Fatalf("TriggerCrawl failed: %v", err)
	}

	st, err = svc.CrawlStatus(t.Context())
	if err != nil {
		t.Fatalf("CrawlStatus error: %v", err)
	}
	if !st.Queued {
		t.Errorf("expected Queued = true after trigger")
	}
}

func TestService_AddBoard(t *testing.T) {
	boards := &mockBoards{}
	svc := NewService(Options{
		Boards: boards,
	})

	// 1. Direct Lever ATS URL
	b, err := svc.AddBoard(t.Context(), "https://jobs.lever.co/musterfirma")
	if err != nil {
		t.Fatalf("AddBoard Lever failed: %v", err)
	}
	if b.ATS != "lever" || b.Slug != "musterfirma" {
		t.Errorf("Board = %+v, want lever / musterfirma", b)
	}
	if len(boards.imported) != 1 {
		t.Errorf("imported boards = %d, want 1", len(boards.imported))
	}

	// 2. Careers page with embedded Greenhouse
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Careers at Beispiel Labs</title></head>
<body>
  <iframe src="https://boards.greenhouse.io/embed/job_board?for=beispiel"></iframe>
</body>
</html>`))
	}))
	defer ts.Close()

	svcWithClient := NewService(Options{
		Client:       ts.Client(),
		AllowPrivate: true,
		Boards:       boards,
	})

	b2, err := svcWithClient.AddBoard(t.Context(), ts.URL+"/careers")
	if err != nil {
		t.Fatalf("AddBoard embedded failed: %v", err)
	}
	if b2.ATS != "greenhouse" || b2.Slug != "beispiel" {
		t.Errorf("Board = %+v, want greenhouse / beispiel", b2)
	}
}
