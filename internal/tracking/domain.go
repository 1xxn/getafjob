// Package tracking checks user inboxes (IMAP) read-only for application updates,
// applies heuristic rules for confirmations, rejections, and interview invitations,
// and produces status suggestions for user approval.
// Security codes are strictly excluded and never read automatically (ADR 0015).
package tracking

import (
	"errors"
	"net/mail"
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
		return CategorySecurityCode, []string{"security code or verification email (ignored per ADR 0015)"}
	}

	// 2. Invitation to interview
	if match, reasons := isInvitation(subj, body, full); match {
		return CategoryInvitation, reasons
	}

	// 3. Rejection
	if match, reasons := isRejection(subj, body, full); match {
		return CategoryRejection, reasons
	}

	// 4. Confirmation / Received
	if match, reasons := isReceived(subj, body, full); match {
		return CategoryReceived, reasons
	}

	return CategoryOther, nil
}

// isSecurityCode detects 2FA, OTP, verification and security codes.
func isSecurityCode(subj, body string) bool {
	secTerms := []string{
		"verification code", "security code", "one-time password", "otp", "2fa", "two-factor",
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
	inviteTerms := []struct {
		pattern string
		reason  string
	}{
		{"einladung zum vorstellungsgespräch", "Einladung zum Vorstellungsgespräch"},
		{"einladung zum interview", "Einladung zum Interview"},
		{"einladung zum kennenlerngespräch", "Einladung zum Kennenlerngespräch"},
		{"einladung zu einem", "Einladung zu einem Gespräch"},
		{"terminvorschlag für ein", "Terminvorschlag für ein Interview"},
		{"vorstellungsgespräch", "Erwähnung Vorstellungsgespräch"},
		{"kennenlerngespräch", "Erwähnung Kennenlerngespräch"},
		{"telefoninterview", "Einladung zu Telefoninterview"},
		{"videointerview", "Einladung zu Videointerview"},
		{"gesprächtstermin", "Gesprächstermin vorgeschlagen"},
		{"interview invitation", "Interview invitation"},
		{"invite you to an interview", "Invitation to interview"},
		{"schedule an interview", "Schedule an interview"},
		{"invitation to interview", "Invitation to interview"},
		{"next steps in our process", "Next steps in hiring process"},
		{"chat about your application", "Chat about your application"},
		{"phone screen", "Phone screen interview"},
		{"technical interview", "Technical interview"},
		{"like to invite you", "Invitation extended"},
	}

	var reasons []string
	for _, it := range inviteTerms {
		if strings.Contains(full, it.pattern) {
			reasons = append(reasons, it.reason)
		}
	}
	if len(reasons) > 0 {
		return true, reasons
	}
	return false, nil
}

// isRejection detects rejection notices.
func isRejection(subj, body, full string) (bool, []string) {
	rejectTerms := []struct {
		pattern string
		reason  string
	}{
		{"leider müssen wir ihnen mitteilen", "Absage (leider müssen wir Ihnen mitteilen)"},
		{"nicht näher in betracht ziehen", "Absage (nicht näher in Betracht ziehen)"},
		{"anderweitig entschieden", "Absage (anderweitig entschieden)"},
		{"haben uns für einen anderen", "Absage (für einen anderen Bewerber entschieden)"},
		{"haben uns für eine andere", "Absage (für eine andere Person entschieden)"},
		{"leider eine absage", "Absage erhalten"},
		{"können ihre bewerbung nicht berücksichtigen", "Bewerbung kann nicht berücksichtigt werden"},
		{"müssen ihnen leider absagen", "Absage erhalten"},
		{"nicht für die engere auswahl", "Nicht in der engeren Auswahl"},
		{"unable to offer you an interview", "Rejection (unable to offer interview)"},
		{"decided not to move forward", "Rejection (decided not to move forward)"},
		{"pursuing other candidates", "Rejection (pursuing other candidates)"},
		{"not selected for this role", "Rejection (not selected for role)"},
		{"position has been filled", "Position filled"},
		{"will not be moving forward", "Rejection (will not be moving forward)"},
		{"other applicants whose qualifications", "Rejection (other candidates chosen)"},
		{"cannot consider your application further", "Rejection (cannot consider further)"},
		{"regret to inform you", "Rejection (regret to inform you)"},
	}

	var reasons []string
	for _, rt := range rejectTerms {
		if strings.Contains(full, rt.pattern) {
			reasons = append(reasons, rt.reason)
		}
	}
	if len(reasons) > 0 {
		return true, reasons
	}
	return false, nil
}

// isReceived detects confirmation and receipt notices.
func isReceived(subj, body, full string) (bool, []string) {
	receivedTerms := []struct {
		pattern string
		reason  string
	}{
		{"eingangsbestätigung", "Eingangsbestätigung erhalten"},
		{"haben ihre bewerbung erhalten", "Bestätigung: Bewerbung erhalten"},
		{"vielen dank für ihre bewerbung", "Vielen Dank für Ihre Bewerbung"},
		{"bestätigung ihrer bewerbung", "Bestätigung der Bewerbung"},
		{"eingang ihrer bewerbung", "Bestätigung des Bewerbungseingangs"},
		{"bewerbung ist bei uns eingegangen", "Bewerbung eingegangen"},
		{"bewerbungseingang", "Bewerbungseingang"},
		{"thank you for your application", "Thank you for your application"},
		{"we have received your application", "Application received confirmation"},
		{"application received", "Application received"},
		{"successfully submitted", "Successfully submitted"},
		{"confirmation of application", "Confirmation of application"},
		{"received your resume", "Resume received"},
		{"application has been received", "Application has been received"},
	}

	var reasons []string
	for _, rt := range receivedTerms {
		if strings.Contains(full, rt.pattern) {
			reasons = append(reasons, rt.reason)
		}
	}
	if len(reasons) > 0 {
		return true, reasons
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

		// Check if company domain matches email domain
		appDomain := extractDomainFromURL(app.URL)
		if fromDomain != "" && appDomain != "" && (fromDomain == appDomain || strings.HasSuffix(fromDomain, "."+appDomain) || strings.HasSuffix(appDomain, "."+fromDomain)) {
			score += 50
			reasons = append(reasons, "Sender domain matches job URL domain ("+fromDomain+")")
		}

		// Check company name in sender address / display name
		if strings.Contains(strings.ToLower(msg.From), company) {
			score += 40
			reasons = append(reasons, "Company name '"+app.Company+"' in sender")
		}

		// Check company name in subject
		if strings.Contains(subj, company) {
			score += 35
			reasons = append(reasons, "Company name '"+app.Company+"' in subject")
		} else if strings.Contains(body, company) {
			score += 20
			reasons = append(reasons, "Company name '"+app.Company+"' in message body")
		}

		// Check job title keywords in subject or body
		title := strings.ToLower(strings.TrimSpace(app.Title))
		if title != "" && len(title) >= 4 {
			if strings.Contains(subj, title) {
				score += 30
				reasons = append(reasons, "Job title '"+app.Title+"' in subject")
			} else if strings.Contains(body, title) {
				score += 15
				reasons = append(reasons, "Job title '"+app.Title+"' in message body")
			}
		}

		if score >= 35 {
			matches = append(matches, scoredApp{app: app, score: score, reasons: reasons})
		}
	}

	if len(matches) == 0 {
		return nil, nil
	}

	// Pick the highest scoring match
	best := matches[0]
	for _, m := range matches[1:] {
		if m.score > best.score {
			best = m
		}
	}

	return &best.app, best.reasons
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
