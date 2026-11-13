package cvimport

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/profile"
)

// TestMain lets the test binary act as the child process, like the real binary does.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == ChildCommand {
		os.Exit(RunChild(os.Args[2:], os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

const cvEN = `Max Mustermann
Platform Engineer
Musterstrasse 1, 10115 Berlin
max.mustermann@example.org | +49 30 1234567
linkedin.com/in/max-mustermann | https://github.com/maxmustermann | https://max.example.org

Summary
Platform engineer with eight years of experience in Go and Kubernetes.
I like reliable systems.

Experience
Senior Platform Engineer at Muster GmbH    01/2021 - present
• Built the internal developer platform
• Cut deployment time from hours to minutes
Backend Developer, Beispiel AG    2017 - 2020
- Payment services in Go

Skills
Go, PostgreSQL, Kubernetes | Terraform; Linux

Languages
German: native
English - C1

Education
B.Sc. Computer Science, TU Musterstadt    2013 - 2017

Certifications
CKA
AWS Solutions Architect Associate
`

const cvDE = `LEBENSLAUF
Erika Mustermann
Beispielweg 2 · 80331 München
erika@example.org · 0151 23456789

Berufserfahrung
Softwareentwicklerin bei Beispiel GmbH    03/2019 – heute
• Microservices in Go

Kenntnisse
Go · Python · Docker

Sprachen
Deutsch (Muttersprache)
Englisch fließend
`

func TestParseEnglish(t *testing.T) {
	p := Parse(cvEN)
	pe := p.Personal
	if pe.FirstName != "Max" || pe.LastName != "Mustermann" || pe.Email != "max.mustermann@example.org" ||
		pe.Phone != "+49 30 1234567" || pe.PostalCode != "10115" || pe.City != "Berlin" ||
		pe.LinkedIn != "https://linkedin.com/in/max-mustermann" || pe.GitHub != "https://github.com/maxmustermann" ||
		pe.Website != "https://max.example.org" {
		t.Errorf("personal = %+v", pe)
	}
	if !strings.HasPrefix(p.Summary, "Platform engineer with eight years") || !strings.HasSuffix(p.Summary, "reliable systems.") {
		t.Errorf("summary = %q", p.Summary)
	}
	if !slices.Equal(p.Skills, []string{"Go", "PostgreSQL", "Kubernetes", "Terraform", "Linux"}) {
		t.Errorf("skills = %q", p.Skills)
	}
	if len(p.Languages) != 2 || p.Languages[0] != (profile.Language{Name: "German", Level: "native"}) || p.Languages[1].Level != "C1" {
		t.Errorf("languages = %+v", p.Languages)
	}
	if w := ParseAll(cvEN).Profile.Work; w.CurrentTitle != "Senior Platform Engineer" || w.CurrentEmployer != "Muster GmbH" {
		t.Errorf("work = %+v", w)
	}
	if !slices.Equal(p.Certifications, []string{"CKA", "AWS Solutions Architect Associate"}) {
		t.Errorf("certifications = %q", p.Certifications)
	}
	if errs := p.Validate(); len(errs) != 0 {
		t.Errorf("parsed profile invalid: %v", errs)
	}
}

func TestParseGerman(t *testing.T) {
	p := Parse(cvDE)
	if p.Personal.FirstName != "Erika" || p.Personal.LastName != "Mustermann" || p.Personal.City != "München" ||
		p.Personal.Phone != "0151 23456789" {
		t.Errorf("personal = %+v", p.Personal)
	}
	if !slices.Equal(p.Skills, []string{"Go", "Python", "Docker"}) {
		t.Errorf("skills = %q", p.Skills)
	}
	if len(p.Languages) != 2 || p.Languages[0].Name != "Deutsch" || p.Languages[1].Level != "fließend" {
		t.Errorf("languages = %+v", p.Languages)
	}
}

func TestMergeKeepsUserValues(t *testing.T) {
	cur := profile.Profile{Personal: profile.Personal{FirstName: "Maximilian"}, Skills: []string{"Rust"}}
	got := Merge(cur, Parse(cvEN))
	if got.Personal.FirstName != "Maximilian" || got.Personal.LastName != "Mustermann" || !slices.Equal(got.Skills, []string{"Rust"}) {
		t.Errorf("merged = %+v", got)
	}
}

const cvRich = `Max Mustermann
Staff Platform Engineer
10115 Berlin · max.mustermann@example.org · +49 30 1234567

Experience
Staff Platform Engineer at Muster Cloud GmbH | Berlin, Germany (Remote)    03/2022 - present
• Architected multi-region Kubernetes platform
• Reduced cloud compute spending by 35%
Senior Backend Developer at Beispiel Systems - Munich (Hybrid)    2018 - 2022
• Designed distributed caching layer with Redis and Go
• Maintained PostgreSQL databases

Education
M.Sc. Computer Science, TU Berlin    2016 - 2018
Grade: 1.3
B.Sc. Informatik, TU München    2012 - 2016
GPA 3.9

Skills
Go, Kubernetes, PostgreSQL, Linux
`

func TestExtractStationsEnglish(t *testing.T) {
	exps, edus := ExtractStations(cvEN)
	if len(exps) != 2 {
		t.Fatalf("expected 2 work experiences, got %d", len(exps))
	}
	e0 := exps[0]
	if e0.Title != "Senior Platform Engineer" || e0.Company != "Muster GmbH" || e0.StartDate != "01/2021" || !e0.IsCurrent || e0.EndDate != "" {
		t.Errorf("e0 mismatch: %+v", e0)
	}
	if len(e0.DescriptionBullets) != 2 || e0.DescriptionBullets[0] != "Built the internal developer platform" {
		t.Errorf("e0 bullets mismatch: %v", e0.DescriptionBullets)
	}
	e1 := exps[1]
	if e1.Title != "Backend Developer" || e1.Company != "Beispiel AG" || e1.StartDate != "2017" || e1.EndDate != "2020" || e1.IsCurrent {
		t.Errorf("e1 mismatch: %+v", e1)
	}
	if len(e1.DescriptionBullets) != 1 || e1.DescriptionBullets[0] != "Payment services in Go" {
		t.Errorf("e1 bullets mismatch: %v", e1.DescriptionBullets)
	}

	if len(edus) != 1 {
		t.Fatalf("expected 1 education station, got %d", len(edus))
	}
	ed0 := edus[0]
	if ed0.Institution != "TU Musterstadt" || ed0.Degree != "B.Sc." || ed0.FieldOfStudy != "Computer Science" || ed0.StartDate != "2013" || ed0.EndDate != "2017" {
		t.Errorf("ed0 mismatch: %+v", ed0)
	}
}

func TestExtractStationsGerman(t *testing.T) {
	exps, edus := ExtractStations(cvDE)
	if len(exps) != 1 {
		t.Fatalf("expected 1 work experience, got %d", len(exps))
	}
	e0 := exps[0]
	if e0.Title != "Softwareentwicklerin" || e0.Company != "Beispiel GmbH" || e0.StartDate != "03/2019" || !e0.IsCurrent {
		t.Errorf("e0 mismatch: %+v", e0)
	}
	if len(e0.DescriptionBullets) != 1 || e0.DescriptionBullets[0] != "Microservices in Go" {
		t.Errorf("e0 bullets mismatch: %v", e0.DescriptionBullets)
	}
	if len(edus) != 0 {
		t.Errorf("expected 0 educations for cvDE, got %d", len(edus))
	}
}

func TestExtractStationsRich(t *testing.T) {
	parsed := ParseAll(cvRich)
	if len(parsed.Experiences) != 2 {
		t.Fatalf("expected 2 work experiences, got %d", len(parsed.Experiences))
	}
	e0 := parsed.Experiences[0]
	if e0.Title != "Staff Platform Engineer" || e0.Company != "Muster Cloud GmbH" || e0.Location != "Berlin, Germany" || e0.WorkplaceType != profile.WorkplaceRemote || !e0.IsCurrent {
		t.Errorf("e0 mismatch: %+v", e0)
	}
	if len(e0.DescriptionBullets) != 2 {
		t.Errorf("e0 bullets count: %v", e0.DescriptionBullets)
	}

	e1 := parsed.Experiences[1]
	if e1.Title != "Senior Backend Developer" || e1.Company != "Beispiel Systems" || e1.Location != "Munich" || e1.WorkplaceType != profile.WorkplaceHybrid || e1.IsCurrent {
		t.Errorf("e1 mismatch: %+v", e1)
	}

	if len(parsed.Educations) != 2 {
		t.Fatalf("expected 2 educations, got %d", len(parsed.Educations))
	}
	ed0 := parsed.Educations[0]
	if ed0.Institution != "TU Berlin" || ed0.Degree != "M.Sc." || ed0.FieldOfStudy != "Computer Science" || ed0.Grade != "1.3" {
		t.Errorf("ed0 mismatch: %+v", ed0)
	}

	ed1 := parsed.Educations[1]
	if ed1.Institution != "TU München" || ed1.Degree != "B.Sc." || ed1.FieldOfStudy != "Informatik" || ed1.Grade != "3.9" {
		t.Errorf("ed1 mismatch: %+v", ed1)
	}
}

func docx(t *testing.T, text string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("[Content_Types].xml")
	_, _ = w.Write([]byte("<Types/>"))
	w, _ = zw.Create("word/document.xml")
	var body strings.Builder
	for _, l := range strings.Split(text, "\n") {
		fmt.Fprintf(&body, `<w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, strings.NewReplacer("&", "&amp;", "<", "&lt;").Replace(l))
	}
	_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>%s</w:body></w:document>`, body.String())
	_ = zw.Close()
	return buf.Bytes()
}

// pdfFile builds a one-page PDF with the lines as text (Helvetica), with a correct cross-reference table. Lines end
// with T*; positioned places each line with Td instead, as most PDF writers do, and splits it into two text runs.
func pdfFile(lines []string, positioned bool) []byte {
	var content strings.Builder
	content.WriteString("BT /F1 10 Tf 12 TL 50 780 Td\n")
	esc := strings.NewReplacer(`\`, `\\`, "(", `\(`, ")", `\)`)
	for _, l := range lines {
		if !positioned {
			fmt.Fprintf(&content, "(%s) Tj T*\n", esc.Replace(l))
			continue
		}
		first, rest, _ := strings.Cut(l, " ")
		fmt.Fprintf(&content, "(%s) Tj %d 0 Td (%s) Tj %d -12 Td\n", esc.Replace(first), 6*len(first)+4, esc.Replace(rest), -6*len(first)-4)
	}
	content.WriteString("ET")
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", content.Len(), content.String()),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func TestTextFromFiles(t *testing.T) {
	got, err := Text(profile.MimeDOCX, docx(t, cvEN))
	if err != nil || !strings.Contains(got, "Senior Platform Engineer at Muster GmbH") || Parse(got).Personal.LastName != "Mustermann" {
		t.Fatalf("docx text = %q, %v", got, err)
	}
	for _, positioned := range []bool{false, true} {
		got, err = Text(profile.MimePDF, pdfFile(strings.Split(cvEN, "\n"), positioned))
		if err != nil || !strings.Contains(got, "\nmax.mustermann@example.org") || !strings.Contains(got, "Senior Platform Engineer at Muster GmbH") {
			t.Fatalf("pdf text (positioned %v) = %q, %v", positioned, got, err)
		}
		if cv := ParseAll(got); cv.Profile.Personal.LastName != "Mustermann" || len(cv.Profile.Skills) == 0 || len(cv.Experiences) == 0 {
			t.Errorf("pdf import (positioned %v) = %+v", positioned, cv)
		}
	}
	if _, err := Text(profile.MimePDF, pdfFile([]string{"short"}, false)); !errors.Is(err, ErrNoText) {
		t.Errorf("scan-like pdf: %v", err)
	}
	if _, err := Text(profile.MimePDF, []byte("%PDF-1.4 garbage")); !errors.Is(err, profile.ErrFileType) {
		t.Errorf("broken pdf: %v", err)
	}
	if got := Clean("M¨ unchen  ﬁle\n\n\n\nnext     col"); got != "München  file\n\nnext  col" {
		t.Errorf("Clean = %q", got)
	}
}

func TestExtractorChild(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ex := NewExtractorFor(exe, 20*time.Second)
	got, err := ex.Text(t.Context(), profile.MimeDOCX, docx(t, cvEN))
	if err != nil || !strings.Contains(got, "Max Mustermann") {
		t.Fatalf("child text = %q, %v", got, err)
	}
	if _, err := ex.Text(t.Context(), profile.MimePDF, []byte("%PDF-1.4 garbage")); !errors.Is(err, profile.ErrFileType) {
		t.Errorf("broken pdf in child: %v", err)
	}
	if _, err := ex.Text(t.Context(), profile.MimeDOCX, docx(t, "too short")); !errors.Is(err, ErrNoText) {
		t.Errorf("short docx in child: %v", err)
	}
	slow := NewExtractorFor(exe, time.Nanosecond)
	if _, err := slow.Text(t.Context(), profile.MimeDOCX, docx(t, cvEN)); !errors.Is(err, profile.ErrFileType) {
		t.Errorf("timeout: %v", err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(cvEN)
	f.Add(cvDE)
	f.Fuzz(func(t *testing.T, s string) {
		p := ParseAll(s)
		if len(p.Experiences) > profile.MaxStations || len(p.Educations) > profile.MaxStations || len(p.Profile.Skills) > 80 {
			t.Fatal("unbounded result")
		}
	})
}

func FuzzText(f *testing.F) {
	f.Add([]byte("%PDF-1.4"))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = Text(profile.MimePDF, b) // must not panic or hang
		_, _ = Text(profile.MimeDOCX, b)
	})
}

func TestLinksNeedTheirHost(t *testing.T) {
	p := Parse("Max Mustermann\nevil-linkedin.com/in/max and xgithub.com/max\nhttps://www.linkedin.com/in/max-m (github.com/maxm)")
	if p.Personal.LinkedIn != "https://www.linkedin.com/in/max-m" || p.Personal.GitHub != "https://github.com/maxm" {
		t.Errorf("links = %q, %q", p.Personal.LinkedIn, p.Personal.GitHub)
	}
	if p := Parse("evil-linkedin.com/in/max"); p.Personal.LinkedIn != "" {
		t.Errorf("look-alike host taken: %q", p.Personal.LinkedIn)
	}
}
