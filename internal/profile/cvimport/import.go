package cvimport

import (
	"context"
	"reflect"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/profile"
)

// TextSource extracts text from a document (Extractor in production).
type TextSource interface {
	Text(ctx context.Context, mime string, data []byte) (string, error)
}

// Importer fills a profile from one of the user's documents.
type Importer struct {
	docs     *profile.Documents
	profiles *profile.Service
	text     TextSource
}

// NewImporter wires the import.
func NewImporter(docs *profile.Documents, profiles *profile.Service, text TextSource) *Importer {
	return &Importer{docs: docs, profiles: profiles, text: text}
}

// Import reads the document, finds what the heuristics can and stores it in the empty fields of the profile.
// Nothing the user entered is changed. It reports whether anything was filled.
func (i *Importer) Import(ctx context.Context, userID, docID uuid.UUID) (bool, error) {
	d, data, err := i.docs.Read(ctx, userID, docID)
	if err != nil {
		return false, err
	}
	if d.Mime != profile.MimePDF && d.Mime != profile.MimeDOCX {
		return false, profile.ErrFileType
	}
	text, err := i.text.Text(ctx, d.Mime, data)
	if err != nil {
		return false, err
	}
	snap, err := i.profiles.Get(ctx, userID)
	if err != nil {
		return false, err
	}
	merged := Merge(snap.Profile, Parse(text))
	if reflect.DeepEqual(merged, snap.Profile) {
		return false, nil
	}
	// Heuristics can find odd values; drop what the schema refuses instead of failing the whole import.
	merged = dropInvalid(merged, snap.Profile)
	return true, i.profiles.SaveProfile(ctx, userID, merged, snap.Version)
}

// dropInvalid resets fields that fail validation to the user's value.
func dropInvalid(p, before profile.Profile) profile.Profile {
	for _, e := range p.Validate() {
		switch e.Field {
		case "email":
			p.Personal.Email = before.Personal.Email
		case "phone":
			p.Personal.Phone = before.Personal.Phone
		case "postal_code":
			p.Personal.PostalCode = before.Personal.PostalCode
		case "linkedin":
			p.Personal.LinkedIn = before.Personal.LinkedIn
		case "github":
			p.Personal.GitHub = before.Personal.GitHub
		case "website":
			p.Personal.Website = before.Personal.Website
		case "summary":
			p.Summary = before.Summary
		case "skills":
			p.Skills = before.Skills
		case "certifications":
			p.Certifications = before.Certifications
		case "languages":
			p.Languages = before.Languages
		case "experience":
			p.Experience = before.Experience
		case "education":
			p.Education = before.Education
		default:
			return before
		}
	}
	return p
}
