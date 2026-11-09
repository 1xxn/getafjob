package planner

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/executor"
)

// WorkExperience is a career position for form filling.
type WorkExperience struct {
	ID                 string
	Company            string
	Title              string
	Location           string
	WorkplaceType      string // remote, hybrid, onsite
	StartDate          string // YYYY-MM or YYYY
	EndDate            string // YYYY-MM or YYYY (empty if current)
	IsCurrent          bool
	DescriptionBullets []string
	Description        string
	SortOrder          int
}

// EducationStation is an academic station for form filling.
type EducationStation struct {
	ID           string
	Institution  string
	Degree       string
	FieldOfStudy string
	StartDate    string // YYYY-MM or YYYY
	EndDate      string // YYYY-MM or YYYY
	IsCurrent    bool
	Grade        string
	SortOrder    int
}

var (
	// Section recognition
	rxExpSection = regexp.MustCompile(`(?i)experience|employment|work[-_ ]history|beruf|t[äa]tigkeit`)
	rxEduSection = regexp.MustCompile(`(?i)education|ausbildung|studium|school|universit|academic`)

	// Work experience attributes
	rxExpCompany = regexp.MustCompile(`(?i)^(company|employer|arbeitgeber|firma|organization|company name|unternehmensname)$|\b(company|employer|arbeitgeber|firma)\b`)
	rxExpTitle   = regexp.MustCompile(`(?i)^(job title|title|position|berufsbezeichnung|rolle|role|position title)$|\b(job title|position|berufsbezeichnung)\b`)
	rxExpLoc     = regexp.MustCompile(`(?i)^(location|ort|city|standort)$|\b(job location|work location)\b`)
	rxExpType    = regexp.MustCompile(`(?i)(workplace|employment|work) type|remote|hybrid|onsite|arbeitszeitmodell|beschaeftigungsart`)
	rxExpDesc    = regexp.MustCompile(`(?i)description|summary|responsibilities|role description|aufgaben|beschreibung|t[äa]tigkeiten`)

	// Education attributes
	rxEduSchool = regexp.MustCompile(`(?i)^(school|university|institution|college|schule|universit[äa]t|hochschule|ausbildungsst[äa]tte)$|\b(school|university|institution|college|universit[äa]t|hochschule)\b`)
	rxEduDegree = regexp.MustCompile(`(?i)^(degree|abschluss|degree type|akademischer grad)$|\b(degree|abschluss)\b`)
	rxEduMajor  = regexp.MustCompile(`(?i)field of study|major|discipline|fachrichtung|studiengang|schwerpunkt|studienfach`)
	rxEduGrade  = regexp.MustCompile(`(?i)\b(gpa|grade|abschlussnote|note)\b`)

	// Date and status attributes
	rxStartDate = regexp.MustCompile(`(?i)^(from|von|beginn|eintritt)(\s*[*:(].*)?$|\b(start (date|time)?|start month|start year)\b`)
	rxEndDate   = regexp.MustCompile(`(?i)^(to|bis|ende)(\s*[*:(].*)?$|\b(end (date|time)?|end month|end year|graduation( date)?|abschlussdatum|bis datum)\b`)
	rxIsCurrent = regexp.MustCompile(`(?i)currently work here|current role|present|bis heute|aktuelle position|current`)

	rxMonth = regexp.MustCompile(`(?i)\b(month|monat)\b`)
	rxYear  = regexp.MustCompile(`(?i)\b(year|jahr)\b`)

	// Buttons for adding stations
	addExpRx = regexp.MustCompile(`(?i)(add|hinzuf[üu]gen|weitere[rn]?).*(experience|employment|position|job|berufserfahrung|arbeitgeber|t[äa]tigkeit)|\+( ?add)? (experience|employment|position|job)|add another (role|position|experience)`)
	addEduRx = regexp.MustCompile(`(?i)(add|hinzuf[üu]gen|weitere[rn]?).*(education|school|degree|ausbildung|studium|universit[äa]t|abschluss)|\+( ?add)? (education|school|degree)|add another (education|school|degree)`)
	addGenRx = regexp.MustCompile(`(?i)\+\s*add (another|entry|item|one)|add another`)
)

