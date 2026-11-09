package web

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/web/views"
)

func (s *Server) approvalsPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	var rows []views.ApprovalRow
	if s.Apps != nil {
		list, err := s.Apps.Approvals(r.Context(), u.ID)
		if err != nil {
			s.failed(w, r, u, err)
			return
		}
		var docMap map[uuid.UUID]string
		if s.Documents != nil {
			docMap = make(map[uuid.UUID]string)
			if docs, err := s.Documents.List(r.Context(), u.ID); err == nil {
				for _, doc := range docs {
					label := doc.Label
					if label == "" {
						label = doc.Name
					}
					docMap[doc.ID] = label
				}
			}
		}
		for _, a := range list {
			var reasons []string
			for _, re := range a.Reasons {
				reasons = append(reasons, views.T(r.Context(), "rule.reason."+string(re)))
			}
			cvVariant := ""
			if a.CvDocumentID != nil && docMap != nil {
				cvVariant = docMap[*a.CvDocumentID]
			}
			rows = append(rows, views.ApprovalRow{
				ID:        a.ID.String(),
				JobID:     a.JobID,
				State:     string(a.State),
				Title:     a.Title,
				Company:   a.Company,
				Score:     a.Score,
				CVVariant: cvVariant,
				Reasons:   reasons,
				AskedAt:   a.Asked,
			})
		}
	}
	notice := ""
	if r.URL.Query().Get("approved") == "1" {
		notice = views.T(r.Context(), "approvals.approved")
	}
	s.render(w, r, http.StatusOK, views.ApprovalsPage(views.ApprovalsData{
		Name:   u.DisplayName,
		Rows:   rows,
		Notice: notice,
	}))
}

func (s *Server) approvalApprove(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	if s.Apps != nil {
		if _, err := s.Apps.Approve(r.Context(), u.ID, appID); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}
	http.Redirect(w, r, "/approvals?approved=1", http.StatusSeeOther)
}

func (s *Server) approvalSetCV(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	cvIDStr := r.PostFormValue("cv_id")
	var cvDocID *uuid.UUID
	if cvIDStr != "" {
		parsed, err := uuid.Parse(cvIDStr)
		if err != nil {
			http.Error(w, "invalid cv id", http.StatusBadRequest)
			return
		}
		cvDocID = &parsed
	}

	if s.Apps != nil {
		if err := s.Apps.SetCV(r.Context(), u.ID, appID, cvDocID); err != nil {
			s.failed(w, r, u, err)
			return
		}
	}

	if partial(r) {
		s.approvalsPage(w, r, u)
		return
	}
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}
