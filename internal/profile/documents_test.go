package profile

import (
	"archive/zip"
	"bytes"
	"errors"
	"testing"
	"time"
)

// DocxFile builds a minimal Word file; extra entries can be added (tests).
func DocxFile(t testing.TB, extra map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{"[Content_Types].xml": "<Types/>", "word/document.xml": "<w:document/>"}
	for k, v := range extra {
		files[k] = v
	}
	for name, body := range files {
		if body == "-" {
			continue
		}
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSniff(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
		err  error
	}{
		{"pdf", []byte("%PDF-1.7\n..."), MimePDF, nil},
		{"png", []byte("\x89PNG\r\n\x1a\n...."), MimePNG, nil},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0}, MimeJPEG, nil},
		{"docx", DocxFile(t, nil), MimeDOCX, nil},
		{"zip without document", DocxFile(t, map[string]string{"word/document.xml": "-"}), "", ErrFileType},
		{"zip slip", DocxFile(t, map[string]string{"../../evil": "x"}), "", ErrFileType},
		{"html claiming pdf", []byte("<html><script>alert(1)</script>"), "", ErrFileType},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`), "", ErrFileType},
		{"empty", nil, "", ErrFileType},
		{"too large", append([]byte("%PDF-"), make([]byte, MaxDocument)...), "", ErrFileTooLarge},
	}
	for _, c := range cases {
		got, err := Sniff(c.data)
		if got != c.want || !errors.Is(err, c.err) {
			t.Errorf("%s: %q, %v", c.name, got, err)
		}
	}
}

func TestCleanName(t *testing.T) {
	for in, want := range map[[2]string]string{
		{`C:\Users\x\CV Max Mustermann.pdf`, MimePDF}: "CV Max Mustermann.pdf",
		{"../../etc/passwd", MimePDF}:                 "passwd.pdf",
		{"cv\r\n\"evil\".docx", MimeDOCX}:             "cvevil.docx",
		{"", MimeJPEG}:                                "document.jpg",
		{"photo.JPEG", MimeJPEG}:                      "photo.JPEG",
		{"cv.pdf.exe", MimePDF}:                       "cv.pdf.exe.pdf",
	} {
		if got := CleanName(in[0], in[1]); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in[0], got, want)
		}
	}
}

func TestChooseCV(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	docs := []Document{
		{Label: "general en", Kind: KindCV, Language: "en", CreatedAt: now},
		{Label: "platform en", Kind: KindCV, Language: "en", Focus: []string{"platform", "kubernetes"}, CreatedAt: now.Add(-time.Hour)},
		{Label: "allgemein de", Kind: KindCV, Language: "de", CreatedAt: now},
		{Label: "certificate", Kind: KindCertificate, Language: "en", Focus: []string{"platform"}, CreatedAt: now},
	}
	for _, c := range []struct{ lang, title, want string }{
		{"en", "Platform Engineer (Kubernetes)", "platform en"},
		{"en", "Backend Developer", "general en"},
		{"de", "Platform Engineer", "allgemein de"},
		{"fr", "Platform Engineer", "platform en"},
	} {
		if got, ok := ChooseCV(docs, c.lang, c.title, ""); !ok || got.Label != c.want {
			t.Errorf("ChooseCV(%s, %q) = %q", c.lang, c.title, got.Label)
		}
	}
	// Focus words count once and only as whole words, and never outweigh the language.
	noisy := "Go Go Go: going to Google, good goals, go go go"
	lang := []Document{
		{Label: "go de", Kind: KindCV, Language: "de", Focus: []string{"go", "golang"}, CreatedAt: now},
		{Label: "any", Kind: KindCV, Focus: []string{"go"}, CreatedAt: now.Add(-time.Hour)},
		{Label: "plain en", Kind: KindCV, Language: "en", CreatedAt: now.Add(-2 * time.Hour)},
	}
	if got, _ := ChooseCV(lang, "en", "Backend Engineer", noisy); got.Label != "plain en" {
		t.Errorf("language outweighed by focus: %q", got.Label)
	}
	sub := []Document{
		{Label: "go", Kind: KindCV, Focus: []string{"go"}, CreatedAt: now.Add(-time.Hour)},
		{Label: "newer", Kind: KindCV, CreatedAt: now},
	}
	if got, _ := ChooseCV(sub, "en", "Engineer", "We use Google Cloud"); got.Label != "newer" {
		t.Errorf("substring counted as focus word: %q", got.Label)
	}
	if got, _ := ChooseCV([]Document{{Label: "c++", Kind: KindCV, Focus: []string{"C++"}}, {Label: "c#", Kind: KindCV, Focus: []string{"c#"}, CreatedAt: now}},
		"", "C++ Developer", ""); got.Label != "c++" {
		t.Errorf("C++ focus: %q", got.Label)
	}
	if _, ok := ChooseCV(docs[3:], "en", "x", ""); ok {
		t.Error("certificate chosen as CV")
	}
}

func FuzzSniff(f *testing.F) {
	f.Add([]byte("%PDF-1.4"))
	f.Add(DocxFile(f, nil))
	f.Fuzz(func(t *testing.T, b []byte) {
		if m, err := Sniff(b); err == nil && m == "" {
			t.Fatal("accepted without a type")
		}
	})
}
