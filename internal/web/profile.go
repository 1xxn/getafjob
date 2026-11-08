package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/notify"
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
	s.render(w, r, http.StatusOK, views.ProfilePage(u.DisplayName, f))
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
	switch {
	case err == nil:
		http.Redirect(w, r, "/profile?saved=1", http.StatusSeeOther)
	case errors.Is(err, profile.ErrInvalid):
		f := profileForm(profile.Snapshot{Profile: p, Version: version})
		f.Errors, f.Notice, f.Failed = fieldErrors(r, err), views.T(r.Context(), "profile.invalid"), true
		s.render(w, r, http.StatusUnprocessableEntity, views.ProfilePage(u.DisplayName, f))
	case errors.Is(err, profile.ErrConflict):
		f := profileForm(snap)
		f.Notice, f.Failed = views.T(r.Context(), "profile.conflict"), true
		s.render(w, r, http.StatusConflict, views.ProfilePage(u.DisplayName, f))
	default:
		s.failed(w, r, u, err)
	}
}

func preferencesForm(p profile.Preferences, a application.Settings, mb tracking.MailboxAccount, np notify.Preferences) views.Form {
	salary := ""
	if p.MinSalary > 0 {
		salary = strconv.Itoa(p.MinSalary)
	}
	level := string(a.Level)
	if level == "" {
		level = string(application.LevelApprove)
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
	return views.Form{
		Values: map[string]string{
			"titles": joinLines(p.Titles), "exclude_titles": joinLines(p.ExcludeTitles), "locations": joinLines(p.Locations),
			"remote_regions": joinLines(p.RemoteRegions), "min_salary": salary, "cover_letter_lang": p.CoverLetterLang,
			"diversity":                 p.Diversity,
			"autonomy_level":            level,
			"min_score":                 strconv.Itoa(a.MinScore),
			"daily_limit":               strconv.Itoa(a.DailyLimit),
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
		},
		Checks: map[string]bool{
			"remote":                       p.Remote,
			"talent_pool":                  p.TalentPool,
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
	f := preferencesForm(snap.Preferences, auto, mb, np)
	if r.URL.Query().Get("saved") == "1" {
		f.Notice = views.T(r.Context(), "profile.saved")
	}
	if r.URL.Query().Get("tested") == "1" {
		f.Notice = views.T(r.Context(), "profile.notify.tested")
	}
	if code := r.URL.Query().Get("assist_code"); code != "" {
		f.Notice = views.T(r.Context(), "profile.assist.code", "code", code)
	}
	s.render(w, r, http.StatusOK, views.PreferencesPage(u.DisplayName, f))
}

func (s *Server) preferencesSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
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
	autoSettings := application.Settings{
		Level:      autoLevel,
		MinScore:   minScore,
		DailyLimit: dailyLimit,
	}
	autoErr := errLevel || errScore != nil || errLimit != nil || autoSettings.Validate() != nil

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
		f := preferencesForm(p, autoSettings, mb, np)
		f.Values["min_salary"] = r.PostFormValue("min_salary")
		f.Values["min_score"] = r.PostFormValue("min_score")
		f.Values["daily_limit"] = r.PostFormValue("daily_limit")
		f.Errors = make(map[string]string)
		if salaryErr {
			f.Errors["min_salary"] = views.T(r.Context(), "profile.error.invalid")
		}
		if autoErr {
			if errLevel {
				f.Errors["autonomy_level"] = views.T(r.Context(), "profile.error.invalid")
			}
			if errScore != nil || minScore < 0 || minScore > 100 {
				f.Errors["min_score"] = views.T(r.Context(), "profile.error.invalid")
			}
			if errLimit != nil || dailyLimit < 0 || dailyLimit > application.MaxDailyLimit {
				f.Errors["daily_limit"] = views.T(r.Context(), "profile.error.invalid")
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
			f := preferencesForm(p, autoSettings, mb, np)
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