var monthNames = []string{
	"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December",
}

var deMonthNames = []string{
	"Januar", "Februar", "März", "April", "Mai", "Juni",
	"Juli", "August", "September", "Oktober", "November", "Dezember",
}

// planStations inspects all fields on the page, discovers repeating station groups, maps them to the candidate's
// career stations, and returns planned executor values along with the set of field IDs that were satisfied.
func (p *Planner) planStations(st *browser.State) ([]executor.Value, map[string]bool) {
	var vals []executor.Value
	handled := make(map[string]bool)

	expGroups, eduGroups := detectStationGroups(st)

	// Map work experiences
	for idx, group := range expGroups {
		if idx >= len(p.K.WorkExperiences) {
			break // form has more slots than candidate has experiences
		}
		exp := p.K.WorkExperiences[idx]
		vList := mapWorkExperience(exp, idx, group)
		for _, v := range vList {
			vals = append(vals, v)
			handled[v.FieldID] = true
		}
	}

	// Map educations
	for idx, group := range eduGroups {
		if idx >= len(p.K.Educations) {
			break // form has more slots than candidate has educations
		}
		edu := p.K.Educations[idx]
		vList := mapEducation(edu, idx, group)
		for _, v := range vList {
			vals = append(vals, v)
			handled[v.FieldID] = true
		}
	}

	return vals, handled
}

// NextStationButton checks whether the form requires clicking "+ Add Experience" or "+ Add Education"
// to reveal more fields for the candidate's stations.
func (p *Planner) NextStationButton(st *browser.State) (string, bool) {
	expGroups, eduGroups := detectStationGroups(st)

	// Check if candidate has more work experiences than currently present
	if len(p.K.WorkExperiences) > len(expGroups) {
		for _, b := range st.Buttons() {
			if b.Disabled || b.Cookie || b.Autofill {
				continue
			}
			if addExpRx.MatchString(b.Text) || (addGenRx.MatchString(b.Text) && rxExpSection.MatchString(b.Section)) {
				return b.ID, true
			}
		}
	}

	// Check if candidate has more educations than currently present
	if len(p.K.Educations) > len(eduGroups) {
		for _, b := range st.Buttons() {
			if b.Disabled || b.Cookie || b.Autofill {
				continue
			}
			if addEduRx.MatchString(b.Text) || (addGenRx.MatchString(b.Text) && rxEduSection.MatchString(b.Section)) {
				return b.ID, true
			}
		}
	}

	return "", false
}

// stationGroup collects form fields that belong to a single career station entry.
type stationGroup struct {
	Index  int
	Fields []stationField
}

type stationField struct {
	Role  string // company, title, location, workplace_type, start_date, end_date, is_current, description, institution, degree, field_of_study, grade
	Field browser.Field
}

