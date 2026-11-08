package pgstore_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/application"
)

func TestAutonomySettingsAndApprovals(t *testing.T) {
	ctx := context.Background()
	pool, st, svc, now := setup(t)
	runs := application.NewRuns(svc, st)

	max := user(t, pool, "max@example.org")

	// 1. Default settings
	settings, err := svc.Settings(ctx, max)
	if err != nil {
		t.Fatalf("Settings() err = %v", err)
	}
	if settings != application.DefaultSettings {
		t.Fatalf("Settings() = %+v, want DefaultSettings", settings)
	}

	// 2. Save settings
	newSettings := application.Settings{
		Level:      application.LevelAutonomous,
		MinScore:   90,
		DailyLimit: 10,
	}
	if err := svc.SaveSettings(ctx, max, newSettings); err != nil {
		t.Fatalf("SaveSettings() err = %v", err)
	}
	settings, err = svc.Settings(ctx, max)
	if err != nil {
		t.Fatalf("Settings() err = %v", err)
	}
	if settings != newSettings {
		t.Fatalf("Settings() = %+v, want %+v", settings, newSettings)
	}

	// 3. Application that needs approval because level is 'approve'
	if err := svc.SaveSettings(ctx, max, application.Settings{
		Level:      application.LevelApprove,
		MinScore:   70,
		DailyLimit: 5,
	}); err != nil {
		t.Fatal(err)
	}

	jobID := job(t, pool, "test", "Acme", "match-key-1")
	appID, err := svc.Create(ctx, max, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Queue(ctx, max, jobID); err != nil {
		t.Fatal(err)
	}

	lease, ok, err := runs.Lease(ctx, "runner-1")
	if err != nil || !ok {
		t.Fatalf("Lease() = %v, %v", ok, err)
	}
	ref, err := runs.Authorize(ctx, lease.Token)
	if err != nil {
		t.Fatal(err)
	}

	// Set run adapter
	if err := runs.SetAdapter(ctx, ref, "greenhouse"); err != nil {
		t.Fatal(err)
	}

	// BeginSubmit fails because level=approve -> triggers approval request
	err = runs.BeginSubmit(ctx, ref)
	var ae *application.ApprovalError
	if !errors.As(err, &ae) {
		t.Fatalf("BeginSubmit() err = %v, want ApprovalError", err)
	}
	if !slices.Contains(ae.Reasons, application.ReasonLevel) {
		t.Fatalf("ae.Reasons = %v, want ReasonLevel", ae.Reasons)
	}

	// Application is now awaiting approval
	approvals, err := svc.Approvals(ctx, max)
	if err != nil {
		t.Fatalf("Approvals() err = %v", err)
	}
	if len(approvals) != 1 || approvals[0].ID != appID {
		t.Fatalf("Approvals() = %+v, want 1 approval for app %s", approvals, appID)
	}
	if approvals[0].Company != "Acme" {
		t.Fatalf("Approvals() company = %q, want Acme", approvals[0].Company)
	}
	if !slices.Contains(approvals[0].Reasons, application.ReasonLevel) {
		t.Fatalf("Approvals() reasons = %v, want ReasonLevel", approvals[0].Reasons)
	}

	// User approves application
	*now = now.Add(time.Minute)
	toState, err := svc.Approve(ctx, max, appID)
	if err != nil {
		t.Fatalf("Approve() err = %v", err)
	}
	// Application was in NeedsInput, so it stays in NeedsInput until resumed/requeued
	app, err := st.Get(ctx, max, appID)
	if err != nil {
		t.Fatal(err)
	}
	if app.Approved == nil {
		t.Fatalf("app.Approved = nil after Approve")
	}

	// Approvals list is now empty
	approvals, err = svc.Approvals(ctx, max)
	if err != nil {
		t.Fatal(err)
	}
	if len(approvals) != 0 {
		t.Fatalf("Approvals() = %+v, want empty after approval", approvals)
	}
	_ = toState
}
