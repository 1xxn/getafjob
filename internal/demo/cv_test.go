package demo

import (
	"bytes"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/cvimport"
)

func TestCVIsAPDFTheDocumentStoreAccepts(t *testing.T) {
	for _, p := range People() {
		b := CV(p)
		if mime, err := profile.Sniff(b); err != nil || mime != profile.MimePDF {
			t.Fatalf("%s: %q, %v", p.Email, mime, err)
		}
		if !bytes.Contains(b, []byte(p.Profile.Personal.LastName)) || !bytes.HasSuffix(b, []byte("%%EOF\n")) {
			t.Fatalf("%s: name or trailer missing", p.Email)
		}
	}
}

// The review import reads the demo CV like a real one (#214).
func TestCVTextIsExtractable(t *testing.T) {
	for _, p := range People() {
		text, err := cvimport.Text(profile.MimePDF, CV(p))
		if err != nil {
			t.Fatalf("%s: %v", p.Email, err)
		}
		for _, want := range []string{p.Profile.Personal.LastName, "Experience", "Muster Software GmbH", p.Profile.Skills[0]} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: %q missing in %q", p.Email, want, text)
			}
		}
	}
}
