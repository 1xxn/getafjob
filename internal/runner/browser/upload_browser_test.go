//go:build browser

package browser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func documents(t *testing.T, names ...string) *Documents {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("%PDF-1.4\n% fictional\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d, err := NewDocuments(dir)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestUploadIntoVisibleFields(t *testing.T) {
	s := newSpike(t, "upload.html")
	docs := documents(t, "Max_Mustermann_CV.pdf", "Zeugnis_1.pdf", "Zeugnis_2.pdf")
	st := s.extract()
	cv, certs, city := s.field(st, "file", "CV").ID, s.field(st, "file", "Certificates").ID, s.field(st, "", "City").ID
	if err := st.Upload(docs, cv, []string{"Max_Mustermann_CV.pdf"}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := st.Upload(docs, certs, []string{"Zeugnis_1.pdf", "Zeugnis_2.pdf"}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		id    string
		files []string
		want  error
	}{
		"two files into a single field": {cv, []string{"Zeugnis_1.pdf", "Zeugnis_2.pdf"}, ErrNotFileField},
		"text field":                    {city, []string{"Zeugnis_1.pdf"}, ErrNotFileField},
		"path outside the documents":    {cv, []string{"../etc/passwd"}, ErrNotADocument},
		"unknown document":              {cv, []string{"other.pdf"}, ErrNotADocument},
	} {
		if err := st.Upload(docs, tc.id, tc.files, time.Second); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestUploadThroughDropZoneChooser(t *testing.T) {
	s := newSpike(t, "dropzone.html")
	docs := documents(t, "Anschreiben.pdf")
	st := s.extract()
	if err := st.Upload(docs, s.field(st, "file", "").ID, []string{"Anschreiben.pdf"}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if txt := s.page.MustElement("#label").MustText(); txt != "Anschreiben.pdf" {
		t.Fatalf("zone label = %q", txt) // the zone only accepts files from its own chooser
	}
}

func TestUploadThroughDropZoneInOutOfProcessIframe(t *testing.T) {
	s := newSpike(t, "posting.html")
	other := strings.Replace(s.site.URL, "127.0.0.1", "localhost", 1) + "/dropzone.html"
	s.open(s.site.URL + "/oopif?src=" + other)
	docs := documents(t, "Anschreiben.pdf")
	var st *State
	eventually(t, 10*time.Second, func() error {
		var err error
		if st, err = Extract(s.page); err != nil {
			return err
		}
		if len(st.Frames) < 2 || len(st.Frames[1].Fields) == 0 {
			return fmt.Errorf("iframe not ready (%d frames)", len(st.Frames))
		}
		return nil
	})
	if st.Frames[1].sess == nil {
		t.Fatal("iframe is not out-of-process")
	}
	if err := st.Upload(docs, s.field(st, "file", "").ID, []string{"Anschreiben.pdf"}, 3*time.Second); err != nil {
		t.Fatal(err)
	}
	v, err := st.Frames[1].run(`() => document.getElementById("label").textContent`, nil)
	if err != nil || v.Str() != "Anschreiben.pdf" {
		t.Fatalf("zone label in iframe = %q, %v", v.Str(), err)
	}
}
