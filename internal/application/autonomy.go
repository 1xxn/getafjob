package application

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Level is how far a user lets getafjob go on its own (ADR 0016).
type Level string

// Levels.
const (
	// LevelSuggest only suggests jobs; nothing is queued on the user's behalf, and a run the user queues still
	// waits for the approval before the submit.
	LevelSuggest Level = "suggest"
	// LevelApprove fills in the application and waits for the user's approval before the submit (default).
	LevelApprove Level = "approve"
	// LevelAutonomous submits when the rule is green; otherwise the application goes to the approval list.
	LevelAutonomous Level = "autonomous"
)

// RunnerChoice is where a user wants their applications to be executed (ADR 0007, #40).
type RunnerChoice string

// Runner choices.
const (
	RunnerServer     RunnerChoice = "server"
	RunnerHomeRunner RunnerChoice = "home_runner"
	RunnerExtension  RunnerChoice = "extension"
)

// Settings are a user's autonomy settings.
type Settings struct {
	Level                    Level
	MinScore                 int          // the match score an application needs for the green rule
	DailyLimit               int          // submits per 24 hours the green rule allows
	Unlimited                bool         // instance runs without limits (not stored; GETAFJOB_LIMITS=off)
	RunnerChoice             RunnerChoice // server, home_runner, or extension
	EasyApplyEnabled         bool         // opt-in to Easy Apply for portals (#43)
	EasyApplyTermsAcceptedAt *time.Time   // when the user accepted terms of use and ban risk
}

// DefaultSettings apply until the user saves their own.
var DefaultSettings = Settings{Level: LevelApprove, MinScore: 80, DailyLimit: 5, RunnerChoice: RunnerServer}

// Limits of the settings.
const (
	MaxDailyLimit = 500
)

// ErrInvalidSettings reports settings outside their limits.
var ErrInvalidSettings = errors.New("invalid autonomy settings")

// Validate checks the settings.
func (s Settings) Validate() error {
	switch {
	case s.Level != LevelSuggest && s.Level != LevelApprove && s.Level != LevelAutonomous:
		return fmt.Errorf("%w: unknown autonomy level", ErrInvalidSettings)
	case s.MinScore < 0 || s.MinScore > 100:
		return fmt.Errorf("%w: min score out of range", ErrInvalidSettings)
	case s.DailyLimit < 0 || s.DailyLimit > MaxDailyLimit:
		return fmt.Errorf("%w: daily limit out of range", ErrInvalidSettings)
	case s.RunnerChoice != "" && s.RunnerChoice != RunnerServer && s.RunnerChoice != RunnerHomeRunner && s.RunnerChoice != RunnerExtension:
		return fmt.Errorf("%w: unknown runner choice", ErrInvalidSettings)
	case s.EasyApplyEnabled && s.EasyApplyTermsAcceptedAt == nil:
		return fmt.Errorf("%w: easy apply requires accepting terms and ban risk", ErrInvalidSettings)
	}
	return nil
}

// NewHomeRunnerToken generates a random home runner token and its SHA-256 hash.
func NewHomeRunnerToken() (plain string, hash []byte, err error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", nil, err
	}
	plain = "gaj_hr_" + hex.EncodeToString(b)
	h := sha256.Sum256([]byte(plain))
	return plain, h[:], nil
}

// HashHomeRunnerToken hashes a plain home runner token for database lookup.
func HashHomeRunnerToken(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}

// AutoQueue reports whether matching jobs may be queued on the user's behalf (the planner, #36).
func (s Settings) AutoQueue() bool { return s.Level == LevelApprove || s.Level == LevelAutonomous }

// Maturity: an ATS adapter is mature enough for the green rule when at least MaturityMinRuns of its runs finished
// within MaturityWindow and at least MaturityMin of them were submitted.
const (
	MaturityMinRuns = 5
	MaturityMin     = 0.8
	MaturityWindow  = 90 * 24 * time.Hour
)

// QuotaWindow is the window of the daily limit.
const QuotaWindow = 24 * time.Hour

// QuestionField is the kind of a question about a form field; only those are answered in the web form.
const QuestionField = "field"

// QuestionApproval is the kind (and label) of the question a run asks when the submit needs the user's approval.
const QuestionApproval = "approval"

// Reason names a part of the rule that is not green.
type Reason string

