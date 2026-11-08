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
		Level:        application.LevelAutonomous,
		MinScore:     90,
		DailyLimit:   10,
		RunnerChoice: application.RunnerServer,
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
		Level:        application.LevelApprove,
		MinScore:     70,
		DailyLimit:   5,
		RunnerChoice: application.RunnerServer,
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

	lease, ok, err := runs.Lease(ctx, "runner-1", application.RunnerServer, nil)
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

func TestRunnerChoiceAndHomeRunnerTokens(t *testing.T) {
	ctx := context.Background()
	pool, st, svc, now := setup(t)
	runs := application.NewRuns(svc, st)

	alice := user(t, pool, "alice@example.org")
	bob := user(t, pool, "bob@example.org")
	carol := user(t, pool, "carol@example.org")

	// Set choices
	if err := svc.SaveSettings(ctx, alice, application.Settings{
		Level:        application.LevelApprove,
		MinScore:     70,
		DailyLimit:   5,
		RunnerChoice: application.RunnerServer,
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.SaveSettings(ctx, bob, application.Settings{
		Level:        application.LevelApprove,
		MinScore:     70,
		DailyLimit:   5,
		RunnerChoice: application.RunnerHomeRunner,
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.SaveSettings(ctx, carol, application.Settings{
		Level:        application.LevelApprove,
		MinScore:     70,
		DailyLimit:   5,
		RunnerChoice: application.RunnerExtension,
	}); err != nil {
		t.Fatal(err)
	}

	// Home runner token generation & lookup for Bob
	bobToken, err := svc.GenerateHomeRunnerToken(ctx, bob)
	if err != nil || bobToken == "" {
		t.Fatalf("GenerateHomeRunnerToken() err = %v, token = %q", err, bobToken)
	}
	bobHash := application.HashHomeRunnerToken(bobToken)
	foundUser, err := st.UserByHomeRunnerToken(ctx, bobHash)
	if err != nil || foundUser != bob {
		t.Fatalf("UserByHomeRunnerToken() = %v, err = %v, want %v", foundUser, err, bob)
	}

	// Queue applications
	jAlice := jobAt(t, pool, "https://alice.example.org/1", "Acme", "match-alice")
	aAlice, _ := svc.Create(ctx, alice, jAlice)
	_, _, _ = svc.Queue(ctx, alice, jAlice)

	*now = now.Add(time.Second)
	jBob := jobAt(t, pool, "https://bob.example.org/1", "Beispiel", "match-bob")
	aBob, _ := svc.Create(ctx, bob, jBob)
	_, _, _ = svc.Queue(ctx, bob, jBob)

	*now = now.Add(time.Second)
	jCarol := jobAt(t, pool, "https://carol.example.org/1", "Muster", "match-carol")
	aCarol, _ := svc.Create(ctx, carol, jCarol)
	_, _, _ = svc.Queue(ctx, carol, jCarol)

	// 1. Server runner leases: must lease Alice's job only
	leaseServer, ok, err := runs.Lease(ctx, "srv-1", application.RunnerServer, nil)
	if err != nil || !ok || leaseServer.AppID != aAlice {
		t.Fatalf("server runner lease: %+v, ok=%v, err=%v, want AppID=%v", leaseServer, ok, err, aAlice)
	}

	// Second server runner lease finds nothing
	if _, ok, err := runs.Lease(ctx, "srv-2", application.RunnerServer, nil); ok || err != nil {
		t.Fatalf("server runner second lease found unexpected run: ok=%v, err=%v", ok, err)
	}

	// 2. Bob's home runner leases: must lease Bob's job only
	leaseBob, ok, err := runs.Lease(ctx, "home-bob", application.RunnerHomeRunner, &bob)
	if err != nil || !ok || leaseBob.AppID != aBob {
		t.Fatalf("bob home runner lease: %+v, ok=%v, err=%v, want AppID=%v", leaseBob, ok, err, aBob)
	}

	// Bob's home runner second lease finds nothing
	if _, ok, err := runs.Lease(ctx, "home-bob", application.RunnerHomeRunner, &bob); ok || err != nil {
		t.Fatalf("bob home runner second lease found unexpected run: ok=%v, err=%v", ok, err)
	}

	// 3. Carol's home runner (she chose extension, so home runner has nothing)
	if _, ok, err := runs.Lease(ctx, "home-carol", application.RunnerHomeRunner, &carol); ok || err != nil {
		t.Fatalf("carol home runner found run but choice is extension: ok=%v, err=%v", ok, err)
	}

	// Carol switches to server
	if err := svc.SaveSettings(ctx, carol, application.Settings{
		Level:        application.LevelApprove,
		MinScore:     70,
		DailyLimit:   5,
		RunnerChoice: application.RunnerServer,
	}); err != nil {
		t.Fatal(err)
	}

	// Now server runner can lease Carol's job
	leaseCarol, ok, err := runs.Lease(ctx, "srv-3", application.RunnerServer, nil)
	if err != nil || !ok || leaseCarol.AppID != aCarol {
		t.Fatalf("carol switched to server: lease=%+v, ok=%v, err=%v, want AppID=%v", leaseCarol, ok, err, aCarol)
	}
}
