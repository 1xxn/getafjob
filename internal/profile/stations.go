package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrStationNotFound reports that the requested work experience or education station was not found.
	ErrStationNotFound = errors.New("profile: station not found")
	// ErrTooManyStations reports exceeding the station limit per category.
	ErrTooManyStations = errors.New("profile: too many stations")
)

// MaxStations limits how many entries a user may keep per category.
const MaxStations = 100

// Workplace types for WorkExperience.
const (
	WorkplaceRemote = "remote"
	WorkplaceHybrid = "hybrid"
	WorkplaceOnsite = "onsite"
)

// monthDateRx matches the month and year notations CVs use: 04/2021, 4.2021, 04-2021 and 2021/04.
var monthDateRx = regexp.MustCompile(`^(?:(\d{1,2})[./-](\d{4})|(\d{4})/(\d{1,2}))$`)

// monthNameRx matches a month name and a year as English and German CVs write them: Jan 2021, Jan. 2021,
// January 2021, März 2021, Sept. 2019.
var monthNameRx = regexp.MustCompile(`(?i)^([a-zä]{3,9})\.?\s+(\d{4})$`)

// monthNames maps the first letters of a month name (English or German) to its number.
var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "mär": 3, "apr": 4, "may": 5, "mai": 5, "jun": 6,
	"jul": 7, "aug": 8, "sep": 9, "oct": 10, "okt": 10, "nov": 11, "dec": 12, "dez": 12}

// MonthDate cleans a station date and brings month notations to YYYY-MM, the form the planner fills from. Anything
// else (YYYY-MM, YYYY, free text) stays as written.
func MonthDate(s string) string {
	s = cleanLine(s)
	if n := monthNameRx.FindStringSubmatch(s); n != nil {
		if month, ok := monthNames[strings.ToLower(string([]rune(n[1])[:3]))]; ok {
			return fmt.Sprintf("%s-%02d", n[2], month)
		}
		return s
	}
	m := monthDateRx.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	month, year := m[1], m[2]
	if year == "" {
		month, year = m[4], m[3]
	}
	n, _ := strconv.Atoi(month)
	if n < 1 || n > 12 {
		return s
	}
	return fmt.Sprintf("%s-%02d", year, n)
}

// WorkExperience represents a structured employment position station.
type WorkExperience struct {
	ID                 uuid.UUID `json:"id"`
	Company            string    `json:"company"`
	Title              string    `json:"title"`
	Location           string    `json:"location,omitempty"`
	WorkplaceType      string    `json:"workplace_type,omitempty"` // "", "remote", "hybrid", "onsite"
	StartDate          string    `json:"start_date"`
	EndDate            string    `json:"end_date,omitempty"`
	IsCurrent          bool      `json:"is_current"`
	DescriptionBullets []string  `json:"description_bullets,omitempty"`
	SortOrder          int       `json:"sort_order"`
}

// Validate checks work experience fields.
func (w WorkExperience) Validate() []FieldError {
	var c checker
	c.text("company", w.Company, maxShort)
	c.text("title", w.Title, maxShort)
	c.text("location", w.Location, maxShort)
	c.text("start_date", w.StartDate, 30)
	c.text("end_date", w.EndDate, 30)
	if strings.TrimSpace(w.Company) == "" && strings.TrimSpace(w.Title) == "" {
		c.add("title", "profile.error.required")
	}
	switch w.WorkplaceType {
	case "", "remote", "hybrid", "onsite":
	default:
		c.add("workplace_type", "profile.error.invalid")
	}
	if len(w.DescriptionBullets) > 50 {
		c.add("description_bullets", "profile.error.too_many")
	}
	for _, b := range w.DescriptionBullets {
		c.text("description_bullets", b, 2000)
	}
	return c.errs
}

// EducationStation represents a structured educational station.
type EducationStation struct {
	ID           uuid.UUID `json:"id"`
	Institution  string    `json:"institution"`
	Degree       string    `json:"degree"`
	FieldOfStudy string    `json:"field_of_study,omitempty"`
	StartDate    string    `json:"start_date,omitempty"`
	EndDate      string    `json:"end_date,omitempty"`
	IsCurrent    bool      `json:"is_current"`
	Grade        string    `json:"grade,omitempty"`
	SortOrder    int       `json:"sort_order"`
}

