package cvimport

import (
	"os"
	"slices"
	"testing"

	"github.com/1xxn/getafjob/internal/profile"
)

// LaTeX CVs from Jake Gutierrez's resume template and a variant of it with the Lato font, Font Awesome icons and
// Profile, Certifications and Languages sections (testdata/latex, compiled with TeX Live, fictional people).
// Ligatures, icon glyphs and the two-column subheadings once broke the import (#610).
func TestLaTeXCVs(t *testing.T) {
	read := func(f string) ParsedCV {
		t.Helper()
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text, err := Text(profile.MimePDF, b)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		return ParseAll(text)
	}

	jake := read("testdata/latex-jake.pdf")
	if p := jake.Profile.Personal; p.FirstName != "Erika" || p.LastName != "Musterfrau" || p.Email != "erika.musterfrau@example.org" ||
		p.Phone != "+49 30 7654321" || p.GitHub != "https://github.com/erika-musterfrau" {
		t.Errorf("jake personal = %+v", p)
	}
	if len(jake.Experiences) != 2 { // the Projects section is no work experience
		t.Fatalf("jake experiences = %+v", jake.Experiences)
	}
	if w := jake.Experiences[0]; w.Title != "Backend Engineer" || w.Company != "Spreeufer Labs GmbH" || w.Location != "Berlin, Germany" ||
		w.StartDate != "2022-01" || !w.IsCurrent || len(w.DescriptionBullets) != 2 {
		t.Errorf("jake first position = %+v", w)
	}
	if w := jake.Experiences[1]; w.Company != "Acme Data GmbH" || w.StartDate != "2019-10" || w.EndDate != "2021-12" {
		t.Errorf("jake second position = %+v", w)
	}
	if len(jake.Educations) != 1 || jake.Educations[0].Institution != "Universität Beispielstadt" || jake.Educations[0].Degree != "B.Sc." ||
		jake.Educations[0].StartDate != "2016-10" {
		t.Errorf("jake education = %+v", jake.Educations)
	}
	if !slices.Contains(jake.Profile.Skills, "Kubernetes") || !slices.Contains(jake.Profile.Skills, "TypeScript") {
		t.Errorf("jake skills = %v", jake.Profile.Skills)
	}

	icons := read("testdata/latex-icons.pdf")
	p := icons.Profile
	if p.Personal.FirstName != "Max" || p.Personal.Email != "max.mustermann@example.org" || p.Personal.Phone != "+49 40 1234567" ||
		p.Personal.City != "Hamburg" || p.Personal.Country != "Germany" || p.Personal.LinkedIn != "https://linkedin.com/in/max-mustermann" {
		t.Errorf("icons personal = %+v", p.Personal)
	}
	if p.Summary == "" || !slices.Equal(p.Certifications, []string{"Certified Kubernetes Administrator (CKA)"}) || len(p.Languages) != 2 {
		t.Errorf("icons summary %q, certifications %v, languages %v", p.Summary, p.Certifications, p.Languages)
	}
	if len(icons.Experiences) != 2 {
		t.Fatalf("icons experiences = %+v", icons.Experiences)
	}
	if w := icons.Experiences[0]; w.Title != "Senior Platform Engineer" || w.Company != "Musterfirma GmbH" || w.Location != "Hamburg, Germany" ||
		w.StartDate != "2021-04" || !w.IsCurrent {
		t.Errorf("icons first position = %+v", w)
	}
	if w := icons.Experiences[1]; w.Company != "Beispiel AG" || w.EndDate != "2021-03" {
		t.Errorf("icons second position = %+v", w)
	}
	if len(icons.Educations) != 1 || icons.Educations[0].Institution != "Technische Universität Musterstadt" {
		t.Errorf("icons education = %+v", icons.Educations)
	}
}

func TestGlyphText(t *testing.T) {
	for name, want := range map[string]string{"uniFB01": "\ufb01", "fi": "fi", "f_f_i": "ffi", "u1F600": "\U0001F600", "uni00660069": "fi"} {
		if got, ok := glyphText(name); !ok || got != want {
			t.Errorf("glyphText(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	for _, name := range []string{"envelope", "a", "uniXYZ1", "u12"} {
		if got, ok := glyphText(name); ok {
			t.Errorf("glyphText(%q) = %q, want no text", name, got)
		}
	}
	if !iconFont("FontAwesome5Brands-Regular") || !iconFont("Marvosym") || iconFont("Lato-Regular") {
		t.Error("iconFont")
	}
}
