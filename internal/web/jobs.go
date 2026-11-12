package web

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/discovery/urlimport"
	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/platform/applymode"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/web/views"
)

func (s *Server) jobPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	j, err := s.Jobs.Detail(r.Context(), u.ID, id)
	if errors.Is(err, matching.ErrNotFound) {
		s.notFound(w, r, u)
		return
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	d := views.JobData{
		Name:        u.DisplayName,
		Row:         jobRow(r.Context(), j.Item, time.Now()),
		Description: j.Description,
		URL:         webLink(j.URL),
		ApplyURL:    webLink(j.ApplyURL),
		Open:        j.Open,
		Computed:    views.When(r.Context(), j.ComputedAt),
		ApplyMode:   string(applymode.Of(j.URL, j.ApplyURL)),
		ApplyHost:   applymode.Host(cmp.Or(j.ApplyURL, j.URL)),
		Back:        "/jobs",
	}
	for _, rs := range j.Reasons {
		d.Reasons = append(d.Reasons, views.Reason{Text: reasonText(r.Context(), rs), Points: rs.Points, Hard: rs.Hard})
	}
	var snap profile.Snapshot
	if s.Profile != nil {
		snap, _ = s.Profile.Get(r.Context(), u.ID)
	}
	c := criteriaFromSnapshot(snap)
	mj := matchingJob(j)
	inspection := matching.InspectFit(c, mj, time.Now())
	d.Fit = buildFitData(r.Context(), j.JobID, inspection, "")
	d.CV = s.buildCVVariantData(r.Context(), u.ID, j, "")
	if s.Apps != nil {
		if app, err := s.Apps.ByJob(r.Context(), u.ID, j.JobID); err == nil {
			d.ApplicationState = string(app.State)
			d.ApplicationID = app.ID.String()
			d.Row.State = stateKey(app.State, d.Row.Age)
		}
	}
	s.render(w, r, http.StatusOK, views.JobPage(d))
}

