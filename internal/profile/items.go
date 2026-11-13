package profile

import (
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Item kinds.
const (
	ItemAnswer = "answer"
	ItemText   = "text"
)

// Limits of the answer bank and text blocks.
const (
	MaxItems    = 500
	maxQuestion = 300
	maxAnswer   = 5000
	maxTextName = 100
	maxTextBody = 20000
)

// Answer is a saved answer to a recurring form question. The planner uses it only for the same question.
type Answer struct {
	ID       uuid.UUID `json:"-"`
	Question string    `json:"question"`
	Answer   string    `json:"answer"`
}

// TextBlock is a reusable text (cover letter parts, motivation) with placeholders like {company}.
type TextBlock struct {
	ID       uuid.UUID `json:"-"`
	Name     string    `json:"name"`
	Language string    `json:"language,omitempty"` // en, de or empty (any)
	Body     string    `json:"body"`
}

// Placeholders lists what text blocks may use; the values come from the profile and the job.
var Placeholders = []string{
	"first_name", "last_name", "full_name", "email", "phone", "city", "current_title", "notice_period",
	"earliest_start", "company", "job_title", "job_location", "date",
}

var (
	placeholderRx = regexp.MustCompile(`\{([a-z_]{1,40})\}`)
	// anyPlaceholderRx finds everything that looks like a placeholder, so {Company} or {Firma} is refused instead
	// of reaching an application as typed.
	anyPlaceholderRx = regexp.MustCompile(`\{([\p{L}\p{N}_ -]{1,40})\}`)
	optionalRx    = regexp.MustCompile(`\((optional|required|pflichtfeld|freiwillig)\)`)
)

// NormalizeQuestion is the key under which an answer is found: lower case, no punctuation or required markers.
func NormalizeQuestion(q string) string {
	q = strings.ToLower(strings.ReplaceAll(q, "*", " "))
	q = optionalRx.ReplaceAllString(q, " ")
	q = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return ' '
	}, q)
	q = strings.Join(strings.Fields(q), " ")
	if utf8.RuneCountInString(q) > maxQuestion {
		q = string([]rune(q)[:maxQuestion])
	}
	return q
}

// FindAnswer returns the answer saved for the same question.
func FindAnswer(answers []Answer, question string) (Answer, bool) {
	key := NormalizeQuestion(question)
	if key == "" {
		return Answer{}, false
	}
	for _, a := range answers {
		if NormalizeQuestion(a.Question) == key {
			return a, true
		}
	}
	return Answer{}, false
}

// Validate checks an answer.
func (a Answer) Validate() []FieldError {
	var c checker
	c.text("question", a.Question, maxQuestion)
	c.text("answer", a.Answer, maxAnswer)
	if NormalizeQuestion(a.Question) == "" {
		c.add("question", "profile.error.required")
	}
	if strings.TrimSpace(a.Answer) == "" {
		c.add("answer", "profile.error.required")
	}
	return c.errs
}

// Validate checks a text block; unknown placeholders are errors, so a typo never reaches an application.
func (t TextBlock) Validate() []FieldError {
	var c checker
	c.text("name", t.Name, maxTextName)
	c.text("body", t.Body, maxTextBody)
	if strings.TrimSpace(t.Name) == "" {
		c.add("name", "profile.error.required")
	}
	if strings.TrimSpace(t.Body) == "" {
		c.add("body", "profile.error.required")
	}
	if t.Language != "" && t.Language != "en" && t.Language != "de" {
		c.add("language", "profile.error.invalid")
	}
	known := map[string]bool{}
	for _, p := range Placeholders {
		known[p] = true
	}
	for _, m := range anyPlaceholderRx.FindAllStringSubmatch(t.Body, -1) {
		if !known[m[1]] {
			c.add("body", "profile.error.placeholder")
			break
		}
	}
	return c.errs
}

// Fill replaces the placeholders with values. Missing lists placeholders without a value; they stay in the text,
// so a caller never sends a text with a gap it did not notice.
func (t TextBlock) Fill(values map[string]string) (text string, missing []string) {
	seen := map[string]bool{}
	text = placeholderRx.ReplaceAllStringFunc(t.Body, func(m string) string {
		name := m[1 : len(m)-1]
		if v := strings.TrimSpace(values[name]); v != "" {
			return v
		}
		if !seen[name] {
			seen[name] = true
			missing = append(missing, name)
		}
		return m
	})
	return text, missing
}

// ProfileValues are the placeholder values a profile provides.
func ProfileValues(p Profile) map[string]string {
	full := strings.TrimSpace(p.Personal.FirstName + " " + p.Personal.LastName)
	return map[string]string{
		"first_name": p.Personal.FirstName, "last_name": p.Personal.LastName, "full_name": full,
		"email": p.Personal.Email, "phone": p.Personal.Phone, "city": p.Personal.City,
		"current_title": p.Work.CurrentTitle, "notice_period": p.Work.NoticePeriod, "earliest_start": p.Work.EarliestStart,
	}
}

// ErrTooManyItems reports a full answer bank or text block list.
var ErrTooManyItems = errors.New("profile: too many entries")

func cleanLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// trimText trims a multi-line text and normalizes line ends.
func trimText(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n"))
}
