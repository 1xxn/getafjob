package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/ai"
	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/portals"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/tracking"
	"github.com/1xxn/getafjob/internal/web/views"
)

func joinLines(vs []string) string { return strings.Join(vs, "\n") }

func profileForm(snap profile.Snapshot) views.Form {
	p := snap.Profile
	langs := make([]string, len(p.Languages))
	for i, l := range p.Languages {
		langs[i] = strings.TrimSpace(l.Name + ": " + l.Level)
		langs[i] = strings.TrimSuffix(langs[i], ":")
	}
	pe, w := p.Personal, p.Work
	return views.Form{
		Version: strconv.FormatInt(snap.Version, 10),
		Values: map[string]string{
			"first_name": pe.FirstName, "last_name": pe.LastName, "email": pe.Email, "phone": pe.Phone,
			"street": pe.Street, "postal_code": pe.PostalCode, "city": pe.City, "country": pe.Country,
			"linkedin": pe.LinkedIn, "github": pe.GitHub, "website": pe.Website, "pronouns": pe.Pronouns,
			"current_title": w.CurrentTitle, "current_employer": w.CurrentEmployer, "years_total": w.YearsTotal,
			"years_relevant": w.YearsRelevant, "notice_period": w.NoticePeriod, "earliest_start": w.EarliestStart,
			"salary_expectation": w.SalaryExpectation, "workplace_preferred": w.WorkplacePreferred,
			"drivers_license": w.DriversLicense, "summary": p.Summary, "skills": joinLines(p.Skills),
			"languages": joinLines(langs), "certifications": joinLines(p.Certifications),
		},
		Checks: map[string]bool{
			"work_authorized_eu": w.WorkAuthorizedEU, "needs_sponsorship": w.NeedsSponsorship,
			"willing_to_relocate": w.WillingToRelocate,
		},
	}
}

// readProfile applies the form to p; experience and education are not on this form and stay as they are.
func readProfile(r *http.Request, p profile.Profile) profile.Profile {
	v := func(name string) string { return strings.TrimSpace(r.PostFormValue(name)) }
	on := func(name string) bool { return r.PostFormValue(name) == "on" }
	p.Personal = profile.Personal{FirstName: v("first_name"), LastName: v("last_name"), Email: v("email"), Phone: v("phone"),
		Street: v("street"), PostalCode: v("postal_code"), City: v("city"), Country: v("country"), LinkedIn: v("linkedin"),
		GitHub: v("github"), Website: v("website"), Pronouns: v("pronouns")}
	p.Work = profile.Work{CurrentTitle: v("current_title"), CurrentEmployer: v("current_employer"), YearsTotal: v("years_total"),
		YearsRelevant: v("years_relevant"), NoticePeriod: v("notice_period"), EarliestStart: v("earliest_start"),
		SalaryExpectation: v("salary_expectation"), WorkplacePreferred: v("workplace_preferred"),
		DriversLicense: v("drivers_license"), WorkAuthorizedEU: on("work_authorized_eu"),
		NeedsSponsorship: on("needs_sponsorship"), WillingToRelocate: on("willing_to_relocate")}
	p.Summary = strings.TrimSpace(r.PostFormValue("summary"))
	p.Skills = profile.Lines(r.PostFormValue("skills"))
	p.Certifications = profile.Lines(r.PostFormValue("certifications"))
	p.Languages = nil
	for _, l := range strings.Split(r.PostFormValue("languages"), "\n") {
		name, level, _ := strings.Cut(l, ":")
		if name = strings.TrimSpace(name); name != "" {
			p.Languages = append(p.Languages, profile.Language{Name: name, Level: strings.TrimSpace(level)})
		}
	}
	return p
}

// fieldErrors translates validation errors for the form.
func fieldErrors(r *http.Request, err error) map[string]string {
	var ve *profile.ValidationError
	if !errors.As(err, &ve) {
		return nil
	}
	out := map[string]string{}
	for _, f := range ve.Fields {
		if _, ok := out[f.Field]; !ok {
			out[f.Field] = views.T(r.Context(), f.Code)
		}
	}
	return out
}

