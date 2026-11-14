package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/gateway"
	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/applymode"
	"github.com/1xxn/getafjob/internal/platform/blob"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/web/views"
)

// HandoffHub accesses active runner handoff sessions.
type HandoffHub interface {
	SessionFor(appID uuid.UUID) (*gateway.HandoffSession, bool)
	ActiveRuns(userID uuid.UUID) []uuid.UUID
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

// Close codes of the live view socket; the client stops reconnecting on screenNotLive.
const (
	screenNotLive = websocket.StatusCode(4000) // the run is over or never started: show evidence instead
	screenWaiting = websocket.StatusCode(4001) // the run is queued but no browser streams yet: try again
)

// liveStates are the application states in which a runner may stream a live view.
var liveStates = map[application.State]bool{application.Queued: true, application.Running: true,
	application.NeedsInput: true, application.Submitting: true}

// screenStatus tells the client whether its input reaches the page, and whether the run waits for answers to
// questions: those are given on the answer form, the page itself cannot carry their provenance.
type screenStatus struct {
	Type        string `json:"type"`
	Interactive bool   `json:"interactive"`
	Answers     bool   `json:"answers,omitempty"`
}

// openFieldQuestions reports whether the waiting run asks the user for field answers.
func (s *Server) openFieldQuestions(ctx context.Context, userID, appID uuid.UUID) bool {
	qs, err := s.Apps.Questions(ctx, userID, appID)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(qs, func(q application.Question) bool { return q.Kind == application.QuestionField })
}

// screenWebSocket streams the run's live view to the user and, while the runner waits for the user, passes mouse
// and keyboard input back. Any number of viewers can watch the same run. Input is never logged or stored.
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
	if s.Apps == nil || s.Handoffs == nil {
		http.Error(w, "live view unavailable", http.StatusServiceUnavailable)
		return
	}
	app, err := s.Apps.Get(r.Context(), u.ID, appID)
	if errors.Is(err, application.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"getafjob-screencast"}})
	if err != nil {
		return
	}
	defer func() { _ = ws.CloseNow() }()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sess, ok := s.Handoffs.SessionFor(appID)
	if !ok {
		if !liveStates[app.State] {
			_ = ws.Close(screenNotLive, "not live")
			return
		}
		// A queued or starting run gets its browser within seconds; wait a little before the client retries.
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		timeout := time.After(20 * time.Second)
		for !ok {
			select {
			case <-ctx.Done():
				return
			case <-timeout:
				_ = ws.Close(screenWaiting, "waiting for the browser")
				return
			case <-tick.C:
				sess, ok = s.Handoffs.SessionFor(appID)
			}
		}
	}

	frames, unsubscribe := sess.Subscribe()
	defer unsubscribe()
	gate := newFrameGate(r.URL.Query().Get("fps"))
	go func() {
		defer cancel()
		if last := sess.LastFrame(); len(last) > 0 {
			if ws.Write(ctx, websocket.MessageBinary, last) != nil {
				return
			}
		}
		status := func(interactive bool) screenStatus {
			return screenStatus{Type: "status", Interactive: interactive,
				Answers: interactive && s.openFieldQuestions(ctx, u.ID, appID)}
		}
		interactive := sess.Interactive()
		if wsJSON(ctx, ws, status(interactive)) != nil {
			return
		}
		statusTick := time.NewTicker(time.Second)
		defer statusTick.Stop()
		defer gate.stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-sess.Done:
				_ = ws.Close(websocket.StatusNormalClosure, "session ended")
				return
			case <-statusTick.C:
				if now := sess.Interactive(); now != interactive {
					interactive = now
					if wsJSON(ctx, ws, status(now)) != nil {
						return
					}
				}
			case f := <-frames:
				if f = gate.offer(f, time.Now()); f != nil && ws.Write(ctx, websocket.MessageBinary, f) != nil {
					return
				}
			case now := <-gate.tick():
				if f := gate.flush(now); f != nil && ws.Write(ctx, websocket.MessageBinary, f) != nil {
					return
				}
			}
		}
	}()

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		var in screenInputMsg
		if json.Unmarshal(data, &in) != nil {
			continue
		}
		ev := s.screenEvent(ctx, u, appID, in)
		// Input only counts while the runner waits for the user; queued clicks must never replay into the page later.
		if ev == nil || !sess.Interactive() {
			continue
		}
		select {
		case sess.Input <- ev:
		default:
		}
	}
}

