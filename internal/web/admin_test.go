package web_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/identity"
)

func signedInAs(t *testing.T, a *app, email string, role identity.Role) string {
	t.Helper()
	link, _, err := a.ids.Invite(t.Context(), identity.InviteRequest{Email: email, Role: role})
	if err != nil {
		t.Fatal(err)
	}
	rec := a.do(http.MethodPost, strings.TrimPrefix(link, "https://jobs.example.org"), url.Values{"name": {strings.Split(email, "@")[0]}, "locale": {"en"}}, "", nil)
	return sessionFrom(t, rec)
}

// Only admins see the console; an admin invites without the CLI, changes roles, signs out and deletes accounts,
// never their own, and every action shows in the log.
func TestAdminConsole(t *testing.T) {
	a := newApp(t)
	admin := signedInAs(t, a, "admin@example.org", identity.RoleAdmin)
	user := signedIn(t, a)

	for _, p := range []string{"/admin/users", "/admin/system"} {
		if rec := a.do(http.MethodGet, p, nil, user, nil); rec.Code != http.StatusNotFound {
			t.Errorf("user sees %s: %d", p, rec.Code)
		}
	}
	if rec := a.do(http.MethodPost, "/admin/invites", url.Values{"email": {"x@example.org"}}, user, nil); rec.Code != http.StatusNotFound {
		t.Errorf("user invites: %d", rec.Code)
	}
	if home := a.do(http.MethodGet, "/", nil, user, nil).Body.String(); strings.Contains(home, `href="/admin/users"`) {
		t.Error("user sees the admin navigation")
	}
	if home := a.do(http.MethodGet, "/", nil, admin, nil).Body.String(); !strings.Contains(home, `href="/admin/users"`) {
		t.Error("admin lacks the admin navigation")
	}

	// Invite: the link shows once; a taken address is refused.
	rec := a.do(http.MethodPost, "/admin/invites", url.Values{"email": {"erika.musterfrau@example.org"}, "role": {"user"}}, admin, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "https://jobs.example.org/invite/") || !strings.Contains(rec.Body.String(), "data-invite-row=\"erika.musterfrau@example.org\"") {
		t.Fatalf("invite %d: %s", rec.Code, rec.Body.String())
	}
	if rec := a.do(http.MethodPost, "/admin/invites", url.Values{"email": {"max.mustermann@example.com"}, "role": {"user"}}, admin, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "exists already") {
		t.Errorf("taken address %d", rec.Code)
	}
	invites, _ := a.ids.PendingInvites(t.Context())
	if len(invites) != 1 {
		t.Fatalf("invites %v", invites)
	}
	if rec := a.do(http.MethodPost, "/admin/invites/"+invites[0].ID.String()+"/revoke", nil, admin, nil); rec.Header().Get("Location") != "/admin/users?done=revoked" {
		t.Errorf("revoke %d", rec.Code)
	}

	maxUser, _ := a.ids.UserByEmail(t.Context(), "max.mustermann@example.com")
	adm, _ := a.ids.UserByEmail(t.Context(), "admin@example.org")
	post := func(path string, form url.Values) *http.Response {
		return a.do(http.MethodPost, path, form, admin, nil).Result()
	}
	if r := post("/admin/users/"+maxUser.ID.String()+"/role", url.Values{"role": {"admin"}}); r.Header.Get("Location") != "/admin/users?done=role" {
		t.Fatalf("make admin %d", r.StatusCode)
	}
	if r := post("/admin/users/"+maxUser.ID.String()+"/delete", nil); r.StatusCode != http.StatusConflict {
		t.Errorf("deleted an admin: %d", r.StatusCode)
	}
	_ = post("/admin/users/"+maxUser.ID.String()+"/role", url.Values{"role": {"user"}})
	if r := post("/admin/users/"+adm.ID.String()+"/role", url.Values{"role": {"user"}}); r.StatusCode != http.StatusConflict {
		t.Errorf("changed the own role: %d", r.StatusCode)
	}
	if err := a.ids.SetRole(t.Context(), uuid.New(), adm.ID, identity.RoleUser); !errors.Is(err, identity.ErrLastAdmin) {
		t.Errorf("last admin demoted: %v", err)
	}

	// Sign out ends the user's session; delete removes the account.
	if r := post("/admin/users/"+maxUser.ID.String()+"/sign-out", nil); r.Header.Get("Location") != "/admin/users?done=signed_out" {
		t.Fatalf("sign out %d", r.StatusCode)
	}
	if rec := a.do(http.MethodGet, "/profile", nil, user, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("session still valid: %d", rec.Code)
	}
	if r := post("/admin/users/"+maxUser.ID.String()+"/delete", nil); r.Header.Get("Location") != "/admin/users?done=deleted" {
		t.Fatalf("delete %d", r.StatusCode)
	}
	page := a.do(http.MethodGet, "/admin/users?done=deleted", nil, admin, nil).Body.String()
	for _, want := range []string{"The account and all its data are deleted.", "An account was deleted", "Invitation created (role User)",
		"Invitation revoked", "a deleted account signed out everywhere", "That is you"} {
		if !strings.Contains(page, want) {
			t.Errorf("users page lacks %q", want)
		}
	}
	if strings.Contains(page, "max.mustermann@example.com") {
		t.Error("deleted account still listed")
	}
	if sys := a.do(http.MethodGet, "/admin/system", nil, admin, nil).Body.String(); !strings.Contains(sys, "Passkeys") {
		t.Errorf("system page: %s", sys)
	}
}
