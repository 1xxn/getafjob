package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/web/views"
)

// plStages are the pipeline columns and the event that moves an application there.
var plStages = []struct {
	Key string
	Ev  application.Event
}{
	{"applied", application.EvSubmitted}, {"interview", application.EvInterview}, {"offer", application.EvOffer},
	{"rejected", application.EvReject}, {"withdrawn", application.EvWithdraw},
}

// plColumn is the column of a sent application; others are not on the board (they live in Applications).
func plColumn(st application.State) string {
	switch st {
	case application.Submitted, application.Confirmed:
		return "applied"
	case application.Interview:
		return "interview"
	case application.Offer:
		return "offer"
	case application.Rejected:
		return "rejected"
	case application.Withdrawn:
		return "withdrawn"
	default:
		return ""
	}
}

// plMoves lists the columns an application may move to; the state machine decides.
func plMoves(st application.State) []string {
	var out []string
	for _, s := range plStages {
		if s.Key == plColumn(st) {
			continue
		}
		if _, err := application.Next(st, s.Ev); err == nil {
			out = append(out, s.Key)
		}
	}
	return out
}

func plView(v string) string {
	if v == "compact" || v == "table" {
		return v
	}
	return "board"
}

func (s *Server) pipelinePage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d, err := s.buildPipeline(r.Context(), u, plView(r.URL.Query().Get("view")))
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	if partial(r) {
		s.render(w, r, http.StatusOK, views.PipelineBoard(d))
		return
	}
	s.render(w, r, http.StatusOK, views.PipelinePage(d))
}

// trackerRedirect keeps old links working (docs/ui/sitemap.md).
func trackerRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/pipeline", http.StatusPermanentRedirect)
}

// pipelineMove moves a card to another stage; a move the state machine refuses changes nothing.
func (s *Server) pipelineMove(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	appID, err := uuid.Parse(r.PostFormValue("id"))
	if err != nil || s.Apps == nil {
		s.notFound(w, r, u)
		return
	}
	stage, status := r.PostFormValue("stage"), ""
	for _, st := range plStages {
		if st.Key != stage {
			continue
		}
		if _, err := s.Apps.Fire(r.Context(), u.ID, appID, st.Ev, application.ActorUser, map[string]any{"source": "pipeline"}); err == nil {
			status = views.T(r.Context(), "pl.moved_to", "stage", views.T(r.Context(), "pl.col."+st.Key))
		} else {
			status = views.T(r.Context(), "pl.not_moved")
		}
	}
	if !partial(r) {
		http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
		return
	}
	d, err := s.buildPipeline(r.Context(), u, plView(r.PostFormValue("view")))
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d.Status = status
	s.render(w, r, http.StatusOK, views.PipelineBoard(d))
}

// pipelineCard renders one card for the drawer.
func (s *Server) pipelineCard(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if c, ok := s.pipelineCardData(w, r, u); ok {
		s.render(w, r, http.StatusOK, views.PipelineCardDetail(c, ""))
	}
}

// pipelineICS hands out the interview as a calendar entry.
func (s *Server) pipelineICS(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	c, ok := s.pipelineCardData(w, r, u)
	if !ok {
		return
	}
	if c.InterviewAt == nil {
		s.notFound(w, r, u)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="interview.ics"`)
	_, _ = w.Write([]byte(interviewICS(c, time.Now())))
}

// interviewICS writes one VEVENT of an hour (RFC 5545).
func interviewICS(c views.PLCard, now time.Time) string {
	const f = "20060102T150405Z"
	lines := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//getafjob//pipeline//EN", "BEGIN:VEVENT",
		"UID:" + c.ID + "@getafjob", "DTSTAMP:" + now.UTC().Format(f), "DTSTART:" + c.InterviewAt.UTC().Format(f),
		"DURATION:PT1H", "SUMMARY:" + icsText("Interview: "+c.Title+", "+c.Company), "END:VEVENT", "END:VCALENDAR"}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// icsText escapes a text value: backslash, semicolon, comma and line breaks.
func icsText(s string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`).Replace(s)
}

func (s *Server) pipelineCardData(w http.ResponseWriter, r *http.Request, u *identity.User) (views.PLCard, bool) {
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil || s.Apps == nil {
		s.notFound(w, r, u)
		return views.PLCard{}, false
	}
	apps, err := s.Apps.Pipeline(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return views.PLCard{}, false
	}
	for _, a := range apps {
		if a.ID == appID {
			return s.plCard(r.Context(), u.ID, a), true
		}
	}
	s.notFound(w, r, u)
	return views.PLCard{}, false
}

func (s *Server) buildPipeline(ctx context.Context, u *identity.User, view string) (views.PipelineData, error) {
	d := views.PipelineData{Name: u.DisplayName, View: view}
	for _, st := range plStages {
		d.Columns = append(d.Columns, views.PLColumn{Key: st.Key})
	}
	if s.Apps == nil {
		return d, nil
	}
	apps, err := s.Apps.Pipeline(ctx, u.ID)
	if err != nil {
		return d, err
	}
	for _, a := range apps {
		col := plColumn(a.State)
		for i := range d.Columns {
			if d.Columns[i].Key == col {
				d.Columns[i].Cards = append(d.Columns[i].Cards, s.plCard(ctx, u.ID, a))
				d.Total++
			}
		}
	}
	return d, nil
}

func (s *Server) plCard(ctx context.Context, userID uuid.UUID, a application.PipelineApplication) views.PLCard {
	c := views.PLCard{ID: a.ID.String(), JobID: a.JobID, Title: a.Title, Company: a.Company, Location: a.Location,
		State: string(a.State), Col: plColumn(a.State), Moves: plMoves(a.State), Score: a.Score, Salary: a.SalaryDiscussed,
		Notes: a.Notes, InterviewAt: a.InterviewAt, FollowUpAt: a.FollowUpAt, URL: webLink(a.URL), Updated: a.UpdatedAt}
	if s.Tracking != nil {
		if sugs, err := s.Tracking.SuggestionsForApplication(ctx, userID, a.ID); err == nil {
			for _, sg := range sugs {
				if sg.Status == tracking.SuggestionPending {
					c.Mail, c.MailID = "tracker.email."+string(sg.Category), sg.ID.String()
					c.MailFrom, c.MailSubject = sg.MessageFrom, sg.MessageSubject
					break
				}
			}
		}
	}
	return c
}