func (s *Server) addJobSkill(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	skill := strings.TrimSpace(r.PostFormValue("skill"))
	if skill == "" || len(skill) > 100 {
		http.Error(w, "invalid skill", http.StatusBadRequest)
		return
	}

	if s.Profile != nil {
		for attempts := 0; attempts < 3; attempts++ {
			snap, err := s.Profile.Get(r.Context(), u.ID)
			if err != nil {
				s.failed(w, r, u, err)
				return
			}
			exists := false
			for _, sk := range snap.Profile.Skills {
				if strings.EqualFold(sk, skill) {
					exists = true
					break
				}
			}
			if !exists {
				snap.Profile.Skills = append(snap.Profile.Skills, skill)
				err = s.Profile.SaveProfile(r.Context(), u.ID, snap.Profile, snap.Version)
				if errors.Is(err, profile.ErrConflict) {
					continue
				}
				if err != nil {
					s.failed(w, r, u, err)
					return
				}
			}
			break
		}
	}

	if partial(r) {
		j, err := s.Jobs.Detail(r.Context(), u.ID, id)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		var snap profile.Snapshot
		if s.Profile != nil {
			snap, _ = s.Profile.Get(r.Context(), u.ID)
		}
		c := criteriaFromSnapshot(snap)
		mj := matchingJob(j)
		inspection := matching.InspectFit(c, mj, time.Now())
		d := buildFitData(r.Context(), id, inspection, skill)
		s.render(w, r, http.StatusOK, views.FitInspector(d))
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d", id), http.StatusSeeOther)
}

func (s *Server) setJobCV(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cvIDStr := strings.TrimSpace(r.PostFormValue("cv_id"))
	var cvDocID *uuid.UUID
	if cvIDStr != "" {
		parsed, err := uuid.Parse(cvIDStr)
		if err != nil {
			http.Error(w, "invalid cv id", http.StatusBadRequest)
			return
		}
		cvDocID = &parsed
	}

	if s.Apps != nil {
		if _, err := s.Apps.SetJobCV(r.Context(), u.ID, id, cvDocID); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}

	if partial(r) {
		j, err := s.Jobs.Detail(r.Context(), u.ID, id)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		notice := views.T(r.Context(), "jobs.cv_switcher.saved")
		cvData := s.buildCVVariantData(r.Context(), u.ID, j, notice)
		s.render(w, r, http.StatusOK, views.CVVariantSwitcher(cvData))
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/jobs/%d", id), http.StatusSeeOther)
}

func (s *Server) jobApplyPreview(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		s.notFound(w, r, u)
		return
	}
	if s.Jobs == nil || s.Apps == nil {
		s.failed(w, r, u, errors.New("service unavailable"))
		return
	}
	j, err := s.Jobs.Detail(r.Context(), u.ID, id)
	if err != nil {
		s.notFound(w, r, u)
		return
	}

	d := views.ApplyPreviewData{
		Name:      u.DisplayName,
		JobID:     j.JobID,
		Title:     j.Title,
		Company:   j.Company,
		Location:  j.Location,
		Workplace: j.Workplace,
		Score:     j.Score,
	}

	if s.Profile != nil {
		if snap, err := s.Profile.Get(r.Context(), u.ID); err == nil {
			p := snap.Profile.Personal
			name := strings.TrimSpace(p.FirstName + " " + p.LastName)
			if name == "" {
				name = u.DisplayName
			}
			if name == "" {
				name = u.Email
			}
			d.CandidateName = name
			d.CandidateEmail = p.Email
			if d.CandidateEmail == "" {
				d.CandidateEmail = u.Email
			}
			d.CandidatePhone = p.Phone
			loc := strings.TrimSpace(p.City)
			if p.Country != "" {
				if loc != "" {
					loc += ", " + p.Country
				} else {
					loc = p.Country
				}
			}
			d.CandidateLocation = loc
			d.CandidateLinkedIn = p.LinkedIn
			d.CandidateGitHub = p.GitHub
			d.CandidatePortfolio = p.Website
		}
	}
	if d.CandidateName == "" {
		d.CandidateName = u.Email
	}
	if d.CandidateEmail == "" {
		d.CandidateEmail = u.Email
	}

	d.CV = s.buildCVVariantData(r.Context(), u.ID, j, "")
	for _, v := range d.CV.Variants {
		if v.Selected {
			d.SelectedCVName = v.Label
			if d.SelectedCVName == "" {
				d.SelectedCVName = v.Name
			}
			break
		}
	}

	if s.Stations != nil {
		if exps, err := s.Stations.WorkExperiences(r.Context(), u.ID); err == nil {
			for _, exp := range exps {
				dates := exp.StartDate
				if exp.IsCurrent {
					dates += " – " + views.T(r.Context(), "profile.station.present")
				} else if exp.EndDate != "" {
					dates += " – " + exp.EndDate
				}
				d.WorkExperiences = append(d.WorkExperiences, views.PreviewExperience{
					Title:       exp.Title,
					Company:     exp.Company,
					Location:    exp.Location,
					Dates:       dates,
					Current:     exp.IsCurrent,
					Description: strings.Join(exp.DescriptionBullets, "\n"),
				})
			}
		}
		if edus, err := s.Stations.Educations(r.Context(), u.ID); err == nil {
			for _, edu := range edus {
				dates := edu.StartDate
				if edu.EndDate != "" {
					dates += " – " + edu.EndDate
				}
				d.Educations = append(d.Educations, views.PreviewEducation{
					Institution: edu.Institution,
					Degree:      edu.Degree,
					Field:       edu.FieldOfStudy,
					Dates:       dates,
					Grade:       edu.Grade,
				})
			}
		}
	}

	if s.Items != nil {
		if ans, err := s.Items.Answers(r.Context(), u.ID); err == nil {
			for _, a := range ans {
				d.Answers = append(d.Answers, views.PreviewAnswer{
					Question: a.Question,
					Answer:   a.Answer,
				})
			}
		}
	}

	d.Guard = views.PreviewGuardStatus{Allowed: true}
	if err := s.Apps.CheckGuard(r.Context(), u.ID, id); err != nil {
		d.Guard.Allowed = false
		switch {
		case errors.Is(err, application.ErrDuplicate):
			d.Guard.Reason = views.T(r.Context(), "jobs.apply_preview.error_duplicate")
		case errors.Is(err, application.ErrCompanyWindow):
			d.Guard.Reason = views.T(r.Context(), "jobs.apply_preview.error_company_window")
		case errors.Is(err, application.ErrExcluded):
			d.Guard.Reason = views.T(r.Context(), "jobs.apply_preview.error_excluded")
		default:
			d.Guard.Reason = views.T(r.Context(), "jobs.apply_preview.error_other")
		}
	}

	if partial(r) {
		s.render(w, r, http.StatusOK, views.ApplyPreview(d))
		return
	}
	s.render(w, r, http.StatusOK, views.ApplyPreviewPage(d))
}

func (s *Server) jobApplySubmit(w http.ResponseWriter, r *http.Request, u *identity.User) {
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
	if s.Jobs != nil {
		j, err := s.Jobs.Detail(r.Context(), u.ID, id)
		if err != nil {
			s.notFound(w, r, u)
			return
		}
		if applymode.Of(j.URL, j.ApplyURL) == applymode.Email {
			http.Redirect(w, r, "/jobs/"+strconv.FormatInt(id, 10), http.StatusSeeOther) // applied by mail, never run
			return
		}
	}

	if err := s.Apps.CheckGuard(r.Context(), u.ID, id); err != nil {
		s.Logger.WarnContext(r.Context(), "instant apply refused by safety guard", "job_id", id, "err", err)
		s.flash(w, guardFlash(err))
		http.Redirect(w, r, fmt.Sprintf("/jobs/%d", id), http.StatusSeeOther)
		return
	}

	if cvStr := strings.TrimSpace(r.PostFormValue("cv_id")); cvStr != "" {
		if cvID, err := uuid.Parse(cvStr); err == nil {
			_, _ = s.Apps.SetJobCV(r.Context(), u.ID, id, &cvID)
		}
	}

	appID, _, err := s.Apps.Queue(r.Context(), u.ID, id)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}

	destURL := "/live?run=" + appID.String() // the run in focus: the user watches it start
	if partial(r) {
		w.Header().Set("HX-Location", destURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, destURL, http.StatusSeeOther) //nolint:gosec // G710: destURL is a fixed path with an internal UUID
}

func criteriaFromSnapshot(snap profile.Snapshot) matching.Criteria {
	pr, pf := snap.Preferences, snap.Profile
	c := matching.Criteria{
		Titles:        pr.Titles,
		ExcludeTitles: pr.ExcludeTitles,
		Locations:     pr.Locations,
		Remote:        pr.Remote,
		RemoteRegions: pr.RemoteRegions,
		MinSalary:     pr.MinSalary,
		Skills:        pf.Skills,
		YearsTotal:    pf.Work.YearsTotal,
	}
	for _, l := range pf.Languages {
		c.Languages = append(c.Languages, matching.Language{Name: l.Name, Level: l.Level})
	}
	return c
}

func matchingJob(j matching.Detail) matching.Job {
	return matching.Job{
		ID:          j.JobID,
		Company:     j.Company,
		Title:       j.Title,
		Location:    j.Location,
		Workplace:   j.Workplace,
		Description: j.Description,
		Posted:      j.Posted,
		FirstSeen:   j.FirstSeen,
	}
}

func buildFitData(ctx context.Context, jobID int64, fit matching.FitInspection, addedSkill string) views.FitData {
	d := views.FitData{
		JobID:          jobID,
		Score:          fit.Score,
		Decision:       string(fit.Decision),
		MatchingSkills: fit.MatchingSkills,
		MissingSkills:  fit.MissingSkills,
		AddedSkill:     addedSkill,
	}

	d.Breakdown = append(d.Breakdown, views.FitBreakdownItem{
		Dimension: "title",
		Label:     views.T(ctx, "jobs.fit.dim_title"),
		Points:    fit.TitlePoints,
		Max:       fit.MaxTitlePoints,
	})

	skillsDetail := views.T(ctx, "jobs.fit.dim_skills_detail", "matches", len(fit.MatchingSkills), "missing", len(fit.MissingSkills))
	d.Breakdown = append(d.Breakdown, views.FitBreakdownItem{
		Dimension: "skills",
		Label:     views.T(ctx, "jobs.fit.dim_skills"),
		Points:    fit.SkillPoints,
		Max:       fit.MaxSkillPoints,
		Detail:    skillsDetail,
	})

	candLvl := fit.CandidateLevel
	if candLvl == "" {
		candLvl = "–"
	}
	jobLvl := fit.JobLevel
	if jobLvl == "" {
		jobLvl = "–"
	}
	seniorityDetail := views.T(ctx, "jobs.fit.dim_seniority_detail", "candidate", candLvl, "job", jobLvl)
	d.Breakdown = append(d.Breakdown, views.FitBreakdownItem{
		Dimension: "seniority",
		Label:     views.T(ctx, "jobs.fit.dim_seniority"),
		Points:    fit.SeniorityPoints,
		Max:       fit.MaxSeniorityPoints,
		Detail:    seniorityDetail,
	})

	locDetail := ""
	if fit.LocationReason != "" {
		locDetail = views.T(ctx, "match.reason."+fit.LocationReason)
	}
	d.Breakdown = append(d.Breakdown, views.FitBreakdownItem{
		Dimension: "location",
		Label:     views.T(ctx, "jobs.fit.dim_location"),
		Points:    fit.LocationPoints,
		Max:       fit.MaxLocationPoints,
		Detail:    locDetail,
	})

	if fit.LanguagePoints != 0 {
		d.Breakdown = append(d.Breakdown, views.FitBreakdownItem{
			Dimension: "language",
			Label:     views.T(ctx, "jobs.fit.dim_language"),
			Points:    fit.LanguagePoints,
		})
	}

	if fit.SalaryPoints != 0 {
		d.Breakdown = append(d.Breakdown, views.FitBreakdownItem{
			Dimension: "salary",
			Label:     views.T(ctx, "jobs.fit.dim_salary"),
			Points:    fit.SalaryPoints,
		})
	}

	if fit.RecencyPoints != 0 {
		d.Breakdown = append(d.Breakdown, views.FitBreakdownItem{
			Dimension: "recency",
			Label:     views.T(ctx, "jobs.fit.dim_recency"),
			Points:    fit.RecencyPoints,
		})
	}

	return d
}

func (s *Server) buildCVVariantData(ctx context.Context, userID uuid.UUID, j matching.Detail, notice string) views.CVVariantData {
	data := views.CVVariantData{
		JobID:  j.JobID,
		Notice: notice,
	}
	if s.Documents == nil {
		return data
	}
	allDocs, err := s.Documents.List(ctx, userID)
	if err != nil {
		return data
	}
	var cvDocs []profile.Document
	for _, doc := range allDocs {
		if doc.Kind == profile.KindCV {
			cvDocs = append(cvDocs, doc)
		}
	}
	if len(cvDocs) == 0 {
		return data
	}

	recCV, hasRec := profile.ChooseCV(cvDocs, "", j.Title, j.Description)

	var selectedID *uuid.UUID
	if s.Apps != nil {
		if app, err := s.Apps.ByJob(ctx, userID, j.JobID); err == nil && app.CvDocumentID != nil {
			selectedID = app.CvDocumentID
		}
	}

	for _, doc := range cvDocs {
		isRec := hasRec && doc.ID == recCV.ID
		isSelected := (selectedID != nil && *selectedID == doc.ID) || (selectedID == nil && isRec)
		data.Variants = append(data.Variants, views.CVVariantItem{
			ID:          doc.ID.String(),
			Label:       doc.Label,
			Name:        doc.Name,
			Language:    doc.Language,
			Focus:       doc.Focus,
			Selected:    isSelected,
			Recommended: isRec,
		})
	}
	if selectedID != nil {
		data.SelectedID = selectedID.String()
	} else if hasRec {
		data.SelectedID = recCV.ID.String()
	}
	return data
}

func (s *Server) addBlocklist(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	kind := r.PostFormValue("kind")
	if kind != "company" && kind != "keyword" {
		kind = "company"
	}
	pattern := r.PostFormValue("pattern")
	if pattern != "" {
		_ = s.Jobs.AddBlocklist(r.Context(), u.ID, kind, pattern)
	}
	if partial(r) {
		s.jobsPage(w, r, u)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

func (s *Server) deleteBlocklist(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if idStr := r.PostFormValue("id"); idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil && id > 0 {
			_ = s.Jobs.RemoveBlocklist(r.Context(), u.ID, id)
		}
	} else if pattern := r.PostFormValue("pattern"); pattern != "" {
		kind := r.PostFormValue("kind")
		if kind != "company" && kind != "keyword" {
			kind = "company"
		}
		_ = s.Jobs.RemoveBlocklistByPattern(r.Context(), u.ID, kind, pattern)
	}
	if partial(r) {
		s.jobsPage(w, r, u)
		return
	}
	http.Redirect(w, r, "/jobs", http.StatusSeeOther)
}

func jobRow(ctx context.Context, it matching.Item, now time.Time) views.JobRow {
	at := it.Posted
	if at.IsZero() || it.FirstSeen.Before(at) {
		at = it.FirstSeen
	}
	age := -1
	if !at.IsZero() {
		age = max(int(now.Sub(at).Hours()/24), 0)
	}
	return views.JobRow{
		ID:        it.JobID,
		Title:     it.Title,
		Company:   it.Company,
		Location:  it.Location,
		Workplace: it.Workplace,
		Salary:    formatSalary(ctx, it.SalaryMin, it.SalaryMax, it.SalaryCurrency),
		Source:    it.Source,
		Blocked:   it.Blocked,
		Age:       age,
		Score:     it.Score,
		Decision:  string(it.Decision),
	}
}

func formatSalary(ctx context.Context, min, max int, currency string) string {
	if min <= 0 && max <= 0 {
		return ""
	}
	sym := "€"
	switch currency {
	case "USD":
		sym = "$"
	case "GBP":
		sym = "£"
	}
	minStr := sym + views.Amount(ctx, strconv.Itoa(min))
	maxStr := sym + views.Amount(ctx, strconv.Itoa(max))
	if min > 0 && max > 0 && min != max {
		return minStr + " – " + maxStr
	}
	if min > 0 && max > 0 && min == max {
		return minStr
	}
	if min > 0 {
		return views.T(ctx, "jobs.min_salary.val", "val", views.Amount(ctx, strconv.Itoa(min)))
	}
	return "≤ " + maxStr
}

// reasonText translates a reason; levels and languages in its parameters are translated too.
func reasonText(ctx context.Context, rs matching.Reason) string {
	params := make([]any, 0, 2*len(rs.Params))
	for k, v := range rs.Params {
		switch k {
		case "job", "user":
			v = views.T(ctx, "match.level."+v)
		case "lang":
			v = views.T(ctx, "match.lang."+v)
		case "top", "min":
			v = views.Amount(ctx, v)
		}
		params = append(params, k, v)
	}
	return views.T(ctx, "match.reason."+rs.Code, params...)
}

// webLink keeps http(s) links of postings; anything else is dropped.
func webLink(s string) string {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return u.String()
}

// partial reports an htmx request for a fragment; history restores and plain requests get the whole page.
func partial(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-Request-Type") != "full" &&
		r.Header.Get("HX-History-Restore-Request") != "true"
}

func (s *Server) importJob(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Discovery == nil {
		s.failed(w, r, u, errors.New("discovery service unavailable"))
		return
	}
	rawURL := strings.TrimSpace(r.FormValue("url"))
	jobID, err := s.Discovery.ImportJob(r.Context(), u.ID, rawURL)
	if err != nil {
		s.Logger.WarnContext(r.Context(), "job import failed", "url", rawURL, "err", err)
		if partial(r) {
			w.Header().Set("HX-Reswap", "innerHTML")
			w.Header().Set("HX-Retarget", "#import-result")
			s.render(w, r, http.StatusOK, views.Notice("error", views.T(r.Context(), importErrorKey(err))))
			return
		}
		http.Redirect(w, r, "/jobs", http.StatusSeeOther)
		return
	}
	detailURL := fmt.Sprintf("/jobs/%d", jobID)
	if partial(r) {
		w.Header().Set("HX-Location", detailURL)
		w.WriteHeader(http.StatusOK)
		return
	}
	//nolint:gosec // G710: detailURL is formatted /jobs/%d with internal ID
	http.Redirect(w, r, detailURL, http.StatusSeeOther)
}

func (s *Server) triggerCrawl(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Discovery == nil {
		s.failed(w, r, u, errors.New("discovery service unavailable"))
		return
	}
	_, _ = s.Discovery.TriggerCrawl(r.Context())
	st, _ := s.Discovery.CrawlStatus(r.Context())
	if !st.Running {
		st.Queued = true
	}
	s.render(w, r, http.StatusOK, views.CrawlStatus(toCrawlStatusData(r.Context(), st)))
}

func (s *Server) crawlStatus(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Discovery == nil {
		s.render(w, r, http.StatusOK, views.CrawlStatus(views.CrawlStatusData{}))
		return
	}
	st, _ := s.Discovery.CrawlStatus(r.Context())
	s.render(w, r, http.StatusOK, views.CrawlStatus(toCrawlStatusData(r.Context(), st)))
}

func (s *Server) addBoard(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.Discovery == nil {
		s.failed(w, r, u, errors.New("discovery service unavailable"))
		return
	}
	rawURL := strings.TrimSpace(r.FormValue("url"))
	b, err := s.Discovery.AddBoard(r.Context(), rawURL)
	if err != nil {
		s.Logger.WarnContext(r.Context(), "add board failed", "url", rawURL, "err", err)
		s.render(w, r, http.StatusOK, views.BoardAddError(views.T(r.Context(), "jobs.board_add_error")))
		return
	}
	s.render(w, r, http.StatusOK, views.BoardAddSuccess(b.Company, b.ATS))
}

func toCrawlStatusData(ctx context.Context, st urlimport.CrawlStatus) views.CrawlStatusData {
	var lastRun string
	if st.LastRunAt != nil && !st.LastRunAt.IsZero() {
		lastRun = views.When(ctx, *st.LastRunAt)
	}
	return views.CrawlStatusData{
		Running: st.Running,
		Queued:  st.Queued,
		LastRun: lastRun,
	}
}

// importErrorKey names what went wrong with a URL import, so the user knows whether to fix the link or try another.
func importErrorKey(err error) string {
	switch {
	case errors.Is(err, urlimport.ErrInvalidURL):
		return "jobs.import_error.invalid"
	case errors.Is(err, urlimport.ErrNoJobFound):
		return "jobs.import_error.no_job"
	case errors.Is(err, urlimport.ErrFetchFailed):
		return "jobs.import_error.fetch"
	default:
		return "jobs.import_error"
	}
}
