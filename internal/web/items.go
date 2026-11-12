package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/web/views"
)

func (s *Server) renderAnswers(w http.ResponseWriter, r *http.Request, u *identity.User, status int, f views.Form) {
	list, err := s.Items.Answers(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	var uses map[string]int
	if s.Apps != nil {
		uses, _ = s.Apps.AnswerUsage(r.Context(), u.ID) // a count, not worth failing the page for
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	needle := strings.ToLower(q)
	d := views.AnswersData{Query: q, Total: len(list)}
	for _, a := range list {
		if needle != "" && !strings.Contains(strings.ToLower(a.Question+" "+a.Answer), needle) {
			continue
		}
		d.Rows = append(d.Rows, views.AnswerRow{ID: a.ID.String(), Question: a.Question, Answer: a.Answer, Uses: uses[a.ID.String()]})
	}
	s.render(w, r, status, views.AnswersPage(u.DisplayName, s.candidateHead(r.Context(), u, "answers"), f, d))
}

func (s *Server) renderTexts(w http.ResponseWriter, r *http.Request, u *identity.User, status int, f views.Form) {
	list, err := s.Items.TextBlocks(r.Context(), u.ID)
	if err != nil {
		s.failed(w, r, u, err)
		return
	}
	rows := make([]views.TextRow, len(list))
	for i, t := range list {
		rows[i] = views.TextRow{ID: t.ID.String(), Name: t.Name, Language: t.Language, Body: t.Body}
	}
	s.render(w, r, status, views.TextsPage(u.DisplayName, s.candidateHead(r.Context(), u, "texts"), f, rows, profile.Placeholders))
}

// itemID parses the {id} of an existing item; the add routes have none.
func itemID(r *http.Request) (uuid.UUID, bool) {
	v := r.PathValue("id")
	if v == "" {
		return uuid.Nil, true
	}
	id, err := uuid.Parse(v)
	return id, err == nil && id != uuid.Nil
}

// itemSaved answers a save: back to the list, or the form with errors.
func (s *Server) itemSaved(w http.ResponseWriter, r *http.Request, u *identity.User, err error, f views.Form,
	render func(http.ResponseWriter, *http.Request, *identity.User, int, views.Form), back string,
) {
	switch {
	case err == nil:
		http.Redirect(w, r, back+"?saved=1", http.StatusSeeOther)
	case errors.Is(err, profile.ErrInvalid):
		f.Errors, f.Notice, f.Failed = fieldErrors(r, err), views.T(r.Context(), "profile.invalid"), true
		render(w, r, u, http.StatusUnprocessableEntity, f)
	case errors.Is(err, profile.ErrTooManyItems):
		f.Notice, f.Failed = views.T(r.Context(), "profile.error.too_many"), true
		render(w, r, u, http.StatusConflict, f)
	case errors.Is(err, profile.ErrNotFound):
		s.notFound(w, r, u)
	default:
		s.failed(w, r, u, err)
	}
}

func savedForm(r *http.Request) views.Form {
	f := views.Form{Values: map[string]string{}}
	if r.URL.Query().Get("saved") == "1" {
		f.Notice = views.T(r.Context(), "profile.saved")
	}
	return f
}

func (s *Server) answersPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.renderAnswers(w, r, u, http.StatusOK, savedForm(r))
}

func (s *Server) answerSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, ok := itemID(r)
	if !ok {
		s.notFound(w, r, u)
		return
	}
	a := profile.Answer{ID: id, Question: r.PostFormValue("question"), Answer: r.PostFormValue("answer")}
	_, err := s.Items.SaveAnswer(r.Context(), u.ID, a)
	f := views.Form{Values: map[string]string{}}
	if id == uuid.Nil { // only the add form keeps typed values on error
		f.Values = map[string]string{"question": a.Question, "answer": a.Answer}
	}
	s.itemSaved(w, r, u, err, f, s.renderAnswers, "/answers")
}

func (s *Server) itemDelete(kind, back string) func(http.ResponseWriter, *http.Request, *identity.User) {
	return func(w http.ResponseWriter, r *http.Request, u *identity.User) {
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		id, ok := itemID(r)
		if !ok || id == uuid.Nil {
			s.notFound(w, r, u)
			return
		}
		if err := s.Items.Delete(r.Context(), u.ID, kind, id); errors.Is(err, profile.ErrNotFound) {
			s.notFound(w, r, u)
			return
		} else if err != nil {
			s.failed(w, r, u, err)
			return
		}
		http.Redirect(w, r, back+"?saved=1", http.StatusSeeOther)
	}
}

func (s *Server) textsPage(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	s.renderTexts(w, r, u, http.StatusOK, savedForm(r))
}

func (s *Server) textSave(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	id, ok := itemID(r)
	if !ok {
		s.notFound(w, r, u)
		return
	}
	t := profile.TextBlock{ID: id, Name: r.PostFormValue("name"), Language: r.PostFormValue("language"), Body: r.PostFormValue("body")}
	_, err := s.Items.SaveTextBlock(r.Context(), u.ID, t)
	f := views.Form{Values: map[string]string{}}
	if id == uuid.Nil {
		f.Values = map[string]string{"name": t.Name, "language": t.Language, "body": t.Body}
	}
	s.itemSaved(w, r, u, err, f, s.renderTexts, "/texts")
}