// screenEvent turns a client message into a runner event; resume and park also move the application.
func (s *Server) screenEvent(ctx context.Context, u *identity.User, appID uuid.UUID, in screenInputMsg) *runnerv1.HandoffEvent {
	switch in.Type {
	case "resume":
		_, _ = s.Apps.Fire(ctx, u.ID, appID, application.EvAnswer, application.ActorUser, nil)
		return &runnerv1.HandoffEvent{Msg: &runnerv1.HandoffEvent_Resume{Resume: true}}
	case "park":
		_, _ = s.Apps.Fire(ctx, u.ID, appID, application.EvPark, application.ActorUser, map[string]any{"reason": "parked by user"})
		return &runnerv1.HandoffEvent{Msg: &runnerv1.HandoffEvent_Park{Park: true}}
	case "mouse":
		return &runnerv1.HandoffEvent{Msg: &runnerv1.HandoffEvent_Mouse{Mouse: &runnerv1.InputMouseEvent{
			Type: in.Action, X: in.X, Y: in.Y, Button: in.Button, ClickCount: in.ClickCount}}}
	case "key":
		return &runnerv1.HandoffEvent{Msg: &runnerv1.HandoffEvent_Key{Key: &runnerv1.InputKeyEvent{
			Type: in.Action, Text: in.Text, Key: in.Key, Code: in.Code, WindowsVirtualKeyCode: in.WindowsVirtualKeyCode}}}
	}
	return nil
}

func wsJSON(ctx context.Context, ws *websocket.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, b)
}

// activeRuns lists the user's runs that stream a live view, newest data from the job index.
func (s *Server) activeRuns(ctx context.Context, userID, current uuid.UUID) []views.ActiveRunItem {
	if s.Handoffs == nil || s.Apps == nil {
		return nil
	}
	ids := s.Handoffs.ActiveRuns(userID)
	briefs, err := s.Apps.Briefs(ctx, userID, ids)
	if err != nil {
		return nil
	}
	byID := make(map[uuid.UUID]application.Brief, len(briefs))
	for _, b := range briefs {
		byID[b.ID] = b
	}
	var out []views.ActiveRunItem
	for _, id := range ids { // the order of the gateway: oldest run first
		b, ok := byID[id]
		if !ok || !liveStates[b.State] {
			continue // a parked or finished run may hold its browser a moment longer; it is not live any more
		}
		out = append(out, views.ActiveRunItem{AppID: id.String(), JobID: b.JobID, Current: id == current,
			Waiting: b.State == application.NeedsInput, JobTitle: b.Title, Company: b.Company})
	}
	return out
}

// liveIndicator renders the live runs pill of the top bar; pages poll it until the event stream exists (#166).
func (s *Server) liveIndicator(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.render(w, r, http.StatusOK, views.LiveIndicator(s.activeRuns(r.Context(), u.ID, uuid.Nil)))
}

