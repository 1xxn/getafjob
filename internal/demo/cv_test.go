package demo

import (
	"bytes"
	"testing"

	"github.com/1xxn/getafjob/internal/profile"
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
