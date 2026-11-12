package web

import (
	"context"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/web/views"
)

// candidateHead measures how complete the profile is: the fields every form asks for, a CV and one station of the
// work history. Each gap links to the place that fills it.
func (s *Server) candidateHead(ctx context.Context, u *identity.User, tab string) views.CandidateHead {
	h := views.CandidateHead{Tab: tab}
	if s.Profile != nil {
		if snap, err := s.Profile.Get(ctx, u.ID); err == nil {
			missing := snap.Profile.Missing()
			h.Total += 9
			h.Done += 9 - len(missing)
			for _, f := range missing {
				h.Gaps = append(h.Gaps, views.CandidateGap{Key: "cand.gap." + f, Href: "/profile#f-" + f})
			}
		}
	}
	if s.Documents != nil {
		h.Total++
		cv := false
		if docs, err := s.Documents.List(ctx, u.ID); err == nil {
			for _, d := range docs {
				cv = cv || d.Kind == "cv"
			}
		}
		if cv {
			h.Done++
		} else {
			h.Gaps = append(h.Gaps, views.CandidateGap{Key: "cand.gap.cv", Href: "/documents#f-file"})
		}
	}
	if s.Stations != nil {
		h.Total++
		if exps, err := s.Stations.WorkExperiences(ctx, u.ID); err == nil && len(exps) > 0 {
			h.Done++
		} else {
			h.Gaps = append(h.Gaps, views.CandidateGap{Key: "cand.gap.experience", Href: "/profile#stations"})
		}
	}
	return h
}