// Validate checks education station fields.
func (e EducationStation) Validate() []FieldError {
	var c checker
	c.text("institution", e.Institution, maxShort)
	c.text("degree", e.Degree, maxShort)
	c.text("field_of_study", e.FieldOfStudy, maxShort)
	c.text("start_date", e.StartDate, 30)
	c.text("end_date", e.EndDate, 30)
	c.text("grade", e.Grade, 50)
	if strings.TrimSpace(e.Institution) == "" && strings.TrimSpace(e.Degree) == "" {
		c.add("institution", "profile.error.required")
	}
	return c.errs
}

// Stations manages work experiences and educations. Each entry is sealed as a whole with the user's data key; only
// its place in the list is readable.
type Stations struct {
	store  StationStore
	sealer Sealer
	now    func() time.Time
}

// NewStations returns a new stations service.
func NewStations(store StationStore, sealer Sealer) *Stations {
	return &Stations{store: store, sealer: sealer, now: time.Now}
}

func workPurpose(id uuid.UUID) string      { return "station:work-entry:" + id.String() }
func educationPurpose(id uuid.UUID) string { return "station:education:" + id.String() }

// workBulletPurpose sealed the bullets of a row from before sealing (#559).
func workBulletPurpose(id uuid.UUID) string { return "station:work:" + id.String() }

func (s *Stations) seal(ctx context.Context, userID uuid.UUID, purpose string, v any) ([]byte, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return s.sealer.Seal(ctx, userID, purpose, plain)
}

func (s *Stations) open(ctx context.Context, userID uuid.UUID, purpose string, sealed []byte, v any) error {
	plain, err := s.sealer.Open(ctx, userID, purpose, sealed)
	if err != nil {
		return err
	}
	return json.Unmarshal(plain, v)
}

// work turns a row into an entry; a row from before sealing is read from its plain columns.
func (s *Stations) work(ctx context.Context, userID uuid.UUID, r StoredWorkExperience) (WorkExperience, error) {
	var w WorkExperience
	if r.Sealed != nil {
		if err := s.open(ctx, userID, workPurpose(r.ID), r.Sealed, &w); err != nil {
			return WorkExperience{}, err
		}
	} else {
		w = WorkExperience{Company: r.Company, Title: r.Title, Location: r.Location, WorkplaceType: r.WorkplaceType,
			StartDate: r.StartDate, EndDate: r.EndDate, IsCurrent: r.IsCurrent}
		if len(r.SealedBullets) > 0 {
			if err := s.open(ctx, userID, workBulletPurpose(r.ID), r.SealedBullets, &w.DescriptionBullets); err != nil {
				return WorkExperience{}, err
			}
		}
	}
	w.ID, w.SortOrder = r.ID, r.SortOrder
	return w, nil
}

// education turns a row into an entry; a row from before sealing is read from its plain columns.
func (s *Stations) education(ctx context.Context, userID uuid.UUID, r StoredEducation) (EducationStation, error) {
	var e EducationStation
	if r.Sealed != nil {
		if err := s.open(ctx, userID, educationPurpose(r.ID), r.Sealed, &e); err != nil {
			return EducationStation{}, err
		}
	} else {
		e = EducationStation{Institution: r.Institution, Degree: r.Degree, FieldOfStudy: r.FieldOfStudy,
			StartDate: r.StartDate, EndDate: r.EndDate, IsCurrent: r.IsCurrent, Grade: r.Grade}
	}
	e.ID, e.SortOrder = r.ID, r.SortOrder
	return e, nil
}

// WorkExperiences lists the user's work experiences in sort order.
func (s *Stations) WorkExperiences(ctx context.Context, userID uuid.UUID) ([]WorkExperience, error) {
	rows, err := s.store.ListWorkExperiences(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]WorkExperience, 0, len(rows))
	for _, r := range rows {
		w, err := s.work(ctx, userID, r)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, nil
}

// GetWorkExperience returns one work experience by ID.
func (s *Stations) GetWorkExperience(ctx context.Context, userID, id uuid.UUID) (WorkExperience, error) {
	r, err := s.store.GetWorkExperience(ctx, userID, id)
	if errors.Is(err, ErrNotFound) {
		return WorkExperience{}, ErrStationNotFound
	}
	if err != nil {
		return WorkExperience{}, err
	}
	return s.work(ctx, userID, r)
}

