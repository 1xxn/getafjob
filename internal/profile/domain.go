// Package profile holds what a user tells getafjob about themselves: the applicant profile, search preferences,
// documents, answers and text blocks. Personal content is stored sealed with the user's data key.
package profile

import (
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Profile is the applicant profile; empty fields are unknown (the planner asks instead of guessing).
type Profile struct {
	Personal       Personal   `json:"personal"`
	Work           Work       `json:"work"`
	Summary        string     `json:"summary,omitempty"`
	Skills         []string   `json:"skills,omitempty"`
	Languages      []Language `json:"languages,omitempty"`
	Certifications []string   `json:"certifications,omitempty"`
}

// Personal holds contact data.
type Personal struct {
	FirstName  string `json:"first_name,omitempty"`
	LastName   string `json:"last_name,omitempty"`
	Email      string `json:"email,omitempty"`
	Phone      string `json:"phone,omitempty"`
	Street     string `json:"street,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	City       string `json:"city,omitempty"`
	Country    string `json:"country,omitempty"`
	LinkedIn   string `json:"linkedin,omitempty"`
	GitHub     string `json:"github,omitempty"`
	Website    string `json:"website,omitempty"`
	Pronouns   string `json:"pronouns,omitempty"`
}

// Work holds the facts application forms ask about.
type Work struct {
	CurrentTitle       string `json:"current_title,omitempty"`
	CurrentEmployer    string `json:"current_employer,omitempty"`
	YearsTotal         string `json:"years_total,omitempty"`
	YearsRelevant      string `json:"years_relevant,omitempty"`
	NoticePeriod       string `json:"notice_period,omitempty"`
	EarliestStart      string `json:"earliest_start,omitempty"`
	SalaryExpectation  string `json:"salary_expectation,omitempty"` // yearly gross in EUR, as typed
	WorkAuthorizedEU   bool   `json:"work_authorized_eu"`
	NeedsSponsorship   bool   `json:"needs_sponsorship"`
	WillingToRelocate  bool   `json:"willing_to_relocate"`
	DriversLicense     string `json:"drivers_license,omitempty"`
	WorkplacePreferred string `json:"workplace_preferred,omitempty"` // free text, e.g. "Remote in Germany"
}

// Language is a language with a level (e.g. "German", "native" or "C1").
type Language struct {
	Name  string `json:"name"`
	Level string `json:"level"`
}

// Preferences steer search and matching; they hold no contact data and stay readable for the crawler.
type Preferences struct {
	Titles          []string `json:"titles,omitempty"`         // wanted job titles and keywords
	ExcludeTitles   []string `json:"exclude_titles,omitempty"` // titles with these words are skipped
	Locations       []string `json:"locations,omitempty"`      // places for on-site or hybrid work
	Remote          bool     `json:"remote"`
	RemoteRegions   []string `json:"remote_regions,omitempty"` // e.g. Germany, EU
	MinSalary       int      `json:"min_salary,omitempty"`     // yearly gross in EUR, 0: none
	CoverLetterLang string   `json:"cover_letter_lang,omitempty"`
	Diversity       string   `json:"diversity,omitempty"` // decline or answer
	TalentPool      bool     `json:"talent_pool"`
}

// Limits of the schema.
const (
	maxShort    = 200
	maxLong     = 5000
	maxItems    = 100
	maxSalary   = 10_000_000
	maxListItem = 120
)

// FieldError names an invalid field and an i18n code (profile.error.*).
type FieldError struct {
	Field string
	Code  string
}

var (
	phoneRx  = regexp.MustCompile(`^\+?[0-9 ()./-]{5,30}$`)
	postalRx = regexp.MustCompile(`^[A-Za-z0-9 -]{2,12}$`)
)

type checker struct{ errs []FieldError }

func (c *checker) add(field, code string) {
	c.errs = append(c.errs, FieldError{Field: field, Code: code})
}

func (c *checker) text(field, v string, limit int) {
	switch {
	case !utf8.ValidString(v):
		c.add(field, "profile.error.invalid")
	case utf8.RuneCountInString(v) > limit:
		c.add(field, "profile.error.too_long")
	case strings.ContainsAny(v, "\x00"):
		c.add(field, "profile.error.invalid")
	}
}

func (c *checker) list(field string, vs []string) {
	if len(vs) > maxItems {
		c.add(field, "profile.error.too_many")
		return
	}
	for _, v := range vs {
		c.text(field, v, maxListItem)
	}
}

func (c *checker) url(field, v string) {
	c.text(field, v, maxShort)
	if low := strings.ToLower(v); v != "" && !strings.HasPrefix(low, "https://") && !strings.HasPrefix(low, "http://") {
		c.add(field, "profile.error.url")
	}
}

// Validate checks the profile against the schema; it returns every problem, not just the first.
func (p Profile) Validate() []FieldError {
	var c checker
	pe := p.Personal
	for f, v := range map[string]string{
		"first_name": pe.FirstName, "last_name": pe.LastName, "street": pe.Street, "city": pe.City,
		"country": pe.Country, "pronouns": pe.Pronouns,
	} {
		c.text(f, v, maxShort)
	}
	if pe.Email != "" {
		if a, err := mail.ParseAddress(pe.Email); err != nil || a.Address != pe.Email || a.Name != "" {
			c.add("email", "profile.error.email")
		}
		c.text("email", pe.Email, 254)
	}
	if pe.Phone != "" && !phoneRx.MatchString(pe.Phone) {
		c.add("phone", "profile.error.phone")
	}
	if pe.PostalCode != "" && !postalRx.MatchString(pe.PostalCode) {
		c.add("postal_code", "profile.error.invalid")
	}
	c.url("linkedin", pe.LinkedIn)
	c.url("github", pe.GitHub)
	c.url("website", pe.Website)
	w := p.Work
	for f, v := range map[string]string{
		"current_title": w.CurrentTitle, "current_employer": w.CurrentEmployer, "years_total": w.YearsTotal,
		"years_relevant": w.YearsRelevant, "notice_period": w.NoticePeriod, "earliest_start": w.EarliestStart,
		"salary_expectation": w.SalaryExpectation, "drivers_license": w.DriversLicense,
		"workplace_preferred": w.WorkplacePreferred,
	} {
		c.text(f, v, maxShort)
	}
	c.text("summary", p.Summary, maxLong)
	c.list("skills", p.Skills)
	c.list("certifications", p.Certifications)
	if len(p.Languages) > 30 {
		c.add("languages", "profile.error.too_many")
	}
	for _, l := range p.Languages {
		c.text("languages", l.Name, maxListItem)
		c.text("languages", l.Level, maxListItem)
		if strings.TrimSpace(l.Name) == "" {
			c.add("languages", "profile.error.required")
		}
	}
	return c.errs
}

// Missing lists the fields most forms need that are still empty, in the order of the profile form (the assistant
// asks for them, the profile page links to them).
func (p Profile) Missing() []string {
	var out []string
	for _, f := range []struct{ key, v string }{
		{"first_name", p.Personal.FirstName}, {"last_name", p.Personal.LastName}, {"email", p.Personal.Email},
		{"phone", p.Personal.Phone}, {"city", p.Personal.City}, {"years_total", p.Work.YearsTotal},
		{"notice_period", p.Work.NoticePeriod}, {"earliest_start", p.Work.EarliestStart},
		{"salary_expectation", p.Work.SalaryExpectation},
	} {
		if strings.TrimSpace(f.v) == "" {
			out = append(out, f.key)
		}
	}
	return out
}

// Validate checks the preferences.
func (p Preferences) Validate() []FieldError {
	var c checker
	c.list("titles", p.Titles)
	c.list("exclude_titles", p.ExcludeTitles)
	c.list("locations", p.Locations)
	c.list("remote_regions", p.RemoteRegions)
	if p.MinSalary < 0 || p.MinSalary > maxSalary {
		c.add("min_salary", "profile.error.invalid")
	}
	if p.CoverLetterLang != "" && p.CoverLetterLang != "auto" && p.CoverLetterLang != "en" && p.CoverLetterLang != "de" {
		c.add("cover_letter_lang", "profile.error.invalid")
	}
	if p.Diversity != "" && p.Diversity != "decline" && p.Diversity != "answer" {
		c.add("diversity", "profile.error.invalid")
	}
	return c.errs
}

// Lines splits a textarea into trimmed, non-empty, unique lines (also accepts commas).
func Lines(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' || r == ';' }) {
		l = strings.Join(strings.Fields(l), " ")
		if l == "" || seen[strings.ToLower(l)] {
			continue
		}
		seen[strings.ToLower(l)] = true
		out = append(out, l)
	}
	return out
}

// WebURL adds https:// to a bare link ("linkedin.com/in/max" → "https://linkedin.com/in/max").
func WebURL(v string) string {
	v = strings.TrimSpace(v)
	low := strings.ToLower(v)
	if v == "" || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://") {
		return v
	}
	return "https://" + strings.TrimLeft(v, "/")
}
