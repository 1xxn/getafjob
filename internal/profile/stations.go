package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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

// MonthDate cleans a station date and brings month notations to YYYY-MM, the form the planner fills from. Anything
// else (YYYY-MM, YYYY, free text) stays as written.
func MonthDate(s string) string {
	s = cleanLine(s)
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

// Stations manages work experiences and educations with envelope encryption.
type Stations struct {
	store  StationStore
	sealer Sealer
	now    func() time.Time
}

// NewStations returns a new stations service.
func NewStations(store StationStore, sealer Sealer) *Stations {
	return &Stations{store: store, sealer: sealer, now: time.Now}
}

func workBulletPurpose(id uuid.UUID) string { return "station:work:" + id.String() }

// WorkExperiences lists the user's work experiences in sort order.
func (s *Stations) WorkExperiences(ctx context.Context, userID uuid.UUID) ([]WorkExperience, error) {
	rows, err := s.store.ListWorkExperiences(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]WorkExperience, 0, len(rows))
	for _, r := range rows {
		we := WorkExperience{
			ID:            r.ID,
			Company:       r.Company,
			Title:         r.Title,
			Location:      r.Location,
			WorkplaceType: r.WorkplaceType,
			StartDate:     r.StartDate,
			EndDate:       r.EndDate,
			IsCurrent:     r.IsCurrent,
			SortOrder:     r.SortOrder,
		}
		if len(r.SealedBullets) > 0 {
			if s.sealer != nil {
				plain, err := s.sealer.Open(ctx, userID, workBulletPurpose(r.ID), r.SealedBullets)
				if err != nil {
					return nil, err
				}
				_ = json.Unmarshal(plain, &we.DescriptionBullets)
			} else {
				_ = json.Unmarshal(r.SealedBullets, &we.DescriptionBullets)
			}
		}
		out = append(out, we)
	}
	return out, nil
}

// GetWorkExperience returns one work experience by ID.
func (s *Stations) GetWorkExperience(ctx context.Context, userID, id uuid.UUID) (WorkExperience, error) {
	r, err := s.store.GetWorkExperience(ctx, userID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return WorkExperience{}, ErrStationNotFound
		}
		return WorkExperience{}, err
	}
	we := WorkExperience{
		ID:            r.ID,
		Company:       r.Company,
		Title:         r.Title,
		Location:      r.Location,
		WorkplaceType: r.WorkplaceType,
		StartDate:     r.StartDate,
		EndDate:       r.EndDate,
		IsCurrent:     r.IsCurrent,
		SortOrder:     r.SortOrder,
	}
	if len(r.SealedBullets) > 0 {
		if s.sealer != nil {
			plain, err := s.sealer.Open(ctx, userID, workBulletPurpose(r.ID), r.SealedBullets)
			if err != nil {
				return WorkExperience{}, err
			}
			_ = json.Unmarshal(plain, &we.DescriptionBullets)
		} else {
			_ = json.Unmarshal(r.SealedBullets, &we.DescriptionBullets)
		}
	}
	return we, nil
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
		cnt, err := s.store.CountWorkExperiences(ctx, userID)
		if err != nil {
			return uuid.Nil, err
		}
		if cnt >= MaxStations {
			return uuid.Nil, ErrTooManyStations
		}
		id, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, err
		}
		w.ID = id
		w.SortOrder = cnt
	}
	var sealedBullets []byte
	if len(w.DescriptionBullets) > 0 {
		plain, err := json.Marshal(w.DescriptionBullets)
		if err != nil {
			return uuid.Nil, err
		}
		if s.sealer != nil {
			sealedBullets, err = s.sealer.Seal(ctx, userID, workBulletPurpose(w.ID), plain)
			if err != nil {
				return uuid.Nil, err
			}
		} else {
			sealedBullets = plain
		}
	}
	now := s.now()
	st := StoredWorkExperience{
		ID:            w.ID,
		UserID:        userID,
		Company:       w.Company,
		Title:         w.Title,
		Location:      w.Location,
		WorkplaceType: w.WorkplaceType,
		StartDate:     w.StartDate,
		EndDate:       w.EndDate,
		IsCurrent:     w.IsCurrent,
		SealedBullets: sealedBullets,
		SortOrder:     w.SortOrder,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if insert {
		if err := s.store.InsertWorkExperience(ctx, st); err != nil {
			return uuid.Nil, err
		}
		return w.ID, nil
	}
	ok, err := s.store.UpdateWorkExperience(ctx, st)
	if err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, ErrStationNotFound
	}
	return w.ID, nil
}

// DeleteWorkExperience removes a work experience.
func (s *Stations) DeleteWorkExperience(ctx context.Context, userID, id uuid.UUID) error {
	ok, err := s.store.DeleteWorkExperience(ctx, userID, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStationNotFound
	}
	return nil
}

