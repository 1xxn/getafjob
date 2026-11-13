// Package apiv1 serves the JSON API under /api/v1 (spec-first: api/openapi.yaml → apiv1.gen.go).
package apiv1

//go:generate go tool oapi-codegen -config ../../../api/oapi-codegen.yaml ../../../api/openapi.yaml

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/1xxn/getafjob/internal/assist"
	"github.com/1xxn/getafjob/internal/platform/buildinfo"
)

// newPairLimiter bounds pairing attempts for the whole instance: pairing is rare, guessing needs many tries.
func newPairLimiter() *rate.Limiter { return rate.NewLimiter(rate.Every(2*time.Second), 10) }

type userCtxKeyType struct{}

var userCtxKey = userCtxKeyType{}

// AssistService provides extension and assist operations.
type AssistService interface {
	Pair(ctx context.Context, code, clientName string) (assist.Token, string, error)
	Authenticate(ctx context.Context, tokenPlain string) (assist.Token, error)
	GetProfile(ctx context.Context, userID uuid.UUID) (map[string]string, []assist.Answer, []assist.TextBlock, []assist.Document, error)
	Inspect(ctx context.Context, userID uuid.UUID, pageURL, pageTitle string, fields []assist.FormField) (assist.InspectResult, error)
	Submit(ctx context.Context, userID uuid.UUID, pageURL, company, title string, fieldsFilled int, approved bool, notes string) (uuid.UUID, string, error)
	ListApprovals(ctx context.Context, userID uuid.UUID) ([]assist.ApprovalItem, error)
	DecideApproval(ctx context.Context, userID, applicationID uuid.UUID, decision string) error
}

// Server implements the generated StrictServerInterface.
type Server struct {
	Assist AssistService
	pairs  *rate.Limiter // nil: no limit (tests)
}

var _ StrictServerInterface = (*Server)(nil)

// GetMeta returns the server version and API level.
func (s *Server) GetMeta(_ context.Context, _ GetMetaRequestObject) (GetMetaResponseObject, error) {
	return GetMeta200JSONResponse{Version: buildinfo.Get().Version, Api: V1}, nil
}

// PostAssistPair pairs an extension using a one-time pairing code.
func (s *Server) PostAssistPair(ctx context.Context, req PostAssistPairRequestObject) (PostAssistPairResponseObject, error) {
	if s.Assist == nil {
		return PostAssistPairdefaultApplicationProblemPlusJSONResponse{
			StatusCode: http.StatusServiceUnavailable,
			Body: Problem{
				Status: http.StatusServiceUnavailable,
				Code:   "api.service_unavailable",
			},
		}, nil
	}
	if s.pairs != nil && !s.pairs.Allow() {
		return PostAssistPairdefaultApplicationProblemPlusJSONResponse{
			StatusCode: http.StatusTooManyRequests,
			Body:       Problem{Status: http.StatusTooManyRequests, Code: "api.rate_limited"},
		}, nil
	}
	tok, userName, err := s.Assist.Pair(ctx, req.Body.Code, req.Body.ClientName)
	if err != nil {
		return PostAssistPairdefaultApplicationProblemPlusJSONResponse{ //nolint:nilerr // translated to problem JSON response
			StatusCode: http.StatusBadRequest,
			Body: Problem{
				Status: http.StatusBadRequest,
				Code:   "assist.invalid_pairing_code",
			},
		}, nil
	}
	return PostAssistPair200JSONResponse{
		Token:     tok.TokenPlain,
		UserId:    tok.UserID,
		UserName:  userName,
		ExpiresAt: tok.ExpiresAt,
	}, nil
}

