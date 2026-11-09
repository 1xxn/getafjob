package urlimport

import (
	"testing"

	"github.com/1xxn/getafjob/internal/discovery"
)

func TestExtract_SchemaOrgJSONLD(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head>
  <title>Senior Go Developer - ACME Robotics</title>
  <script type="application/ld+json">
  {
    "@context": "https://schema.org/",
    "@type": "JobPosting",
    "title": "Senior Go Developer",
    "description": "<p>Build high-performance microservices in Go with PostgreSQL.</p>",
    "identifier": {
      "@type": "PropertyValue",
      "name": "ACME Robotics",
      "value": "acme-12345"
    },
    "datePosted": "2026-11-05",
    "hiringOrganization": {
      "@type": "Organization",
      "name": "ACME Robotics"
    },
    "jobLocation": {
      "@type": "Place",
      "address": {
        "@type": "PostalAddress",
        "addressLocality": "Berlin",
        "addressCountry": "DE"
      }
    },
    "baseSalary": {
      "@type": "MonetaryAmount",
      "currency": "EUR",
      "value": {
        "@type": "QuantitativeValue",
        "minValue": 75000,
        "maxValue": 95000,
        "unitText": "YEAR"
      }
    }
  }
  </script>
</head>
<body>
  <h1>Senior Go Developer</h1>
</body>
</html>`

	rawURL := "https://jobs.lever.co/acme-robotics/acme-12345"
	job, err := Extract(rawURL, []byte(html))
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	if job.Title != "Senior Go Developer" {
		t.Errorf("Title = %q, want 'Senior Go Developer'", job.Title)
	}
	if job.Company != "ACME Robotics" {
		t.Errorf("Company = %q, want 'ACME Robotics'", job.Company)
	}
	if job.Location != "Berlin, DE" {
		t.Errorf("Location = %q, want 'Berlin, DE'", job.Location)
	}
	if job.ExternalID != "acme-12345" {
		t.Errorf("ExternalID = %q, want 'acme-12345'", job.ExternalID)
	}
	if job.SalaryMin != 75000 || job.SalaryMax != 95000 {
		t.Errorf("Salary = %d - %d, want 75000 - 95000", job.SalaryMin, job.SalaryMax)
	}
	if job.SalaryCurrency != "EUR" {
		t.Errorf("SalaryCurrency = %q, want 'EUR'", job.SalaryCurrency)
	}
	if job.ATS != "lever" {
		t.Errorf("ATS = %q, want 'lever'", job.ATS)
	}
	if job.Source != "lever:acme-robotics" {
		t.Errorf("Source = %q, want 'lever:acme-robotics'", job.Source)
	}
}

func TestExtract_OpenGraphFallback(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head>
  <meta property="og:title" content="Lead Platform Engineer" />
  <meta property="og:site_name" content="Musterwerk Systems" />
  <meta property="og:description" content="Lead our cloud infrastructure team in Hamburg. Kubernetes, Terraform, and Go. €80,000 – €105,000 p.a." />
  <meta property="job:location" content="Hamburg, Germany" />
  <title>Lead Platform Engineer | Musterwerk Systems</title>
</head>
<body>
  <h1>Lead Platform Engineer</h1>
  <p>Some body text about the team.</p>
</body>
</html>`

	rawURL := "https://careers.musterwerk.example/jobs/lead-platform-engineer"
	job, err := Extract(rawURL, []byte(html))
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	if job.Title != "Lead Platform Engineer" {
		t.Errorf("Title = %q, want 'Lead Platform Engineer'", job.Title)
	}
	if job.Company != "Musterwerk Systems" {
		t.Errorf("Company = %q, want 'Musterwerk Systems'", job.Company)
	}
	if job.Location != "Hamburg, Germany" {
		t.Errorf("Location = %q, want 'Hamburg, Germany'", job.Location)
	}
	if job.Workplace != discovery.WorkplaceOnsite {
		t.Errorf("Workplace = %q, want 'onsite'", job.Workplace)
	}
	if job.SalaryMin != 80000 || job.SalaryMax != 105000 {
		t.Errorf("Salary = %d - %d, want 80000 - 105000", job.SalaryMin, job.SalaryMax)
	}
	if job.Source != "import:careers.musterwerk.example" {
		t.Errorf("Source = %q, want 'import:careers.musterwerk.example'", job.Source)
	}
}

