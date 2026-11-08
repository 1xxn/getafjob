package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/application"
	"github.com/1xxn/getafjob/internal/portals"
)

// autonomyAdapter adapts application.Service to portals.AutonomySettingsReader.
type autonomyAdapter struct {
	apps *application.Service
}

var _ portals.AutonomySettingsReader = autonomyAdapter{}

func newAutonomyAdapter(apps *application.Service) autonomyAdapter {
	return autonomyAdapter{apps: apps}
}

func (a autonomyAdapter) IsEasyApplyEnabled(ctx context.Context, userID uuid.UUID) (bool, bool, error) {
	if a.apps == nil {
		return false, false, nil
	}
	st, err := a.apps.Settings(ctx, userID)
	if err != nil {
		return false, false, err
	}
	return st.EasyApplyEnabled, st.EasyApplyTermsAcceptedAt != nil, nil
}
