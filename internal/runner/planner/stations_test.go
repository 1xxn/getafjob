package planner

import (
	"context"
	"slices"
	"testing"

	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/executor"
)

func TestStationSectionDetectionAndMapping(t *testing.T) {
	k := Knowledge{
		Origins:  []string{"https://example.org"},
		Language: "en",
		Profile: map[string]string{
			"personal.first_name": "Max",
			"personal.last_name":  "Mustermann",
		},
		WorkExperiences: []WorkExperience{
			{
				ID:            "exp-1",
				Company:       "Acme Cloud GmbH",
				Title:         "Senior Platform Engineer",
				Location:      "Berlin",
				WorkplaceType: "hybrid",
				StartDate:     "2022-03",
				EndDate:       "",
				IsCurrent:     true,
				Description:   "Architected Kubernetes platform and CI/CD pipelines.",
				SortOrder:     0,
			},
			{
				ID:            "exp-2",
				Company:       "Startup Labs",
				Title:         "Backend Developer",
				Location:      "Munich",
				WorkplaceType: "remote",
				StartDate:     "2019-09",
				EndDate:       "2022-02",
				IsCurrent:     false,
				Description:   "Developed Go microservices.",
				SortOrder:     1,
			},
		},
		Educations: []EducationStation{
			{
				ID:           "edu-1",
				Institution:  "Technical University of Berlin",
				Degree:       "Master of Science",
				FieldOfStudy: "Computer Science",
				StartDate:    "2017",
				EndDate:      "2019",
				Grade:        "1.3",
				SortOrder:    0,
			},
		},
	}

	p := &Planner{K: k}

	fields := []browser.Field{
		{ID: "0-1", Kind: "text", Label: "First Name", Name: "first_name", Required: true},
		{ID: "0-2", Kind: "text", Label: "Last Name", Name: "last_name", Required: true},

		// Experience 1 (fieldset with legend)
		{ID: "0-3", Kind: "text", Label: "Company", Name: "experience[0][company]", Section: "Work Experience 1", Required: true},
		{ID: "0-4", Kind: "text", Label: "Title", Name: "experience[0][title]", Section: "Work Experience 1", Required: true},
		{ID: "0-5", Kind: "text", Label: "Location", Name: "experience[0][location]", Section: "Work Experience 1"},
		{ID: "0-6", Kind: "text", Label: "Start Date", Name: "experience[0][start_date]", Hint: "MM/YYYY", Section: "Work Experience 1", Required: true},
		{ID: "0-7", Kind: "checkbox", Label: "Currently work here", Name: "experience[0][current]", Section: "Work Experience 1"},
		{ID: "0-8", Kind: "textarea", Label: "Job Description", Name: "experience[0][description]", Section: "Work Experience 1"},

		// Experience 2
		{ID: "0-9", Kind: "text", Label: "Company", Name: "experience[1][company]", Section: "Work Experience 2", Required: true},
		{ID: "0-10", Kind: "text", Label: "Title", Name: "experience[1][title]", Section: "Work Experience 2", Required: true},
		{ID: "0-11", Kind: "text", Label: "Location", Name: "experience[1][location]", Section: "Work Experience 2"},
		{ID: "0-12", Kind: "text", Label: "Start Date", Name: "experience[1][start_date]", Hint: "MM/YYYY", Section: "Work Experience 2", Required: true},
		{ID: "0-13", Kind: "text", Label: "End Date", Name: "experience[1][end_date]", Hint: "MM/YYYY", Section: "Work Experience 2", Required: true},
		{ID: "0-14", Kind: "textarea", Label: "Job Description", Name: "experience[1][description]", Section: "Work Experience 2"},

		// Education 1
		{ID: "0-15", Kind: "text", Label: "University / School", Name: "education[0][school]", Section: "Education", Required: true},
		{ID: "0-16", Kind: "text", Label: "Degree", Name: "education[0][degree]", Section: "Education", Required: true},
		{ID: "0-17", Kind: "text", Label: "Field of Study", Name: "education[0][major]", Section: "Education"},
		{ID: "0-18", Kind: "text", Label: "Start Date", Name: "education[0][start]", Section: "Education"},
		{ID: "0-19", Kind: "text", Label: "End Date", Name: "education[0][end]", Section: "Education"},
		{ID: "0-20", Kind: "text", Label: "GPA / Grade", Name: "education[0][grade]", Section: "Education"},
	}

	st := &browser.State{
		Frames: []*browser.Frame{{Fields: fields}},
	}

	plan, qs, err := p.Plan(context.Background(), st)
	if err != nil {
		t.Fatalf("Plan err = %v", err)
	}
	if len(qs) > 0 {
		t.Fatalf("expected 0 questions, got %d: %+v", len(qs), qs)
	}

	fieldValues := make(map[string]executor.Value)
	for _, v := range plan.Values {
		fieldValues[v.FieldID] = v
		if !executor.ValidSource(v.Source) {
			t.Errorf("invalid provenance source: %q", v.Source)
		}
	}

	// Verify Station 1 (Experience 0)
	if v, ok := fieldValues["0-3"]; !ok || v.Text != "Acme Cloud GmbH" || v.Source != "profile.work_experience.0.company" {
		t.Errorf("Exp 0 company mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-4"]; !ok || v.Text != "Senior Platform Engineer" || v.Source != "profile.work_experience.0.title" {
		t.Errorf("Exp 0 title mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-5"]; !ok || v.Text != "Berlin" {
		t.Errorf("Exp 0 location mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-6"]; !ok || v.Text != "03/2022" || v.Source != "profile.work_experience.0.start_date" {
		t.Errorf("Exp 0 start date mismatch (expected MM/YYYY): %+v", v)
	}
	if v, ok := fieldValues["0-7"]; !ok || v.Text != "true" || v.Source != "profile.work_experience.0.is_current" {
		t.Errorf("Exp 0 is_current mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-8"]; !ok || v.Text != "Architected Kubernetes platform and CI/CD pipelines." {
		t.Errorf("Exp 0 description mismatch: %+v", v)
	}

	// Verify Station 2 (Experience 1)
	if v, ok := fieldValues["0-9"]; !ok || v.Text != "Startup Labs" || v.Source != "profile.work_experience.1.company" {
		t.Errorf("Exp 1 company mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-10"]; !ok || v.Text != "Backend Developer" || v.Source != "profile.work_experience.1.title" {
		t.Errorf("Exp 1 title mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-12"]; !ok || v.Text != "09/2019" {
		t.Errorf("Exp 1 start date mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-13"]; !ok || v.Text != "02/2022" || v.Source != "profile.work_experience.1.end_date" {
		t.Errorf("Exp 1 end date mismatch: %+v", v)
	}

	// Verify Education
	if v, ok := fieldValues["0-15"]; !ok || v.Text != "Technical University of Berlin" || v.Source != "profile.education.0.institution" {
		t.Errorf("Edu 0 institution mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-16"]; !ok || v.Text != "Master of Science" || v.Source != "profile.education.0.degree" {
		t.Errorf("Edu 0 degree mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-17"]; !ok || v.Text != "Computer Science" || v.Source != "profile.education.0.field_of_study" {
		t.Errorf("Edu 0 major mismatch: %+v", v)
	}
	if v, ok := fieldValues["0-20"]; !ok || v.Text != "1.3" || v.Source != "profile.education.0.grade" {
		t.Errorf("Edu 0 grade mismatch: %+v", v)
	}
}

func TestSequentialStationGroupingWithoutExplicitIndex(t *testing.T) {
	// Form has repeated fields in DOM order without index in name or section
	k := Knowledge{
		Origins:  []string{"https://example.org"},
		Language: "en",
		WorkExperiences: []WorkExperience{
			{Company: "Company A", Title: "Title A", StartDate: "2023-01", IsCurrent: true},
			{Company: "Company B", Title: "Title B", StartDate: "2021-01", EndDate: "2022-12"},
		},
	}
	p := &Planner{K: k}

	fields := []browser.Field{
		// First entry
		{ID: "f1", Kind: "text", Label: "Arbeitgeber", Section: "Berufserfahrung"},
		{ID: "f2", Kind: "text", Label: "Berufsbezeichnung", Section: "Berufserfahrung"},
		{ID: "f3", Kind: "text", Label: "Von", Section: "Berufserfahrung"},
		// Second entry (repeats Arbeitgeber, Title, Von)
		{ID: "f4", Kind: "text", Label: "Arbeitgeber", Section: "Berufserfahrung"},
		{ID: "f5", Kind: "text", Label: "Berufsbezeichnung", Section: "Berufserfahrung"},
		{ID: "f6", Kind: "text", Label: "Von", Section: "Berufserfahrung"},
		{ID: "f7", Kind: "text", Label: "Bis", Section: "Berufserfahrung"},
	}

	st := &browser.State{Frames: []*browser.Frame{{Fields: fields}}}
	plan, qs, err := p.Plan(context.Background(), st)
	if err != nil {
		t.Fatalf("Plan err = %v", err)
	}
	if len(qs) > 0 {
		t.Fatalf("unexpected questions: %+v", qs)
	}

	m := make(map[string]string)
	for _, v := range plan.Values {
		m[v.FieldID] = v.Text
	}

	if m["f1"] != "Company A" || m["f2"] != "Title A" || m["f3"] != "2023-01" {
		t.Errorf("group 0 mismatch: %v", m)
	}
	if m["f4"] != "Company B" || m["f5"] != "Title B" || m["f6"] != "2021-01" || m["f7"] != "2022-12" {
		t.Errorf("group 1 mismatch: %v", m)
	}
}

func TestDateDropdownsAndOptionMatching(t *testing.T) {
	k := Knowledge{
		Origins:  []string{"https://example.org"},
		Language: "en",
		WorkExperiences: []WorkExperience{
			{Company: "Cloud Org", Title: "Dev", StartDate: "2021-04", EndDate: "2023-11"},
		},
		Educations: []EducationStation{
			{Institution: "Harvard", Degree: "Bachelor of Science", FieldOfStudy: "Software Engineering"},
		},
	}
	p := &Planner{K: k}

	fields := []browser.Field{
		{ID: "c1", Kind: "text", Label: "Company", Section: "Experience"},
		// Separate Month & Year dropdowns
		{
			ID: "m1", Kind: "select", Label: "Start Month", Section: "Experience",
			Options: []browser.Option{{Label: "Select month"}, {Label: "03 - March"}, {Label: "04 - April"}, {Label: "05 - May"}},
		},
		{
			ID: "y1", Kind: "select", Label: "Start Year", Section: "Experience",
			Options: []browser.Option{{Label: "Select year"}, {Label: "2020"}, {Label: "2021"}, {Label: "2022"}},
		},
		{
			ID: "m2", Kind: "select", Label: "End Month", Section: "Experience",
			Options: []browser.Option{{Label: "Select month"}, {Label: "October"}, {Label: "November"}, {Label: "December"}},
		},
		{
			ID: "y2", Kind: "select", Label: "End Year", Section: "Experience",
			Options: []browser.Option{{Label: "Select year"}, {Label: "2022"}, {Label: "2023"}, {Label: "2024"}},
		},
		// Education degree dropdown with partial name
		{
			ID: "ed1", Kind: "select", Label: "Degree", Section: "Education",
			Options: []browser.Option{{Label: "Select..."}, {Label: "Bachelor of Arts"}, {Label: "Bachelor of Science"}, {Label: "Master of Science"}},
		},
	}

	st := &browser.State{Frames: []*browser.Frame{{Fields: fields}}}
	plan, _, err := p.Plan(context.Background(), st)
	if err != nil {
		t.Fatalf("Plan err = %v", err)
	}

	m := make(map[string]string)
	for _, v := range plan.Values {
		m[v.FieldID] = v.Text
	}

	if m["m1"] != "04 - April" {
		t.Errorf("Start Month mismatch: %q", m["m1"])
	}
	if m["y1"] != "2021" {
		t.Errorf("Start Year mismatch: %q", m["y1"])
	}
	if m["m2"] != "November" {
		t.Errorf("End Month mismatch: %q", m["m2"])
	}
	if m["y2"] != "2023" {
		t.Errorf("End Year mismatch: %q", m["y2"])
	}
	if m["ed1"] != "Bachelor of Science" {
		t.Errorf("Degree mismatch: %q", m["ed1"])
	}
}

func TestNextStationButtonDetection(t *testing.T) {
	k := Knowledge{
		Origins:  []string{"https://example.org"},
		Language: "en",
		WorkExperiences: []WorkExperience{
			{Company: "Exp 1"},
			{Company: "Exp 2"},
		},
		Educations: []EducationStation{
			{Institution: "School 1"},
		},
	}
	p := &Planner{K: k}

	// Currently page only has 1 experience block
	fields := []browser.Field{
		{ID: "f1", Kind: "text", Label: "Company", Section: "Experience"},
		{ID: "f2", Kind: "text", Label: "Title", Section: "Experience"},
		{ID: "f3", Kind: "text", Label: "School", Section: "Education"},
	}
	buttons := []browser.Button{
		{ID: "b-cookie", Text: "Accept Cookies", Cookie: true},
		{ID: "b-add-exp", Text: "+ Add another position", Section: "Experience"},
		{ID: "b-add-edu", Text: "+ Add education", Section: "Education"},
		{ID: "b-submit", Text: "Submit Application", Kind: "submit"},
	}

	st := &browser.State{
		Frames: []*browser.Frame{{Fields: fields, Buttons: buttons}},
	}

	btnID, needMore := p.NextStationButton(st)
	if !needMore || btnID != "b-add-exp" {
		t.Fatalf("expected b-add-exp, got needMore=%v, btnID=%q", needMore, btnID)
	}

	// Now add the second experience to fields
	fields2 := append(slices.Clone(fields),
		browser.Field{ID: "f4", Kind: "text", Label: "Company", Section: "Experience 2"},
		browser.Field{ID: "f5", Kind: "text", Label: "Title", Section: "Experience 2"},
	)

	st2 := &browser.State{
		Frames: []*browser.Frame{{Fields: fields2, Buttons: buttons}},
	}

	btnID2, needMore2 := p.NextStationButton(st2)
	// Now both experiences (2) and education (1) are satisfied!
	if needMore2 || btnID2 != "" {
		t.Fatalf("expected no more buttons needed, got needMore=%v, btnID=%q", needMore2, btnID2)
	}
}

func TestEmptyOrPartiallyFilledStationsSafety(t *testing.T) {
	// Candidate has ZERO stations
	k := Knowledge{
		Origins:  []string{"https://example.org"},
		Language: "en",
		Profile: map[string]string{
			"personal.first_name": "Max",
		},
	}
	p := &Planner{K: k}

	fields := []browser.Field{
		{ID: "f1", Kind: "text", Label: "First Name", Required: true},
		{ID: "f2", Kind: "text", Label: "Company", Section: "Work Experience", Required: false},
		{ID: "f3", Kind: "text", Label: "Title", Section: "Work Experience", Required: false},
		{ID: "f4", Kind: "text", Label: "School", Section: "Education", Required: false},
	}

	st := &browser.State{Frames: []*browser.Frame{{Fields: fields}}}
	plan, qs, err := p.Plan(context.Background(), st)
	if err != nil {
		t.Fatalf("Plan err = %v", err)
	}
	if len(qs) > 0 {
		t.Fatalf("unexpected questions for optional fields: %+v", qs)
	}
	if len(plan.Values) != 1 || plan.Values[0].FieldID != "f1" {
		t.Fatalf("expected only first_name planned, got: %+v", plan.Values)
	}

	// Verify NextStationButton does NOT trigger when candidate has no stations
	btnID, needMore := p.NextStationButton(&browser.State{
		Frames: []*browser.Frame{{
			Fields: fields,
			Buttons: []browser.Button{
				{ID: "b-exp", Text: "+ Add Experience"},
			},
		}},
	})
	if needMore || btnID != "" {
		t.Fatalf("expected needMore=false for 0 stations, got %v (%s)", needMore, btnID)
	}
}
