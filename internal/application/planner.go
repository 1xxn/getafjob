package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PacingInterval returns the target gap between two applications so that dailyLimit applications
// are spread evenly across QuotaWindow (ADR 0016).
func PacingInterval(dailyLimit int) time.Duration {
	if dailyLimit <= 0 {
		return 0
	}
	return QuotaWindow / time.Duration(dailyLimit)
}

// Plan queues matching applications for users whose autonomy settings allow it: up to the daily quota (submits in
// QuotaWindow plus applications in flight), or all of them without limits. Candidates come by score, and those
// that fail a guard are left out. A failing user does not stop the others; their errors are returned together.
func (s *Service) Plan(ctx context.Context) (PlanStats, error) {
	users, err := s.store.PlanUsers(ctx, s.unlimited)
	if err != nil {
		return PlanStats{}, err
	}
	st := PlanStats{UsersEligible: len(users)}
	var errs []error
	for _, u := range users {
		n, err := s.planUser(ctx, u)
		st.JobsPlanned += n
		if err != nil {
			errs = append(errs, fmt.Errorf("plan user %s: %w", u, err))
		}
	}
	return st, errors.Join(errs...)
}

// planUser queues the user's candidates and returns how many it queued.
func (s *Service) planUser(ctx context.Context, u uuid.UUID) (int, error) {
	cfg, err := s.Settings(ctx, u)
	if err != nil {
		return 0, err
	}
	if !cfg.AutoQueue() || (!cfg.Unlimited && cfg.DailyLimit <= 0) {
		return 0, nil
	}
	now := s.now()
	submits, err := s.store.SubmitsSince(ctx, u, now.Add(-QuotaWindow))
	if err != nil {
		return 0, err
	}
	active, err := s.store.ActiveApplicationsCount(ctx, u)
	if err != nil {
		return 0, err
	}
	full := func() bool { return !cfg.Unlimited && submits+active >= cfg.DailyLimit }
	if full() {
		return 0, nil
	}
	cands, err := s.store.PlannerCandidates(ctx, u, cfg.MinScore)
	if err != nil {
		return 0, err
	}
	planned := 0
	var errs []error
	for _, c := range cands {
		if full() {
			break
		}
		cand, err := s.store.Candidate(ctx, u, c.JobID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if Guard(cand, now) != nil {
			continue
		}
		if _, _, err := s.Queue(ctx, u, c.JobID); err != nil {
			errs = append(errs, err)
			continue
		}
		planned++
		active++
	}
	return planned, errors.Join(errs...)
}