// MoveWorkExperience moves an entry up or down in sort order.
func (s *Stations) MoveWorkExperience(ctx context.Context, userID, id uuid.UUID, direction string) error {
	all, err := s.WorkExperiences(ctx, userID)
	if err != nil {
		return err
	}
	idx := -1
	for i, item := range all {
		if item.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrStationNotFound
	}
	target := idx
	if direction == "up" && idx > 0 {
		target = idx - 1
	} else if direction == "down" && idx < len(all)-1 {
		target = idx + 1
	}
	if target == idx {
		return nil
	}
	now := s.now()
	// swap sort orders
	_, err1 := s.store.UpdateWorkExperienceOrder(ctx, userID, all[idx].ID, target, now)
	if err1 != nil {
		return err1
	}
	_, err2 := s.store.UpdateWorkExperienceOrder(ctx, userID, all[target].ID, idx, now)
	return err2
}

// Educations lists the user's education entries in sort order.
func (s *Stations) Educations(ctx context.Context, userID uuid.UUID) ([]EducationStation, error) {
	rows, err := s.store.ListEducations(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]EducationStation, 0, len(rows))
	for _, r := range rows {
		out = append(out, EducationStation{
			ID:           r.ID,
			Institution:  r.Institution,
			Degree:       r.Degree,
			FieldOfStudy: r.FieldOfStudy,
			StartDate:    r.StartDate,
			EndDate:      r.EndDate,
			IsCurrent:    r.IsCurrent,
			Grade:        r.Grade,
			SortOrder:    r.SortOrder,
		})
	}
	return out, nil
}

// GetEducation returns one education entry by ID.
func (s *Stations) GetEducation(ctx context.Context, userID, id uuid.UUID) (EducationStation, error) {
	r, err := s.store.GetEducation(ctx, userID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return EducationStation{}, ErrStationNotFound
		}
		return EducationStation{}, err
	}
	return EducationStation{
		ID:           r.ID,
		Institution:  r.Institution,
		Degree:       r.Degree,
		FieldOfStudy: r.FieldOfStudy,
		StartDate:    r.StartDate,
		EndDate:      r.EndDate,
		IsCurrent:    r.IsCurrent,
		Grade:        r.Grade,
		SortOrder:    r.SortOrder,
	}, nil
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
		cnt, err := s.store.CountEducations(ctx, userID)
		if err != nil {
			return uuid.Nil, err
		}
		if cnt >= MaxStations {
			return uuid.Nil, ErrTooManyStations
		}
		id, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, err
		}
		e.ID = id
		e.SortOrder = cnt
	}
	now := s.now()
	st := StoredEducation{
		ID:           e.ID,
		UserID:       userID,
		Institution:  e.Institution,
		Degree:       e.Degree,
		FieldOfStudy: e.FieldOfStudy,
		StartDate:    e.StartDate,
		EndDate:      e.EndDate,
		IsCurrent:    e.IsCurrent,
		Grade:        e.Grade,
		SortOrder:    e.SortOrder,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if insert {
		if err := s.store.InsertEducation(ctx, st); err != nil {
			return uuid.Nil, err
		}
		return e.ID, nil
	}
	ok, err := s.store.UpdateEducation(ctx, st)
	if err != nil {
		return uuid.Nil, err
	}
	if !ok {
		return uuid.Nil, ErrStationNotFound
	}
	return e.ID, nil
}

// DeleteEducation removes an education entry.
func (s *Stations) DeleteEducation(ctx context.Context, userID, id uuid.UUID) error {
	ok, err := s.store.DeleteEducation(ctx, userID, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrStationNotFound
	}
	return nil
}

// MoveEducation moves an education entry up or down in sort order.
func (s *Stations) MoveEducation(ctx context.Context, userID, id uuid.UUID, direction string) error {
	all, err := s.Educations(ctx, userID)
	if err != nil {
		return err
	}
	idx := -1
	for i, item := range all {
		if item.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return ErrStationNotFound
	}
	target := idx
	if direction == "up" && idx > 0 {
		target = idx - 1
	} else if direction == "down" && idx < len(all)-1 {
		target = idx + 1
	}
	if target == idx {
		return nil
	}
	now := s.now()
	_, err1 := s.store.UpdateEducationOrder(ctx, userID, all[idx].ID, target, now)
	if err1 != nil {
		return err1
	}
	_, err2 := s.store.UpdateEducationOrder(ctx, userID, all[target].ID, idx, now)
	return err2
}

// ReorderWorkExperiences puts the user's positions in the given order; ids must name each of them exactly once.
func (s *Stations) ReorderWorkExperiences(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error {
	all, err := s.WorkExperiences(ctx, userID)
	if err != nil {
		return err
	}
	have := make([]uuid.UUID, len(all))
	for i, w := range all {
		have[i] = w.ID
	}
	if !sameIDs(have, ids) {
		return ErrStationNotFound
	}
	now := s.now()
	for i, id := range ids {
		if _, err := s.store.UpdateWorkExperienceOrder(ctx, userID, id, i, now); err != nil {
			return err
		}
	}
	return nil
}

// ReorderEducations puts the user's degrees in the given order; ids must name each of them exactly once.
func (s *Stations) ReorderEducations(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) error {
	all, err := s.Educations(ctx, userID)
	if err != nil {
		return err
	}
	have := make([]uuid.UUID, len(all))
	for i, e := range all {
		have[i] = e.ID
	}
	if !sameIDs(have, ids) {
		return ErrStationNotFound
	}
	now := s.now()
	for i, id := range ids {
		if _, err := s.store.UpdateEducationOrder(ctx, userID, id, i, now); err != nil {
			return err
		}
	}
	return nil
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
