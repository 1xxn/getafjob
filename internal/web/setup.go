package web

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/cvimport"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/web/views"
)

// maxHistory bounds the entries the history step accepts.
const maxHistory = 30

func validStep(step string) bool { return slices.Contains(views.SetupSteps, step) }

func (s *Server) setupStart(w http.ResponseWriter, r *http.Request, _ *identity.User) {
	http.Redirect(w, r, "/setup/cv", http.StatusSeeOther)
}

// setupPage shows one step with the stored data.
func (s *Server) setupPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	step := r.PathValue("step")
	if !validStep(step) {
		s.notFound(w, r, u)
		return
	}
	s.renderSetup(w, r, u, step, http.StatusOK, views.Form{})
}

func (s *Server) renderSetup(w http.ResponseWriter, r *http.Request, u *identity.User, step string, status int, override views.Form) {
	snap, err := s.Profile.Get(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d := views.SetupData{Name: u.DisplayName, Step: step}
	switch step {
	case "search":
		st := application.DefaultSettings
		if s.Apps != nil {
			if cfg, err := s.Apps.Settings(r.Context(), u.ID); err == nil {
				st = cfg
			}
		}
		d.Form = preferencesForm(snap.Preferences, st, tracking.MailboxAccount{}, notify.Preferences{}, nil, false)
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
	case "history":
		for _, e := range snap.Profile.Experience {
			d.Experience = append(d.Experience, views.ExperienceRow{Title: e.Title, Company: e.Company, Start: e.Start, End: e.End,
				Location: e.Location, Highlights: strings.Join(e.Highlights, "\n")})
		}
		for _, e := range snap.Profile.Education {
			d.Education = append(d.Education, views.EducationRow{Degree: e.Degree, Institution: e.Institution, Start: e.Start,
				End: e.End, Grade: e.Grade})
		}
		d.Experience = append(d.Experience, views.ExperienceRow{}) // room for one more
		d.Education = append(d.Education, views.EducationRow{})
	case "done":
		d.Missing = snap.Profile.Missing()
		if s.Identity.PasskeysEnabled() {
			keys, err := s.Identity.Passkeys(r.Context(), u.ID)
			if err != nil {
				s.failed(w, r, u, err)
				return
			}
			d.Passkeys = len(keys) > 0
		} else {
			d.Passkeys = true // nothing to nudge about
		}
	}
	s.render(w, r, status, views.SetupPage(d))
}

// setupSave stores one step and moves on; errors show the step again with the typed values.
func (s *Server) setupSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	step := r.PathValue("step")
	if !validStep(step) || step == "cv" || step == "done" {
		s.notFound(w, r, u)
		return
	}
	next := "/setup/" + views.SetupSteps[slices.Index(views.SetupSteps, step)+1]
	if step == "search" {
		p := profile.Preferences{Titles: profile.Lines(r.PostFormValue("titles")), Locations: profile.Lines(r.PostFormValue("locations")),
			Remote: r.PostFormValue("remote") == "on"}
		snap, err := s.Profile.Get(r.Context(), u.ID)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		keep := snap.Preferences
		keep.Titles, keep.Locations, keep.Remote = p.Titles, p.Locations, p.Remote
		var serr error
		if v := strings.TrimSpace(r.PostFormValue("min_salary")); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				serr = &profile.ValidationError{Fields: []profile.FieldError{{Field: "min_salary", Code: "profile.error.invalid"}}}
			}
			keep.MinSalary = n
		} else {
			keep.MinSalary = 0
		}
		if serr == nil {
			serr = s.Profile.SavePreferences(r.Context(), u.ID, keep)
		}
		st := application.DefaultSettings
		if s.Apps != nil {
			if cfg, err := s.Apps.Settings(r.Context(), u.ID); err == nil {
				st = cfg
			}
		}
		s.setupSaved(w, r, u, step, next, serr, preferencesForm(keep, st, tracking.MailboxAccount{}, notify.Preferences{}, nil, false))
		return
	}
	snap, err := s.Profile.Get(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	version, _ := strconv.ParseInt(r.PostFormValue("version"), 10, 64)
	p := snap.Profile
	v := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	switch step {
	case "contact":
		pe := &p.Personal
		pe.FirstName, pe.LastName, pe.Email, pe.Phone = v("first_name"), v("last_name"), v("email"), v("phone")
		pe.PostalCode, pe.City, pe.LinkedIn, pe.GitHub = v("postal_code"), v("city"), v("linkedin"), v("github")
	case "work":
		wk := &p.Work
		wk.CurrentTitle, wk.YearsTotal, wk.NoticePeriod = v("current_title"), v("years_total"), v("notice_period")
		wk.EarliestStart, wk.SalaryExpectation = v("earliest_start"), v("salary_expectation")
		wk.WorkAuthorizedEU, wk.NeedsSponsorship = r.PostFormValue("work_authorized_eu") == "on", r.PostFormValue("needs_sponsorship") == "on"
		p.Skills = profile.Lines(r.PostFormValue("skills"))
		p.Languages = nil
		for _, l := range strings.Split(r.PostFormValue("languages"), "\n") {
			if name, level, _ := strings.Cut(l, ":"); strings.TrimSpace(name) != "" {
				p.Languages = append(p.Languages, profile.Language{Name: strings.TrimSpace(name), Level: strings.TrimSpace(level)})
			}
		}
	case "history":
		p.Experience, p.Education = readHistory(r)
	}
	err = s.Profile.SaveProfile(r.Context(), u.ID, p, version)
	f := profileForm(profile.Snapshot{Profile: p, Version: version})
	s.setupSaved(w, r, u, step, next, err, f)
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

// readHistory reads the repeated fieldsets; empty and removed entries are dropped.
func readHistory(r *http.Request) ([]profile.Experience, []profile.Education) {
	count := func(name string) int {
		n, _ := strconv.Atoi(r.PostFormValue(name))
		return min(max(n, 0), maxHistory)
	}
	f := func(group string, i int, field string) string {
		return strings.TrimSpace(r.PostFormValue(group + "-" + strconv.Itoa(i) + "-" + field))
	}
	var exp []profile.Experience
	for i := range count("exp-count") {
		e := profile.Experience{Title: f("exp", i, "title"), Company: f("exp", i, "company"), Start: f("exp", i, "start"),
			End: f("exp", i, "end"), Location: f("exp", i, "location"), Highlights: lines(r.PostFormValue("exp-" + strconv.Itoa(i) + "-highlights"))}
		if f("exp", i, "remove") == "on" || (e.Title == "" && e.Company == "") {
			continue
		}
		exp = append(exp, e)
	}
	var edu []profile.Education
	for i := range count("edu-count") {
		e := profile.Education{Degree: f("edu", i, "degree"), Institution: f("edu", i, "institution"), Start: f("edu", i, "start"),
			End: f("edu", i, "end"), Grade: f("edu", i, "grade")}
		if f("edu", i, "remove") == "on" || (e.Degree == "" && e.Institution == "") {
			continue
		}
		edu = append(edu, e)
	}
	return exp, edu
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
		http.Redirect(w, r, "/setup/contact", http.StatusSeeOther)
		return
	}
	_, err := s.Importer.Import(r.Context(), u.ID, id)
	switch {
	case err == nil:
		http.Redirect(w, r, "/setup/contact?imported=1", http.StatusSeeOther)
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

// lines splits a textarea at line breaks only (highlights may contain commas).
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.Join(strings.Fields(l), " "); l != "" && len(out) < 20 {
			out = append(out, l)
		}
	}
	return out
}