func (s *Server) stationRows(ctx context.Context, userID uuid.UUID) ([]views.ProfileExperienceRow, []views.ProfileEducationRow, error) {
	if s.Stations == nil {
		return nil, nil, nil
	}
	works, err := s.Stations.WorkExperiences(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	edus, err := s.Stations.Educations(ctx, userID)
	if err != nil {
		return nil, nil, err
	}
	expRows := make([]views.ProfileExperienceRow, len(works))
	for i, w := range works {
		expRows[i] = views.ProfileExperienceRow{
			ID:                 w.ID.String(),
			Company:            w.Company,
			Title:              w.Title,
			Location:           w.Location,
			WorkplaceType:      w.WorkplaceType,
			StartDate:          w.StartDate,
			EndDate:            w.EndDate,
			IsCurrent:          w.IsCurrent,
			DescriptionBullets: strings.Join(w.DescriptionBullets, "\n"),
			SortOrder:          w.SortOrder,
		}
	}
	eduRows := make([]views.ProfileEducationRow, len(edus))
	for i, e := range edus {
		eduRows[i] = views.ProfileEducationRow{
			ID:           e.ID.String(),
			Institution:  e.Institution,
			Degree:       e.Degree,
			FieldOfStudy: e.FieldOfStudy,
			StartDate:    e.StartDate,
			EndDate:      e.EndDate,
			IsCurrent:    e.IsCurrent,
			Grade:        e.Grade,
			SortOrder:    e.SortOrder,
		}
	}
	return expRows, eduRows, nil
}

func (s *Server) profilePage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	snap, err := s.Profile.Get(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	f := profileForm(snap)
	switch {
	case r.URL.Query().Get("saved") == "1":
		f.Notice = views.T(r.Context(), "profile.saved")
	case r.URL.Query().Get("imported") == "1":
		f.Notice = views.T(r.Context(), "profile.imported")
	}
	expRows, eduRows, err := s.stationRows(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	s.render(w, r, http.StatusOK, views.ProfilePage(u.DisplayName, f, expRows, eduRows))
}

func (s *Server) profileSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	snap, err := s.Profile.Get(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	version, _ := strconv.ParseInt(r.PostFormValue("version"), 10, 64)
	p := readProfile(r, snap.Profile)
	err = s.Profile.SaveProfile(r.Context(), u.ID, p, version)
	expRows, eduRows, _ := s.stationRows(r.Context(), u.ID)
	switch {
	case err == nil:
		http.Redirect(w, r, "/profile?saved=1", http.StatusSeeOther)
	case errors.Is(err, profile.ErrInvalid):
		f := profileForm(profile.Snapshot{Profile: p, Version: version})
		f.Errors, f.Notice, f.Failed = fieldErrors(r, err), views.T(r.Context(), "profile.invalid"), true
		s.render(w, r, http.StatusUnprocessableEntity, views.ProfilePage(u.DisplayName, f, expRows, eduRows))
	case errors.Is(err, profile.ErrConflict):
		f := profileForm(snap)
		f.Notice, f.Failed = views.T(r.Context(), "profile.conflict"), true
		s.render(w, r, http.StatusConflict, views.ProfilePage(u.DisplayName, f, expRows, eduRows))
	default:
		s.failed(w, r, u, err)
	}
}

func (s *Server) experienceAdd(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Stations == nil {
		s.notFound(w, r, u)
		return
	}
	we := profile.WorkExperience{
		Title:              strings.TrimSpace(r.PostFormValue("title")),
		Company:            strings.TrimSpace(r.PostFormValue("company")),
		Location:           strings.TrimSpace(r.PostFormValue("location")),
		WorkplaceType:      strings.TrimSpace(r.PostFormValue("workplace_type")),
		StartDate:          strings.TrimSpace(r.PostFormValue("start_date")),
		EndDate:            strings.TrimSpace(r.PostFormValue("end_date")),
		IsCurrent:          r.PostFormValue("is_current") == "on",
		DescriptionBullets: profile.Lines(r.PostFormValue("bullets")),
	}
	_, err := s.Stations.SaveWorkExperience(r.Context(), u.ID, we)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/profile?saved=1#stations", http.StatusSeeOther)
}

func (s *Server) experienceSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Stations == nil {
		s.notFound(w, r, u)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	we := profile.WorkExperience{
		ID:                 id,
		Title:              strings.TrimSpace(r.PostFormValue("title")),
		Company:            strings.TrimSpace(r.PostFormValue("company")),
		Location:           strings.TrimSpace(r.PostFormValue("location")),
		WorkplaceType:      strings.TrimSpace(r.PostFormValue("workplace_type")),
		StartDate:          strings.TrimSpace(r.PostFormValue("start_date")),
		EndDate:            strings.TrimSpace(r.PostFormValue("end_date")),
		IsCurrent:          r.PostFormValue("is_current") == "on",
		DescriptionBullets: profile.Lines(r.PostFormValue("bullets")),
	}
	_, err = s.Stations.SaveWorkExperience(r.Context(), u.ID, we)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/profile?saved=1#stations", http.StatusSeeOther)
}

func (s *Server) experienceMove(direction string) func(http.ResponseWriter, *http.Request, *identity.User) {
	return func(w http.ResponseWriter, r *http.Request, u *identity.User) {
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if s.Stations == nil {
			s.notFound(w, r, u)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			s.notFound(w, r, u)
			return
		}
		if err := s.Stations.MoveWorkExperience(r.Context(), u.ID, id, direction); err != nil {
			s.failed(w, r, u, err)
			return
		}
		http.Redirect(w, r, "/profile#stations", http.StatusSeeOther)
	}
}

func (s *Server) experienceDelete(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Stations == nil {
		s.notFound(w, r, u)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	if err := s.Stations.DeleteWorkExperience(r.Context(), u.ID, id); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/profile#stations", http.StatusSeeOther)
}

func (s *Server) educationAdd(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Stations == nil {
		s.notFound(w, r, u)
		return
	}
	e := profile.EducationStation{
		Degree:       strings.TrimSpace(r.PostFormValue("degree")),
		Institution:  strings.TrimSpace(r.PostFormValue("institution")),
		FieldOfStudy: strings.TrimSpace(r.PostFormValue("field_of_study")),
		StartDate:    strings.TrimSpace(r.PostFormValue("start_date")),
		EndDate:      strings.TrimSpace(r.PostFormValue("end_date")),
		IsCurrent:    r.PostFormValue("is_current") == "on",
		Grade:        strings.TrimSpace(r.PostFormValue("grade")),
	}
	_, err := s.Stations.SaveEducation(r.Context(), u.ID, e)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/profile?saved=1#stations", http.StatusSeeOther)
}

func (s *Server) educationSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Stations == nil {
		s.notFound(w, r, u)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	e := profile.EducationStation{
		ID:           id,
		Degree:       strings.TrimSpace(r.PostFormValue("degree")),
		Institution:  strings.TrimSpace(r.PostFormValue("institution")),
		FieldOfStudy: strings.TrimSpace(r.PostFormValue("field_of_study")),
		StartDate:    strings.TrimSpace(r.PostFormValue("start_date")),
		EndDate:      strings.TrimSpace(r.PostFormValue("end_date")),
		IsCurrent:    r.PostFormValue("is_current") == "on",
		Grade:        strings.TrimSpace(r.PostFormValue("grade")),
	}
	_, err = s.Stations.SaveEducation(r.Context(), u.ID, e)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/profile?saved=1#stations", http.StatusSeeOther)
}

func (s *Server) educationMove(direction string) func(http.ResponseWriter, *http.Request, *identity.User) {
	return func(w http.ResponseWriter, r *http.Request, u *identity.User) {
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if s.Stations == nil {
			s.notFound(w, r, u)
			return
		}
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			s.notFound(w, r, u)
			return
		}
		if err := s.Stations.MoveEducation(r.Context(), u.ID, id, direction); err != nil {
			s.failed(w, r, u, err)
			return
		}
		http.Redirect(w, r, "/profile#stations", http.StatusSeeOther)
	}
}

func (s *Server) educationDelete(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Stations == nil {
		s.notFound(w, r, u)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	if err := s.Stations.DeleteEducation(r.Context(), u.ID, id); err != nil {
		s.failed(w, r, u, err)
		return
	}
	http.Redirect(w, r, "/profile#stations", http.StatusSeeOther)
}

func preferencesForm(p profile.Preferences, a application.Settings, mb tracking.MailboxAccount, np notify.Preferences, accounts []portals.AccountView, easyApplyAdmin bool, aiSettings ai.UserSettings, aiAdmin bool) views.Form {
	salary := ""
	if p.MinSalary > 0 {
		salary = strconv.Itoa(p.MinSalary)
	}
	level := string(a.Level)
	if level == "" {
		level = string(application.LevelApprove)
	}
	runnerChoice := string(a.RunnerChoice)
	if runnerChoice == "" {
		runnerChoice = string(application.RunnerServer)
	}
	portStr := ""
	if mb.Port > 0 {
		portStr = strconv.Itoa(mb.Port)
	}
	summaryTime := np.DailySummaryTime
	if summaryTime == "" {
		summaryTime = "18:00"
	}
	quietStart := np.QuietHoursStart
	if quietStart == "" {
		quietStart = "22:00"
	}
	quietEnd := np.QuietHoursEnd
	if quietEnd == "" {
		quietEnd = "08:00"
	}
	ntfyURL := np.NtfyURL
	if ntfyURL == "" {
		ntfyURL = "https://ntfy.sh"
	}
	placeholder := "sk-ant-api03-..."
	if aiSettings.HasCustomKey {
		placeholder = "••••••••••••••••"
	}
	vals := map[string]string{
		"titles": joinLines(p.Titles), "exclude_titles": joinLines(p.ExcludeTitles), "locations": joinLines(p.Locations),
		"remote_regions": joinLines(p.RemoteRegions), "min_salary": salary, "cover_letter_lang": p.CoverLetterLang,
		"diversity":                 p.Diversity,
		"autonomy_level":            level,
		"runner_choice":             runnerChoice,
		"min_score":                 strconv.Itoa(a.MinScore),
		"daily_limit":               strconv.Itoa(a.DailyLimit),
		"easy_apply_admin_enabled":  strconv.FormatBool(easyApplyAdmin),
		"portal_status_linkedin":    "disconnected",
		"portal_status_indeed":      "disconnected",
		"portal_limit_linkedin":     "5",
		"portal_limit_indeed":       "5",
		"ai_admin_enabled":          strconv.FormatBool(aiAdmin),
		"ai_spent":                  fmt.Sprintf("%.2f", float64(aiSettings.SpentCents)/100.0),
		"ai_budget":                 fmt.Sprintf("%.2f", float64(aiSettings.BudgetCents)/100.0),
		"ai_key_placeholder":        placeholder,
		"mailbox_host":              mb.Host,
		"mailbox_port":              portStr,
		"mailbox_username":          mb.Username,
		"notify_email_address":      np.EmailAddress,
		"notify_ntfy_url":           ntfyURL,
		"notify_ntfy_topic":         np.NtfyTopic,
		"notify_ntfy_token":         np.NtfyToken,
		"notify_daily_summary_time": summaryTime,
		"notify_quiet_hours_start":  quietStart,
		"notify_quiet_hours_end":    quietEnd,
	}
	for _, acc := range accounts {
		vals["portal_status_"+acc.Portal] = acc.Status
		if acc.DailyLimit > 0 {
			vals["portal_limit_"+acc.Portal] = strconv.Itoa(acc.DailyLimit)
		}
		vals["portal_submits_"+acc.Portal] = strconv.Itoa(acc.SubmitsToday)
	}
	return views.Form{
		Values: vals,
		Checks: map[string]bool{
			"remote":                       p.Remote,
			"talent_pool":                  p.TalentPool,
			"easy_apply_enabled":           a.EasyApplyEnabled,
			"easy_apply_terms_accepted":    a.EasyApplyTermsAcceptedAt != nil,
			"ai_enabled":                   aiSettings.Enabled,
			"ai_has_custom_key":            aiSettings.HasCustomKey,
			"mailbox_enabled":              mb.Enabled,
			"notify_email_enabled":         np.EmailEnabled,
			"notify_webpush_enabled":       np.WebPushEnabled,
			"notify_ntfy_enabled":          np.NtfyEnabled,
			"notify_daily_summary_enabled": np.DailySummaryEnabled,
			"notify_quiet_hours_enabled":   np.QuietHoursEnabled,
		},
	}
}

func (s *Server) preferencesPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	snap, err := s.Profile.Get(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	auto := application.DefaultSettings
	if s.Apps != nil {
		if st, err := s.Apps.Settings(r.Context(), u.ID); err == nil {
			auto = st
		}
	}
	var mb tracking.MailboxAccount
	if s.Tracking != nil {
		if m, err := s.Tracking.GetMailbox(r.Context(), u.ID); err == nil {
			mb = m
		}
	}
	np := notify.DefaultPreferences(u.ID, u.Email)
	if s.Notify != nil {
		if p, err := s.Notify.GetPreferences(r.Context(), u.ID); err == nil {
			np = p
		}
	}
	var portalAccounts []portals.AccountView
	easyApplyAdmin := false
	if s.Portals != nil {
		easyApplyAdmin = s.Portals.AdminEnabled()
		if accs, err := s.Portals.ListAccounts(r.Context(), u.ID); err == nil {
			portalAccounts = accs
		}
	}
	var aiSettings ai.UserSettings
	aiAdmin := false
	if s.AI != nil {
		aiAdmin = s.AI.AdminEnabled()
		if st, err := s.AI.GetSettings(r.Context(), u.ID); err == nil {
			aiSettings = st
		}
	}
	f := preferencesForm(snap.Preferences, auto, mb, np, portalAccounts, easyApplyAdmin, aiSettings, aiAdmin)
	if r.URL.Query().Get("saved") == "1" {
		f.Notice = views.T(r.Context(), "profile.saved")
	}
	if r.URL.Query().Get("tested") == "1" {
		f.Notice = views.T(r.Context(), "profile.notify.tested")
	}
	if code := r.URL.Query().Get("assist_code"); code != "" {
		f.Notice = views.T(r.Context(), "profile.assist.code", "code", code)
	}
	if tok := r.URL.Query().Get("runner_token"); tok != "" {
		f.Notice = views.T(r.Context(), "profile.home_runner.token_notice", "token", tok)
	}
	s.render(w, r, http.StatusOK, views.PreferencesPage(u.DisplayName, f))
}

func (s *Server) preferencesSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if disconnectPortal := strings.TrimSpace(r.PostFormValue("disconnect_portal")); disconnectPortal != "" {
		if s.Portals != nil {
			_ = s.Portals.Disconnect(r.Context(), u.ID, disconnectPortal)
		}
		http.Redirect(w, r, "/preferences?saved=1", http.StatusSeeOther)
		return
	}
	p := profile.Preferences{
		Titles: profile.Lines(r.PostFormValue("titles")), ExcludeTitles: profile.Lines(r.PostFormValue("exclude_titles")),
		Locations: profile.Lines(r.PostFormValue("locations")), RemoteRegions: profile.Lines(r.PostFormValue("remote_regions")),
		Remote: r.PostFormValue("remote") == "on", TalentPool: r.PostFormValue("talent_pool") == "on",
		CoverLetterLang: r.PostFormValue("cover_letter_lang"), Diversity: r.PostFormValue("diversity"),
	}
	salaryErr := false
	if v := strings.TrimSpace(r.PostFormValue("min_salary")); v != "" {
		n, err := strconv.Atoi(v)
		p.MinSalary, salaryErr = n, err != nil
	}
	autoLevel := application.LevelApprove
	var errLevel bool
	switch strings.TrimSpace(r.PostFormValue("autonomy_level")) {
	case "":
		autoLevel = application.LevelApprove
	case string(application.LevelSuggest):
		autoLevel = application.LevelSuggest
	case string(application.LevelApprove):
		autoLevel = application.LevelApprove
	case string(application.LevelAutonomous):
		autoLevel = application.LevelAutonomous
	default:
		errLevel = true
	}
	runnerChoice := application.RunnerServer
	var errRunnerChoice bool
	switch strings.TrimSpace(r.PostFormValue("runner_choice")) {
	case "":
		runnerChoice = application.RunnerServer
	case string(application.RunnerServer):
		runnerChoice = application.RunnerServer
	case string(application.RunnerHomeRunner):
		runnerChoice = application.RunnerHomeRunner
	case string(application.RunnerExtension):
		runnerChoice = application.RunnerExtension
	default:
		errRunnerChoice = true
	}
	minScore := application.DefaultSettings.MinScore
	var errScore error
	if v := strings.TrimSpace(r.PostFormValue("min_score")); v != "" {
		n, err := strconv.ParseInt(v, 10, 16)
		if err != nil || n < 0 || n > 100 {
			errScore = errors.New("invalid min_score")
		} else {
			minScore = int(n)
		}
	}
	dailyLimit := application.DefaultSettings.DailyLimit
	var errLimit error
	if v := strings.TrimSpace(r.PostFormValue("daily_limit")); v != "" {
		n, err := strconv.ParseInt(v, 10, 16)
		if err != nil || n < 0 || n > 50 {
			errLimit = errors.New("invalid daily_limit")
		} else {
			dailyLimit = int(n)
		}
	}
	var autoTermsAt *time.Time
	if s.Apps != nil {
		if st, err := s.Apps.Settings(r.Context(), u.ID); err == nil {
			autoTermsAt = st.EasyApplyTermsAcceptedAt
		}
	}
	easyApplyEnabled := r.PostFormValue("easy_apply_enabled") == "on"
	termsAccepted := r.PostFormValue("easy_apply_terms_accepted") == "on"
	var termsAcceptedAt *time.Time
	if autoTermsAt != nil {
		termsAcceptedAt = autoTermsAt
	}
	if termsAccepted && termsAcceptedAt == nil {
		now := time.Now()
		termsAcceptedAt = &now
	}
	var errEasyApplyTerms bool
	if easyApplyEnabled && termsAcceptedAt == nil {
		errEasyApplyTerms = true
	}
	autoSettings := application.Settings{
		Level:                    autoLevel,
		RunnerChoice:             runnerChoice,
		MinScore:                 minScore,
		DailyLimit:               dailyLimit,
		EasyApplyEnabled:         easyApplyEnabled,
		EasyApplyTermsAcceptedAt: termsAcceptedAt,
	}
	autoErr := errLevel || errRunnerChoice || errScore != nil || errLimit != nil || errEasyApplyTerms || autoSettings.Validate() != nil

	np := notify.DefaultPreferences(u.ID, u.Email)
	if s.Notify != nil {
		if cur, err := s.Notify.GetPreferences(r.Context(), u.ID); err == nil {
			np = cur
		}
	}
	np.EmailEnabled = r.PostFormValue("notify_email_enabled") == "on"
	np.EmailAddress = strings.TrimSpace(r.PostFormValue("notify_email_address"))
	np.WebPushEnabled = r.PostFormValue("notify_webpush_enabled") == "on"
	np.NtfyEnabled = r.PostFormValue("notify_ntfy_enabled") == "on"
	if urlStr := strings.TrimSpace(r.PostFormValue("notify_ntfy_url")); urlStr != "" {
		np.NtfyURL = urlStr
	}
	np.NtfyTopic = strings.TrimSpace(r.PostFormValue("notify_ntfy_topic"))
	if tok := strings.TrimSpace(r.PostFormValue("notify_ntfy_token")); tok != "" {
		np.NtfyToken = tok
	}
	np.DailySummaryEnabled = r.PostFormValue("notify_daily_summary_enabled") == "on"
	if sumTime := strings.TrimSpace(r.PostFormValue("notify_daily_summary_time")); sumTime != "" {
		np.DailySummaryTime = sumTime
	}
	np.QuietHoursEnabled = r.PostFormValue("notify_quiet_hours_enabled") == "on"
	if qStart := strings.TrimSpace(r.PostFormValue("notify_quiet_hours_start")); qStart != "" {
		np.QuietHoursStart = qStart
	}
	if qEnd := strings.TrimSpace(r.PostFormValue("notify_quiet_hours_end")); qEnd != "" {
		np.QuietHoursEnd = qEnd
	}

	if salaryErr || autoErr {
		var mb tracking.MailboxAccount
		if s.Tracking != nil {
			if m, err := s.Tracking.GetMailbox(r.Context(), u.ID); err == nil {
				mb = m
			}
		}
		var portalAccounts []portals.AccountView
		easyApplyAdmin := false
		if s.Portals != nil {
			easyApplyAdmin = s.Portals.AdminEnabled()
			if accs, err := s.Portals.ListAccounts(r.Context(), u.ID); err == nil {
				portalAccounts = accs
			}
		}
		var aiSettings ai.UserSettings
		aiAdmin := false
		if s.AI != nil {
			aiAdmin = s.AI.AdminEnabled()
			if st, err := s.AI.GetSettings(r.Context(), u.ID); err == nil {
				aiSettings = st
			}
		}
		f := preferencesForm(p, autoSettings, mb, np, portalAccounts, easyApplyAdmin, aiSettings, aiAdmin)
		f.Values["min_salary"] = r.PostFormValue("min_salary")
		f.Values["min_score"] = r.PostFormValue("min_score")
		f.Values["daily_limit"] = r.PostFormValue("daily_limit")
		f.Values["runner_choice"] = r.PostFormValue("runner_choice")
		f.Checks["easy_apply_enabled"] = easyApplyEnabled
		f.Checks["easy_apply_terms_accepted"] = termsAccepted
		f.Checks["ai_enabled"] = r.PostFormValue("ai_enabled") == "on"
		f.Errors = make(map[string]string)
		if salaryErr {
			f.Errors["min_salary"] = views.T(r.Context(), "profile.error.invalid")
		}
		if autoErr {
			if errLevel {
				f.Errors["autonomy_level"] = views.T(r.Context(), "profile.error.invalid")
			}
			if errRunnerChoice {
				f.Errors["runner_choice"] = views.T(r.Context(), "profile.error.invalid")
			}
			if errScore != nil || minScore < 0 || minScore > 100 {
				f.Errors["min_score"] = views.T(r.Context(), "profile.error.invalid")
			}
			if errLimit != nil || dailyLimit < 0 || dailyLimit > application.MaxDailyLimit {
				f.Errors["daily_limit"] = views.T(r.Context(), "profile.error.invalid")
			}
			if errEasyApplyTerms {
				f.Errors["easy_apply_terms_accepted"] = views.T(r.Context(), "profile.error.invalid")
			}
		}
		f.Notice, f.Failed = views.T(r.Context(), "profile.invalid"), true
		s.render(w, r, http.StatusUnprocessableEntity, views.PreferencesPage(u.DisplayName, f))
		return
	}

	if err := s.Profile.SavePreferences(r.Context(), u.ID, p); err != nil {
		if errors.Is(err, profile.ErrInvalid) {
			var mb tracking.MailboxAccount
			if s.Tracking != nil {
				if m, err := s.Tracking.GetMailbox(r.Context(), u.ID); err == nil {
					mb = m
				}
			}
			var portalAccounts []portals.AccountView
			easyApplyAdmin := false
			if s.Portals != nil {
				easyApplyAdmin = s.Portals.AdminEnabled()
				if accs, err := s.Portals.ListAccounts(r.Context(), u.ID); err == nil {
					portalAccounts = accs
				}
			}
			var aiSettings ai.UserSettings
			aiAdmin := false
			if s.AI != nil {
				aiAdmin = s.AI.AdminEnabled()
				if st, err := s.AI.GetSettings(r.Context(), u.ID); err == nil {
					aiSettings = st
				}
			}
			f := preferencesForm(p, autoSettings, mb, np, portalAccounts, easyApplyAdmin, aiSettings, aiAdmin)
			f.Errors, f.Notice, f.Failed = fieldErrors(r, err), views.T(r.Context(), "profile.invalid"), true
			s.render(w, r, http.StatusUnprocessableEntity, views.PreferencesPage(u.DisplayName, f))
			return
		}
		s.failed(w, r, u, err)
		return
	}
	if s.Apps != nil {
		if err := s.Apps.SaveSettings(r.Context(), u.ID, autoSettings); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}
	if s.Portals != nil {
		for _, portal := range []string{portals.PortalLinkedIn, portals.PortalIndeed} {
			if v := strings.TrimSpace(r.PostFormValue("portal_limit_" + portal)); v != "" {
				if lim, err := strconv.Atoi(v); err == nil && lim >= portals.MinDailyLimit && lim <= portals.MaxDailyLimit {
					_ = s.Portals.SetDailyLimit(r.Context(), u.ID, portal, lim)
				}
			}
		}
	}
	if s.Tracking != nil {
		mbHost := strings.TrimSpace(r.PostFormValue("mailbox_host"))
		mbUser := strings.TrimSpace(r.PostFormValue("mailbox_username"))
		mbPass := r.PostFormValue("mailbox_password")
		mbPort := 993
		if v := strings.TrimSpace(r.PostFormValue("mailbox_port")); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 65535 {
				mbPort = n
			}
		}
		mbEnabled := r.PostFormValue("mailbox_enabled") == "on"
		if mbHost != "" && mbUser != "" {
			if err := s.Tracking.SaveMailbox(r.Context(), u.ID, mbHost, mbPort, true, mbUser, mbPass, mbEnabled); err != nil {
				s.failed(w, r, u, err)
				return
			}
		}
	}
	if s.Notify != nil {
		if err := s.Notify.SavePreferences(r.Context(), np); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}
	if s.AI != nil {
		aiEnabled := r.PostFormValue("ai_enabled") == "on"
		aiCustomKey := strings.TrimSpace(r.PostFormValue("ai_custom_api_key"))
		aiClearKey := r.PostFormValue("ai_clear_custom_key") == "on"
		_ = s.AI.SaveSettings(r.Context(), u.ID, aiEnabled, aiCustomKey, aiClearKey)
	}
	http.Redirect(w, r, "/preferences?saved=1", http.StatusSeeOther)
}

func (s *Server) notifyTest(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Notify != nil {
		if err := s.Notify.SendTestNotification(r.Context(), u.ID); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}
	http.Redirect(w, r, "/preferences?tested=1", http.StatusSeeOther)
}

func (s *Server) assistPair(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Assist != nil {
		code, err := s.Assist.GeneratePairingCode(r.Context(), u.ID)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		http.Redirect(w, r, "/preferences?assist_code="+url.QueryEscape(code), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/preferences", http.StatusSeeOther)
}

func (s *Server) runnerToken(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Apps != nil {
		tok, err := s.Apps.GenerateHomeRunnerToken(r.Context(), u.ID)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		http.Redirect(w, r, "/preferences?runner_token="+url.QueryEscape(tok), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/preferences", http.StatusSeeOther)
}
