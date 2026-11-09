package privacy

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

// ExportData is the complete portable data dump of a user.
type ExportData struct {
	ExportedAt   time.Time            `json:"exported_at"`
	User         UserSummary          `json:"user"`
	Passkeys     []PasskeySummary     `json:"passkeys,omitempty"`
	Sessions     []SessionSummary     `json:"sessions,omitempty"`
	Profile      any                  `json:"profile,omitempty"`
	Preferences  any                  `json:"preferences,omitempty"`
	Answers      []AnswerSummary      `json:"answers,omitempty"`
	Texts        []TextSummary        `json:"texts,omitempty"`
	Documents    []DocumentSummary    `json:"documents,omitempty"`
	Matches      []MatchSummary       `json:"matches,omitempty"`
	Applications []ApplicationSummary `json:"applications,omitempty"`
	Settings     SettingsSummary      `json:"settings,omitempty"`
	AuditEvents  []AuditSummary       `json:"audit_events,omitempty"`
}

// UserSummary holds user account basics.
type UserSummary struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	Locale      string    `json:"locale"`
	CreatedAt   time.Time `json:"created_at"`
}

// PasskeySummary holds passkey metadata without secret credentials.
type PasskeySummary struct {
	Name       string    `json:"name"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// SessionSummary holds session metadata.
type SessionSummary struct {
	Method     string    `json:"method"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// AnswerSummary is one entry in the answer bank.
type AnswerSummary struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// TextSummary is one reusable text block.
type TextSummary struct {
	Name     string `json:"name"`
	Language string `json:"language,omitempty"`
	Body     string `json:"body"`
}

// DocumentSummary holds document metadata.
type DocumentSummary struct {
	ID        uuid.UUID `json:"id"`
	Kind      string    `json:"kind"`
	Name      string    `json:"name"`
	Mime      string    `json:"mime"`
	Size      int       `json:"size"`
	CreatedAt time.Time `json:"created_at"`
}

// MatchSummary holds matched job postings.
type MatchSummary struct {
	JobID      int64     `json:"job_id"`
	Title      string    `json:"title"`
	Company    string    `json:"company"`
	Location   string    `json:"location"`
	Workplace  string    `json:"workplace"`
	Score      int       `json:"score"`
	Decision   string    `json:"decision"`
	Reasons    any       `json:"reasons,omitempty"`
	ComputedAt time.Time `json:"computed_at"`
}

// ApplicationSummary holds application history.
type ApplicationSummary struct {
	ID        uuid.UUID      `json:"id"`
	JobID     int64          `json:"job_id"`
	State     string         `json:"state"`
	Company   string         `json:"company"`
	Title     string         `json:"title"`
	Location  string         `json:"location"`
	URL       string         `json:"url,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Events    []EventSummary `json:"events,omitempty"`
}

// EventSummary is one timeline event of an application.
type EventSummary struct {
	Event string         `json:"event"`
	From  string         `json:"from"`
	To    string         `json:"to"`
	Actor string         `json:"actor"`
	At    time.Time      `json:"at"`
	Data  map[string]any `json:"data,omitempty"`
}

// SettingsSummary holds user configuration.
type SettingsSummary struct {
	AutonomyLevel    string          `json:"autonomy_level,omitempty"`
	DailyLimit       int             `json:"daily_limit,omitempty"`
	RunnerChoice     string          `json:"runner_choice,omitempty"`
	EasyApplyEnabled bool            `json:"easy_apply_enabled,omitempty"`
	Portals          []PortalSummary `json:"portals,omitempty"`
	AIEnabled        bool            `json:"ai_enabled,omitempty"`
	AISpentCents     int64           `json:"ai_spent_cents,omitempty"`
}

// PortalSummary holds portal session metadata.
type PortalSummary struct {
	Portal     string    `json:"portal"`
	Status     string    `json:"status"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// AuditSummary holds audit log entries for the user.
type AuditSummary struct {
	Actor     string         `json:"actor"`
	Code      string         `json:"code"`
	Params    map[string]any `json:"params,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
}

// WriteArchive builds a standard ZIP archive containing formatted JSON files and decrypted documents.
func WriteArchive(w io.Writer, data ExportData, files map[string][]byte) error {
	zw := zip.NewWriter(w)
	defer zw.Close()

	writeJSON := func(name string, v any) error {
		fw, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("create zip entry %s: %w", name, err)
		}
		enc := json.NewEncoder(fw)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}

	if err := writeJSON("profile.json", map[string]any{
		"user":        data.User,
		"profile":     data.Profile,
		"preferences": data.Preferences,
		"answers":     data.Answers,
		"texts":       data.Texts,
		"documents":   data.Documents,
	}); err != nil {
		return err
	}

	if err := writeJSON("applications.json", data.Applications); err != nil {
		return err
	}

	if err := writeJSON("matches.json", data.Matches); err != nil {
		return err
	}

	if err := writeJSON("settings.json", map[string]any{
		"settings": data.Settings,
		"passkeys": data.Passkeys,
		"sessions": data.Sessions,
	}); err != nil {
		return err
	}

	if err := writeJSON("audit_events.json", data.AuditEvents); err != nil {
		return err
	}

	for filename, content := range files {
		fw, err := zw.Create("documents/" + filename)
		if err != nil {
			return fmt.Errorf("create doc entry %s: %w", filename, err)
		}
		if _, err := fw.Write(content); err != nil {
			return fmt.Errorf("write doc entry %s: %w", filename, err)
		}
	}

	return nil
}
