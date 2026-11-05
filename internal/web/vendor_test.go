package web

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// The vendored htmx files stay exactly as downloaded and checked (static/vendor/htmx/README.md).
func TestVendoredFilesMatchChecksums(t *testing.T) {
	sums, err := staticFiles.ReadFile("static/vendor/htmx/SHA256SUMS")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		want, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("bad line %q", line)
		}
		data, err := staticFiles.ReadFile("static/vendor/htmx/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != want {
			t.Errorf("%s changed", name)
		}
		n++
	}
	if n != 3 {
		t.Fatalf("%d files listed", n)
	}
}
