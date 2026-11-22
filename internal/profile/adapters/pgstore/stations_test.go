package pgstore_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
)

func TestStationsPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	store := pgstore.New(pool)
	sealer := sealer(t, pool)
	stations := profile.NewStations(store, sealer)

	max, erika := user(t, pool, "max.stations@example.org"), user(t, pool, "erika.stations@example.org")

	// 1. Work Experience CRUD & Sealing
	wid, err := stations.SaveWorkExperience(ctx, max, profile.WorkExperience{
		Company:            "Muster Corp",
		Title:              "Lead Engineer",
		Location:           "Munich",
		WorkplaceType:      "hybrid",
		StartDate:          "2021-03",
		IsCurrent:          true,
		DescriptionBullets: []string{"Designed distributed streaming pipeline", "Managed 10 direct reports"},
	})
	if err != nil {
		t.Fatalf("save work experience: %v", err)
	}

	works, err := stations.WorkExperiences(ctx, max)
	if err != nil || len(works) != 1 {
		t.Fatalf("expected 1 work experience, got %d, err %v", len(works), err)
	}
	if works[0].Title != "Lead Engineer" || len(works[0].DescriptionBullets) != 2 {
		t.Fatalf("unexpected work experience: %+v", works[0])
	}

	// Sealed at rest: no plain column holds the entry.
	var sealed []byte
	var company, title string
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT sealed, company, title FROM work_experiences WHERE id = $1", wid).Scan(&sealed, &company, &title)
	}); err != nil {
		t.Fatalf("read raw row: %v", err)
	}
	if len(sealed) == 0 || company != "" || title != "" || bytes.Contains(sealed, []byte("Muster Corp")) {
		t.Fatalf("entry not sealed at rest: company %q, title %q", company, title)
	}

	// 2. Tenant isolation on work experiences (Erika cannot see or delete Max's entry)
	erikaWorks, err := stations.WorkExperiences(ctx, erika)
	if err != nil || len(erikaWorks) != 0 {
		t.Fatalf("erika should see 0 entries, got %+v, err %v", erikaWorks, err)
	}
	if err := stations.DeleteWorkExperience(ctx, erika, wid); !errors.Is(err, profile.ErrStationNotFound) {
		t.Fatalf("erika should not be able to delete max's entry: %v", err)
	}

	// 3. Educations CRUD
	eid, err := stations.SaveEducation(ctx, max, profile.EducationStation{
		Institution:  "TU Munich",
		Degree:       "M.Sc.",
		FieldOfStudy: "Informatics",
		StartDate:    "2017",
		EndDate:      "2019",
		Grade:        "1.2",
	})
	if err != nil {
		t.Fatalf("save education: %v", err)
	}

	edus, err := stations.Educations(ctx, max)
	if err != nil || len(edus) != 1 || edus[0].ID != eid {
		t.Fatalf("expected 1 education with id %v, got %+v", eid, edus)
	}

	// Tenant isolation on educations
	erikaEdus, err := stations.Educations(ctx, erika)
	if err != nil || len(erikaEdus) != 0 {
		t.Fatalf("erika should see 0 educations, got %+v", erikaEdus)
	}
	if err := stations.DeleteEducation(ctx, erika, eid); !errors.Is(err, profile.ErrStationNotFound) {
		t.Fatalf("erika should not be able to delete max's education: %v", err)
	}

	// Cleanup
	if err := stations.DeleteWorkExperience(ctx, max, wid); err != nil {
		t.Fatalf("delete work experience: %v", err)
	}
	if err := stations.DeleteEducation(ctx, max, eid); err != nil {
		t.Fatalf("delete education: %v", err)
	}
}