// detectStationGroups groups fields into work experience and education station entries.
func detectStationGroups(st *browser.State) (expGroups, eduGroups []stationGroup) {
	var expEntries []stationGroup
	var eduEntries []stationGroup

	activeExpIdx := -1
	activeEduIdx := -1

	for _, f := range st.Fields() {
		if f.Kind == "password" || f.Kind == "file" {
			continue
		}

		sec := f.Section
		normLabel := executor.Normalize(f.Label)
		normName := executor.Normalize(f.Name)
		normID := executor.Normalize(f.HTMLID)

		// Top-level personal current employer/title (not inside repeating station sections)
		if strings.Contains(normLabel, "current employer") || strings.Contains(normLabel, "current company") ||
			strings.Contains(normLabel, "current job title") || strings.Contains(normLabel, "current title") ||
			strings.Contains(normLabel, "aktuelle position") || strings.Contains(normLabel, "aktueller arbeitgeber") {
			if !rxExpSection.MatchString(sec) && !hasExplicitIndex(f) {
				continue
			}
		}

		isEdu := rxEduSection.MatchString(sec) || rxEduSection.MatchString(normName) || rxEduSection.MatchString(normID)
		isExp := rxExpSection.MatchString(sec) || rxExpSection.MatchString(normName) || rxExpSection.MatchString(normID)

		// Checkboxes in station sections can only be is_current; other checkboxes are general terms/consent
		if f.Kind == "checkbox" || f.Kind == "aria_checkbox" {
			if rxIsCurrent.MatchString(normLabel) || rxIsCurrent.MatchString(normName) {
				role := "is_current"
				idx := extractFieldIndex(f)
				if idx >= 0 {
					expEntries = addFieldToEntry(expEntries, idx, role, f)
					activeExpIdx = idx
				} else {
					if activeExpIdx < 0 || entryHasRole(expEntries, activeExpIdx, role) {
						activeExpIdx++
					}
					expEntries = addFieldToEntry(expEntries, activeExpIdx, role, f)
				}
			}
			continue
		}

		// Attribute identification
		var role string
		var kind string // "exp" or "edu"

		if isEdu || (!isExp && (rxEduSchool.MatchString(normLabel) || rxEduDegree.MatchString(normLabel) || rxEduMajor.MatchString(normLabel) || rxEduGrade.MatchString(normLabel))) {
			kind = "edu"
			switch {
			case rxEduSchool.MatchString(normLabel) || rxEduSchool.MatchString(normName):
				role = "institution"
			case rxEduDegree.MatchString(normLabel) || rxEduDegree.MatchString(normName):
				role = "degree"
			case rxEduMajor.MatchString(normLabel) || rxEduMajor.MatchString(normName):
				role = "field_of_study"
			case rxEduGrade.MatchString(normLabel) || rxEduGrade.MatchString(normName):
				role = "grade"
			case rxStartDate.MatchString(normLabel) || rxStartDate.MatchString(normName):
				switch {
				case rxMonth.MatchString(normLabel) || rxMonth.MatchString(normName):
					role = "start_month"
				case rxYear.MatchString(normLabel) || rxYear.MatchString(normName):
					role = "start_year"
				default:
					role = "start_date"
				}
			case rxEndDate.MatchString(normLabel) || rxEndDate.MatchString(normName):
				switch {
				case rxMonth.MatchString(normLabel) || rxMonth.MatchString(normName):
					role = "end_month"
				case rxYear.MatchString(normLabel) || rxYear.MatchString(normName):
					role = "end_year"
				default:
					role = "end_date"
				}
			}
		} else if isExp || rxExpCompany.MatchString(normLabel) || rxExpTitle.MatchString(normLabel) || rxExpDesc.MatchString(normLabel) {
			kind = "exp"
			switch {
			case rxExpCompany.MatchString(normLabel) || rxExpCompany.MatchString(normName):
				role = "company"
			case rxExpTitle.MatchString(normLabel) || rxExpTitle.MatchString(normName):
				role = "title"
			case rxExpLoc.MatchString(normLabel) || rxExpLoc.MatchString(normName):
				role = "location"
			case rxExpType.MatchString(normLabel) || rxExpType.MatchString(normName):
				role = "workplace_type"
			case rxExpDesc.MatchString(normLabel) || rxExpDesc.MatchString(normName):
				role = "description"
			case rxIsCurrent.MatchString(normLabel) || rxIsCurrent.MatchString(normName):
				role = "is_current"
			case rxStartDate.MatchString(normLabel) || rxStartDate.MatchString(normName):
				switch {
				case rxMonth.MatchString(normLabel) || rxMonth.MatchString(normName):
					role = "start_month"
				case rxYear.MatchString(normLabel) || rxYear.MatchString(normName):
					role = "start_year"
				default:
					role = "start_date"
				}
			case rxEndDate.MatchString(normLabel) || rxEndDate.MatchString(normName):
				switch {
				case rxMonth.MatchString(normLabel) || rxMonth.MatchString(normName):
					role = "end_month"
				case rxYear.MatchString(normLabel) || rxYear.MatchString(normName):
					role = "end_year"
				default:
					role = "end_date"
				}
			}
		}

		if role == "" {
			continue
		}

		// Explicit index lookup
		idx := extractFieldIndex(f)

		switch kind {
		case "exp":
			if idx >= 0 {
				expEntries = addFieldToEntry(expEntries, idx, role, f)
				activeExpIdx = idx
			} else {
				if activeExpIdx < 0 || entryHasRole(expEntries, activeExpIdx, role) {
					activeExpIdx++
				}
				expEntries = addFieldToEntry(expEntries, activeExpIdx, role, f)
			}
		case "edu":
			if idx >= 0 {
				eduEntries = addFieldToEntry(eduEntries, idx, role, f)
				activeEduIdx = idx
			} else {
				if activeEduIdx < 0 || entryHasRole(eduEntries, activeEduIdx, role) {
					activeEduIdx++
				}
				eduEntries = addFieldToEntry(eduEntries, activeEduIdx, role, f)
			}
		}
	}

	return expEntries, eduEntries
}

