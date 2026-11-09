package application

import (
	"context"
	"time"
)

// PacingInterval returns the target gap between two applications so that dailyLimit applications
// are spread evenly across QuotaWindow (ADR 0016).
func PacingInterval(dailyLimit int) time.Duration {
	if dailyLimit <= 0 {
		return 0
	}
	return QuotaWindow / time.Duration(dailyLimit)
}

// Plan plans and queues matching applications on behalf of users whose autonomy settings allow it.
// Applications are queued immediately up to the user's daily quota (#156).
func (s *Service) Plan(ctx context.Context) (PlanStats, error) {
	users, err := s.store.PlanUsers(ctx)
	if err != nil {
		return PlanStats{}, err
	}
	now := s.now()
	st := PlanStats{UsersEligible: len(users)}
	for _, u := range users {
		cfg, err := s.Settings(ctx, u)
		if err != nil || !cfg.AutoQueue() || cfg.DailyLimit <= 0 {
			continue
		}
		// Check user's submit quota in the 24h window
		submits, err := s.store.SubmitsSince(ctx, u, now.Add(-QuotaWindow))
		if err != nil {
			return st, err
		}
		// Check user's active applications in flight
		active, err := s.store.ActiveApplicationsCount(ctx, u)
		if err != nil {
			return st, err
		}
		if submits+active >= cfg.DailyLimit {
			continue
		}

		// Find candidates matching the user's criteria, ordered by score descending
		cands, err := s.store.PlannerCandidates(ctx, u, cfg.MinScore)
		if err != nil {
			return st, err
		}

		for _, c := range cands {
			if submits+active >= cfg.DailyLimit {
				break
			}
			// Check candidate guards (exclusion, duplicate, company window)
			cand, err := s.store.Candidate(ctx, u, c.JobID)
			if err != nil {
				continue
			}
			if err := Guard(cand, now); err != nil {
				continue
			}

			// Queue the application
			if _, _, err := s.Queue(ctx, u, c.JobID); err != nil {
				continue
			}
			st.JobsPlanned++
			active++
		}
	}
	return st, nil
}
