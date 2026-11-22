package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/adapters/pgstore/q"
)

var _ profile.StationStore = (*Store)(nil)

func toStoredWorkExperience(r q.WorkExperience) profile.StoredWorkExperience {
	return profile.StoredWorkExperience{
		ID:            r.ID,
		UserID:        r.UserID,
		Company:       r.Company,
		Title:         r.Title,
		Location:      r.Location,
		WorkplaceType: r.WorkplaceType,
		StartDate:     r.StartDate,
		EndDate:       r.EndDate,
		IsCurrent:     r.IsCurrent,
		SealedBullets: r.SealedBullets,
		Sealed:        r.Sealed,
		SortOrder:     int(r.SortOrder),
		CreatedAt:     r.CreatedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

func toStoredEducation(r q.Education) profile.StoredEducation {
	return profile.StoredEducation{
		ID:           r.ID,
		UserID:       r.UserID,
		Institution:  r.Institution,
		Degree:       r.Degree,
		FieldOfStudy: r.FieldOfStudy,
		StartDate:    r.StartDate,
		EndDate:      r.EndDate,
		IsCurrent:    r.IsCurrent,
		Grade:        r.Grade,
		Sealed:       r.Sealed,
		SortOrder:    int(r.SortOrder),
		CreatedAt:    r.CreatedAt,
		UpdatedAt:    r.UpdatedAt,
	}
}

func (s *Store) ListWorkExperiences(ctx context.Context, userID uuid.UUID) (out []profile.StoredWorkExperience, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.ListWorkExperiences(ctx, userID)
		for _, r := range rows {
			out = append(out, toStoredWorkExperience(r))
		}
		return err
	})
	return out, err
}

func (s *Store) GetWorkExperience(ctx context.Context, userID, id uuid.UUID) (w profile.StoredWorkExperience, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.GetWorkExperience(ctx, q.GetWorkExperienceParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return profile.ErrNotFound
		}
		if err != nil {
			return err
		}
		w = toStoredWorkExperience(r)
		return nil
	})
	return w, err
}

func (s *Store) InsertWorkExperience(ctx context.Context, w profile.StoredWorkExperience) error {
	return s.user(ctx, w.UserID, func(qs *q.Queries) error {
		return qs.InsertWorkExperience(ctx, q.InsertWorkExperienceParams{ID: w.ID, UserID: w.UserID, Sealed: w.Sealed,
			SortOrder: int32(w.SortOrder), CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}) //nolint:gosec // bounded by MaxStations
	})
}

func (s *Store) UpdateWorkExperience(ctx context.Context, w profile.StoredWorkExperience) (ok bool, err error) {
	err = s.user(ctx, w.UserID, func(qs *q.Queries) error {
		n, err := qs.UpdateWorkExperience(ctx, q.UpdateWorkExperienceParams{ID: w.ID, UserID: w.UserID, Sealed: w.Sealed, UpdatedAt: w.UpdatedAt})
		ok = n == 1
		return err
	})
	return ok, err
}

func (s *Store) UpdateWorkExperienceOrder(ctx context.Context, userID, id uuid.UUID, sortOrder int, updatedAt time.Time) (bool, error) {
	var ok bool
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.UpdateWorkExperienceOrder(ctx, q.UpdateWorkExperienceOrderParams{
			ID:        id,
			UserID:    userID,
			SortOrder: int32(sortOrder), //nolint:gosec // bounded by MaxStations
			UpdatedAt: updatedAt,
		})
		ok = n == 1
		return err
	})
	return ok, err
}

func (s *Store) DeleteWorkExperience(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	var ok bool
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.DeleteWorkExperience(ctx, q.DeleteWorkExperienceParams{ID: id, UserID: userID})
		ok = n == 1
		return err
	})
	return ok, err
}

func (s *Store) CountWorkExperiences(ctx context.Context, userID uuid.UUID) (n int, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		c, err := qs.CountWorkExperiences(ctx, userID)
		n = int(c)
		return err
	})
	return n, err
}

func (s *Store) ListEducations(ctx context.Context, userID uuid.UUID) (out []profile.StoredEducation, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.ListEducations(ctx, userID)
		for _, r := range rows {
			out = append(out, toStoredEducation(r))
		}
		return err
	})
	return out, err
}

func (s *Store) GetEducation(ctx context.Context, userID, id uuid.UUID) (e profile.StoredEducation, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.GetEducation(ctx, q.GetEducationParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return profile.ErrNotFound
		}
		if err != nil {
			return err
		}
		e = toStoredEducation(r)
		return nil
	})
	return e, err
}

func (s *Store) InsertEducation(ctx context.Context, e profile.StoredEducation) error {
	return s.user(ctx, e.UserID, func(qs *q.Queries) error {
		return qs.InsertEducation(ctx, q.InsertEducationParams{ID: e.ID, UserID: e.UserID, Sealed: e.Sealed,
			SortOrder: int32(e.SortOrder), CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt}) //nolint:gosec // bounded by MaxStations
	})
}

func (s *Store) UpdateEducation(ctx context.Context, e profile.StoredEducation) (ok bool, err error) {
	err = s.user(ctx, e.UserID, func(qs *q.Queries) error {
		n, err := qs.UpdateEducation(ctx, q.UpdateEducationParams{ID: e.ID, UserID: e.UserID, Sealed: e.Sealed, UpdatedAt: e.UpdatedAt})
		ok = n == 1
		return err
	})
	return ok, err
}

func (s *Store) UpdateEducationOrder(ctx context.Context, userID, id uuid.UUID, sortOrder int, updatedAt time.Time) (bool, error) {
	var ok bool
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.UpdateEducationOrder(ctx, q.UpdateEducationOrderParams{
			ID:        id,
			UserID:    userID,
			SortOrder: int32(sortOrder), //nolint:gosec // bounded by MaxStations
			UpdatedAt: updatedAt,
		})
		ok = n == 1
		return err
	})
	return ok, err
}

func (s *Store) DeleteEducation(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	var ok bool
	err := s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.DeleteEducation(ctx, q.DeleteEducationParams{ID: id, UserID: userID})
		ok = n == 1
		return err
	})
	return ok, err
}

func (s *Store) CountEducations(ctx context.Context, userID uuid.UUID) (n int, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		c, err := qs.CountEducations(ctx, userID)
		n = int(c)
		return err
	})
	return n, err
}

// UnsealedStationUsers lists the users with positions or degrees from before sealing.
func (s *Store) UnsealedStationUsers(ctx context.Context) (ids []uuid.UUID, err error) {
	err = s.system(ctx, func(qs *q.Queries) error {
		ids, err = qs.UnsealedStationUsers(ctx)
		return err
	})
	return ids, err
}