// SaveWorkExperience creates (ID nil) or updates a work experience.
func (s *Stations) SaveWorkExperience(ctx context.Context, userID uuid.UUID, w WorkExperience) (uuid.UUID, error) {
	w.Company = cleanLine(w.Company)
	w.Title = cleanLine(w.Title)
	w.Location = cleanLine(w.Location)
	w.StartDate = MonthDate(w.StartDate)
	w.EndDate = MonthDate(w.EndDate)
	w.WorkplaceType = strings.TrimSpace(strings.ToLower(w.WorkplaceType))
	if errs := w.Validate(); len(errs) > 0 {
		return uuid.Nil, &ValidationError{Fields: errs}
	}
	insert := w.ID == uuid.Nil
	if insert {
		id, order, err := s.newEntry(ctx, userID, s.store.CountWorkExperiences)
		if err != nil {
			return uuid.Nil, err
		}
		w.ID, w.SortOrder = id, order
	}
	return w.ID, s.writeWork(ctx, userID, w, insert)
}

// writeWork seals w and stores it.
func (s *Stations) writeWork(ctx context.Context, userID uuid.UUID, w WorkExperience, insert bool) error {
	sealed, err := s.seal(ctx, userID, workPurpose(w.ID), w)
	if err != nil {
		return err
	}
	now := s.now()
	st := StoredWorkExperience{ID: w.ID, UserID: userID, Sealed: sealed, SortOrder: w.SortOrder, CreatedAt: now, UpdatedAt: now}
	if insert {
		return s.store.InsertWorkExperience(ctx, st)
	}
	return found(s.store.UpdateWorkExperience(ctx, st))
}

// DeleteWorkExperience removes a work experience.
func (s *Stations) DeleteWorkExperience(ctx context.Context, userID, id uuid.UUID) error {
	return found(s.store.DeleteWorkExperience(ctx, userID, id))
}

// MoveWorkExperience moves an entry one place up or down.
func (s *Stations) MoveWorkExperience(ctx context.Context, userID, id uuid.UUID, direction string) error {
	ids, err := s.workIDs(ctx, userID)
	if err != nil {
		return err
	}
	return s.reorder(ctx, userID, ids, moved(ids, id, direction), s.store.UpdateWorkExperienceOrder)
}

// ReorderWorkExperiences puts the user's positions in the given order; ids must name each of them exactly once.
func (s *Stations) ReorderWorkExperiences(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error {
	have, err := s.workIDs(ctx, userID)
	if err != nil {
		return err
	}
	return s.reorder(ctx, userID, have, ids, s.store.UpdateWorkExperienceOrder)
}

func (s *Stations) workIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.store.ListWorkExperiences(ctx, userID)
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids, err
}

