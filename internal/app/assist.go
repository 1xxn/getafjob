package app

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/assist"
	assiststore "github.com/1xxn/getafjob/internal/assist/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/profile"
)

type assistProfileReader struct {
	profiles *profile.Service
	items    *profile.Items
	docs     *profile.Documents
}

func (r assistProfileReader) ReadAssistProfile(ctx context.Context, userID uuid.UUID) (map[string]string, []assist.Answer, []assist.TextBlock, []assist.Document, error) {
	snap, err := r.profiles.Get(ctx, userID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	answers, err := r.items.Answers(ctx, userID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	blocks, err := r.items.TextBlocks(ctx, userID)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	docList, err := r.docs.List(ctx, userID)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	ans := make([]assist.Answer, len(answers))
	for i, a := range answers {
		ans[i] = assist.Answer{ID: a.ID.String(), Question: a.Question, Answer: a.Answer}
	}

	txt := make([]assist.TextBlock, len(blocks))
	for i, b := range blocks {
		txt[i] = assist.TextBlock{ID: b.ID.String(), Name: b.Name, Body: b.Body}
	}

	dc := make([]assist.Document, len(docList))
	for i, d := range docList {
		dc[i] = assist.Document{ID: d.ID.String(), Name: d.Name, Kind: d.Kind}
	}

	return facts(snap.Profile, snap.Preferences), ans, txt, dc, nil
}

type assistAppService struct {
	apps *application.Service
}

func (s assistAppService) ListPendingApprovals(ctx context.Context, userID uuid.UUID) ([]assist.ApprovalItem, error) {
	approvals, err := s.apps.Approvals(ctx, userID)
	if err != nil {
		return nil, err
	}
	items := make([]assist.ApprovalItem, len(approvals))
	for i, a := range approvals {
		items[i] = assist.ApprovalItem{
			ID:        a.ID,
			Company:   a.Company,
			Title:     a.Title,
			Score:     a.Score,
			CreatedAt: a.Asked,
		}
	}
	return items, nil
}

func (s assistAppService) DecideApproval(ctx context.Context, userID, applicationID uuid.UUID, approve bool) error {
	if approve {
		_, err := s.apps.Approve(ctx, userID, applicationID)
		return err
	}
	_, err := s.apps.Fire(ctx, userID, applicationID, application.EvSkip, application.ActorUser, map[string]any{"reason": "assist_rejected"})
	return err
}

func (s assistAppService) RecordExternalSubmit(_ context.Context, _ uuid.UUID, _, _, _ string, _ int, _ string) (uuid.UUID, error) {
	return uuid.New(), nil
}

type assistUserReader struct {
	profiles *profile.Service
}

func (r assistUserReader) GetUserName(ctx context.Context, userID uuid.UUID) (string, error) {
	snap, err := r.profiles.Get(ctx, userID)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(snap.Profile.Personal.FirstName + " " + snap.Profile.Personal.LastName)
	if name == "" {
		name = "User"
	}
	return name, nil
}

func newAssistService(pool *pgxpool.Pool, profiles *profile.Service, items *profile.Items, docs *profile.Documents, apps *application.Service, logger *slog.Logger) *assist.Service {
	store := assiststore.New(pool)
	profileReader := assistProfileReader{profiles: profiles, items: items, docs: docs}
	appService := assistAppService{apps: apps}
	userReader := assistUserReader{profiles: profiles}
	return assist.NewService(store, profileReader, appService, userReader, logger)
}
