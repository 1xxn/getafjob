package web

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/web/views"
)

func TestParkReasonKey(t *testing.T) {
	for reason, want := range map[string]string{
		"parked by user": "dash.park.user",
		"waiting for your answers; the browser was closed to free the runner": "dash.park.answers",
		"handoff window closed": "dash.park.timeout",
		"open the posting: navigation failed: net::ERR_TUNNEL_CONNECTION_FAILED": "dash.park.unreachable",
		"run: submit not registered, no click: unknown":                          "dash.park.no_confirm",
		"captcha needs a human":                 "dash.park.check",
		"submit had no effect after one retry":  "dash.park.no_confirm",
		application.RunnerLost:                  "dash.park.runner_lost",
		"the form rejected the submit: [email]": "dash.park.form_rejected",
		application.ErrExcluded.Error():         "dash.park.excluded",
		application.ErrDuplicate.Error():        "dash.park.duplicate",
		application.ErrCompanyWindow.Error():    "dash.park.company",
		"":                                      "dash.park.other",
	} {
		if got := parkReasonKey(reason); got != want {
			t.Errorf("%q: %s, want %s", reason, got, want)
		}
	}
}

func TestFillApplications(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	since := now.Truncate(24*time.Hour).AddDate(0, 0, -29)
	asked := now.Add(-time.Hour)
	app := func(st application.State, age time.Duration) application.PipelineApplication {
		return application.PipelineApplication{ID: uuid.New(), State: st, Title: "Backend Engineer", Company: "Acme",
			UpdatedAt: now.Add(-age)}
	}
	approval := app(application.NeedsInput, 2*time.Hour)
	approval.ApprovalAskedAt = &asked
	question := app(application.NeedsInput, time.Hour)
	var apps []application.PipelineApplication
	apps = append(apps, approval, question, app(application.Queued, 0), app(application.Submitted, 0),
		app(application.Interview, 0), app(application.Rejected, 0))
	reasons := map[uuid.UUID]string{}
	for i := range 7 {
		p := app(application.Parked, time.Duration(10+i)*time.Hour)
		reasons[p.ID] = "parked by user"
		apps = append(apps, p)
	}
	var d views.DashboardData
	fillApplications(&d, application.Overview{Apps: apps, ParkReasons: reasons, Since: since,
		SubmitsByDay: map[time.Time]int{since.AddDate(0, 0, 29): 2, since: 1}})

	if d.Answers != 1 || d.Approvals != 1 || d.Queued != 1 || d.Parked != 7 {
		t.Fatalf("counts: answers %d approvals %d queued %d parked %d", d.Answers, d.Approvals, d.Queued, d.Parked)
	}
	if len(d.Actions) != 2+maxParked || d.Actions[0].Kind != "answer" || d.Actions[1].Kind != "approve" ||
		d.Actions[2].Kind != "parked" || d.Actions[2].ReasonKey != "dash.park.user" {
		t.Fatalf("actions: %+v", d.Actions)
	}
	if d.Pipeline != (views.DashPipeline{Applied: 1, Interview: 1, Closed: 1}) {
		t.Fatalf("pipeline: %+v", d.Pipeline)
	}
	if len(d.Trend) != 30 || d.Sent30 != 3 || d.TrendMax != 2 || d.Trend[29].Count != 2 || d.Trend[0].Count != 1 {
		t.Fatalf("trend: sent %d max %d", d.Sent30, d.TrendMax)
	}
}

// A parked application with a submit attempt offers marking it as applied, never a retry (#336).
func TestAppActionsAfterSubmitAttempt(t *testing.T) {
	d := views.AppDetailData{Attempted: true}
	d.AppID, d.JobID, d.ApplyMode, d.ExternalURL = "a1", 7, "form", "https://jobs.example.org/7"
	var keys []string
	for _, a := range appActions(d, application.Parked) {
		keys = append(keys, a.Key)
	}
	if len(keys) != 2 || keys[0] != "apply.mark_applied" || keys[1] != "apps.action.open_external" {
		t.Fatalf("actions = %v, want mark applied and open posting", keys)
	}
	d.Attempted = false
	if a := appActions(d, application.Parked); len(a) == 0 || a[0].Key != "apps.action.retry" {
		t.Fatalf("without attempt = %+v, want retry first", a)
	}
}