// Educations lists the user's education entries in sort order.
func (s *Stations) Educations(ctx context.Context, userID uuid.UUID) ([]EducationStation, error) {
	rows, err := s.store.ListEducations(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]EducationStation, 0, len(rows))
	for _, r := range rows {
		e, err := s.education(ctx, userID, r)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// GetEducation returns one education entry by ID.
func (s *Stations) GetEducation(ctx context.Context, userID, id uuid.UUID) (EducationStation, error) {
	r, err := s.store.GetEducation(ctx, userID, id)
	if errors.Is(err, ErrNotFound) {
		return EducationStation{}, ErrStationNotFound
	}
	if err != nil {
		return EducationStation{}, err
	}
	return s.education(ctx, userID, r)
}

// SaveEducation creates (ID nil) or updates an education station.
func (s *Stations) SaveEducation(ctx context.Context, userID uuid.UUID, e EducationStation) (uuid.UUID, error) {
	e.Institution = cleanLine(e.Institution)
	e.Degree = cleanLine(e.Degree)
	e.FieldOfStudy = cleanLine(e.FieldOfStudy)
	e.StartDate = MonthDate(e.StartDate)
	e.EndDate = MonthDate(e.EndDate)
	e.Grade = cleanLine(e.Grade)
	if errs := e.Validate(); len(errs) > 0 {
		return uuid.Nil, &ValidationError{Fields: errs}
	}
	insert := e.ID == uuid.Nil
	if insert {
		id, order, err := s.newEntry(ctx, userID, s.store.CountEducations)
		if err != nil {
			return uuid.Nil, err
		}
		e.ID, e.SortOrder = id, order
	}
	return e.ID, s.writeEducation(ctx, userID, e, insert)
}

// writeEducation seals e and stores it.
func (s *Stations) writeEducation(ctx context.Context, userID uuid.UUID, e EducationStation, insert bool) error {
	sealed, err := s.seal(ctx, userID, educationPurpose(e.ID), e)
	if err != nil {
		return err
	}
	now := s.now()
	st := StoredEducation{ID: e.ID, UserID: userID, Sealed: sealed, SortOrder: e.SortOrder, CreatedAt: now, UpdatedAt: now}
	if insert {
		return s.store.InsertEducation(ctx, st)
	}
	return found(s.store.UpdateEducation(ctx, st))
}

// DeleteEducation removes an education entry.
func (s *Stations) DeleteEducation(ctx context.Context, userID, id uuid.UUID) error {
	return found(s.store.DeleteEducation(ctx, userID, id))
}

// MoveEducation moves an education entry one place up or down.
func (s *Stations) MoveEducation(ctx context.Context, userID, id uuid.UUID, direction string) error {
	ids, err := s.educationIDs(ctx, userID)
	if err != nil {
		return err
	}
	return s.reorder(ctx, userID, ids, moved(ids, id, direction), s.store.UpdateEducationOrder)
}

// ReorderEducations puts the user's degrees in the given order; ids must name each of them exactly once.
func (s *Stations) ReorderEducations(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error {
	have, err := s.educationIDs(ctx, userID)
	if err != nil {
		return err
	}
	return s.reorder(ctx, userID, have, ids, s.store.UpdateEducationOrder)
}

func (s *Stations) educationIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.store.ListEducations(ctx, userID)
	ids := make([]uuid.UUID, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return ids, err
}

// SealUnsealed seals the rows stored before sealing (#559) and blanks their plain columns; it returns how many it
// sealed. The worker runs it until none are left. Rows are written as they are, without validation, so an old row
// that the rules would refuse today still gets sealed.
func (s *Stations) SealUnsealed(ctx context.Context) (int, error) {
	users, err := s.store.UnsealedStationUsers(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, userID := range users {
		works, err := s.store.ListWorkExperiences(ctx, userID)
		if err != nil {
			return n, err
		}
		for _, r := range works {
			if r.Sealed != nil {
				continue
			}
			w, err := s.work(ctx, userID, r)
			if err == nil {
				err = s.writeWork(ctx, userID, w, false)
			}
			if err != nil {
				return n, fmt.Errorf("seal position %s: %w", r.ID, err)
			}
			n++
		}
		edus, err := s.store.ListEducations(ctx, userID)
		if err != nil {
			return n, err
		}
		for _, r := range edus {
			if r.Sealed != nil {
				continue
			}
			e, err := s.education(ctx, userID, r)
			if err == nil {
				err = s.writeEducation(ctx, userID, e, false)
			}
			if err != nil {
				return n, fmt.Errorf("seal degree %s: %w", r.ID, err)
			}
			n++
		}
	}
	return n, nil
}

// newEntry returns the ID and list place of a new entry, or ErrTooManyStations.
func (s *Stations) newEntry(ctx context.Context, userID uuid.UUID, count func(context.Context, uuid.UUID) (int, error)) (uuid.UUID, int, error) {
	n, err := count(ctx, userID)
	if err != nil {
		return uuid.Nil, 0, err
	}
	if n >= MaxStations {
		return uuid.Nil, 0, ErrTooManyStations
	}
	id, err := uuid.NewV7()
	return id, n, err
}

// reorder stores want as the order of the entries have (both every ID once) and numbers them from 0.
func (s *Stations) reorder(ctx context.Context, userID uuid.UUID, have, want []uuid.UUID,
	update func(context.Context, uuid.UUID, uuid.UUID, int, time.Time) (bool, error)) error {
	if want == nil || !sameIDs(have, want) {
		return ErrStationNotFound
	}
	now := s.now()
	for i, id := range want {
		if _, err := update(ctx, userID, id, i, now); err != nil {
			return err
		}
	}
	return nil
}

// moved returns ids with id one place up or down (unchanged at the edge), or nil if id is not in ids.
func moved(ids []uuid.UUID, id uuid.UUID, direction string) []uuid.UUID {
	i := slices.Index(ids, id)
	if i < 0 {
		return nil
	}
	out := slices.Clone(ids)
	switch {
	case direction == "up" && i > 0:
		out[i-1], out[i] = out[i], out[i-1]
	case direction == "down" && i < len(out)-1:
		out[i+1], out[i] = out[i], out[i+1]
	}
	return out
}

// found turns an update or delete that hit no row into ErrStationNotFound.
func found(ok bool, err error) error {
	if err == nil && !ok {
		return ErrStationNotFound
	}
	return err
}

// sameIDs reports whether b holds exactly the IDs of a, each once, in any order.
func sameIDs(a, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[uuid.UUID]bool, len(a))
	for _, id := range a {
		seen[id] = true
	}
	for _, id := range b {
		if !seen[id] {
			return false
		}
		delete(seen, id)
	}
	return true
}