// TestStationEditKeepsOrder: saving an edited station (the forms carry no sort order) leaves it in its place.
func TestStationEditKeepsOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	stations := profile.NewStations(pgstore.New(pool), sealer(t, pool))
	max := user(t, pool, "max.order@example.org")

	var works []uuid.UUID
	for _, title := range []string{"First", "Second", "Third"} {
		id, err := stations.SaveWorkExperience(ctx, max, profile.WorkExperience{Title: title})
		if err != nil {
			t.Fatal(err)
		}
		works = append(works, id)
	}
	if _, err := stations.SaveWorkExperience(ctx, max, profile.WorkExperience{ID: works[2], Title: "Third, edited"}); err != nil {
		t.Fatal(err)
	}
	list, err := stations.WorkExperiences(ctx, max)
	if err != nil || len(list) != 3 || list[2].ID != works[2] || list[2].Title != "Third, edited" {
		t.Fatalf("order after edit: %+v, %v", list, err)
	}

	var edus []uuid.UUID
	for _, degree := range []string{"B.Sc.", "M.Sc."} {
		id, err := stations.SaveEducation(ctx, max, profile.EducationStation{Degree: degree})
		if err != nil {
			t.Fatal(err)
		}
		edus = append(edus, id)
	}
	if _, err := stations.SaveEducation(ctx, max, profile.EducationStation{ID: edus[1], Degree: "M.Sc. Informatics"}); err != nil {
		t.Fatal(err)
	}
	el, err := stations.Educations(ctx, max)
	if err != nil || len(el) != 2 || el[1].ID != edus[1] {
		t.Fatalf("education order after edit: %+v, %v", el, err)
	}
}

// TestSealUnsealedStations: rows from before sealing (#559) read as before, and the worker seals them and blanks
// their plain columns.
func TestSealUnsealedStations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping postgres integration test in short mode")
	}
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	sl := sealer(t, pool)
	stations := profile.NewStations(pgstore.New(pool), sl)
	max := user(t, pool, "max.legacy@example.org")

	wid, eid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	bullets, err := sl.Seal(ctx, max, "station:work:"+wid.String(), []byte(`["Ran the platform team"]`))
	if err != nil {
		t.Fatal(err)
	}
	err = db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO work_experiences (id, user_id, company, title, start_date, sealed_bullets, sort_order, created_at, updated_at)
			VALUES ($1, $2, 'Muster AG', 'Lead', '2020-01', $3, 0, now(), now())`, wid, max, bullets); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO educations (id, user_id, institution, degree, grade, sort_order, created_at, updated_at)
			VALUES ($1, $2, 'TU Musterstadt', 'M.Sc.', '1.3', 0, now(), now())`, eid, max)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		t.Helper()
		works, err := stations.WorkExperiences(ctx, max)
		if err != nil || len(works) != 1 || works[0].Company != "Muster AG" || works[0].StartDate != "2020-01" ||
			len(works[0].DescriptionBullets) != 1 {
			t.Fatalf("%s: positions %+v, %v", when, works, err)
		}
		edus, err := stations.Educations(ctx, max)
		if err != nil || len(edus) != 1 || edus[0].Institution != "TU Musterstadt" || edus[0].Grade != "1.3" {
			t.Fatalf("%s: degrees %+v, %v", when, edus, err)
		}
	}
	check("before sealing")
	if n, err := stations.SealUnsealed(ctx); err != nil || n != 2 {
		t.Fatalf("sealed %d, %v", n, err)
	}
	check("after sealing")
	var plain string
	err = db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT w.company || w.title || w.start_date || coalesce(encode(w.sealed_bullets, 'hex'), '') ||
			e.institution || e.degree || e.grade FROM work_experiences w, educations e WHERE w.id = $1 AND e.id = $2
			AND w.sealed IS NOT NULL AND e.sealed IS NOT NULL`, wid, eid).Scan(&plain)
	})
	if err != nil || plain != "" {
		t.Fatalf("plain columns left: %q, %v", plain, err)
	}
	if n, err := stations.SealUnsealed(ctx); err != nil || n != 0 {
		t.Fatalf("second run sealed %d, %v", n, err)
	}
}
