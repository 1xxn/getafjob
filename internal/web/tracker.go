package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/web/views"
)

func (s *Server) trackerPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d, err := s.buildTrackerData(r.Context(), u, "")
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.render(w, r, http.StatusOK, views.TrackerPage(d))
}

func (s *Server) trackerSetStage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	targetStage := strings.TrimSpace(r.PostFormValue("stage"))
	if targetStage == "" {
		http.Error(w, "missing stage", http.StatusBadRequest)
		return
	}

	app, err := s.Apps.Get(r.Context(), u.ID, appID)
	if err != nil {
		s.notFound(w, r, u)
		return
	}

	if err := s.transitionToStage(r.Context(), u.ID, app, targetStage); err != nil {
		s.Logger.WarnContext(r.Context(), "stage transition failed", "app_id", appID, "from", app.State, "err", err)
	}

	if partial(r) {
		d, err := s.buildTrackerData(r.Context(), u, "")
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		s.render(w, r, http.StatusOK, views.TrackerBoard(d))
		return
	}
	http.Redirect(w, r, "/tracker", http.StatusSeeOther)
}

func (s *Server) transitionToStage(ctx context.Context, userID uuid.UUID, app application.App, target string) error {
	from := app.State
	// If already in target state, nothing to do
	if string(from) == target {
		return nil
	}

	// Handle direct transitions or multi-step progression
	switch target {
	case "matched", "saved":
		if from == application.Parked || from == application.Queued || from == application.Running || from == application.NeedsInput {
			_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvPark, application.ActorUser, map[string]any{"source": "tracker"})
			return err
		}
	case "needs_input", "review":
		if from == application.Matched {
			_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvQueue, application.ActorUser, map[string]any{"source": "tracker"})
			return err
		}
		if from == application.Parked || from == application.Failed {
			_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvRequeue, application.ActorUser, map[string]any{"source": "tracker"})
			return err
		}
		if from == application.Running {
			_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvAsk, application.ActorUser, map[string]any{"source": "tracker"})
			return err
		}
	case "queued":
		if from == application.Matched {
			_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvQueue, application.ActorUser, map[string]any{"source": "tracker"})
			return err
		}
		if from == application.Parked || from == application.Failed {
			_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvRequeue, application.ActorUser, map[string]any{"source": "tracker"})
			return err
		}
	case "submitted":
		_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvSubmitted, application.ActorUser, map[string]any{"source": "tracker"})
		return err
	case "interview":
		if from == application.Matched || from == application.Queued || from == application.NeedsInput || from == application.Parked {
			if _, err := s.Apps.Fire(ctx, userID, app.ID, application.EvSubmitted, application.ActorUser, map[string]any{"source": "tracker"}); err != nil {
				return err
			}
		}
		_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvInterview, application.ActorUser, map[string]any{"source": "tracker"})
		return err
	case "offer":
		if from == application.Matched || from == application.Queued || from == application.NeedsInput || from == application.Parked {
			if _, err := s.Apps.Fire(ctx, userID, app.ID, application.EvSubmitted, application.ActorUser, map[string]any{"source": "tracker"}); err != nil {
				return err
			}
		}
		_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvOffer, application.ActorUser, map[string]any{"source": "tracker"})
		return err
	case "rejected":
		if from == application.Matched || from == application.Queued || from == application.NeedsInput || from == application.Parked {
			if _, err := s.Apps.Fire(ctx, userID, app.ID, application.EvSubmitted, application.ActorUser, map[string]any{"source": "tracker"}); err != nil {
				return err
			}
		}
		_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvReject, application.ActorUser, map[string]any{"source": "tracker"})
		return err
	case "withdrawn":
		if from == application.Matched || from == application.Queued || from == application.NeedsInput || from == application.Parked {
			if _, err := s.Apps.Fire(ctx, userID, app.ID, application.EvSubmitted, application.ActorUser, map[string]any{"source": "tracker"}); err != nil {
				return err
			}
		}
		_, err := s.Apps.Fire(ctx, userID, app.ID, application.EvWithdraw, application.ActorUser, map[string]any{"source": "tracker"})
		return err
	}
	return application.ErrTransition
}

