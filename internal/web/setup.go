package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/cvimport"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/web/views"
)

// setupTop is how many ranked jobs the last step shows.
const setupTop = 5

// oldSetupSteps lead bookmarks of the earlier assistant to the step that holds their fields now.
var oldSetupSteps = map[string]string{"contact": "profile", "work": "profile", "history": "profile", "done": "matches"}

func validStep(step string) bool { return slices.Contains(views.SetupSteps, step) }

// setupStart resumes the onboarding at the first step that still lacks something: a CV, the fields forms ask
// for, a job title to look for. With all of that in place it shows the matches.
func (s *Server) setupStart(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/setup/"+s.resumeStep(r.Context(), u), http.StatusSeeOther)
}

func (s *Server) resumeStep(ctx context.Context, u *identity.User) string {
	if !s.hasCV(ctx, u) {
		return "cv"
	}
	snap, err := s.Profile.Get(ctx, u.ID)
	if err != nil {
		return "cv"
	}
	if len(snap.Profile.Missing()) > 0 {
		return "profile"
	}
	if len(snap.Preferences.Titles) == 0 {
		return "search"
	}
	return "matches"
}

func (s *Server) hasCV(ctx context.Context, u *identity.User) bool {
	if s.Documents == nil {
		return true
	}
	docs, err := s.Documents.List(ctx, u.ID)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(docs, func(d profile.Document) bool { return d.Kind == profile.KindCV })
}

// setupPage shows one step with the stored data.
func (s *Server) setupPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	step := r.PathValue("step")
	if to, ok := oldSetupSteps[step]; ok {
		http.Redirect(w, r, "/setup/"+to, http.StatusPermanentRedirect)
		return
	}
	if !validStep(step) {
		s.notFound(w, r, u)
		return
	}
	if step == "matches" && partial(r) {
		s.render(w, r, http.StatusOK, views.SetupMatches(s.setupMatches(r.Context(), u)))
		return
	}
	s.renderSetup(w, r, u, step, http.StatusOK, views.Form{})
}

func (s *Server) automation(ctx context.Context, u *identity.User) application.Settings {
	st := application.DefaultSettings
	if s.Apps != nil {
		if cfg, err := s.Apps.Settings(ctx, u.ID); err == nil {
			st = cfg
		}
	}
	return st
}

