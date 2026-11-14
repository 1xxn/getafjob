package profile

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memoryStationStore struct {
	works []StoredWorkExperience
	edus  []StoredEducation
}

func (m *memoryStationStore) ListWorkExperiences(ctx context.Context, userID uuid.UUID) ([]StoredWorkExperience, error) {
	var out []StoredWorkExperience
	for _, w := range m.works {
		if w.UserID == userID {
			out = append(out, w)
		}
	}
	slices.SortFunc(out, func(a, b StoredWorkExperience) int {
		return a.SortOrder - b.SortOrder
	})
	return out, nil
}

func (m *memoryStationStore) GetWorkExperience(ctx context.Context, userID, id uuid.UUID) (StoredWorkExperience, error) {
	for _, w := range m.works {
		if w.UserID == userID && w.ID == id {
			return w, nil
		}
	}
	return StoredWorkExperience{}, ErrNotFound
}

func (m *memoryStationStore) InsertWorkExperience(ctx context.Context, w StoredWorkExperience) error {
	m.works = append(m.works, w)
	return nil
}

func (m *memoryStationStore) UpdateWorkExperience(ctx context.Context, w StoredWorkExperience) (bool, error) {
	for i, existing := range m.works {
		if existing.UserID == w.UserID && existing.ID == w.ID {
			m.works[i] = w
			return true, nil
		}
	}
	return false, nil
}

func (m *memoryStationStore) UpdateWorkExperienceOrder(ctx context.Context, userID, id uuid.UUID, sortOrder int, _ time.Time) (bool, error) {
	for i, existing := range m.works {
		if existing.UserID == userID && existing.ID == id {
			m.works[i].SortOrder = sortOrder
			return true, nil
		}
	}
	return false, nil
}

