// Package assist coordinates the browser extension with user profile data,
// form field inspection, answer suggestions, and approval-gated submissions (#39).
package assist

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidPairingCode = errors.New("invalid or expired pairing code")
	ErrInvalidToken       = errors.New("invalid or expired assist token")
	ErrUnapprovedSubmit   = errors.New("submission requires user approval")
	ErrNotFound           = errors.New("assist entity not found")
)

// PairingCode represents a short-lived one-time code to link an extension.
type PairingCode struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	CodeHash  []byte
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Token is a persistent bearer credential granted to an extension.
type Token struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	TokenHash  []byte
	TokenPlain string // populated only upon creation
	ClientName string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt *time.Time
}

// Answer represents an answer bank entry.
type Answer struct {
	ID       string
	Question string
	Answer   string
}

// TextBlock represents a reusable text block.
type TextBlock struct {
	ID   string
	Name string
	Body string
}

// Document represents an available document (e.g. CV).
type Document struct {
	ID   string
	Name string
	Kind string
}

// FormField describes a detected input or select field on a web page.
type FormField struct {
	ID       string
	Name     string
	Label    string
	Type     string
	Required bool
	Selector string
}

// FieldMatch holds a value and provenance for an inspected field.
type FieldMatch struct {
	FieldID    string
	Value      string
	Provenance string
	Confidence float64
	Source     string
}

// InspectResult summarizes suggestions and required items for a form.
type InspectResult struct {
	Matches          []FieldMatch
	MissingRequired  []string
	RequiresApproval bool
}

// ApprovalItem represents an application awaiting human review.
type ApprovalItem struct {
	ID        uuid.UUID
	Company   string
	Title     string
	URL       string
	Score     int
	CreatedAt time.Time
}

// GenerateCode creates a human-friendly pairing code like "GAJ-W9X2-K4PM": 8 random characters from 32 (40 bits),
// so that guessing a code within its 10 minutes is out of reach even before the rate limit.
func GenerateCode() (plain string, hash []byte, err error) {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var sb strings.Builder
	sb.WriteString("GAJ-")
	for i := range 8 {
		if i == 4 {
			sb.WriteByte('-')
		}
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", nil, err
		}
		sb.WriteByte(chars[idx.Int64()])
	}
	plain = sb.String()
	h := sha256.Sum256([]byte(strings.ToUpper(strings.TrimSpace(plain))))
	return plain, h[:], nil
}

// HashCode computes the SHA-256 hash of a normalized pairing code.
func HashCode(code string) []byte {
	norm := strings.ToUpper(strings.TrimSpace(code))
	h := sha256.Sum256([]byte(norm))
	return h[:]
}

// NewToken generates a 32-byte secure bearer token.
func NewToken(userID uuid.UUID, clientName string, now time.Time, ttl time.Duration) (Token, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, err
	}
	plain := hex.EncodeToString(raw)
	h := sha256.Sum256([]byte(plain))
	return Token{
		ID:         uuid.New(),
		UserID:     userID,
		TokenHash:  h[:],
		TokenPlain: plain,
		ClientName: clientName,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
	}, nil
}

// HashToken computes the SHA-256 hash of a bearer token.
func HashToken(tok string) []byte {
	h := sha256.Sum256([]byte(strings.TrimSpace(tok)))
	return h[:]
}

// MatchForm inspects detected fields against known user facts and answers.
func MatchForm(fields []FormField, facts map[string]string, answers []Answer, texts []TextBlock) InspectResult {
	res := InspectResult{
		Matches:          make([]FieldMatch, 0, len(fields)),
		MissingRequired:  make([]string, 0),
		RequiresApproval: true, // Always require approval before submit per ADR 0016
	}

	for _, f := range fields {
		match, found := matchField(f, facts, answers, texts)
		if found {
			res.Matches = append(res.Matches, match)
		} else if f.Required {
			label := f.Label
			if label == "" {
				label = f.Name
			}
			res.MissingRequired = append(res.MissingRequired, label)
		}
	}

	return res
}

func matchField(f FormField, facts map[string]string, answers []Answer, _ []TextBlock) (FieldMatch, bool) {
	norm := strings.ToLower(f.Label + " " + f.Name)

	// 1. Direct fact mappings
	factKeys := []struct {
		needles []string
		key     string
	}{
		{[]string{"first name", "firstname", "vorname", "given name"}, "personal.first_name"},
		{[]string{"last name", "lastname", "nachname", "surname", "family name"}, "personal.last_name"},
		{[]string{"full name", "fullname", "name", "complete name"}, "personal.full_name"},
		{[]string{"email", "e-mail", "mail address"}, "personal.email"},
		{[]string{"phone", "telephone", "mobile", "telefon", "mobilnummer"}, "personal.phone"},
		{[]string{"linkedin", "linked in"}, "personal.linkedin"},
		{[]string{"github"}, "personal.github"},
		{[]string{"portfolio", "website", "personal site", "url"}, "personal.website"},
		{[]string{"city", "location", "stadt", "ort", "residence"}, "personal.city"},
		{[]string{"postal", "zip", "postleitzahl", "plz"}, "personal.postal_code"},
		{[]string{"country", "land", "staat"}, "personal.country"},
		{[]string{"address", "street", "strasse", "anschrift"}, "personal.address"},
		{[]string{"salary", "gehalt", "compensation", "expectation"}, "work.salary_expectation"},
		{[]string{"notice", "kuendigung", "start date", "earliest", "verfuegbarkeit"}, "work.notice_period"},
	}

	for _, fk := range factKeys {
		for _, needle := range fk.needles {
			if strings.Contains(norm, needle) {
				if val, ok := facts[fk.key]; ok && strings.TrimSpace(val) != "" {
					return FieldMatch{
						FieldID:    f.ID,
						Value:      val,
						Provenance: "profile:" + fk.key,
						Confidence: 0.95,
						Source:     "profile",
					}, true
				}
			}
		}
	}

	// 2. Full name synthesized fallback if full_name key is absent
	if strings.Contains(norm, "name") && !strings.Contains(norm, "first") && !strings.Contains(norm, "last") && !strings.Contains(norm, "company") {
		first := facts["personal.first_name"]
		last := facts["personal.last_name"]
		if first != "" && last != "" {
			return FieldMatch{
				FieldID:    f.ID,
				Value:      first + " " + last,
				Provenance: "profile:personal.first_name+last_name",
				Confidence: 0.9,
				Source:     "profile",
			}, true
		}
	}

	// 3. Answer bank questions
	for _, a := range answers {
		qNorm := strings.ToLower(a.Question)
		if strings.Contains(norm, qNorm) || strings.Contains(qNorm, strings.TrimSpace(f.Label)) {
			return FieldMatch{
				FieldID:    f.ID,
				Value:      a.Answer,
				Provenance: "answer:" + a.ID,
				Confidence: 0.85,
				Source:     "answer_bank",
			}, true
		}
	}

	return FieldMatch{}, false
}