func addFieldToEntry(entries []stationGroup, idx int, role string, f browser.Field) []stationGroup {
	for i := range entries {
		if entries[i].Index == idx {
			entries[i].Fields = append(entries[i].Fields, stationField{Role: role, Field: f})
			return entries
		}
	}
	// Create new group
	return append(entries, stationGroup{
		Index:  idx,
		Fields: []stationField{{Role: role, Field: f}},
	})
}

func entryHasRole(entries []stationGroup, idx int, role string) bool {
	for _, entry := range entries {
		if entry.Index == idx {
			for _, sf := range entry.Fields {
				if sf.Role == role {
					return true
				}
			}
		}
	}
	return false
}

func hasExplicitIndex(f browser.Field) bool {
	return extractFieldIndex(f) >= 0
}

var (
	rxIndexBrackets = regexp.MustCompile(`\[(\d+)\]`)
	rxIndexUnder    = regexp.MustCompile(`_(\d+)(?:_|$)`)
	rxIndexDash     = regexp.MustCompile(`-(\d+)(?:-|$)`)
	rxIndexNum      = regexp.MustCompile(`(?i)(?:experience|position|education|station|entry|ausbildung|stelle)\s*#?\s*(\d+)`)
)

func extractFieldIndex(f browser.Field) int {
	for _, target := range []string{f.Name, f.HTMLID, f.Section} {
		if m := rxIndexBrackets.FindStringSubmatch(target); len(m) > 1 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
		if m := rxIndexNum.FindStringSubmatch(target); len(m) > 1 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				if n > 0 {
					return n - 1 // 1-based to 0-based
				}
				return n
			}
		}
		if m := rxIndexUnder.FindStringSubmatch(target); len(m) > 1 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
		if m := rxIndexDash.FindStringSubmatch(target); len(m) > 1 {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return -1
}

