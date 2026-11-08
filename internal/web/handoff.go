package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/gateway"
	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/web/views"
)

// HandoffHub accesses active runner handoff sessions.
type HandoffHub interface {
	SessionFor(appID uuid.UUID) (*gateway.HandoffSession, bool)
}

// Screenshots reads sealed run screenshots per user.
type Screenshots interface {
	Get(ctx context.Context, userID uuid.UUID, key string) ([]byte, error)
}

type screenInputMsg struct {
	Type                  string  `json:"type"`
	Action                string  `json:"action"`
	X                     float64 `json:"x"`
	Y                     float64 `json:"y"`
	Button                string  `json:"button"`
	ClickCount            int32   `json:"clickCount"`
	Text                  string  `json:"text"`
	Key                   string  `json:"key"`
	Code                  string  `json:"code"`
	WindowsVirtualKeyCode int32   `json:"windowsVirtualKeyCode"`
}

// screenWebSocket upgrades the connection to WebSocket and streams the live screencast frames to the user
// while dispatching mouse and keyboard input back to the runner. Input is never logged or stored.
func (s *Server) screenWebSocket(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.Error(w, "invalid application id", http.StatusBadRequest)
		return
	}

	if s.Apps != nil {
		if _, err := s.Apps.Get(r.Context(), u.ID, appID); err != nil {
			if errors.Is(err, application.ErrNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	if s.Handoffs == nil {
		http.Error(w, "handoffs unavailable", http.StatusServiceUnavailable)
		return
	}
	sess, ok := s.Handoffs.SessionFor(appID)
	if !ok {
		http.Error(w, "no active browser session", http.StatusNotFound)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{"getafjob-screencast"},
	})
	if err != nil {
		return
	}
	defer func() { _ = ws.Close(websocket.StatusNormalClosure, "") }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Forward screencast frames to the client
	go func() {
		if last := sess.LastFrame(); len(last) > 0 {
			if err := ws.Write(ctx, websocket.MessageBinary, last); err != nil {
				return
			}
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-sess.Done:
				_ = ws.Close(websocket.StatusNormalClosure, "session ended")
				return
			case frame, ok := <-sess.Frames:
				if !ok {
					return
				}
				if err := ws.Write(ctx, websocket.MessageBinary, frame); err != nil {
					return
				}
			}
		}
	}()

	// Receive input from the client and forward to the runner (never logged)
	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var in screenInputMsg
		if err := json.Unmarshal(data, &in); err != nil {
			continue
		}
		switch in.Type {
		case "resume":
			if s.Apps != nil {
				_, _ = s.Apps.Fire(ctx, u.ID, appID, application.EvAnswer, application.ActorUser, nil)
			}
			select {
			case sess.Input <- &runnerv1.HandoffEvent{Msg: &runnerv1.HandoffEvent_Resume{Resume: true}}:
			default:
			}
		case "park":
			if s.Apps != nil {
				_, _ = s.Apps.Fire(ctx, u.ID, appID, application.EvPark, application.ActorUser, map[string]any{"reason": "parked by user"})
			}
			select {
			case sess.Input <- &runnerv1.HandoffEvent{Msg: &runnerv1.HandoffEvent_Park{Park: true}}:
			default:
			}
		case "mouse":
			select {
			case sess.Input <- &runnerv1.HandoffEvent{
				Msg: &runnerv1.HandoffEvent_Mouse{
					Mouse: &runnerv1.InputMouseEvent{
						Type:       in.Action,
						X:          in.X,
						Y:          in.Y,
						Button:     in.Button,
						ClickCount: in.ClickCount,
					},
				},
			}:
			default:
			}
		case "key":
			select {
			case sess.Input <- &runnerv1.HandoffEvent{
				Msg: &runnerv1.HandoffEvent_Key{
					Key: &runnerv1.InputKeyEvent{
						Type:                  in.Action,
						Text:                  in.Text,
						Key:                   in.Key,
						Code:                  in.Code,
						WindowsVirtualKeyCode: in.WindowsVirtualKeyCode,
					},
				},
			}:
			default:
			}
		}
	}
}

