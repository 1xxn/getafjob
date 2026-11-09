package web

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/cvimport"
	"github.com/1xxn/getafjob/internal/web/views"
)

// MaxUpload is the body limit of the upload route: one document plus the form fields.
const MaxUpload = profile.MaxDocument + 64<<10

func (s *Server) documentsPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	notice := ""
	if r.URL.Query().Get("uploaded") == "1" {
		notice = views.T(r.Context(), "documents.uploaded")
	}
	s.renderDocuments(w, r, u, http.StatusOK, notice, false)
}

func (s *Server) renderDocuments(w http.ResponseWriter, r *http.Request, u *identity.User, status int, notice string, failed bool) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	docs, err := s.Documents.List(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	rows := make([]views.DocumentRow, len(docs))
	for i, d := range docs {
		rows[i] = views.DocumentRow{ID: d.ID.String(), Name: d.Name, Kind: d.Kind, Size: views.Size(d.Size),
			Created: views.When(r.Context(), d.CreatedAt), Label: d.Label, Language: d.Language, Focus: strings.Join(d.Focus, ", "),
			Import: s.Importer != nil && (d.Mime == profile.MimePDF || d.Mime == profile.MimeDOCX)}
	}
	s.render(w, r, status, views.DocumentsPage(u.DisplayName, notice, failed, rows))
}

// readUpload streams the multipart body: the kind field and one file, kept in memory (no temporary files).
func readUpload(r *http.Request) (kind, name string, data []byte, err error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return "", "", nil, profile.ErrFileType
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", "", nil, profile.ErrFileTooLarge
		}
		switch part.FormName() {
		case "kind":
			b, err := io.ReadAll(io.LimitReader(part, 32))
			if err != nil {
				return "", "", nil, err
			}
			kind = string(b)
		case "file":
			if data != nil {
				return "", "", nil, profile.ErrFileType // one file per upload
			}
			name = part.FileName()
			data, err = io.ReadAll(io.LimitReader(part, profile.MaxDocument+1))
			if err != nil {
				return "", "", nil, profile.ErrFileTooLarge
			}
			if len(data) > profile.MaxDocument {
				return "", "", nil, profile.ErrFileTooLarge
			}
		}
		_ = part.Close()
	}
	return kind, name, data, nil
}

func (s *Server) documentUpload(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	kind, name, data, err := readUpload(r)
	if err == nil {
		_, err = s.Documents.Upload(r.Context(), u.ID, kind, name, data)
	}
	switch {
	case err == nil:
		http.Redirect(w, r, "/documents?uploaded=1", http.StatusSeeOther)
	case errors.Is(err, profile.ErrFileType):
		s.renderDocuments(w, r, u, http.StatusUnprocessableEntity, views.T(r.Context(), "documents.error.type"), true)
	case errors.Is(err, profile.ErrFileTooLarge):
		s.renderDocuments(w, r, u, http.StatusRequestEntityTooLarge, views.T(r.Context(), "documents.error.size"), true)
	case errors.Is(err, profile.ErrTooManyFiles):
		s.renderDocuments(w, r, u, http.StatusConflict, views.T(r.Context(), "documents.error.count"), true)
	default:
		s.failed(w, r, u, err)
	}
}

func docID(r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	return id, err == nil
}