func mapWorkExperience(exp WorkExperience, idx int, group stationGroup) []executor.Value {
	var vals []executor.Value
	prefix := fmt.Sprintf("profile.work_experience.%d", idx)

	for _, sf := range group.Fields {
		f := sf.Field
		switch sf.Role {
		case "company":
			if exp.Company != "" {
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: exp.Company, Source: prefix + ".company"})
			}
		case "title":
			if exp.Title != "" {
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: exp.Title, Source: prefix + ".title"})
			}
		case "location":
			if exp.Location != "" {
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: exp.Location, Source: prefix + ".location"})
			}
		case "workplace_type":
			if exp.WorkplaceType != "" {
				text := exp.WorkplaceType
				if len(f.Options) > 0 {
					optLabels := make([]string, len(f.Options))
					for i, o := range f.Options {
						optLabels[i] = o.Label
					}
					if matchIdx, ok := executor.Match(exp.WorkplaceType, optLabels); ok && matchIdx >= 0 {
						text = optLabels[matchIdx]
					}
				}
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: text, Source: prefix + ".workplace_type"})
			}
		case "start_date", "start_month", "start_year":
			if v, ok := formatStationDate(exp.StartDate, f); ok {
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: v, Source: prefix + ".start_date"})
			}
		case "end_date", "end_month", "end_year":
			if exp.IsCurrent && (f.Kind == "checkbox" || f.Kind == "aria_checkbox") {
				continue
			}
			if exp.EndDate != "" {
				if v, ok := formatStationDate(exp.EndDate, f); ok {
					vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: v, Source: prefix + ".end_date"})
				}
			} else if exp.IsCurrent {
				if f.Kind == "text" && (strings.Contains(executor.Normalize(f.Label), "present") || strings.Contains(executor.Normalize(f.Hint), "present")) {
					vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: "Present", Source: prefix + ".end_date"})
				}
			}
		case "is_current":
			if f.Kind == "checkbox" || f.Kind == "aria_checkbox" || f.Kind == "radio" {
				if exp.IsCurrent {
					vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: "true", Source: prefix + ".is_current"})
				}
			}
		case "description":
			desc := exp.Description
			if desc == "" && len(exp.DescriptionBullets) > 0 {
				desc = strings.Join(exp.DescriptionBullets, "\n• ")
			}
			if desc != "" {
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: desc, Source: prefix + ".description"})
			}
		}
	}

	return vals
}

func mapEducation(edu EducationStation, idx int, group stationGroup) []executor.Value {
	var vals []executor.Value
	prefix := fmt.Sprintf("profile.education.%d", idx)

	for _, sf := range group.Fields {
		f := sf.Field
		switch sf.Role {
		case "institution":
			if edu.Institution != "" {
				text := edu.Institution
				if len(f.Options) > 0 {
					optLabels := make([]string, len(f.Options))
					for i, o := range f.Options {
						optLabels[i] = o.Label
					}
					if matchIdx, ok := executor.Match(edu.Institution, optLabels); ok && matchIdx >= 0 {
						text = optLabels[matchIdx]
					}
				}
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: text, Source: prefix + ".institution"})
			}
		case "degree":
			if edu.Degree != "" {
				text := edu.Degree
				if len(f.Options) > 0 {
					optLabels := make([]string, len(f.Options))
					for i, o := range f.Options {
						optLabels[i] = o.Label
					}
					if matchIdx, ok := executor.Match(edu.Degree, optLabels); ok && matchIdx >= 0 {
						text = optLabels[matchIdx]
					}
				}
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: text, Source: prefix + ".degree"})
			}
		case "field_of_study":
			if edu.FieldOfStudy != "" {
				text := edu.FieldOfStudy
				if len(f.Options) > 0 {
					optLabels := make([]string, len(f.Options))
					for i, o := range f.Options {
						optLabels[i] = o.Label
					}
					if matchIdx, ok := executor.Match(edu.FieldOfStudy, optLabels); ok && matchIdx >= 0 {
						text = optLabels[matchIdx]
					}
				}
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: text, Source: prefix + ".field_of_study"})
			}
		case "start_date", "start_month", "start_year":
			if v, ok := formatStationDate(edu.StartDate, f); ok {
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: v, Source: prefix + ".start_date"})
			}
		case "end_date", "end_month", "end_year":
			if v, ok := formatStationDate(edu.EndDate, f); ok {
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: v, Source: prefix + ".end_date"})
			}
		case "grade":
			if edu.Grade != "" {
				vals = append(vals, executor.Value{FieldID: f.ID, Label: f.Label, Text: edu.Grade, Source: prefix + ".grade"})
			}
		}
	}

	return vals
}

