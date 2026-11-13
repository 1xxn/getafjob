package tracking_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/tracking"
)

func TestClassification(t *testing.T) {
	tests := []struct {
		name     string
		msg      tracking.EmailMessage
		wantCat  tracking.Category
		wantSec  bool
		minRules int
	}{
		{
			name: "German security code must be ignored per ADR 0015",
			msg: tracking.EmailMessage{
				Subject: "Ihr Sicherheitscode für die Anmeldung",
				Body:    "Ihr einmaliger Sicherheitscode lautet: 849201. Bitte geben Sie diesen Code ein.",
			},
			wantCat: tracking.CategorySecurityCode,
			wantSec: true,
		},
		{
			name: "English verification code must be ignored per ADR 0015",
			msg: tracking.EmailMessage{
				Subject: "Your one-time verification code",
				Body:    "Please use verification code 491028 to complete your sign in.",
			},
			wantCat: tracking.CategorySecurityCode,
			wantSec: true,
		},
		{
			name: "German interview invitation",
			msg: tracking.EmailMessage{
				Subject: "Einladung zum Vorstellungsgespräch bei Acme GmbH",
				Body:    "Sehr geehrte Bewerberin, wir möchten Sie gerne zu einem persönlichen Kennenlerngespräch einladen.",
			},
			wantCat:  tracking.CategoryInvitation,
			minRules: 1,
		},
		{
			name: "English interview invitation",
			msg: tracking.EmailMessage{
				Subject: "Interview invitation: Senior Backend Engineer at Globex",
				Body:    "We were impressed by your resume and would like to invite you to an interview with our engineering lead.",
			},
			wantCat:  tracking.CategoryInvitation,
			minRules: 1,
		},
		{
			name: "German rejection",
			msg: tracking.EmailMessage{
				Subject: "Ihre Bewerbung als Go Entwickler",
				Body:    "Vielen Dank für das Interesse. Leider müssen wir Ihnen mitteilen, dass wir uns für einen anderen Bewerber entschieden haben.",
			},
			wantCat:  tracking.CategoryRejection,
			minRules: 1,
		},
		{
			name: "English rejection",
			msg: tracking.EmailMessage{
				Subject: "Update on your application",
				Body:    "Thank you for taking the time to apply. Unfortunately, we have decided not to move forward with your application at this time.",
			},
			wantCat:  tracking.CategoryRejection,
			minRules: 1,
		},
		{
			name: "German received confirmation",
			msg: tracking.EmailMessage{
				Subject: "Eingangsbestätigung Ihrer Bewerbung",
				Body:    "Vielen Dank für Ihre Bewerbung. Ihre Unterlagen sind bei uns eingegangen und werden derzeit geprüft.",
			},
			wantCat:  tracking.CategoryReceived,
			minRules: 1,
		},
		{
			name: "English received confirmation",
			msg: tracking.EmailMessage{
				Subject: "Application received - Initech",
				Body:    "Thank you for your application for the Software Engineer role. We have received your application successfully.",
			},
			wantCat:  tracking.CategoryReceived,
			minRules: 1,
		},
		{
			name: "Unrelated newsletter",
			msg: tracking.EmailMessage{
				Subject: "Weekly Tech Digest",
				Body:    "Here are the top stories in Go and cloud computing this week.",
			},
			wantCat: tracking.CategoryOther,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat, reasons := tracking.Classify(tt.msg)
			if cat != tt.wantCat {
				t.Errorf("Classify() = %v, want %v", cat, tt.wantCat)
			}
			if tt.minRules > 0 && len(reasons) < tt.minRules {
				t.Errorf("len(reasons) = %d, want at least %d (%v)", len(reasons), tt.minRules, reasons)
			}
			state, ok := tracking.SuggestedState(cat)
			if tt.wantSec {
				if ok || state != "" {
					t.Errorf("Security codes must not produce a suggested state (got state=%v, ok=%v)", state, ok)
				}
			}
		})
	}
}

