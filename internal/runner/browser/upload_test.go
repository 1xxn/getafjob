package browser

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentsOnlyServePlainFilesInTheirDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cv.pdf"), []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.pdf")
	if err := os.WriteFile(big, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(big, MaxUpload+1); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := os.Symlink(outside, filepath.Join(dir, "link.pdf")) == nil // needs a privilege on Windows

	d, err := NewDocuments(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p, err := d.path("cv.pdf"); err != nil || filepath.Base(p) != "cv.pdf" {
		t.Fatalf("cv.pdf: %q, %v", p, err)
	}
	bad := []string{"", ".", "..", "../secret.txt", "sub", "sub/cv.pdf", `sub\cv.pdf`, "missing.pdf", "big.pdf", outside}
	if links {
		bad = append(bad, "link.pdf")
	}
	for _, name := range bad {
		if _, err := d.path(name); !errors.Is(err, ErrNotADocument) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := NewDocuments(filepath.Join(dir, "cv.pdf")); err == nil {
		t.Error("a file is not a document directory")
	}
}