func (s *Server) trackerSaveNotes(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	notes := strings.TrimSpace(r.PostFormValue("notes"))
	salary := strings.TrimSpace(r.PostFormValue("salary"))
	var interviewAt *time.Time
	if val := strings.TrimSpace(r.PostFormValue("interview_at")); val != "" {
		if t, err := time.Parse("2006-01-02T15:04", val); err == nil {
			interviewAt = &t
		}
	}
	var followUpAt *time.Time
	if val := strings.TrimSpace(r.PostFormValue("follow_up_at")); val != "" {
		if t, err := time.Parse("2006-01-02", val); err == nil {
			followUpAt = &t
		}
	}

	if err := s.Apps.SaveNotes(r.Context(), u.ID, appID, notes, salary, interviewAt, followUpAt); err != nil {
		s.failed(w, r, u, err)
		return
	}

	if partial(r) {
		pipeline, err := s.Apps.Pipeline(r.Context(), u.ID)
		if err == nil {
			for _, item := range pipeline {
				if item.ID == appID {
					card := s.buildTrackerCard(r.Context(), u.ID, item, views.T(r.Context(), "tracker.notes.saved"))
					s.render(w, r, http.StatusOK, views.TrackerCard(card))
					return
				}
			}
		}
	}
	if back := r.PostFormValue("back"); strings.HasPrefix(back, "/applications/") {
		s.flash(w, "flash.apps.notes")
		http.Redirect(w, r, localBack(back), http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
		return
	}
	http.Redirect(w, r, "/tracker", http.StatusSeeOther)
}

func (s *Server) jobBookmark(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	if s.Apps == nil {
		s.failed(w, r, u, errors.New("application service unavailable"))
		return
	}
	if _, err := s.Apps.Bookmark(r.Context(), u.ID, id); err != nil {
		s.failed(w, r, u, err)
		return
	}
	if partial(r) {
		s.render(w, r, http.StatusOK, views.Notice("success", views.T(r.Context(), "jobs.saved_to_tracker")))
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d", id), http.StatusSeeOther)
}

func (s *Server) jobApplyExternal(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	if s.Apps == nil {
		s.failed(w, r, u, errors.New("application service unavailable"))
		return
	}
	if _, err := s.Apps.ApplyExternal(r.Context(), u.ID, id); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/tracker", http.StatusSeeOther)
}

func (s *Server) trackerAddExternal(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Discovery == nil || s.Apps == nil {
		s.failed(w, r, u, errors.New("service unavailable"))
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	rawURL := strings.TrimSpace(r.PostFormValue("url"))
	jobID, err := s.Discovery.ImportJob(r.Context(), u.ID, rawURL)
	if err != nil {
		s.Logger.WarnContext(r.Context(), "tracker external import failed", "err", err)
		if partial(r) {
			d, _ := s.buildTrackerData(r.Context(), u, views.T(r.Context(), "jobs.import_error"))
			s.render(w, r, http.StatusOK, views.TrackerBoard(d))
			return
		}
		http.Redirect(w, r, "/tracker", http.StatusSeeOther)
		return
	}
	if _, err := s.Apps.ApplyExternal(r.Context(), u.ID, jobID); err != nil {
		s.failed(w, r, u, err)
		return
	}
	if partial(r) {
		d, err := s.buildTrackerData(r.Context(), u, views.T(r.Context(), "tracker.external_added"))
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		s.render(w, r, http.StatusOK, views.TrackerBoard(d))
		return
	}
	http.Redirect(w, r, "/tracker", http.StatusSeeOther)
}

func (s *Server) buildTrackerData(ctx context.Context, u *identity.User, notice string) (views.TrackerData, error) {
	var d views.TrackerData
	d.Name = u.Email
	d.Notice = notice

	colSaved := views.TrackerColumnData{Key: "saved", Title: views.T(ctx, "tracker.col.saved")}
	colReview := views.TrackerColumnData{Key: "review", Title: views.T(ctx, "tracker.col.review")}
	colSubmitted := views.TrackerColumnData{Key: "submitted", Title: views.T(ctx, "tracker.col.submitted")}
	colInterview := views.TrackerColumnData{Key: "interview", Title: views.T(ctx, "tracker.col.interview")}
	colOutcome := views.TrackerColumnData{Key: "outcome", Title: views.T(ctx, "tracker.col.outcome")}

	if s.Apps != nil {
		pipeline, err := s.Apps.Pipeline(ctx, u.ID)
		if err != nil {
			return d, err
		}
		d.TotalCount = len(pipeline)

		for _, item := range pipeline {
			card := s.buildTrackerCard(ctx, u.ID, item, "")
			switch item.State {
			case application.Matched, application.Discovered:
				colSaved.Cards = append(colSaved.Cards, card)
			case application.NeedsInput, application.Parked, application.Queued, application.Running:
				colReview.Cards = append(colReview.Cards, card)
			case application.Submitted, application.Submitting, application.Confirmed:
				colSubmitted.Cards = append(colSubmitted.Cards, card)
			case application.Interview:
				colInterview.Cards = append(colInterview.Cards, card)
			case application.Offer, application.Rejected, application.Withdrawn, application.Failed, application.Skipped, application.SubmitUnknown:
				colOutcome.Cards = append(colOutcome.Cards, card)
			default:
				colSaved.Cards = append(colSaved.Cards, card)
			}
		}
	}

	d.Columns = []views.TrackerColumnData{colSaved, colReview, colSubmitted, colInterview, colOutcome}
	return d, nil
}

func (s *Server) buildTrackerCard(ctx context.Context, userID uuid.UUID, app application.PipelineApplication, notice string) views.TrackerCardData {
	card := views.TrackerCardData{
		ID:              app.ID.String(),
		JobID:           app.JobID,
		Title:           app.Title,
		Company:         app.Company,
		Location:        app.Location,
		Workplace:       app.Workplace,
		State:           string(app.State),
		Score:           app.Score,
		URL:             app.URL,
		ApplyURL:        app.ApplyURL,
		UpdatedAt:       views.When(ctx, app.UpdatedAt),
		Notes:           app.Notes,
		SalaryDiscussed: app.SalaryDiscussed,
		Notice:          notice,
	}

	if app.InterviewAt != nil && !app.InterviewAt.IsZero() {
		card.InterviewAt = app.InterviewAt.UTC().Format("2006-01-02T15:04")
		card.InterviewAtDisplay = views.When(ctx, *app.InterviewAt)
	}
	if app.FollowUpAt != nil && !app.FollowUpAt.IsZero() {
		card.FollowUpAt = app.FollowUpAt.UTC().Format("2006-01-02")
		card.FollowUpAtDisplay = app.FollowUpAt.UTC().Format("02.01.2006")
	}

	if s.Tracking != nil {
		if sugs, err := s.Tracking.SuggestionsForApplication(ctx, userID, app.ID); err == nil {
			for _, sg := range sugs {
				if sg.Status == tracking.SuggestionPending {
					card.EmailActivity = views.T(ctx, "tracker.email."+string(sg.Category))
					break
				}
			}
		}
	}

	return card
}