func TestExtract_PlainHTMLFallback(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head>
  <title>Staff Backend Architect | Example Labs</title>
</head>
<body>
  <header>Navigation</header>
  <main>
    <h1>Staff Backend Architect</h1>
    <p>Location: Fully Remote (Europe)</p>
    <div class="job-body">
      We are looking for a Staff Backend Architect to design distributed systems.
      Salary range: €90k - €120k per year.
    </div>
  </main>
</body>
</html>`

	rawURL := "https://example-labs.org/career/staff-backend-architect"
	job, err := Extract(rawURL, []byte(html))
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	if job.Title != "Staff Backend Architect" {
		t.Errorf("Title = %q, want 'Staff Backend Architect'", job.Title)
	}
	if job.Company != "Example Labs" {
		t.Errorf("Company = %q, want 'Example Labs'", job.Company)
	}
	if job.Workplace != discovery.WorkplaceRemote {
		t.Errorf("Workplace = %q, want 'remote'", job.Workplace)
	}
	if job.SalaryMin != 90000 || job.SalaryMax != 120000 {
		t.Errorf("Salary = %d - %d, want 90000 - 120000", job.SalaryMin, job.SalaryMax)
	}
}

func TestExtract_GreenhouseURL(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head>
  <title>Site Reliability Engineer at Synthetik Tech</title>
</head>
<body>
  <h1>Site Reliability Engineer</h1>
  <div class="location">Berlin (Hybrid)</div>
  <div id="content">
    Synthetik Tech is seeking a talented SRE to automate infrastructure.
  </div>
</body>
</html>`

	rawURL := "https://boards.greenhouse.io/synthetiktech/jobs/5678901"
	job, err := Extract(rawURL, []byte(html))
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	if job.ATS != "greenhouse" {
		t.Errorf("ATS = %q, want 'greenhouse'", job.ATS)
	}
	if job.Source != "greenhouse:synthetiktech" {
		t.Errorf("Source = %q, want 'greenhouse:synthetiktech'", job.Source)
	}
	if job.ExternalID != "5678901" {
		t.Errorf("ExternalID = %q, want '5678901'", job.ExternalID)
	}
	if job.Title != "Site Reliability Engineer" {
		t.Errorf("Title = %q, want 'Site Reliability Engineer'", job.Title)
	}
	if job.Workplace != discovery.WorkplaceHybrid {
		t.Errorf("Workplace = %q, want 'hybrid'", job.Workplace)
	}
}

func TestExtract_JobBoardsGreenhouseURL(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head>
  <meta property="og:title" content="Platform Engineer" />
  <meta property="og:description" content="London" />
</head>
<body>
  <h1>Platform Engineer</h1>
</body>
</html>`

	rawURL := "https://job-boards.greenhouse.io/capitalontap/jobs/8902990002"
	job, err := Extract(rawURL, []byte(html))
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}

	if job.ATS != "greenhouse" {
		t.Errorf("ATS = %q, want 'greenhouse'", job.ATS)
	}
	if job.Source != "greenhouse:capitalontap" {
		t.Errorf("Source = %q, want 'greenhouse:capitalontap'", job.Source)
	}
	if job.Company != "Capitalontap" {
		t.Errorf("Company = %q, want 'Capitalontap'", job.Company)
	}
	if job.ExternalID != "8902990002" {
		t.Errorf("ExternalID = %q, want '8902990002'", job.ExternalID)
	}
	if job.Title != "Platform Engineer" {
		t.Errorf("Title = %q, want 'Platform Engineer'", job.Title)
	}
}