// formAnswer turns the posted values of one question into the answer text: a single checkbox posts "No" from a
// hidden input and "Yes" when ticked (the last wins), a checkbox group joins the ticked choices.
func formAnswer(input string, values []string) string {
	var vs []string
	for _, v := range values {
		if v = strings.TrimSpace(strings.ToValidUTF8(v, "")); v != "" {
			vs = append(vs, v)
		}
	}
	switch {
	case len(vs) == 0:
		return ""
	case input == "checkbox_group":
		return strings.Join(vs, "; ")
	default:
		return vs[len(vs)-1]
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

	var sess *gateway.HandoffSession
	if s.Handoffs != nil {
		sess, _ = s.Handoffs.SessionFor(appID)
	}

	// Only a runner that is waiting right now takes the answers in its open browser; otherwise (hold over, restart)
	// the application is queued again and a new run uses the saved answers.
	if sess != nil && !sess.Interactive() {
		sess = nil
	}

	var saved []*runnerv1.Answer
	for i := 0; i < 50; i++ {
		key := strconv.Itoa(i)
		if _, ok := r.Form["question-"+key]; !ok {
			break
		}
		q := strings.TrimSpace(strings.ToValidUTF8(r.Form.Get("question-"+key), ""))
		a := formAnswer(r.Form.Get("input-"+key), r.Form["answer-"+key])
		if q == "" || a == "" {
			continue
		}
		// save-<i> is "0" then "1" when the switch is on; a form without it saves (older pages). Without a waiting
		// run the answers always go to the bank: the next run reads them from there. An answer kept out of the bank
		// has no id: the run fills it with the source "input" (ADR 0013).
		id := ""
		if save := r.Form["save-"+key]; s.Items != nil && (sess == nil || len(save) == 0 || save[len(save)-1] == "1") {
			savedID, err := s.Items.SaveAnswer(r.Context(), u.ID, profile.Answer{Question: q, Answer: a})
			if err != nil {
				s.failed(w, r, u, err)
				return
			}
			id = savedID.String()
		}
		saved = append(saved, &runnerv1.Answer{Id: id, Question: q, Answer: a})
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

	if partial(r) {
		more := false
		if s.Apps != nil {
			if c, err := s.Apps.NavCounts(r.Context(), u.ID); err == nil {
				more = c.NeedsInput > 0 // this run no longer waits: any count is another run
			}
		}
		s.render(w, r, http.StatusOK, views.AnswerSent(appID.String(), sess != nil, more))
		return
	}
	if back := r.PostFormValue("back"); strings.HasPrefix(back, "/needs-input") {
		http.Redirect(w, r, localBack(back), http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
		return
	}
	http.Redirect(w, r, "/applications/"+appID.String(), http.StatusSeeOther) //nolint:gosec // G710: appID is a parsed UUID
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
	d, ok := s.applicationDetail(w, r, u, appID)
	if !ok {
		return
	}
	d.Tab = r.URL.Query().Get("tab")
	if !slices.Contains([]string{"live", "fields", "timeline", "evidence", "notes"}, d.Tab) {
		d.Tab = "overview"
	}
	if d.Tab == "notes" && s.Apps != nil {
		if n, err := s.Apps.GetNotes(r.Context(), u.ID, uuid.MustParse(d.AppID)); err == nil {
			d.Notes = views.NoteForm{Notes: n.Notes, Salary: n.SalaryDiscussed}
			if n.InterviewAt != nil {
				d.Notes.InterviewAt = n.InterviewAt.In(views.Zone(r.Context())).Format("2006-01-02T15:04")
			}
			if n.FollowUpAt != nil {
				d.Notes.FollowUpAt = n.FollowUpAt.Format("2006-01-02")
			}
		}
	}
	s.render(w, r, http.StatusOK, views.ApplicationPage(d))
}

// applicationHead renders the state and next steps alone; the page swaps it in on live changes.
func (s *Server) applicationHead(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	if d, ok := s.applicationDetail(w, r, u, appID); ok {
		s.render(w, r, http.StatusOK, views.AppHead(d))
	}
}

// applicationDetail loads one application of the user with job, questions, evidence and the next steps; on false
// the response is written.
func (s *Server) applicationDetail(w http.ResponseWriter, r *http.Request, u *identity.User, appID uuid.UUID) (views.AppDetailData, bool) {
	if s.Apps == nil {
		s.notFound(w, r, u)
		return views.AppDetailData{}, false
	}
	ctx := r.Context()
	app, err := s.Apps.Get(ctx, u.ID, appID)
	if err != nil {
		if errors.Is(err, application.ErrNotFound) {
			s.notFound(w, r, u)
		} else {
			s.failed(w, r, u, err)
		}
		return views.AppDetailData{}, false
	}
	d := views.AppDetailData{HandoffData: views.HandoffData{Name: u.DisplayName, AppID: appID.String(), JobID: app.JobID,
		State: string(app.State), Live: liveStates[app.State]}}
	d.StateKey, d.Tone, d.NextKey = "apps.state."+string(app.State), appTone(app.State), "apps.next."+string(app.State)
	if app.AwaitingApproval() {
		d.ApprovalAsked = true
		d.NextKey = "apps.next.approval"
		for _, re := range app.ApprovalReasons {
			d.ApprovalReasons = append(d.ApprovalReasons, views.T(ctx, "rule.reason."+string(re)))
		}
	}
	if s.Jobs != nil {
		if j, err := s.Jobs.Detail(ctx, u.ID, app.JobID); err == nil {
			d.JobTitle, d.Company = j.Title, j.Company
			d.ApplyMode = string(applymode.Of(j.URL, j.ApplyURL))
			d.ApplyHost = applymode.Host(cmp.Or(j.ApplyURL, j.URL))
			d.ExternalURL = webLink(cmp.Or(j.ApplyURL, j.URL))
		}
	}
	if app.State == application.NeedsInput || (app.State == application.Parked && !app.Attempted) {
		if asked, err := s.Apps.Questions(ctx, u.ID, appID); err == nil {
			for _, q := range asked {
				if q.Kind == application.QuestionField && q.Label != "" {
					d.Questions = append(d.Questions, views.HandoffQuestion{Kind: q.Kind, Question: q.Label,
						Input: q.Input, Options: q.Options, Keep: app.State != application.NeedsInput})
				}
			}
		}
	}
	if app.State == application.Parked {
		if reason, err := s.Apps.ParkReason(ctx, u.ID, appID); err == nil {
			d.ReasonKey = parkReasonKey(reason)
		}
		if d.ReasonKey == "dash.park.answers" && len(d.Questions) > 0 && !d.ApprovalAsked {
			d.NextKey = "apps.next.answers"
		}
	}
	if ev, err := s.Apps.Evidence(ctx, u.ID, appID); err == nil {
		shot := func(id int64) string {
			if id <= 0 {
				return ""
			}
			return "/applications/" + appID.String() + "/screenshots/" + strconv.FormatInt(id, 10)
		}
		for _, f := range ev.Fields {
			d.Fields = append(d.Fields, views.EvidenceFieldRow{Page: f.Page, Label: f.Label, Value: f.Value, Source: f.Source})
		}
		d.BeforeSubmitURL, d.ConfirmationShotURL = shot(ev.BeforeSubmitCheckpointID), shot(ev.ConfirmationCheckpointID)
		d.ConfirmationURL = webLink(ev.ConfirmationURL)
		d.Confirmed = ev.Confirmed || app.State == application.Confirmed
		for _, t := range ev.Timeline {
			d.Timeline = append(d.Timeline, views.TimelineRow{At: t.At, Kind: t.Kind, Step: t.Step, FromState: string(t.FromState),
				ToState: string(t.ToState), Actor: string(t.Actor), Page: t.Page, URL: t.URL, Detail: t.Detail,
				ScreenshotURL: shot(t.CheckpointID)})
		}
	}
	if s.Tracking != nil {
		if sugs, err := s.Tracking.SuggestionsForApplication(ctx, u.ID, appID); err == nil {
			for _, sg := range sugs {
				if sg.Status == tracking.SuggestionPending {
					d.Suggestion = &views.SuggestionData{ID: sg.ID.String(), Category: string(sg.Category),
						SuggestedState: sg.SuggestedState, Reasons: sg.Reasons, From: sg.MessageFrom, Subject: sg.MessageSubject}
					break
				}
			}
		}
	}
	if app.Attempted && (app.State == application.Parked || app.State == application.Failed) {
		d.Attempted, d.NextKey = true, "apps.next.attempted"
	}
	d.Actions = appActions(d, app.State)
	return d, true
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
	if r.PostFormValue("back") == "/pipeline" {
		http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
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
	if r.PostFormValue("back") == "/pipeline" {
		http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/applications/"+appID.String(), http.StatusSeeOther) //nolint:gosec // G710: appID is a parsed UUID
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

	_ = s.parkApplication(r, u, appID)

	if back := r.PostFormValue("back"); back != "" {
		http.Redirect(w, r, localBack(back), http.StatusSeeOther) //nolint:gosec // localBack keeps only paths on this site
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

// parkApplication parks the application and stops its run if one is open.
func (s *Server) parkApplication(r *http.Request, u *identity.User, appID uuid.UUID) error {
	if s.Apps == nil {
		return errors.New("no application service")
	}
	_, err := s.Apps.Fire(r.Context(), u.ID, appID, application.EvPark, application.ActorUser,
		map[string]any{"reason": "parked by user"})
	if s.Handoffs != nil {
		if sess, ok := s.Handoffs.SessionFor(appID); ok {
			select {
			case sess.Input <- &runnerv1.HandoffEvent{Msg: &runnerv1.HandoffEvent_Park{Park: true}}:
			default:
			}
		}
	}
	return err
}

// maxViewerFPS caps the frame rate a viewer may ask for; 0 (no fps parameter) sends every frame.
const maxViewerFPS = 30

// frameGate drops frames for a viewer that asked for a lower frame rate (grid thumbnails): at most one frame per
// gap, and the newest frame held back is sent when the gap ends, so the picture never stays on an old state.
type frameGate struct {
	gap     time.Duration
	last    time.Time
	pending []byte
	ticker  *time.Ticker
}

func newFrameGate(fps string) *frameGate {
	n, err := strconv.Atoi(fps)
	if err != nil || n <= 0 || n >= maxViewerFPS {
		return &frameGate{}
	}
	gap := time.Second / time.Duration(n)
	return &frameGate{gap: gap, ticker: time.NewTicker(gap)}
}

// offer returns the frame to send now, or nil when it is held back.
func (g *frameGate) offer(f []byte, now time.Time) []byte {
	if g.gap == 0 || now.Sub(g.last) >= g.gap {
		g.last, g.pending = now, nil
		return f
	}
	g.pending = f
	return nil
}

// flush returns the frame held back once its gap has passed.
func (g *frameGate) flush(now time.Time) []byte {
	if g.pending == nil || now.Sub(g.last) < g.gap {
		return nil
	}
	f := g.pending
	g.last, g.pending = now, nil
	return f
}

// tick fires while frames may be held back; nil (never fires) without a frame rate.
func (g *frameGate) tick() <-chan time.Time {
	if g.ticker == nil {
		return nil
	}
	return g.ticker.C
}

func (g *frameGate) stop() {
	if g.ticker != nil {
		g.ticker.Stop()
	}
}
