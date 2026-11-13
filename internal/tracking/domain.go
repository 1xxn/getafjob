// Package tracking checks user inboxes (IMAP) read-only for application updates,
// applies heuristic rules for confirmations, rejections, and interview invitations,
// and produces status suggestions for user approval.
// Security codes are strictly excluded and never read automatically (ADR 0015).
package tracking

import (
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Category classifies an incoming email message.
type Category string

const (
	CategoryReceived     Category = "received"
	CategoryRejection    Category = "rejection"
	CategoryInvitation   Category = "invitation"
	CategorySecurityCode Category = "security_code"
	CategoryOther        Category = "other"
)

// Suggestion statuses.
const (
	SuggestionPending   = "pending"
	SuggestionAccepted  = "accepted"
	SuggestionDismissed = "dismissed"
)

// Target application states.
const (
	StateConfirmed = "confirmed"
	StateRejected  = "rejected"
	StateInterview = "interview"
)

// ErrInvalidSuggestion reports an invalid or already processed suggestion.
var ErrInvalidSuggestion = errors.New("invalid or already processed suggestion")

// SuggestedState maps a category to the corresponding target application state.
func SuggestedState(cat Category) (string, bool) {
	switch cat {
	case CategoryReceived:
		return StateConfirmed, true
	case CategoryRejection:
		return StateRejected, true
	case CategoryInvitation:
		return StateInterview, true
	default:
		return "", false
	}
}

// EmailMessage holds a fetched email message from IMAP.
type EmailMessage struct {
	UID       uint32
	MessageID string
	From      string
	To        []string
	Subject   string
	Date      time.Time
	Body      string // plain text representation
}

// ApplicationSummary holds minimal application details needed for matching incoming emails.
type ApplicationSummary struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	JobID        int64
	CurrentState string
	Company      string
	Title        string
	MatchKey     string
	URL          string
}

// StatusSuggestion is a proposed application status change based on an email.
type StatusSuggestion struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	ApplicationID  uuid.UUID
	MessageUID     uint32
	MessageID      string
	MessageFrom    string
	MessageSubject string
	MessageDate    time.Time
	Category       Category
	SuggestedState string
	Reasons        []string
	Status         string
	CreatedAt      time.Time
	UpdatedAt      time.Time

	// View/display fields
	Company      string
	JobTitle     string
	CurrentState string
}

// Classify classifies an incoming email using heuristic rules in English and German.
// Security codes are identified first and strictly excluded (ADR 0015).
func Classify(msg EmailMessage) (Category, []string) {
	subj := strings.ToLower(msg.Subject)
	body := strings.ToLower(msg.Body)
	full := subj + " " + body

	// 1. Security codes: NEVER read automatically, always ignored for applications.
	if isSecurityCode(subj, body) {
		return CategorySecurityCode, nil
	}

	// 2. Rejection before invitation: a no after an interview mentions the interview too.
	if match, reasons := isRejection(subj, body, full); match {
		return CategoryRejection, reasons
	}

	// 3. Invitation to interview
	if match, reasons := isInvitation(subj, body, full); match {
		return CategoryInvitation, reasons
	}

	// 4. Confirmation / Received
	if match, reasons := isReceived(subj, body, full); match {
		return CategoryReceived, reasons
	}

	return CategoryOther, nil
}

// securityWordRx finds short security terms as whole words ("otp" but not "footprint").
var securityWordRx = regexp.MustCompile(`\b(otp|2fa)\b`)

// isSecurityCode detects 2FA, OTP, verification and security codes.
func isSecurityCode(subj, body string) bool {
	if securityWordRx.MatchString(subj) || securityWordRx.MatchString(body) {
		return true
	}
	secTerms := []string{
		"verification code", "security code", "one-time password", "two-factor",
		"authentication code", "login code", "confirm your email", "password reset", "temporary code",
		"sicherheitscode", "verifizierungscode", "einmal-passwort", "einmalpasswort", "bestätigungscode",
		"anmeldecode", "anmelde-code", "prüfcode", "verifiziere deine", "bestätige deine e-mail",
	}
	for _, term := range secTerms {
		if strings.Contains(subj, term) || strings.Contains(body, term) {
			return true
		}
	}
	return false
}

// isInvitation detects interview or meeting invitations.
func isInvitation(subj, body, full string) (bool, []string) {
	inviteTerms := []string{
		"einladung zum vorstellungsgespräch",
		"einladung zum interview",
		"einladung zum kennenlerngespräch",
		"einladung zu einem",
		"terminvorschlag für ein",
		"vorstellungsgespräch",
		"kennenlerngespräch",
		"telefoninterview",
		"videointerview",
		"gesprächstermin",
		"interview invitation",
		"invite you to an interview",
		"schedule an interview",
		"invitation to interview",
		"next steps in our process",
		"chat about your application",
		"phone screen",
		"technical interview",
		"like to invite you",
	}

	for _, it := range inviteTerms {
		if strings.Contains(full, it) {
			return true, []string{"track.reason.invitation"}
		}
	}
	return false, nil
}

// isRejection detects rejection notices.
func isRejection(subj, body, full string) (bool, []string) {
	rejectTerms := []string{
		"leider müssen wir ihnen mitteilen",
		"nicht näher in betracht ziehen",
		"anderweitig entschieden",
		"haben uns für einen anderen",
		"haben uns für eine andere",
		"leider eine absage",
		"können ihre bewerbung nicht berücksichtigen",
		"müssen ihnen leider absagen",
		"nicht für die engere auswahl",
		"unable to offer you an interview",
		"decided not to move forward",
		"pursuing other candidates",
		"not selected for this role",
		"position has been filled",
		"will not be moving forward",
		"other applicants whose qualifications",
		"cannot consider your application further",
		"regret to inform you",
	}

	for _, rt := range rejectTerms {
		if strings.Contains(full, rt) {
			return true, []string{"track.reason.rejection"}
		}
	}
	return false, nil
}