// formatStationDate formats a candidate date string (e.g. "2021-04" or "2021") into the representation
// required by the target form field.
func formatStationDate(dateStr string, f browser.Field) (string, bool) {
	dateStr = strings.TrimSpace(dateStr)
	if dateStr == "" {
		return "", false
	}

	normLabel := executor.Normalize(f.Label)
	normName := executor.Normalize(f.Name)
	normHint := executor.Normalize(f.Hint)

	// Is this a dedicated Month field?
	if rxMonth.MatchString(normLabel) || rxMonth.MatchString(normName) {
		var monthNum int
		if len(dateStr) >= 7 && dateStr[4] == '-' {
			monthNum, _ = strconv.Atoi(dateStr[5:7])
		}
		if monthNum < 1 || monthNum > 12 {
			return "", false
		}
		numStr := fmt.Sprintf("%02d", monthNum)
		if len(f.Options) > 0 {
			optLabels := make([]string, len(f.Options))
			for i, o := range f.Options {
				optLabels[i] = o.Label
			}
			// Try English month name first (e.g. "April" matches "04 - April" or "April")
			if mIdx, ok := executor.Match(monthNames[monthNum-1], optLabels); ok && mIdx >= 0 {
				return optLabels[mIdx], true
			}
			// Try German month name
			if mIdx, ok := executor.Match(deMonthNames[monthNum-1], optLabels); ok && mIdx >= 0 {
				return optLabels[mIdx], true
			}
			// Try numeric prefix or equality ("04" or "4")
			for _, o := range optLabels {
				normO := executor.Normalize(o)
				if normO == numStr || normO == strconv.Itoa(monthNum) || strings.HasPrefix(normO, numStr+" ") || strings.HasPrefix(normO, strconv.Itoa(monthNum)+" ") {
					return o, true
				}
			}
		}
		return numStr, true
	}

	// Is this a dedicated Year field?
	if rxYear.MatchString(normLabel) || rxYear.MatchString(normName) {
		var yearStr string
		if len(dateStr) >= 4 {
			yearStr = dateStr[:4]
		}
		if yearStr == "" {
			return "", false
		}
		if len(f.Options) > 0 {
			optLabels := make([]string, len(f.Options))
			for i, o := range f.Options {
				optLabels[i] = o.Label
			}
			if mIdx, ok := executor.Match(yearStr, optLabels); ok && mIdx >= 0 {
				return optLabels[mIdx], true
			}
		}
		return yearStr, true
	}

	// Native HTML5 date field
	if f.Kind == "date" || f.InputType == "date" {
		if len(dateStr) == 7 {
			return dateStr + "-01", true
		}
		if len(dateStr) == 4 {
			return dateStr + "-01-01", true
		}
		return dateStr, true
	}

	// Text format hints in label, hint, or name
	rawContext := strings.ToLower(f.Label + " " + f.Hint + " " + f.Name)
	normContext := normLabel + " " + normHint
	if strings.Contains(rawContext, "mm/yyyy") || strings.Contains(rawContext, "mm / yyyy") || strings.Contains(normContext, "mm yyyy") {
		if len(dateStr) >= 7 && dateStr[4] == '-' {
			return dateStr[5:7] + "/" + dateStr[:4], true
		}
	}
	if strings.Contains(rawContext, "dd/mm/yyyy") || strings.Contains(rawContext, "tt.mm.jjjj") || strings.Contains(normContext, "dd mm yyyy") {
		if len(dateStr) >= 7 && dateStr[4] == '-' {
			return "01/" + dateStr[5:7] + "/" + dateStr[:4], true
		}
	}
	if strings.Contains(rawContext, "yyyy/mm") || strings.Contains(normContext, "yyyy mm") {
		if len(dateStr) >= 7 && dateStr[4] == '-' {
			return dateStr[:4] + "/" + dateStr[5:7], true
		}
	}

	return dateStr, true
}
