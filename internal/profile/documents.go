package profile

import (
	"archive/zip"
	"bytes"
	"errors"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Document kinds.
const (
	KindCV          = "cv"
	KindCertificate = "certificate"
	KindOther       = "other"
)

// Accepted file types.
const (
	MimePDF  = "application/pdf"
	MimeDOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	MimePNG  = "image/png"
	MimeJPEG = "image/jpeg"
)

// Upload limits.
const (
	MaxDocument     = 10 << 20
	MaxDocuments    = 50
	maxZipEntries   = 2000
	maxZipUnpacked  = 100 << 20
	maxDocumentName = 200
	maxFocus        = 30
)

// Document errors.
var (
	ErrFileType     = errors.New("profile: file type not accepted")
	ErrFileTooLarge = errors.New("profile: file too large")
	ErrTooManyFiles = errors.New("profile: too many documents")
	ErrNotFound     = errors.New("profile: not found")
)

// Document is a stored file of the user. A CV is also a variant: Label, Language and Focus choose it per job.
type Document struct {
	ID        uuid.UUID
	Kind      string
	Name      string // original file name, cleaned
	Mime      string
	Size      int
	Label     string
	Language  string   // en, de or empty (any)
	Focus     []string // words of jobs this CV fits, e.g. platform, kubernetes
	CreatedAt time.Time
}

// ValidKind reports whether k is a document kind.
func ValidKind(k string) bool { return k == KindCV || k == KindCertificate || k == KindOther }

// Sniff returns the type of a file from its first bytes, never from its name or the browser's claim. A DOCX must
// be a well-formed zip with a Word document inside and bounded entries (no zip bombs).
func Sniff(b []byte) (string, error) {
	switch {
	case len(b) == 0:
		return "", ErrFileType
	case len(b) > MaxDocument:
		return "", ErrFileTooLarge
	case bytes.HasPrefix(b, []byte("%PDF-")):
		return MimePDF, nil
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return MimePNG, nil
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return MimeJPEG, nil
	case bytes.HasPrefix(b, []byte("PK\x03\x04")):
		if isDOCX(b) {
			return MimeDOCX, nil
		}
	}
	return "", ErrFileType
}

func isDOCX(b []byte) bool {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil || len(zr.File) > maxZipEntries {
		return false
	}
	var unpacked uint64
	word, types := false, false
	for _, f := range zr.File {
		unpacked += f.UncompressedSize64
		if unpacked > maxZipUnpacked || strings.Contains(f.Name, "..") || strings.HasPrefix(f.Name, "/") {
			return false
		}
		switch f.Name {
		case "word/document.xml":
			word = true
		case "[Content_Types].xml":
			types = true
		}
	}
	return word && types
}

// CleanName keeps the base name of an uploaded file without control characters, at most 200 characters.
func CleanName(name, mime string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == '"' || r == '/' {
			return -1
		}
		return r
	}, strings.ToValidUTF8(name, ""))
	name = strings.Join(strings.Fields(name), " ")
	if name == "." || name == "" {
		name = "document"
	}
	if utf8.RuneCountInString(name) > maxDocumentName {
		name = string([]rune(name)[:maxDocumentName])
	}
	ext := map[string]string{MimePDF: ".pdf", MimeDOCX: ".docx", MimePNG: ".png", MimeJPEG: ".jpg"}[mime]
	low := strings.ToLower(name)
	hasExt := strings.HasSuffix(low, ext) || (mime == MimeJPEG && strings.HasSuffix(low, ".jpeg"))
	if ext != "" && !hasExt {
		name += ext
	}
	return name
}

// CleanVariant normalizes the variant fields of a CV.
func CleanVariant(label, language string, focus []string) (string, string, []string, error) {
	label = strings.Join(strings.Fields(label), " ")
	if utf8.RuneCountInString(label) > 100 || (language != "" && language != "en" && language != "de") || len(focus) > maxFocus {
		return "", "", nil, ErrFileType
	}
	out := make([]string, 0, len(focus))
	for _, f := range focus {
		if f = strings.ToLower(strings.TrimSpace(f)); f != "" && utf8.RuneCountInString(f) <= 60 {
			out = append(out, f)
		}
	}
	return label, language, out, nil
}

// ChooseCV picks the CV for a job: same language first, then the most focus words found in the job title and
// description, then the newest. It returns false when the user has no CV.
func ChooseCV(docs []Document, lang, title, description string) (Document, bool) {
	text := strings.ToLower(title + " " + title + " " + description) // the title counts twice
	best, found, bestScore := Document{}, false, -1
	for _, d := range docs {
		if d.Kind != KindCV {
			continue
		}
		score := 0
		switch {
		case d.Language != "" && d.Language == lang:
			score += 1000
		case d.Language == "":
			score += 500
		}
		for _, f := range d.Focus {
			score += 10 * strings.Count(text, f)
		}
		if score > bestScore || (score == bestScore && d.CreatedAt.After(best.CreatedAt)) {
			best, found, bestScore = d, true, score
		}
	}
	return best, found
}
