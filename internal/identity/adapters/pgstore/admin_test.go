package pgstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/i18n"
)

// Last seen stays on the account after its sessions end.
func TestLastSeenOutlivesSessions(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	_, session := signIn(t, e)
	e.now = e.now.Add(time.Hour)
	seen := e.now
	if _, _, err := e.svc.Authenticate(ctx, string(session)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SignOutUser(ctx, "max.mustermann@example.com"); err != nil {
		t.Fatal(err)
	}
	users, err := e.svc.Users(ctx)
	if err != nil || len(users) != 1 {
		t.Fatalf("users = %v, %v", users, err)
	}
	if !users[0].LastSeen.Equal(seen) {
		t.Fatalf("last seen = %v, want %v", users[0].LastSeen, seen)
	}
}

func TestSetDisabled(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	u, session := signIn(t, e)

	link, _, err := e.svc.Invite(ctx, identity.InviteRequest{Email: "erika.mustermann@example.com", Role: identity.RoleAdmin})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.AcceptInvitation(ctx, tokenFrom(link), "Erika", i18n.EN); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetDisabledByEmail(ctx, "erika.mustermann@example.com", true); !errors.Is(err, identity.ErrLastAdmin) {
		t.Fatalf("last admin disabled: %v", err)
	}

	if err := e.svc.SetDisabledByEmail(ctx, "max.mustermann@example.com", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Authenticate(ctx, string(session)); !errors.Is(err, identity.ErrSessionExpired) {
		t.Fatalf("session of a disabled account: %v", err)
	}
	mails := e.mail.count()
	if err := e.svc.RequestMagicLink(ctx, "max.mustermann@example.com"); err != nil || e.mail.count() != mails {
		t.Fatalf("link for a disabled account: mails %d, %v", e.mail.count()-mails, err)
	}

	if err := e.svc.SetDisabledByEmail(ctx, "max.mustermann@example.com", false); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.svc.UserByID(ctx, u.ID); got.Disabled {
		t.Fatal("still disabled")
	}
	if err := e.svc.SetDisabledByEmail(ctx, "nobody@example.com", true); !errors.Is(err, identity.ErrNotFound) {
		t.Fatalf("unknown address: %v", err)
	}
}
