package ats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	gotel "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestAllAdaptersAndTiers(t *testing.T) {
	want := []string{
		Greenhouse, Lever, Ashby, Workday, SmartRecruiters, Personio, Recruitee, Workable,
		ICIMS, Taleo, SuccessFactors, Teamtailor,
		LinkedIn, Indeed,
		Generic,
	}
	if got := All(); !slices.Equal(got, want) {
		t.Fatalf("All() = %v, want %v", got, want)
	}
	for _, name := range want[:8] {
		if a := Lookup(name); a.Tier != 1 {
			t.Errorf("%s tier = %d, want 1", name, a.Tier)
		}
	}
	for _, name := range want[8:12] {
		if a := Lookup(name); a.Tier != 2 {
			t.Errorf("%s tier = %d, want 2", name, a.Tier)
		}
	}
	for _, name := range want[12:14] {
		if a := Lookup(name); a.Tier != 3 {
			t.Errorf("%s tier = %d, want 3", name, a.Tier)
		}
	}
	if a := Lookup(Generic); a.Tier != 0 || Lookup("unknown-ats").Name != Generic {
		t.Errorf("generic lookup failed: %+v", a)
	}
}

func TestDetectFromSourceAndURLs(t *testing.T) {
	cases := []struct {
		source string
		urls   []string
		want   string
	}{
		{"greenhouse:acme", nil, Greenhouse},
		{"lever:muster", nil, Lever},
		{"ashby:globex", nil, Ashby},
		{"workday:muster/External", nil, Workday},
		{"smartrecruiters:acme", nil, SmartRecruiters},
		{"personio:beispiel", nil, Personio},
		{"recruitee:acme", nil, Recruitee},
		{"workable:acme", nil, Workable},
		{"icims:acme", nil, ICIMS},
		{"taleo:acme", nil, Taleo},
		{"successfactors:acme", nil, SuccessFactors},
		{"teamtailor:acme", nil, Teamtailor},
		{"arbeitnow", []string{"https://www.arbeitnow.com/jobs/1", "https://jobs.eu.lever.co/muster/1"}, Lever},
		{"careers", []string{"https://acme.example/jobs/1", "https://jobs.ashbyhq.com/acme/1"}, Ashby},
		{"careers", []string{"https://muster.wd3.myworkdayjobs.com/External/job/1"}, Workday},
		{"careers", []string{"https://jobs.smartrecruiters.com/Acme/1"}, SmartRecruiters},
		{"careers", []string{"https://beispiel.jobs.personio.de/job/123"}, Personio},
		{"careers", []string{"https://acme.recruitee.com/o/backend-engineer"}, Recruitee},
		{"careers", []string{"https://apply.workable.com/acme/j/ABCD1234/"}, Workable},
		{"careers", []string{"https://careers-acme.icims.com/jobs/1234/job"}, ICIMS},
		{"careers", []string{"https://acme.taleo.net/careersection/ex/jobdetail.ftl?job=1"}, Taleo},
		{"careers", []string{"https://career.successfactors.eu/career?company=acme"}, SuccessFactors},
		{"careers", []string{"https://acme.teamtailor.com/jobs/1234-go-engineer"}, Teamtailor},
		{"linkedin", []string{"https://www.linkedin.com/jobs/view/12345"}, LinkedIn},
		{"indeed", []string{"https://de.indeed.com/viewjob?jk=abc123"}, Indeed},
		{"arbeitsagentur", []string{"https://www.acme.example/karriere/go"}, Generic},
	}
	for _, tc := range cases {
		if got := Detect(tc.source, tc.urls...); got != tc.want {
			t.Errorf("Detect(%q, %v) = %q, want %q", tc.source, tc.urls, got, tc.want)
		}
	}
}