func (s *Server) renderSetup(w http.ResponseWriter, r *http.Request, u *identity.User, step string, status int, override views.Form) {
	snap, err := s.Profile.Get(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d := views.SetupData{Name: u.DisplayName, Step: step, Passkeys: true}
	switch step {
	case "search", "automation":
		d.Form = preferencesForm(snap.Preferences, s.automation(r.Context(), u), tracking.MailboxAccount{}, notify.Preferences{}, nil, false)
		// A first search starts from the profile: the current title and the city are the likeliest answers.
		if step == "search" && len(snap.Preferences.Titles) == 0 && len(snap.Preferences.Locations) == 0 {
			d.Form.Values["titles"] = snap.Profile.Work.CurrentTitle
			d.Form.Values["locations"] = snap.Profile.Personal.City
		}
	default:
		d.Form = profileForm(snap)
	}
	if override.Values != nil {
		d.Form.Values, d.Form.Checks = override.Values, override.Checks
	}
	d.Form.Errors, d.Form.Notice, d.Form.Failed = override.Errors, override.Notice, override.Failed
	if d.Form.Notice == "" && r.URL.Query().Get("imported") == "1" {
		d.Form.Notice = views.T(r.Context(), "profile.imported")
	}
	switch step {
	case "cv":
		docs, err := s.Documents.List(r.Context(), u.ID)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		for _, doc := range docs {
			if doc.Kind == profile.KindCV && (doc.Mime == profile.MimePDF || doc.Mime == profile.MimeDOCX) {
				d.CVs = append(d.CVs, views.DocumentRow{ID: doc.ID.String(), Name: doc.Name})
			}
		}
	case "profile":
		if s.Stations != nil {
			exps, err := s.Stations.WorkExperiences(r.Context(), u.ID)
			if err != nil {
				s.failed(w, r, u, err)
				return
			}
			for _, e := range exps {
				d.Stations = append(d.Stations, strings.Trim(e.Title+", "+e.Company, ", "))
			}
		}
	case "matches":
		d.Matches = s.setupMatches(r.Context(), u)
		if st := s.automation(r.Context(), u); st.AutoQueue() {
			d.AutoScore = max(st.MinScore, 1)
		}
		if s.Identity.PasskeysEnabled() {
			keys, err := s.Identity.Passkeys(r.Context(), u.ID)
			if err != nil {
				s.failed(w, r, u, err)
				return
			}
			d.Passkeys = len(keys) > 0
		}
	}
	s.render(w, r, status, views.SetupPage(d))
}

// setupMatches reads the best rated jobs and the state of the crawl.
func (s *Server) setupMatches(ctx context.Context, u *identity.User) views.SetupMatchesData {
	var d views.SetupMatchesData
	if snap, err := s.Profile.Get(ctx, u.ID); err == nil && len(snap.Preferences.Titles) == 0 {
		d.NoTitles = true
	}
	if s.Jobs != nil {
		l, err := s.Jobs.List(ctx, u.ID, matching.Filter{Decisions: []matching.Decision{matching.Match, matching.Maybe},
			Sort: "match", Page: 1})
		if err == nil {
			d.Total, d.Rated = l.Total, len(l.Counts) > 0 || l.BlockedCount > 0
			for _, it := range l.Items[:min(setupTop, len(l.Items))] {
				d.Top = append(d.Top, views.DashJob{ID: it.JobID, Title: it.Title, Company: it.Company, Score: it.Score})
			}
		}
	} else {
		d.Rated = true
	}
	if s.Discovery != nil {
		if st, err := s.Discovery.CrawlStatus(ctx); err == nil {
			d.Crawl = toCrawlStatusData(ctx, st)
			d.Started = st.Running || st.Queued || (st.LastRunAt != nil && !st.LastRunAt.IsZero())
		}
	} else {
		d.Started = true
	}
	return d
}

// setupCrawl starts the first crawl when none ran yet and shows the matches step again.
func (s *Server) setupCrawl(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Discovery != nil {
		if _, err := s.Discovery.TriggerCrawl(r.Context()); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}
	http.Redirect(w, r, "/setup/matches", http.StatusSeeOther)
}

// setupSave stores one step and moves on; errors show the step again with the typed values.
func (s *Server) setupSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	step := r.PathValue("step")
	if !validStep(step) || step == "cv" || step == "matches" {
		s.notFound(w, r, u)
		return
	}
	next := "/setup/" + views.SetupSteps[slices.Index(views.SetupSteps, step)+1]
	switch step {
	case "search":
		s.setupSaveSearch(w, r, u, next)
	case "automation":
		s.setupSaveAutomation(w, r, u, next)
	default:
		s.setupSaveProfile(w, r, u, next)
	}
}

func (s *Server) setupSaveSearch(w http.ResponseWriter, r *http.Request, u *identity.User, next string) {
	snap, err := s.Profile.Get(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	keep := snap.Preferences
	keep.Titles, keep.Locations = profile.Lines(r.PostFormValue("titles")), profile.Lines(r.PostFormValue("locations"))
	keep.Remote = r.PostFormValue("remote") == "on"
	var serr error
	keep.MinSalary = 0
	if v := strings.TrimSpace(r.PostFormValue("min_salary")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			serr = &profile.ValidationError{Fields: []profile.FieldError{{Field: "min_salary", Code: "profile.error.invalid"}}}
		}
		keep.MinSalary = n
	}
	if serr == nil {
		serr = s.Profile.SavePreferences(r.Context(), u.ID, keep)
	}
	f := preferencesForm(keep, s.automation(r.Context(), u), tracking.MailboxAccount{}, notify.Preferences{}, nil, false)
	if serr != nil {
		f.Values["min_salary"] = r.PostFormValue("min_salary")
	}
	s.setupSaved(w, r, u, "search", next, serr, f)
}