// Reasons, in the order the rule checks them.
const (
	ReasonLevel        Reason = "level"         // the user's level does not allow autonomous submits
	ReasonScore        Reason = "score"         // the match score is below the user's threshold
	ReasonExcluded     Reason = "excluded"      // the user's filters reject the job
	ReasonDuplicate    Reason = "duplicate"     // the same job is applied for through another source
	ReasonCompanyLimit Reason = "company_limit" // the company limit is used up
	ReasonProvenance   Reason = "provenance"    // a filled field has no valid source
	ReasonFreeText     Reason = "free_text"     // free text that is not a text block, an answer or an approved draft
	ReasonOpenQuestion Reason = "open_question" // a required field is still empty
	ReasonFormErrors   Reason = "form_errors"   // the form shows errors
	ReasonQuota        Reason = "quota"         // the daily limit is used up
	ReasonMaturity     Reason = "maturity"      // the ATS adapter has not earned autonomy yet
)

// SubmitField is a field the run filled, as the rule sees it.
type SubmitField struct {
	Kind   string // the field's kind (text, textarea, richtext, select, ...)
	Source string // the provenance of the value
}

// RuleInput is everything the rule looks at before a submit.
type RuleInput struct {
	Settings  Settings
	Score     int  // the match score of the job
	Matched   bool // a match exists for the job
	Candidate Candidate
	// Filled are the fields the run filled on all form pages.
	Filled []SubmitField
	// OpenFields counts required fields on the final page that are still empty.
	OpenFields int
	// FormErrors counts error messages and invalid fields on the final page.
	FormErrors int
	// Submits counts the user's submits within QuotaWindow.
	Submits int
	// Adapter is the ATS adapter of the run; AdapterRuns and AdapterSubmitted count its finished runs within
	// MaturityWindow.
	Adapter          string
	AdapterRuns      int
	AdapterSubmitted int
}

// sourceRx is the provenance of a value (ADR 0013): a profile field, an answer, a text block or a document. It
// mirrors the executor's check; the server does not trust the runner's report.
var sourceRx = regexp.MustCompile(`^(profile\.[a-z][a-z0-9_.]{0,60}|(answer|textblock)#(` + idRx + `)|document#` + uuidRx + `)$`)

const (
	uuidRx = `[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`
	idRx   = uuidRx + `|[0-9]{1,18}`
)

// freeText reports field kinds that take free text.
func freeText(kind string) bool { return kind == "textarea" || kind == "richtext" }

// userText reports sources of free text the user wrote: a text block or an answer from the answer bank.
func userText(source string) bool {
	return strings.HasPrefix(source, "textblock#") || strings.HasPrefix(source, "answer#")
}

// Rule returns the parts of the green rule that fail; none means green. The level is part of the answer, so the
// reasons explain an approval request at every level.
func Rule(in RuleInput, now time.Time) []Reason {
	var out []Reason
	add := func(r Reason) {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	if in.Settings.Level != LevelAutonomous {
		add(ReasonLevel)
	}
	if !in.Matched || in.Score < in.Settings.MinScore {
		add(ReasonScore)
	}
	switch err := Guard(in.Candidate, now); {
	case errors.Is(err, ErrExcluded):
		add(ReasonExcluded)
	case errors.Is(err, ErrDuplicate):
		add(ReasonDuplicate)
	case errors.Is(err, ErrCompanyWindow):
		add(ReasonCompanyLimit)
	}
	for _, f := range in.Filled {
		if !sourceRx.MatchString(f.Source) {
			add(ReasonProvenance)
			continue
		}
		if freeText(f.Kind) && !userText(f.Source) {
			add(ReasonFreeText)
		}
	}
	if in.OpenFields > 0 {
		add(ReasonOpenQuestion)
	}
	if in.FormErrors > 0 {
		add(ReasonFormErrors)
	}
	if !in.Settings.Unlimited && in.Submits >= in.Settings.DailyLimit {
		add(ReasonQuota)
	}
	if in.Adapter == "" || in.AdapterRuns < MaturityMinRuns ||
		float64(in.AdapterSubmitted) < MaturityMin*float64(in.AdapterRuns) {
		add(ReasonMaturity)
	}
	return out
}

// ApprovalError reports a submit that needs the user's approval first, with the reasons the rule gave.
type ApprovalError struct{ Reasons []Reason }

func (e *ApprovalError) Error() string {
	rs := make([]string, len(e.Reasons))
	for i, r := range e.Reasons {
		rs[i] = string(r)
	}
	return "the submit needs the user's approval: " + strings.Join(rs, ", ")
}

// ErrNeedsApproval matches every ApprovalError.
var ErrNeedsApproval = errors.New("the submit needs the user's approval")

// Is makes errors.Is(err, ErrNeedsApproval) work.
func (e *ApprovalError) Is(target error) bool { return target == ErrNeedsApproval }

// Approvable reports states in which the user can approve an application: it has not been sent and is still
// waiting or can be queued again.
func (s State) Approvable() bool {
	switch s {
	case Matched, Queued, Running, NeedsInput, Parked, Failed:
		return true
	default:
		return false
	}
}