// GetAssistProfile returns user facts, answer bank items, and text blocks.
func (s *Server) GetAssistProfile(ctx context.Context, _ GetAssistProfileRequestObject) (GetAssistProfileResponseObject, error) {
	userID, ok := ctx.Value(userCtxKey).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return GetAssistProfiledefaultApplicationProblemPlusJSONResponse{
			StatusCode: http.StatusUnauthorized,
			Body: Problem{
				Status: http.StatusUnauthorized,
				Code:   "api.unauthorized",
			},
		}, nil
	}

	facts, answers, texts, docs, err := s.Assist.GetProfile(ctx, userID)
	if err != nil {
		return GetAssistProfiledefaultApplicationProblemPlusJSONResponse{ //nolint:nilerr // translated to problem JSON response
			StatusCode: http.StatusInternalServerError,
			Body: Problem{
				Status: http.StatusInternalServerError,
				Code:   "api.internal_error",
			},
		}, nil
	}

	ansItems := make([]AnswerItem, len(answers))
	for i, a := range answers {
		ansItems[i] = AnswerItem{Id: a.ID, Question: a.Question, Answer: a.Answer}
	}
	txtItems := make([]TextBlockItem, len(texts))
	for i, t := range texts {
		txtItems[i] = TextBlockItem{Id: t.ID, Name: t.Name, Body: t.Body}
	}
	docItems := make([]DocumentItem, len(docs))
	for i, d := range docs {
		docItems[i] = DocumentItem{Id: d.ID, Name: d.Name, Kind: d.Kind}
	}

	return GetAssistProfile200JSONResponse{
		Facts:      facts,
		Answers:    ansItems,
		TextBlocks: txtItems,
		Documents:  docItems,
	}, nil
}

// PostAssistInspect inspects page form fields and returns matched values.
func (s *Server) PostAssistInspect(ctx context.Context, req PostAssistInspectRequestObject) (PostAssistInspectResponseObject, error) {
	userID, ok := ctx.Value(userCtxKey).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return PostAssistInspectdefaultApplicationProblemPlusJSONResponse{
			StatusCode: http.StatusUnauthorized,
			Body: Problem{
				Status: http.StatusUnauthorized,
				Code:   "api.unauthorized",
			},
		}, nil
	}

	fields := make([]assist.FormField, len(req.Body.Fields))
	for i, f := range req.Body.Fields {
		lbl := ""
		if f.Label != nil {
			lbl = *f.Label
		}
		sel := ""
		if f.Selector != nil {
			sel = *f.Selector
		}
		fields[i] = assist.FormField{
			ID:       f.Id,
			Name:     f.Name,
			Label:    lbl,
			Type:     f.Type,
			Required: f.Required,
			Selector: sel,
		}
	}

	res, err := s.Assist.Inspect(ctx, userID, req.Body.Url, req.Body.Title, fields)
	if err != nil {
		return PostAssistInspectdefaultApplicationProblemPlusJSONResponse{ //nolint:nilerr // translated to problem JSON response
			StatusCode: http.StatusInternalServerError,
			Body: Problem{
				Status: http.StatusInternalServerError,
				Code:   "api.internal_error",
			},
		}, nil
	}

	matches := make([]FieldMatch, len(res.Matches))
	for i, m := range res.Matches {
		matches[i] = FieldMatch{
			FieldId:    m.FieldID,
			Value:      m.Value,
			Provenance: m.Provenance,
			Confidence: float32(m.Confidence),
			Source:     m.Source,
		}
	}

	return PostAssistInspect200JSONResponse{
		Matches:          matches,
		MissingRequired:  res.MissingRequired,
		RequiresApproval: res.RequiresApproval,
	}, nil
}

// PostAssistSubmit records an application submitted through the extension.
func (s *Server) PostAssistSubmit(ctx context.Context, req PostAssistSubmitRequestObject) (PostAssistSubmitResponseObject, error) {
	userID, ok := ctx.Value(userCtxKey).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return PostAssistSubmitdefaultApplicationProblemPlusJSONResponse{
			StatusCode: http.StatusUnauthorized,
			Body: Problem{
				Status: http.StatusUnauthorized,
				Code:   "api.unauthorized",
			},
		}, nil
	}

	notes := ""
	if req.Body.Notes != nil {
		notes = *req.Body.Notes
	}

	appID, status, err := s.Assist.Submit(ctx, userID, req.Body.Url, req.Body.Company, req.Body.Title, req.Body.FieldsFilled, req.Body.Approved, notes)
	if err != nil {
		if errors.Is(err, assist.ErrUnapprovedSubmit) {
			return PostAssistSubmitdefaultApplicationProblemPlusJSONResponse{
				StatusCode: http.StatusBadRequest,
				Body: Problem{
					Status: http.StatusBadRequest,
					Code:   "assist.unapproved_submit",
				},
			}, nil
		}
		return PostAssistSubmitdefaultApplicationProblemPlusJSONResponse{
			StatusCode: http.StatusInternalServerError,
			Body: Problem{
				Status: http.StatusInternalServerError,
				Code:   "api.internal_error",
			},
		}, nil
	}

	return PostAssistSubmit200JSONResponse{
		ApplicationId: appID,
		Status:        status,
	}, nil
}