// applicationAnswer saves answers provided during a handoff to the answer bank and resumes the application.
func (s *Server) applicationAnswer(w http.ResponseWriter, r *http.Request, u *identity.User) {
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
		s.failed(w, r, u, err)
		return
	}

	questions := r.Form["question"]
	answers := r.Form["answer"]
	var saved []*runnerv1.Answer
	for i := range questions {
		q := strings.TrimSpace(questions[i])
		if i >= len(answers) || q == "" {
			continue
		}
		a := strings.TrimSpace(answers[i])
		if a == "" {
			continue
		}
		id := uuid.New()
		if s.Items != nil {
			if savedID, err := s.Items.SaveAnswer(r.Context(), u.ID, profile.Answer{Question: q, Answer: a}); err == nil {
				id = savedID
			}
		}
		saved = append(saved, &runnerv1.Answer{Id: id.String(), Question: q, Answer: a})
	}

	var sess *gateway.HandoffSession
	if s.Handoffs != nil {
		sess, _ = s.Handoffs.SessionFor(appID)
	}

	if s.Apps != nil {
		if sess != nil {
			_, _ = s.Apps.Fire(r.Context(), u.ID, appID, application.EvAnswer, application.ActorUser, nil)
		} else {
			// No live browser session (e.g. after a restart or hold timeout): park if still waiting and requeue.
			_, _ = s.Apps.Fire(r.Context(), u.ID, appID, application.EvPark, application.ActorUser, map[string]any{"reason": "answered without active session"})
			_, _ = s.Apps.Fire(r.Context(), u.ID, appID, application.EvRequeue, application.ActorUser, nil)
		}
	}

	if sess != nil {
		select {
		case sess.Input <- &runnerv1.HandoffEvent{
			Msg:     &runnerv1.HandoffEvent_Resume{Resume: true},
			Answers: saved,
		}:
		default:
		}
	}

	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

func (s *Server) applicationPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	d := views.HandoffData{
		Name:  u.DisplayName,
		AppID: appID.String(),
	}
	if s.Apps != nil {
		app, err := s.Apps.Get(r.Context(), u.ID, appID)
		if err != nil {
			if errors.Is(err, application.ErrNotFound) {
				s.notFound(w, r, u)
				return
			}
			s.failed(w, r, u, err)
			return
		}
		d.JobID = app.JobID
		d.State = string(app.State)
		d.Status = appStatus(app.State)
		if app.AwaitingApproval() {
			d.ApprovalAsked = true
			for _, re := range app.ApprovalReasons {
				d.ApprovalReasons = append(d.ApprovalReasons, views.T(r.Context(), "rule.reason."+string(re)))
			}
		}
		if s.Jobs != nil {
			if j, err := s.Jobs.Detail(r.Context(), u.ID, app.JobID); err == nil {
				d.JobTitle = j.Title
				d.Company = j.Company
			}
		}
		if app.State == application.NeedsInput || app.State == application.Parked {
			if asked, err := s.Apps.Questions(r.Context(), u.ID, appID); err == nil {
				for _, q := range asked {
					if q.Kind != "captcha" && q.Label != "" {
						d.Questions = append(d.Questions, views.HandoffQuestion{Kind: q.Kind, Question: q.Label, Draft: q.Draft})
					}
				}
			}
		}
		if ev, err := s.Apps.Evidence(r.Context(), u.ID, appID); err == nil {
			for _, f := range ev.Fields {
				d.Fields = append(d.Fields, views.EvidenceFieldRow{
					Page:   f.Page,
					Label:  f.Label,
					Value:  f.Value,
					Source: f.Source,
				})
			}
			if ev.BeforeSubmitCheckpointID > 0 {
				d.BeforeSubmitURL = "/applications/" + appID.String() + "/screenshots/" + strconv.FormatInt(ev.BeforeSubmitCheckpointID, 10)
			}
			if ev.ConfirmationCheckpointID > 0 {
				d.ConfirmationShotURL = "/applications/" + appID.String() + "/screenshots/" + strconv.FormatInt(ev.ConfirmationCheckpointID, 10)
			}
			d.ConfirmationURL = webLink(ev.ConfirmationURL)
			d.Confirmed = ev.Confirmed || app.State == application.Confirmed
			for _, t := range ev.Timeline {
				var shotURL string
				if t.CheckpointID > 0 {
					shotURL = "/applications/" + appID.String() + "/screenshots/" + strconv.FormatInt(t.CheckpointID, 10)
				}
				d.Timeline = append(d.Timeline, views.TimelineRow{
					At:            t.At,
					Kind:          t.Kind,
					Step:          t.Step,
					FromState:     string(t.FromState),
					ToState:       string(t.ToState),
					Actor:         string(t.Actor),
					Page:          t.Page,
					URL:           t.URL,
					Detail:        t.Detail,
					ScreenshotURL: shotURL,
				})
			}
		}
	}
	if s.Tracking != nil {
		if sugs, err := s.Tracking.SuggestionsForApplication(r.Context(), u.ID, appID); err == nil {
			for _, sg := range sugs {
				if sg.Status == tracking.SuggestionPending {
					d.Suggestion = &views.SuggestionData{
						ID:             sg.ID.String(),
						Category:       string(sg.Category),
						SuggestedState: string(sg.SuggestedState),
						Reasons:        sg.Reasons,
						From:           sg.MessageFrom,
						Subject:        sg.MessageSubject,
					}
					break
				}
			}
		}
	}
	s.render(w, r, http.StatusOK, views.HandoffPage(d))
}

