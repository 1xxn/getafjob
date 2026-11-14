package matching

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// page is how many jobs one round reads and writes per user.
const page = 1000

// Service matches open jobs against the criteria of users.
type Service struct {
	profiles Profiles
	jobs     Jobs
	store    Store
	emb      Embedder // nil: rules only
	now      func() time.Time
}

// NewService returns a matching service.
func NewService(p Profiles, j Jobs, s Store) *Service {
	return &Service{profiles: p, jobs: j, store: s, now: time.Now}
}

// WithEmbedder adds the embedding model (ADR 0029); without it the score comes from the rules alone.
func (s *Service) WithEmbedder(e Embedder) *Service { s.emb = e; return s }

// Embedder returns the model the service matches with, nil if none.
func (s *Service) Embedder() Embedder { return s.emb }

// WithClock sets the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

// Stats counts what a run decided.
type Stats struct {
	Users     int              `json:"users"`
	Jobs      int              `json:"jobs"`
	Decisions map[Decision]int `json:"decisions"`
}

// Run matches all open jobs for users, or for every user with a profile when users is empty. Jobs are read once
// for all users.
func (s *Service) Run(ctx context.Context, users []uuid.UUID) (Stats, error) {
	st := Stats{Decisions: map[Decision]int{}}
	if len(users) == 0 {
		var err error
		if users, err = s.profiles.Users(ctx); err != nil {
			return st, err
		}
	}
	matchers := make([]*Matcher, len(users))
	for i, u := range users {
		c, err := s.profiles.Criteria(ctx, u)
		if err != nil {
			return st, err
		}
		matchers[i] = NewMatcher(c, s.emb)
	}
	st.Users = len(users)
	if len(users) == 0 {
		return st, nil
	}
	now := s.now()
	for after := int64(0); ; {
		jobs, err := s.jobs.OpenJobs(ctx, after, page)
		if err != nil {
			return st, err
		}
		if len(jobs) == 0 {
			return st, nil
		}
		st.Jobs += len(jobs)
		after = jobs[len(jobs)-1].ID
		// Each job is analyzed once (text, language, vector) and then scored for every user.
		prepared := make([]*Prepared, len(jobs))
		for k, j := range jobs {
			prepared[k] = Prepare(j, s.emb)
		}
		rs := make([]Result, len(jobs))
		for i, u := range users {
			for k := range prepared {
				rs[k] = matchers[i].EvaluatePrepared(prepared[k], now)
				st.Decisions[rs[k].Decision]++
			}
			if err := s.store.SaveMatches(ctx, u, rs, now); err != nil {
				return st, err
			}
		}
		if err := ctx.Err(); err != nil {
			return st, err
		}
	}
}