func (m *memoryStationStore) DeleteWorkExperience(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	for i, existing := range m.works {
		if existing.UserID == userID && existing.ID == id {
			m.works = append(m.works[:i], m.works[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *memoryStationStore) CountWorkExperiences(ctx context.Context, userID uuid.UUID) (int, error) {
	cnt := 0
	for _, w := range m.works {
		if w.UserID == userID {
			cnt++
		}
	}
	return cnt, nil
}

func (m *memoryStationStore) ListEducations(ctx context.Context, userID uuid.UUID) ([]StoredEducation, error) {
	var out []StoredEducation
	for _, e := range m.edus {
		if e.UserID == userID {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b StoredEducation) int {
		return a.SortOrder - b.SortOrder
	})
	return out, nil
}

func (m *memoryStationStore) GetEducation(ctx context.Context, userID, id uuid.UUID) (StoredEducation, error) {
	for _, e := range m.edus {
		if e.UserID == userID && e.ID == id {
			return e, nil
		}
	}
	return StoredEducation{}, ErrNotFound
}

func (m *memoryStationStore) InsertEducation(ctx context.Context, e StoredEducation) error {
	m.edus = append(m.edus, e)
	return nil
}

func (m *memoryStationStore) UpdateEducation(ctx context.Context, e StoredEducation) (bool, error) {
	for i, existing := range m.edus {
		if existing.UserID == e.UserID && existing.ID == e.ID {
			m.edus[i] = e
			return true, nil
		}
	}
	return false, nil
}

func (m *memoryStationStore) UpdateEducationOrder(ctx context.Context, userID, id uuid.UUID, sortOrder int, _ time.Time) (bool, error) {
	for i, existing := range m.edus {
		if existing.UserID == userID && existing.ID == id {
			m.edus[i].SortOrder = sortOrder
			return true, nil
		}
	}
	return false, nil
}

func (m *memoryStationStore) DeleteEducation(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	for i, existing := range m.edus {
		if existing.UserID == userID && existing.ID == id {
			m.edus = append(m.edus[:i], m.edus[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *memoryStationStore) CountEducations(ctx context.Context, userID uuid.UUID) (int, error) {
	cnt := 0
	for _, e := range m.edus {
		if e.UserID == userID {
			cnt++
		}
	}
	return cnt, nil
}

func TestStationsValidation(t *testing.T) {
	// Work experience validation
	badWork := WorkExperience{WorkplaceType: "invalid_type"}
	errs := badWork.Validate()
	if len(errs) == 0 {
		t.Fatal("expected validation errors for empty title/company and bad workplace type")
	}

	goodWork := WorkExperience{
		Title:              "Senior Backend Engineer",
		Company:            "Muster GmbH",
		Location:           "Berlin",
		WorkplaceType:      "hybrid",
		StartDate:          "2022-01",
		IsCurrent:          true,
		DescriptionBullets: []string{"Built high throughput APIs", "Maintained PostgreSQL"},
	}
	if errs := goodWork.Validate(); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %+v", errs)
	}

	// Education validation
	badEdu := EducationStation{}
	if errs := badEdu.Validate(); len(errs) == 0 {
		t.Fatal("expected validation errors for empty institution/degree")
	}

	goodEdu := EducationStation{
		Institution:  "Technical University of Munich",
		Degree:       "M.Sc.",
		FieldOfStudy: "Computer Science",
		StartDate:    "2018",
		EndDate:      "2020",
		Grade:        "1.3",
	}
	if errs := goodEdu.Validate(); len(errs) != 0 {
		t.Fatalf("unexpected validation errors: %+v", errs)
	}
}

func TestStationsCRUDAndOrdering(t *testing.T) {
	ctx := context.Background()
	store := &memoryStationStore{}
	svc := NewStations(store, nil)
	user := uuid.New()

	// 1. Create work experience
	id1, err := svc.SaveWorkExperience(ctx, user, WorkExperience{
		Company:   "Firma A",
		Title:     "Dev",
		StartDate: "2020",
		EndDate:   "2022",
	})
	if err != nil {
		t.Fatal(err)
	}

	id2, err := svc.SaveWorkExperience(ctx, user, WorkExperience{
		Company:   "Firma B",
		Title:     "Senior Dev",
		StartDate: "2022",
		IsCurrent: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	list, err := svc.WorkExperiences(ctx, user)
	if err != nil || len(list) != 2 {
		t.Fatalf("expected 2 work experiences, got %d, err %v", len(list), err)
	}
	if list[0].ID != id1 || list[1].ID != id2 {
		t.Fatalf("unexpected order: %+v", list)
	}

	// 2. Move up/down
	if err := svc.MoveWorkExperience(ctx, user, id2, "up"); err != nil {
		t.Fatal(err)
	}
	list, err = svc.WorkExperiences(ctx, user)
	if err != nil || list[0].ID != id2 {
		t.Fatalf("expected id2 to be first after move up, got: %+v", list)
	}

	// 3. Delete
	if err := svc.DeleteWorkExperience(ctx, user, id1); err != nil {
		t.Fatal(err)
	}
	list, err = svc.WorkExperiences(ctx, user)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected 1 work experience remaining, got %d", len(list))
	}

	// 4. Educations
	eduID, err := svc.SaveEducation(ctx, user, EducationStation{
		Institution:  "University X",
		Degree:       "B.Sc.",
		FieldOfStudy: "Informatics",
		StartDate:    "2015",
		EndDate:      "2018",
	})
	if err != nil {
		t.Fatal(err)
	}
	edus, err := svc.Educations(ctx, user)
	if err != nil || len(edus) != 1 || edus[0].ID != eduID {
		t.Fatalf("expected 1 education with id %v, got %+v", eduID, edus)
	}
	if err := svc.DeleteEducation(ctx, user, eduID); err != nil {
		t.Fatal(err)
	}
	edus, err = svc.Educations(ctx, user)
	if err != nil || len(edus) != 0 {
		t.Fatalf("expected 0 educations, got %d", len(edus))
	}
	if err := svc.DeleteEducation(ctx, user, uuid.New()); !errors.Is(err, ErrStationNotFound) {
		t.Fatalf("expected ErrStationNotFound, got %v", err)
	}
}

// Month notations from CVs become YYYY-MM, the form the planner fills from; other dates stay as written.
func TestMonthDate(t *testing.T) {
	for in, want := range map[string]string{
		"01/2021": "2021-01", "4.2019": "2019-04", " 12-2018 ": "2018-12", "2021/04": "2021-04",
		"2021-04": "2021-04", "2021": "2021", "13/2021": "13/2021", "Jan 2021": "Jan 2021", "": "",
	} {
		if got := MonthDate(in); got != want {
			t.Errorf("MonthDate(%q) = %q, want %q", in, got, want)
		}
	}
}
