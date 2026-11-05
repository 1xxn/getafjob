// Package cvimport reads the text of a CV (PDF or DOCX) and fills profile fields from it with heuristics, without
// AI. Parsing foreign files runs in a separate, limited process (see Extractor), never inside serve.
package cvimport

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/unicode/norm"

	"github.com/1xxn/getafjob/internal/profile"
)

// Limits of the extraction.
const (
	MaxText     = 1 << 20  // characters of text kept
	maxDocXML   = 20 << 20 // unpacked word/document.xml
	minUsefulCV = 200      // below this the PDF is most likely a scan
	maxPDFPages = 30
)

// ErrNoText reports a file without a text layer (a scan) or with too little text.
var ErrNoText = errors.New("cvimport: no text found")

// Text extracts the plain text of a PDF or DOCX (in the limited child process).
func Text(mime string, b []byte) (string, error) {
	var (
		s   string
		err error
	)
	switch mime {
	case profile.MimeDOCX:
		s, err = docxText(b)
	case profile.MimePDF:
		s, err = pdfText(b)
	default:
		return "", profile.ErrFileType
	}
	if err != nil {
		return "", err
	}
	s = Clean(s)
	if len([]rune(s)) < minUsefulCV {
		return "", ErrNoText
	}
	if r := []rune(s); len(r) > MaxText {
		s = string(r[:MaxText])
	}
	return s, nil
}

func docxText(b []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return "", profile.ErrFileType
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		if f.UncompressedSize64 > maxDocXML {
			return "", profile.ErrFileTooLarge
		}
		rc, err := f.Open()
		if err != nil {
			return "", profile.ErrFileType
		}
		defer func() { _ = rc.Close() }()
		return wordXML(io.LimitReader(rc, maxDocXML))
	}
	return "", profile.ErrFileType
}

// wordXML reads paragraphs (w:p), text runs (w:t), tabs and breaks of a Word document.
func wordXML(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	var sb strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return sb.String(), nil
		}
		if err != nil {
			return "", profile.ErrFileType
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				sb.WriteByte('\t')
			case "br", "cr":
				sb.WriteByte('\n')
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				sb.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		}
		if sb.Len() > 4*MaxText {
			return sb.String(), nil
		}
	}
}

func pdfText(b []byte) (s string, err error) {
	defer func() {
		if r := recover(); r != nil { // the parser panics on some broken files
			s, err = "", fmt.Errorf("%w: unreadable pdf", profile.ErrFileType)
		}
	}()
	r, err := pdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return "", profile.ErrFileType
	}
	if r.NumPage() > maxPDFPages {
		return "", profile.ErrFileTooLarge
	}
	pr, err := r.GetPlainText()
	if err != nil {
		return "", profile.ErrFileType
	}
	out, err := io.ReadAll(io.LimitReader(pr, 4*MaxText))
	return string(out), err
}

var (
	umlautRx = regexp.MustCompile(`¨\s*([aouAOUei])`)
	spaceRx  = regexp.MustCompile(`[ \t\x{00a0}]{3,}`)
	blankRx  = regexp.MustCompile(`\n{3,}`)
	umlauts  = map[string]string{"a": "ä", "o": "ö", "u": "ü", "A": "Ä", "O": "Ö", "U": "Ü", "e": "ë", "i": "ï"}
)

// Clean smooths typical PDF artifacts: split umlauts, ligatures, column gaps, empty lines.
func Clean(s string) string {
	s = strings.ToValidUTF8(s, "")
	s = umlautRx.ReplaceAllStringFunc(s, func(m string) string { return umlauts[m[len(m)-1:]] })
	s = norm.NFKC.String(s)
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = spaceRx.ReplaceAllString(strings.TrimSpace(l), "  ")
	}
	return strings.TrimSpace(blankRx.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