func TestMatchApplication(t *testing.T) {
	app1 := tracking.ApplicationSummary{
		ID:           uuid.New(),
		Company:      "Acme Cloud",
		Title:        "Go Backend Developer",
		CurrentState: "submitted",
		URL:          "https://jobs.acmecloud.example.org/123",
	}
	app2 := tracking.ApplicationSummary{
		ID:           uuid.New(),
		Company:      "Initech",
		Title:        "Site Reliability Engineer",
		CurrentState: "submitted",
		URL:          "https://careers.initech.example.com/apply/456",
	}

	apps := []tracking.ApplicationSummary{app1, app2}

	// 1. Email from Acme sender domain
	msg1 := tracking.EmailMessage{
		From:    "recruiting@jobs.acmecloud.example.org",
		Subject: "Ihre Bewerbung",
		Body:    "Wir haben Ihre Unterlagen erhalten.",
		Date:    time.Now(),
	}
	matched1, reasons1 := tracking.MatchApplication(msg1, apps)
	if matched1 == nil || matched1.ID != app1.ID {
		t.Fatalf("MatchApplication(msg1) = %+v, want app1", matched1)
	}
	if len(reasons1) == 0 {
		t.Errorf("Expected reasons for match1")
	}

	// 2. Email mentioning Initech in subject
	msg2 := tracking.EmailMessage{
		From:    "hr-noreply@greenhouse.io",
		Subject: "Thank you for applying to Initech",
		Body:    "We received your application for the Site Reliability Engineer position.",
		Date:    time.Now(),
	}
	matched2, reasons2 := tracking.MatchApplication(msg2, apps)
	if matched2 == nil || matched2.ID != app2.ID {
		t.Fatalf("MatchApplication(msg2) = %+v, want app2", matched2)
	}
	if len(reasons2) == 0 {
		t.Errorf("Expected reasons for match2")
	}

	// 3. Email from unknown company
	msg3 := tracking.EmailMessage{
		From:    "hiring@unrelated-startup.org",
		Subject: "Cold outreach",
		Body:    "Are you interested in new opportunities?",
		Date:    time.Now(),
	}
	matched3, _ := tracking.MatchApplication(msg3, apps)
	if matched3 != nil {
		t.Errorf("MatchApplication(msg3) = %+v, want nil", matched3)
	}
}

// Audit findings (#362): a no after an interview is a rejection, "otp" counts only as a word, a shared ATS
// sender domain matches no application by itself, and a tie is no match.
func TestClassifyAndMatchEdgeCases(t *testing.T) {
	if cat, _ := tracking.Classify(tracking.EmailMessage{Subject: "Ihre Bewerbung",
		Body: "Vielen Dank für das Vorstellungsgespräch. Leider müssen wir Ihnen mitteilen, dass wir uns anderweitig entschieden haben."}); cat != tracking.CategoryRejection {
		t.Errorf("rejection after interview = %s", cat)
	}
	if cat, _ := tracking.Classify(tracking.EmailMessage{Subject: "Thank you for your application",
		Body: "We care about our carbon footprint."}); cat != tracking.CategoryReceived {
		t.Errorf("footprint mail = %s, want received", cat)
	}
	if cat, _ := tracking.Classify(tracking.EmailMessage{Subject: "Your OTP"}); cat != tracking.CategorySecurityCode {
		t.Errorf("otp mail = %s, want security code", cat)
	}
	gh := func(company string) tracking.ApplicationSummary {
		return tracking.ApplicationSummary{ID: uuid.New(), Company: company, Title: "Go Developer",
			URL: "https://job-boards.greenhouse.io/" + strings.ToLower(company) + "/jobs/1"}
	}
	apps := []tracking.ApplicationSummary{gh("Musterfirma"), gh("Beispielwerk")}
	if m, _ := tracking.MatchApplication(tracking.EmailMessage{From: "no-reply@us.greenhouse-mail.io",
		Subject: "Update", Body: "Regarding your application."}, apps); m != nil {
		t.Errorf("unrelated greenhouse mail matched %s", m.Company)
	}
	if m, _ := tracking.MatchApplication(tracking.EmailMessage{From: "no-reply@greenhouse.io",
		Subject: "Update", Body: "Regarding your application."}, apps); m != nil {
		t.Errorf("shared ATS domain matched %s", m.Company)
	}
	if m, _ := tracking.MatchApplication(tracking.EmailMessage{From: "no-reply@greenhouse.io",
		Subject: "Your application at Beispielwerk", Body: "Thanks."}, apps); m == nil || m.Company != "Beispielwerk" {
		t.Errorf("company in subject = %+v, want Beispielwerk", m)
	}
	twins := []tracking.ApplicationSummary{gh("Musterfirma"), gh("Musterfirma")}
	if m, _ := tracking.MatchApplication(tracking.EmailMessage{From: "jobs@musterfirma.example.org",
		Subject: "Musterfirma: your application", Body: "Thanks."}, twins); m != nil {
		t.Errorf("tie matched %s", m.ID)
	}
}