func TestOrigins(t *testing.T) {
	cases := []struct {
		source string
		urls   []string
		want   []string
	}{
		{
			"greenhouse:acme",
			[]string{"https://www.acme.example/careers/1"},
			[]string{"https://www.acme.example", "https://boards.greenhouse.io", "https://job-boards.greenhouse.io", "https://job-boards.eu.greenhouse.io"},
		},
		{
			"personio:beispiel",
			[]string{"https://www.beispiel.example/jobs/1"},
			[]string{"https://www.beispiel.example", "https://beispiel.jobs.personio.de", "https://beispiel.jobs.personio.com"},
		},
		{
			"recruitee:acme",
			[]string{"https://www.acme.example/jobs/1"},
			[]string{"https://www.acme.example", "https://acme.recruitee.com"},
		},
		{
			"icims:acme",
			[]string{"https://www.acme.example/jobs/1"},
			[]string{"https://www.acme.example", "https://acme.icims.com", "https://careers-acme.icims.com", "https://jobs-acme.icims.com"},
		},
		{
			"taleo:acme",
			[]string{"https://www.acme.example/jobs/1"},
			[]string{"https://www.acme.example", "https://acme.taleo.net"},
		},
		{
			"successfactors:acme",
			[]string{"https://www.acme.example/jobs/1"},
			[]string{"https://www.acme.example", "https://career.successfactors.eu", "https://career.successfactors.com"},
		},
		{
			"teamtailor:acme",
			[]string{"https://www.acme.example/jobs/1"},
			[]string{"https://www.acme.example", "https://career.teamtailor.com", "https://acme.teamtailor.com"},
		},
		{
			"workday:muster/External",
			[]string{"https://Muster.wd3.myworkdayjobs.com/External/job/1"},
			[]string{"https://muster.wd3.myworkdayjobs.com"},
		},
		{
			"x",
			[]string{"http://insecure.example/1", "javascript:alert(1)"},
			nil,
		},
	}
	for _, tc := range cases {
		if got := Origins(tc.source, tc.urls...); !slices.Equal(got, tc.want) {
			t.Errorf("Origins(%q, %v) = %v, want %v", tc.source, tc.urls, got, tc.want)
		}
	}
}

func TestPrecheckNonPublicURLIsNoop(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	for _, name := range All() {
		qs, err := Lookup(name).Precheck(context.Background(), srv.Client(), srv.URL+"/apply.html")
		if err != nil || len(qs) != 0 {
			t.Errorf("%s Precheck = %v, %v", name, qs, err)
		}
	}
}

func TestMaturityTrackerAndMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	prev := gotel.GetMeterProvider()
	gotel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { gotel.SetMeterProvider(prev) })

	tr, err := NewTracker()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	tr.Record(ctx, Greenhouse, OutcomeSubmitted)
	tr.Record(ctx, Greenhouse, OutcomeSubmitted)
	tr.Record(ctx, Greenhouse, OutcomeAsked)
	tr.Record(ctx, Greenhouse, OutcomeParked)
	tr.Record(ctx, Ashby, OutcomeSubmitted)
	tr.Record(ctx, "unknown-portal", OutcomeParked)

	if got := tr.Maturity(Greenhouse); got != 0.5 {
		t.Fatalf("greenhouse maturity = %v, want 0.5", got)
	}
	if got := tr.Maturity(Ashby); got != 1.0 {
		t.Fatalf("ashby maturity = %v, want 1.0", got)
	}
	if got := tr.Maturity(Generic); got != 0.0 {
		t.Fatalf("generic maturity = %v, want 0.0", got)
	}
	if got := tr.Maturity(Workday); got != 0.0 {
		t.Fatalf("unseen adapter maturity = %v, want 0.0", got)
	}

	snap := tr.Snapshot()
	if snap[Greenhouse].Total() != 4 || snap[Greenhouse].Submitted != 2 || snap[Generic].Parked != 1 {
		t.Fatalf("snapshot = %+v", snap)
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	gauges := map[string]float64{}
	runs := map[string]int64{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch m.Name {
			case "getafjob.ats.maturity":
				for _, dp := range m.Data.(metricdata.Gauge[float64]).DataPoints {
					ad, _ := dp.Attributes.Value(attribute.Key("adapter"))
					gauges[ad.AsString()] = dp.Value
				}
			case "getafjob.ats.runs":
				for _, dp := range m.Data.(metricdata.Sum[int64]).DataPoints {
					ad, _ := dp.Attributes.Value(attribute.Key("adapter"))
					out, _ := dp.Attributes.Value(attribute.Key("outcome"))
					runs[ad.AsString()+":"+out.AsString()] += dp.Value
				}
			}
		}
	}
	if gauges[Greenhouse] != 0.5 || gauges[Ashby] != 1.0 || gauges[Generic] != 0.0 {
		t.Fatalf("gauges = %v", gauges)
	}
	if runs["greenhouse:submitted"] != 2 || runs["greenhouse:asked"] != 1 || runs["greenhouse:parked"] != 1 ||
		runs["ashby:submitted"] != 1 || runs["generic:parked"] != 1 {
		t.Fatalf("runs = %v", runs)
	}
}

func TestFillBeforeAskOnlyWhereNoDraftsAreKept(t *testing.T) {
	for name, want := range map[string]bool{Greenhouse: true, Lever: true, Generic: true, Workday: false, LinkedIn: false} {
		if got := Lookup(name).NewEngine(nil, nil, nil, 0).FillBeforeAsk; got != want {
			t.Errorf("%s: FillBeforeAsk %v, want %v", name, got, want)
		}
	}
}
