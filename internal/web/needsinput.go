package web

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/web/views"
)

// needsInputPage is the inbox of runs waiting for the user; ?app= opens one beside the list.
func (s *Server) needsInputPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d, ok := s.needsInput(w, r, u)
	if !ok {
		return
	}
	s.render(w, r, http.StatusOK, views.NeedsInputPage(d))
}

// needsInputList renders the inbox alone for live refreshes.
func (s *Server) needsInputList(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	d, ok := s.needsInput(w, r, u)
	if !ok {
		return
	}
	d.Open = nil // the list only marks it
	s.render(w, r, http.StatusOK, views.NIList(d))
}

func (s *Server) needsInput(w http.ResponseWriter, r *http.Request, u *identity.User) (views.NeedsInputData, bool) {
	d := views.NeedsInputData{Name: u.DisplayName}
	if s.Apps == nil {
		return d, true
	}
	ctx := r.Context()
	apps, reasons, err := s.Apps.List(ctx, u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return d, false
	}
	open, _ := uuid.Parse(r.URL.Query().Get("app"))
	now := time.Now()
	var openApp *application.PipelineApplication
	for i, a := range apps {
		waiting := a.State == application.NeedsInput ||
			(a.State == application.Parked && parkReasonKey(reasons[a.ID]) == "dash.park.answers")
		if !waiting {
			continue
		}
		it := views.NIItem{ID: a.ID.String(), Title: a.Title, Company: a.Company, Since: a.UpdatedAt, LeftMin: -1,
			Current: a.ID == open}
		if a.State == application.NeedsInput && a.HandoffUntil != nil {
			it.LeftMin = max(0, int(a.HandoffUntil.Sub(now).Minutes()))
		}
		if qs, err := s.Apps.Questions(ctx, u.ID, a.ID); err == nil {
			for _, q := range qs {
				if q.Kind == application.QuestionField && q.Label != "" {
					it.Count++
				}
			}
		}
		if it.Current {
			openApp = &apps[i]
		}
		d.Items = append(d.Items, it)
	}
	// Oldest first: the run that waits longest is the next to give up.
	slices.SortStableFunc(d.Items, func(a, b views.NIItem) int { return a.Since.Compare(b.Since) })
	if openApp != nil {
		det, ok := s.applicationDetail(w, r, u, openApp.ID)
		if !ok {
			return d, false
		}
		s.suggestAnswers(r, u, det.Questions)
		o := &views.NIOpen{HandoffData: det.HandoffData, LeftMin: -1}
		for _, it := range d.Items {
			if it.Current {
				o.LeftMin = it.LeftMin
			}
		}
		d.Open = o
	}
	return d, true
}

// suggestAnswers fills empty drafts with the answer to the most similar question in the answer bank. Exact matches
// never get here (the run fills those itself), so this looks for questions that share most of their words.
func (s *Server) suggestAnswers(r *http.Request, u *identity.User, qs []views.HandoffQuestion) {
	if s.Items == nil || len(qs) == 0 {
		return
	}
	bank, err := s.Items.Answers(r.Context(), u.ID)
	if err != nil || len(bank) == 0 {
		return
	}
	for i := range qs {
		if qs[i].Draft != "" || qs[i].Input == "file" {
			continue
		}
		if a, ok := similarAnswer(qs[i].Question, bank); ok && fitsQuestion(qs[i], a.Answer) {
			qs[i].Draft, qs[i].Suggested = a.Answer, true
		}
	}
}

// similarAnswer returns the bank answer whose question shares the most words with this one: at least two, and at
// least three quarters of the shorter question (filler words left out).
func similarAnswer(question string, bank []profile.Answer) (profile.Answer, bool) {
	want := words(question)
	if len(want) == 0 {
		return profile.Answer{}, false
	}
	var best profile.Answer
	bestScore := 0.0
	for _, a := range bank {
		have := words(a.Question)
		inter := 0
		for w := range want {
			if have[w] {
				inter++
			}
		}
		short := min(len(want), len(have))
		oneWord := inter == 1 && len(want) == 1 && len(have) == 1
		if short == 0 || (inter < 2 && !oneWord) {
			continue
		}
		if score := float64(inter) / float64(short); score > bestScore {
			best, bestScore = a, score
		}
	}
	return best, bestScore >= 0.75
}

// fillers are left out when questions are compared.
var fillers = map[string]bool{"the": true, "and": true, "are": true, "you": true, "your": true, "what": true,
	"will": true, "for": true, "per": true, "with": true, "this": true, "that": true, "have": true, "does": true,
	"how": true, "der": true, "die": true, "das": true, "und": true, "sie": true, "ihre": true, "ihr": true,
	"was": true, "wie": true, "haben": true, "sind": true, "fur": true, "mit": true, "ein": true, "eine": true}

func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(profile.NormalizeQuestion(s), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r <= 127
	}) {
		if len(w) > 2 && !fillers[w] {
			out[w] = true
		}
	}
	return out
}

// fitsQuestion keeps a suggestion only when the control can show it (a listed option, Yes or No for a checkbox).
func fitsQuestion(q views.HandoffQuestion, answer string) bool {
	switch {
	case q.Input == "checkbox" || q.Input == "aria_checkbox":
		return answer == "Yes" || answer == "No"
	case len(q.Options) > 0:
		return slices.Contains(q.Options, answer)
	}
	return true
}