// documentDownload sends a file as an attachment; it is never rendered in the page's origin.
func (s *Server) documentDownload(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, ok := docID(r)
	if !ok {
		s.notFound(w, r, u)
		return
	}
	d, data, err := s.Documents.Read(r.Context(), u.ID, id)
	if errors.Is(err, profile.ErrNotFound) {
		s.notFound(w, r, u)
		return
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", d.Mime)
	h.Set("Content-Length", strconv.Itoa(len(data)))
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": d.Name}))
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	_, _ = w.Write(data) //nolint:gosec // G705: the user's own file, type from its content, sent as an attachment under a sandbox CSP
}

func (s *Server) documentVariant(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, ok := docID(r)
	if !ok {
		s.notFound(w, r, u)
		return
	}
	k := id.String()
	err := s.Documents.SetVariant(r.Context(), u.ID, id, r.PostFormValue("label-"+k), r.PostFormValue("language-"+k),
		profile.Lines(r.PostFormValue("focus-"+k)))
	switch {
	case err == nil:
		s.renderDocuments(w, r, u, http.StatusOK, views.T(r.Context(), "profile.saved"), false)
	case errors.Is(err, profile.ErrNotFound):
		s.notFound(w, r, u)
	case errors.Is(err, profile.ErrFileType):
		s.renderDocuments(w, r, u, http.StatusUnprocessableEntity, views.T(r.Context(), "profile.invalid"), true)
	default:
		s.failed(w, r, u, err)
	}
}

func (s *Server) documentDelete(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, ok := docID(r)
	if !ok {
		s.notFound(w, r, u)
		return
	}
	err := s.Documents.Delete(r.Context(), u.ID, id)
	switch {
	case err == nil:
		s.renderDocuments(w, r, u, http.StatusOK, views.T(r.Context(), "documents.deleted"), false)
	case errors.Is(err, profile.ErrNotFound):
		s.notFound(w, r, u)
	default:
		s.failed(w, r, u, err)
	}
}

// documentImport fills empty profile fields from a CV and shows the profile for checking.
func (s *Server) documentImport(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, ok := docID(r)
	if !ok || s.Importer == nil {
		s.notFound(w, r, u)
		return
	}
	filled, err := s.Importer.Import(r.Context(), u.ID, id)
	switch {
	case err == nil && filled:
		http.Redirect(w, r, "/profile?imported=1", http.StatusSeeOther)
	case err == nil:
		s.renderDocuments(w, r, u, http.StatusOK, views.T(r.Context(), "documents.import_nothing"), false)
	case errors.Is(err, profile.ErrNotFound):
		s.notFound(w, r, u)
	case errors.Is(err, cvimport.ErrNoText):
		s.renderDocuments(w, r, u, http.StatusUnprocessableEntity, views.T(r.Context(), "documents.import_no_text"), true)
	case errors.Is(err, profile.ErrFileType), errors.Is(err, profile.ErrFileTooLarge):
		s.renderDocuments(w, r, u, http.StatusUnprocessableEntity, views.T(r.Context(), "documents.import_unreadable"), true)
	case errors.Is(err, profile.ErrConflict):
		s.renderDocuments(w, r, u, http.StatusConflict, views.T(r.Context(), "profile.conflict"), true)
	default:
		s.failed(w, r, u, err)
	}
}

// documentReview renders the interactive review page for extracted career stations and profile fields.
func (s *Server) documentReview(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, ok := docID(r)
	if !ok || s.Importer == nil {
		s.notFound(w, r, u)
		return
	}
	d, _, err := s.Documents.Read(r.Context(), u.ID, id)
	if errors.Is(err, profile.ErrNotFound) {
		s.notFound(w, r, u)
		return
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	parsed, err := s.Importer.Extract(r.Context(), u.ID, id)
	switch {
	case err == nil:
		s.render(w, r, http.StatusOK, views.DocumentReviewPage(views.DocumentReviewProps{
			UserName:    u.DisplayName,
			DocID:       id.String(),
			DocName:     d.Name,
			Profile:     parsed.Profile,
			Experiences: parsed.Experiences,
			Educations:  parsed.Educations,
		}))
	case errors.Is(err, cvimport.ErrNoText):
		s.renderDocuments(w, r, u, http.StatusUnprocessableEntity, views.T(r.Context(), "documents.import_no_text"), true)
	case errors.Is(err, profile.ErrFileType), errors.Is(err, profile.ErrFileTooLarge):
		s.renderDocuments(w, r, u, http.StatusUnprocessableEntity, views.T(r.Context(), "documents.import_unreadable"), true)
	default:
		s.failed(w, r, u, err)
	}
}

// documentReviewSave saves confirmed career stations and merges profile details from the review page.
func (s *Server) documentReviewSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, ok := docID(r)
	if !ok || s.Importer == nil || s.Stations == nil {
		s.notFound(w, r, u)
		return
	}
	_, _, err := s.Documents.Read(r.Context(), u.ID, id)
	if errors.Is(err, profile.ErrNotFound) {
		s.notFound(w, r, u)
		return
	}
	if err != nil {
		s.failed(w, r, u, err)
		return
	}

	expCount, _ := strconv.Atoi(r.PostFormValue("exp_count"))
	for i := 0; i < expCount; i++ {
		if r.PostFormValue(fmt.Sprintf("include_exp_%d", i)) == "" {
			continue
		}
		company := strings.TrimSpace(r.PostFormValue(fmt.Sprintf("exp_%d_company", i)))
		title := strings.TrimSpace(r.PostFormValue(fmt.Sprintf("exp_%d_title", i)))
		if company == "" && title == "" {
			continue
		}
		we := profile.WorkExperience{
			Company:            company,
			Title:              title,
			Location:           strings.TrimSpace(r.PostFormValue(fmt.Sprintf("exp_%d_location", i))),
			WorkplaceType:      strings.TrimSpace(r.PostFormValue(fmt.Sprintf("exp_%d_workplace", i))),
			StartDate:          strings.TrimSpace(r.PostFormValue(fmt.Sprintf("exp_%d_start", i))),
			EndDate:            strings.TrimSpace(r.PostFormValue(fmt.Sprintf("exp_%d_end", i))),
			IsCurrent:          r.PostFormValue(fmt.Sprintf("exp_%d_current", i)) != "",
			DescriptionBullets: profile.Lines(r.PostFormValue(fmt.Sprintf("exp_%d_bullets", i))),
		}
		_, _ = s.Stations.SaveWorkExperience(r.Context(), u.ID, we)
	}

	eduCount, _ := strconv.Atoi(r.PostFormValue("edu_count"))
	for i := 0; i < eduCount; i++ {
		if r.PostFormValue(fmt.Sprintf("include_edu_%d", i)) == "" {
			continue
		}
		inst := strings.TrimSpace(r.PostFormValue(fmt.Sprintf("edu_%d_institution", i)))
		degree := strings.TrimSpace(r.PostFormValue(fmt.Sprintf("edu_%d_degree", i)))
		if inst == "" && degree == "" {
			continue
		}
		ed := profile.EducationStation{
			Institution:  inst,
			Degree:       degree,
			FieldOfStudy: strings.TrimSpace(r.PostFormValue(fmt.Sprintf("edu_%d_field", i))),
			StartDate:    strings.TrimSpace(r.PostFormValue(fmt.Sprintf("edu_%d_start", i))),
			EndDate:      strings.TrimSpace(r.PostFormValue(fmt.Sprintf("edu_%d_end", i))),
			IsCurrent:    r.PostFormValue(fmt.Sprintf("edu_%d_current", i)) != "",
			Grade:        strings.TrimSpace(r.PostFormValue(fmt.Sprintf("edu_%d_grade", i))),
		}
		_, _ = s.Stations.SaveEducation(r.Context(), u.ID, ed)
	}

	if r.PostFormValue("include_profile") != "" {
		parsed, err := s.Importer.Extract(r.Context(), u.ID, id)
		if err == nil {
			snap, err := s.Profile.Get(r.Context(), u.ID)
			if err == nil {
				merged := cvimport.Merge(snap.Profile, parsed.Profile)
				_ = s.Profile.SaveProfile(r.Context(), u.ID, merged, snap.Version)
			}
		}
	}

	http.Redirect(w, r, "/profile?imported=1", http.StatusSeeOther)
}
