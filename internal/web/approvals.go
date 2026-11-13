package web

import (
	"errors"
	"net/http"
	"slices"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
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
	d := views.ApprovalsData{Name: u.DisplayName, Rows: rows}
	if partial(r) {
		s.render(w, r, http.StatusOK, views.ApprovalsList(d))
		return
	}
	s.render(w, r, http.StatusOK, views.ApprovalsPage(d))
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
	key := "flash.approvals.approve"
	if s.Apps != nil {
		if _, err := s.Apps.Approve(r.Context(), u.ID, appID); errors.Is(err, application.ErrAlreadySubmitted) {
			key = "flash.apps.retry_attempted"
		} else if err != nil {
			key = "flash.approvals.approve_none" // it moved on meanwhile
		}
	}
	s.flash(w, key)
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}

// approvalBulkActions are the actions of the approval list's bulk bar.
var approvalBulkActions = map[string]string{"approve": "approve", "reject": "reject"}

// approvalsBulk approves the selected applications, or rejects them: a rejected one is skipped and never sent.
func (s *Server) approvalsBulk(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	action, known := approvalBulkActions[r.PostFormValue("action")]
	var ids []uuid.UUID
	for _, v := range r.PostForm["ids"] {
		if id, err := uuid.Parse(v); err == nil && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if !known || len(ids) == 0 || len(ids) > appsPageSize || s.Apps == nil {
		http.Redirect(w, r, "/approvals", http.StatusSeeOther)
		return
	}
	done := 0
	for _, id := range ids {
		var err error
		if action == "approve" {
			_, err = s.Apps.Approve(r.Context(), u.ID, id)
		} else {
			_, err = s.Apps.Fire(r.Context(), u.ID, id, application.EvSkip, application.ActorUser,
				map[string]any{"reason": "rejected at approval"})
		}
		if err != nil {
			s.Logger.DebugContext(r.Context(), "approval refused", "action", action, "app", id, "err", err)
			continue
		}
		done++
	}
	key := "flash.approvals." + action
	switch {
	case done == 0:
		key += "_none"
	case done < len(ids):
		key += "_partial"
	}
	s.flash(w, key)
	http.Redirect(w, r, "/approvals", http.StatusSeeOther)
}

// approvalReview renders what an approval would send, for the review drawer.
func (s *Server) approvalReview(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	appID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		s.notFound(w, r, u)
		return
	}
	d, ok := s.applicationDetail(w, r, u, appID)
	if !ok {
		return
	}
	var cv views.CVVariantData
	if s.Jobs != nil {
		if j, err := s.Jobs.Detail(r.Context(), u.ID, d.JobID); err == nil {
			cv = s.buildCVVariantData(r.Context(), u.ID, j, "")
		}
	}
	s.render(w, r, http.StatusOK, views.ApprovalReview(d, cv))
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
