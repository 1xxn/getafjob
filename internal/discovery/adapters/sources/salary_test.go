package sources

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/discovery"
)

func ld(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// Only yearly figures count: monthly ones are converted, hourly, daily and weekly ones dropped.
func TestBaseSalary(t *testing.T) {
	for in, want := range map[string]Salary{
		`{"currency":"EUR","value":{"minValue":65000,"maxValue":80000,"unitText":"YEAR"}}`:      {65000, 80000, "EUR"},
		`{"currency":"eur","value":{"minValue":"5.000","maxValue":"6.000","unitText":"MONTH"}}`: {60000, 72000, "EUR"},
		`{"currency":"USD","value":{"value":120000}}`:                                           {120000, 120000, "USD"},
		`{"currency":"EUR","value":72000}`:                                                      {72000, 72000, "EUR"},
		`{"currency":"EUR","value":{"minValue":"72,500.50","unitText":"YEAR"}}`:                 {72501, 72501, "EUR"},
		`[{"currency":"GBP","value":{"minValue":90000,"maxValue":70000}}]`:                      {70000, 90000, "GBP"},
		`{"currency":"Euro","value":{"minValue":50000,"unitText":"YEAR"}}`:                      {50000, 50000, ""},
		`{"currency":"EUR","value":{"minValue":45,"maxValue":60,"unitText":"HOUR"}}`:            {},
		`{"currency":"EUR","value":{"minValue":4500,"maxValue":5500}}`:                          {},
		`{"currency":"EUR","value":{"minValue":-1}}`:                                            {},
		`{"currency":"EUR","value":{"value":99999999999}}`:                                      {},
		`"competitive"`: {},
	} {
		got, ok := BaseSalary(ld(t, in))
		if got != want || ok != (want.Max > 0) {
			t.Errorf("BaseSalary(%s) = %+v, %v; want %+v", in, got, ok, want)
		}
	}
}

// A careers page's baseSalary reaches the posting; a salary the source stated already stays.
func TestJobPostingSalary(t *testing.T) {
	page := []byte(`<script type="application/ld+json">{"@type":"JobPosting","title":"Go Engineer",
		"baseSalary":{"@type":"MonetaryAmount","currency":"EUR","value":{"@type":"QuantitativeValue","minValue":70000,"maxValue":85000,"unitText":"YEAR"}}}</script>`)
	jobs := JobPostings(page)
	if len(jobs) != 1 || jobs[0].Salary != (Salary{70000, 85000, "EUR"}) {
		t.Fatalf("jobs = %+v", jobs)
	}
	var p discovery.Posting
	jobs[0].fill(&p, "Acme")
	if p.SalaryMin != 70000 || p.SalaryMax != 85000 || p.SalaryCurrency != "EUR" {
		t.Fatalf("posting salary = %d-%d %s", p.SalaryMin, p.SalaryMax, p.SalaryCurrency)
	}
	p = discovery.Posting{SalaryMin: 1, SalaryMax: 2, SalaryCurrency: "USD"}
	jobs[0].fill(&p, "Acme")
	if p.SalaryMax != 2 || p.SalaryCurrency != "USD" {
		t.Fatalf("stated salary replaced: %+v", p)
	}
}