func (s *Server) suggestionAccept(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	sugID, err := uuid.Parse(r.PathValue("sugId"))
	if err != nil || s.Tracking == nil {
		s.notFound(w, r, u)
		return
	}
	if err := s.Tracking.AcceptSuggestion(r.Context(), u.ID, sugID); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/applications/"+appID.String(), http.StatusSeeOther) //nolint:gosec // G710: appID is a parsed UUID
}

func (s *Server) suggestionDismiss(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	sugID, err := uuid.Parse(r.PathValue("sugId"))
	if err != nil || s.Tracking == nil {
		s.notFound(w, r, u)
		return
	}
	if err := s.Tracking.DismissSuggestion(r.Context(), u.ID, sugID); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/applications/"+appID.String(), http.StatusSeeOther) //nolint:gosec // G710: appID is a parsed UUID
}

func appStatus(st application.State) string {
	switch st {
	case application.Submitted, application.Confirmed, application.Interview, application.Offer:
		return "done"
	case application.NeedsInput:
		return "your_turn"
	case application.Parked, application.Failed, application.Rejected, application.Withdrawn:
		return "failed"
	default:
		return "waiting"
	}
}

// applicationScreenshot serves a decrypted checkpoint screenshot belonging to an application of the signed-in user.
func (s *Server) applicationScreenshot(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	cpID, err := strconv.ParseInt(r.PathValue("checkpoint"), 10, 64)
	if err != nil || cpID <= 0 || s.Apps == nil || s.Shots == nil {
		s.notFound(w, r, u)
		return
	}
	key, err := s.Apps.CheckpointScreenshot(r.Context(), u.ID, appID, cpID)
	if err != nil {
		if errors.Is(err, application.ErrNotFound) {
			s.notFound(w, r, u)
			return
		}
		s.failed(w, r, u, err)
		return
	}
	png, err := s.Shots.Get(r.Context(), u.ID, key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			s.notFound(w, r, u)
			return
		}
		s.failed(w, r, u, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Content-Length", strconv.Itoa(len(png)))
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	_, _ = w.Write(png) //nolint:gosec // G705: PNG screenshot verified at upload, served as image/png under a sandbox CSP
}

// applicationPark parks the application from the UI.
func (s *Server) applicationPark(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}

	if s.Apps != nil {
		_, _ = s.Apps.Fire(r.Context(), u.ID, appID, application.EvPark, application.ActorUser, map[string]any{"reason": "parked by user"})
	}

	if s.Handoffs != nil {
		if sess, ok := s.Handoffs.SessionFor(appID); ok {
			select {
			case sess.Input <- &runnerv1.HandoffEvent{Msg: &runnerv1.HandoffEvent_Park{Park: true}}:
			default:
			}
		}
	}

	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}
