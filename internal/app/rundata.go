package app

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/gateway"
	runnerv1 "github.com/1xxn/getafjob/internal/gen/proto/runner/v1"
	"github.com/1xxn/getafjob/internal/matching"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/runner/ats"
)

// jobReader reads jobs for runs.
type jobReader interface {
	Job(ctx context.Context, id int64) (application.Job, error)
}

// runData assembles what a run needs from the user's profile, answers, texts and documents.
type runData struct {
	profiles *profile.Service
	items    *profile.Items
	docs     *profile.Documents
	jobs     jobReader
	now      func() time.Time
}

var _ gateway.Data = runData{}

// RunData returns the run's data without ID and token.
func (d runData) RunData(ctx context.Context, userID uuid.UUID, jobID int64) (*runnerv1.Run, error) {
	job, err := d.jobs.Job(ctx, jobID)
	if err != nil {
		return nil, err
	}
	snap, err := d.profiles.Get(ctx, userID)
	if err != nil {
		return nil, err
	}
	answers, err := d.items.Answers(ctx, userID)
	if err != nil {
		return nil, err
	}
	blocks, err := d.items.TextBlocks(ctx, userID)
	if err != nil {
		return nil, err
	}
	docs, err := d.docs.List(ctx, userID)
	if err != nil {
		return nil, err
	}
	lang := formLanguage(snap.Preferences.CoverLetterLang, job)
	start := job.Apply
	if start == "" {
		start = job.URL
	}
	run := &runnerv1.Run{StartUrl: start, Origins: origins(job), Language: lang,
		Profile: facts(snap.Profile, snap.Preferences)}
	for _, a := range answers {
		run.Answers = append(run.Answers, &runnerv1.Answer{Id: a.ID.String(), Question: a.Question, Answer: a.Answer})
	}
	values := placeholders(snap.Profile, job, lang, d.now())
	for _, t := range blocks {
		if text, missing := t.Fill(values); len(missing) == 0 { // never a text with a gap
			run.Texts = append(run.Texts, &runnerv1.Text{Id: t.ID.String(), Name: t.Name, Language: t.Language, Body: text})
		}
	}
	if cv, ok := profile.ChooseCV(docs, lang, job.Title, job.Description); ok {
		run.CvDocumentId = cv.ID.String()
		run.Documents = append(run.Documents, document(cv))
	}
	for _, doc := range docs {
		if doc.Kind == profile.KindCertificate {
			run.Documents = append(run.Documents, document(doc))
		}
	}
	return run, nil
}

func document(d profile.Document) *runnerv1.Document {
	return &runnerv1.Document{Id: d.ID.String(), FileName: d.Name, Kind: d.Kind, Language: d.Language}
}

// Document returns the content of a document of the user.
func (d runData) Document(ctx context.Context, userID uuid.UUID, documentID string) ([]byte, error) {
	id, err := uuid.Parse(documentID)
	if err != nil {
		return nil, profile.ErrNotFound
	}
	_, b, err := d.docs.Read(ctx, userID, id)
	return b, err
}

// facts flattens profile and preferences into keys (personal.first_name, work.needs_sponsorship ...); yes/no
// facts become "true" or "false", empty values are left out.
func facts(p profile.Profile, prefs profile.Preferences) map[string]string {
	out := map[string]string{}
	flatten := func(prefix string, v any) {
		b, _ := json.Marshal(v)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		for k, x := range m {
			switch t := x.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					out[prefix+k] = t
				}
			case bool:
				out[prefix+k] = map[bool]string{true: "true", false: "false"}[t]
			}
		}
	}
	flatten("personal.", p.Personal)
	flatten("work.", p.Work)
	if full := strings.TrimSpace(p.Personal.FirstName + " " + p.Personal.LastName); full != "" {
		out["personal.full_name"] = full
	}
	out["preferences.talent_pool"] = map[bool]string{true: "true", false: "false"}[prefs.TalentPool]
	if prefs.Diversity != "" {
		out["preferences.diversity"] = prefs.Diversity
	}
	return out
}

// formLanguage is the language of the application: the user's choice, else the posting's.
func formLanguage(choice string, job application.Job) string {
	if choice == "en" || choice == "de" {
		return choice
	}
	return matching.DetectLanguage(job.Title + " " + job.Description)
}

// placeholders are the values text blocks may use.
func placeholders(p profile.Profile, job application.Job, lang string, now time.Time) map[string]string {
	date := now.Format("January 2, 2006")
	if lang == "de" {
		date = now.Format("02.01.2006")
	}
	return map[string]string{
		"first_name": p.Personal.FirstName, "last_name": p.Personal.LastName,
		"full_name": strings.TrimSpace(p.Personal.FirstName + " " + p.Personal.LastName), "email": p.Personal.Email,
		"phone": p.Personal.Phone, "city": p.Personal.City, "current_title": p.Work.CurrentTitle,
		"notice_period": p.Work.NoticePeriod, "earliest_start": p.Work.EarliestStart, "company": job.Company,
		"job_title": job.Title, "job_location": job.Location, "date": date,
	}
}

// origins are the origins the run's values may go to: the posting, the application page and the ATS's own form
// hosts.
func origins(job application.Job) []string {
	return ats.Origins(job.Source, job.URL, job.Apply)
}