// setupSaveAutomation stores the level only; the other automation settings stay as they are.
func (s *Server) setupSaveAutomation(w http.ResponseWriter, r *http.Request, u *identity.User, next string) {
	st := s.automation(r.Context(), u)
	switch level := application.Level(r.PostFormValue("autonomy_level")); level {
	case application.LevelSuggest, application.LevelApprove, application.LevelAutonomous:
		st.Level = level
	default:
		s.renderSetup(w, r, u, "automation", http.StatusUnprocessableEntity, views.Form{
			Errors: map[string]string{"autonomy_level": views.T(r.Context(), "profile.error.invalid")},
			Notice: views.T(r.Context(), "profile.invalid"), Failed: true})
		return
	}
	if s.Apps != nil {
		if err := s.Apps.SaveSettings(r.Context(), u.ID, st); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) setupSaveProfile(w http.ResponseWriter, r *http.Request, u *identity.User, next string) {
	snap, err := s.Profile.Get(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	version, _ := strconv.ParseInt(r.PostFormValue("version"), 10, 64)
	p := snap.Profile
	v := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	pe, wk := &p.Personal, &p.Work
	pe.FirstName, pe.LastName, pe.Email, pe.Phone = v("first_name"), v("last_name"), v("email"), v("phone")
	pe.PostalCode, pe.City = v("postal_code"), v("city")
	wk.CurrentTitle, wk.YearsTotal, wk.NoticePeriod = v("current_title"), v("years_total"), v("notice_period")
	wk.EarliestStart, wk.SalaryExpectation = v("earliest_start"), v("salary_expectation")
	wk.WorkAuthorizedEU = r.PostFormValue("work_authorized_eu") == "on"
	p.Skills = profile.Lines(r.PostFormValue("skills"))
	err = s.Profile.SaveProfile(r.Context(), u.ID, p, version)
	s.setupSaved(w, r, u, "profile", next, err, profileForm(profile.Snapshot{Profile: p, Version: version}))
}

func (s *Server) setupSaved(w http.ResponseWriter, r *http.Request, u *identity.User, step, next string, err error, f views.Form) {
	switch {
	case err == nil:
		http.Redirect(w, r, next, http.StatusSeeOther)
	case errors.Is(err, profile.ErrInvalid):
		f.Errors, f.Notice, f.Failed = fieldErrors(r, err), views.T(r.Context(), "profile.invalid"), true
		s.renderSetup(w, r, u, step, http.StatusUnprocessableEntity, f)
	case errors.Is(err, profile.ErrConflict):
		s.renderSetup(w, r, u, step, http.StatusConflict, views.Form{Notice: views.T(r.Context(), "profile.conflict"), Failed: true})
	default:
		s.failed(w, r, u, err)
	}
}

// setupCVUpload stores an uploaded CV and fills the profile from it.
func (s *Server) setupCVUpload(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	_, name, data, err := readUpload(r)
	var doc profile.Document
	if err == nil {
		doc, err = s.Documents.Upload(r.Context(), u.ID, profile.KindCV, name, data)
	}
	if err == nil {
		s.setupImport(w, r, u, doc.ID.String())
		return
	}
	var key string
	switch {
	case errors.Is(err, profile.ErrFileTooLarge):
		key = "documents.error.size"
	case errors.Is(err, profile.ErrTooManyFiles):
		key = "documents.error.count"
	case errors.Is(err, profile.ErrFileType):
		key = "documents.error.type"
	default:
		s.failed(w, r, u, err)
		return
	}
	s.renderSetup(w, r, u, "cv", http.StatusUnprocessableEntity, views.Form{Notice: views.T(r.Context(), key), Failed: true})
}

func (s *Server) setupCVImport(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.setupImport(w, r, u, r.PathValue("id"))
}

func (s *Server) setupImport(w http.ResponseWriter, r *http.Request, u *identity.User, rawID string) {
	r.SetPathValue("id", rawID)
	id, ok := docID(r)
	if !ok || s.Importer == nil {
		http.Redirect(w, r, "/setup/profile", http.StatusSeeOther)
		return
	}
	_, err := s.Importer.Import(r.Context(), u.ID, id)
	switch {
	case err == nil:
		http.Redirect(w, r, "/setup/profile?imported=1", http.StatusSeeOther)
	case errors.Is(err, profile.ErrNotFound):
		s.notFound(w, r, u)
	case errors.Is(err, cvimport.ErrNoText):
		s.renderSetup(w, r, u, "cv", http.StatusUnprocessableEntity, views.Form{Notice: views.T(r.Context(), "documents.import_no_text"), Failed: true})
	case errors.Is(err, profile.ErrFileType), errors.Is(err, profile.ErrFileTooLarge):
		s.renderSetup(w, r, u, "cv", http.StatusUnprocessableEntity, views.Form{Notice: views.T(r.Context(), "documents.import_unreadable"), Failed: true})
	default:
		s.failed(w, r, u, err)
	}
}
