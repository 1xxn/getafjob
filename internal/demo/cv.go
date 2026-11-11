package demo

import (
	"bytes"
	"fmt"
	"strings"
)

// CV returns a small one-page PDF CV of the invented person, so runs on fixture forms can upload a document.
func CV(p Person) []byte {
	pr := p.Profile
	lines := []string{
		pr.Personal.FirstName + " " + pr.Personal.LastName,
		pr.Work.CurrentTitle,
		pr.Personal.Email + " / " + pr.Personal.Phone,
		pr.Personal.City,
		"",
		"Invented demo person for getafjob development. Not a real CV.",
	}
	var text strings.Builder
	text.WriteString("BT /F1 12 Tf 72 760 Td 16 TL\n")
	for _, l := range lines {
		fmt.Fprintf(&text, "(%s) Tj T*\n", pdfEscape(l))
	}
	text.WriteString("ET")
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", text.Len(), text.String()),
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
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// pdfEscape keeps a line printable in a PDF string with the standard font (Latin-1 subset, no umlauts needed).
func pdfEscape(s string) string {
	r := strings.NewReplacer(`\`, `\`, "(", `\(`, ")", `\)`, "ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss", "Ä", "Ae", "Ö", "Oe", "Ü", "Ue")
	return r.Replace(s)
}
