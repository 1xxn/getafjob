package vocab

import (
	"slices"
	"testing"
)

func TestNormalizeAndRoles(t *testing.T) {
	if got := Normalize("Senior Go-Entwickler (m/w/d), Köln"); got != " senior go entwickler m w d koeln " {
		t.Fatalf("Normalize = %q", got)
	}
	for _, role := range []string{"Senior Platform Engineer", "Softwareentwicklerin (m/w/d)", "Backend Developer", "Scrum Master", "Data Analyst"} {
		if !IsKnownRole(role) {
			t.Errorf("IsKnownRole(%q) = false, want true", role)
		}
	}
	for _, nonRole := range []string{"", "Muster Cloud GmbH", "Beispiel AG", "Acme Inc.", "TU Berlin University", "Cloud Solutions"} {
		if IsKnownRole(nonRole) {
			t.Errorf("IsKnownRole(%q) = true, want false", nonRole)
		}
	}
}

func TestKnownSkills(t *testing.T) {
	got := KnownSkills("Cloud: AWS, Docker, Kubernetes & PostgreSQL (plus Google Ads)")
	for _, want := range []string{"AWS", "Docker", "Kubernetes", "PostgreSQL"} {
		if !slices.Contains(got, want) {
			t.Errorf("KnownSkills missing %q in %v", want, got)
		}
	}
	if slices.Contains(got, "Go") {
		t.Errorf("KnownSkills matched substring inside Google: %v", got)
	}
}