// GetAssistApprovals lists pending application approvals.
func (s *Server) GetAssistApprovals(ctx context.Context, _ GetAssistApprovalsRequestObject) (GetAssistApprovalsResponseObject, error) {
	userID, ok := ctx.Value(userCtxKey).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return GetAssistApprovalsdefaultApplicationProblemPlusJSONResponse{
			StatusCode: http.StatusUnauthorized,
			Body: Problem{
				Status: http.StatusUnauthorized,
				Code:   "api.unauthorized",
			},
		}, nil
	}

	items, err := s.Assist.ListApprovals(ctx, userID)
	if err != nil {
		return GetAssistApprovalsdefaultApplicationProblemPlusJSONResponse{ //nolint:nilerr // translated to problem JSON response
			StatusCode: http.StatusInternalServerError,
			Body: Problem{
				Status: http.StatusInternalServerError,
				Code:   "api.internal_error",
			},
		}, nil
	}

	res := make([]ApprovalItem, len(items))
	for i, it := range items {
		res[i] = ApprovalItem{
			Id:        it.ID,
			Company:   it.Company,
			Title:     it.Title,
			Url:       it.URL,
			Score:     it.Score,
			CreatedAt: it.CreatedAt,
		}
	}

	return GetAssistApprovals200JSONResponse{
		Items: res,
	}, nil
}

// PostAssistApprovalDecide approves or rejects an application submission.
func (s *Server) PostAssistApprovalDecide(ctx context.Context, req PostAssistApprovalDecideRequestObject) (PostAssistApprovalDecideResponseObject, error) {
	userID, ok := ctx.Value(userCtxKey).(uuid.UUID)
	if !ok || userID == uuid.Nil {
		return PostAssistApprovalDecidedefaultApplicationProblemPlusJSONResponse{
			StatusCode: http.StatusUnauthorized,
			Body: Problem{
				Status: http.StatusUnauthorized,
				Code:   "api.unauthorized",
			},
		}, nil
	}

	decision := string(req.Body.Decision)
	if err := s.Assist.DecideApproval(ctx, userID, req.Id, decision); err != nil {
		return PostAssistApprovalDecidedefaultApplicationProblemPlusJSONResponse{ //nolint:nilerr // translated to problem JSON response
			StatusCode: http.StatusInternalServerError,
			Body: Problem{
				Status: http.StatusInternalServerError,
				Code:   "api.internal_error",
			},
		}, nil
	}

	status := "approved"
	if decision == "reject" {
		status = "rejected"
	}

	return PostAssistApprovalDecide200JSONResponse{
		Id:     req.Id,
		Status: status,
	}, nil
}

// Mount registers the API on mux under /api/v1.
func Mount(mux *http.ServeMux, assistSvc AssistService) http.Handler {
	server := &Server{Assist: assistSvc, pairs: newPairLimiter()}
	var middlewares []StrictMiddlewareFunc
	if assistSvc != nil {
		middlewares = append(middlewares, func(f StrictHandlerFunc, _ string) StrictHandlerFunc {
			return func(ctx context.Context, w http.ResponseWriter, r *http.Request, request any) (any, error) {
				auth := r.Header.Get("Authorization")
				if strings.HasPrefix(auth, "Bearer ") {
					tokenPlain := strings.TrimPrefix(auth, "Bearer ")
					if tok, err := assistSvc.Authenticate(ctx, tokenPlain); err == nil {
						ctx = context.WithValue(ctx, userCtxKey, tok.UserID)
					}
				}
				return f(ctx, w, r, request)
			}
		})
	}

	return HandlerWithOptions(NewStrictHandler(server, middlewares), StdHTTPServerOptions{
		BaseURL:    "/api/v1",
		BaseRouter: mux,
	})
}