// isReceived detects confirmation and receipt notices.
func isReceived(subj, body, full string) (bool, []string) {
	receivedTerms := []string{
		"eingangsbestätigung",
		"haben ihre bewerbung erhalten",
		"vielen dank für ihre bewerbung",
		"bestätigung ihrer bewerbung",
		"eingang ihrer bewerbung",
		"bewerbung ist bei uns eingegangen",
		"bewerbungseingang",
		"thank you for your application",
		"we have received your application",
		"application received",
		"successfully submitted",
		"confirmation of application",
		"received your resume",
		"application has been received",
	}

	for _, rt := range receivedTerms {
		if strings.Contains(full, rt) {
			return true, []string{"track.reason.received"}
		}
	}
	return false, nil
}

// MatchApplication tries to match an incoming email to one of the user's active applications.
// Returns the matched application and explanation reasons, or nil if no confident match.
func MatchApplication(msg EmailMessage, apps []ApplicationSummary) (*ApplicationSummary, []string) {
	if len(apps) == 0 {
		return nil, nil
	}

	fromAddr := extractEmailAddress(msg.From)
	fromDomain := extractDomain(fromAddr)
	subj := strings.ToLower(msg.Subject)
	body := strings.ToLower(msg.Body)

	type scoredApp struct {
		app     ApplicationSummary
		score   int
		reasons []string
	}

	var matches []scoredApp

	for _, app := range apps {
		company := strings.ToLower(strings.TrimSpace(app.Company))
		if company == "" {
			continue
		}

		score := 0
		var reasons []string

		// Check if company domain matches email domain; an ATS or job board domain is shared by many employers
		// and says nothing about which application the mail belongs to.
		appDomain := extractDomainFromURL(app.URL)
		if fromDomain != "" && appDomain != "" && !sharedDomain(fromDomain) && (fromDomain == appDomain || strings.HasSuffix(fromDomain, "."+appDomain) || strings.HasSuffix(appDomain, "."+fromDomain)) {
			score += 50
			reasons = append(reasons, "track.match.domain")
		}

		// Check company name in sender address / display name
		if strings.Contains(strings.ToLower(msg.From), company) {
			score += 40
			reasons = append(reasons, "track.match.company")
		}

		// Check company name in subject
		if strings.Contains(subj, company) {
			score += 35
			reasons = append(reasons, "track.match.company")
		} else if strings.Contains(body, company) {
			score += 20
			reasons = append(reasons, "track.match.company")
		}

		// Check job title keywords in subject or body
		title := strings.ToLower(strings.TrimSpace(app.Title))
		if title != "" && len(title) >= 4 {
			if strings.Contains(subj, title) {
				score += 30
				reasons = append(reasons, "track.match.title")
			} else if strings.Contains(body, title) {
				score += 15
				reasons = append(reasons, "track.match.title")
			}
		}

		if score >= 35 {
			matches = append(matches, scoredApp{app: app, score: score, reasons: reasons})
		}
	}

	if len(matches) == 0 {
		return nil, nil
	}

	// Pick the highest scoring match; a tie between applications is no confident match.
	best, tie := matches[0], false
	for _, m := range matches[1:] {
		switch {
		case m.score > best.score:
			best, tie = m, false
		case m.score == best.score:
			tie = true
		}
	}
	if tie {
		return nil, nil
	}
	return &best.app, best.reasons
}

// sharedDomains are mail and job hosts that send for many employers (ATS, job boards, mail providers).
var sharedDomains = []string{
	"greenhouse.io", "lever.co", "myworkdayjobs.com", "workday.com", "ashbyhq.com", "smartrecruiters.com",
	"personio.de", "personio.com", "recruitee.com", "join.com", "teamtailor.com", "workable.com", "bamboohr.com",
	"softgarden.io", "softgarden.de", "jobvite.com", "icims.com", "successfactors.com", "successfactors.eu",
	"linkedin.com", "indeed.com", "stepstone.de", "xing.com", "gmail.com", "googlemail.com", "outlook.com",
}

// sharedDomain reports a domain that belongs to one of sharedDomains.
func sharedDomain(d string) bool {
	for _, s := range sharedDomains {
		if d == s || strings.HasSuffix(d, "."+s) {
			return true
		}
	}
	return false
}

func extractEmailAddress(from string) string {
	addr, err := mail.ParseAddress(from)
	if err == nil && addr != nil {
		return strings.ToLower(strings.TrimSpace(addr.Address))
	}
	// fallback if not a full standard RFC 5322 header
	s := strings.TrimSpace(from)
	if start := strings.Index(s, "<"); start >= 0 {
		if end := strings.Index(s[start:], ">"); end > 0 {
			return strings.ToLower(strings.TrimSpace(s[start+1 : start+end]))
		}
	}
	return strings.ToLower(s)
}

func extractDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		return strings.ToLower(strings.TrimSpace(parts[1]))
	}
	return ""
}

func extractDomainFromURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	s := rawURL
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	if slash := strings.Index(s, "/"); slash >= 0 {
		s = s[:slash]
	}
	if colon := strings.Index(s, ":"); colon >= 0 {
		s = s[:colon]
	}
	return strings.ToLower(strings.TrimSpace(s))
}
