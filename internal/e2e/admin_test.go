//go:build e2e

package e2e

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
)

var (
	adminOnce    sync.Once
	adminBrowser *rod.Browser
	adminErr     error
)

// Admin is a browser signed in as an invited admin of the stack.
func Admin(t *testing.T) *rod.Browser {
	t.Helper()
	invite := os.Getenv("GETAFJOB_E2E_ADMIN_INVITE")
	if invite == "" {
		t.Skip("GETAFJOB_E2E_ADMIN_INVITE not set: no admin")
	}
	adminOnce.Do(func() {
		adminBrowser, adminErr = shared.Incognito()
		if adminErr == nil {
			adminErr = signIn(adminBrowser, invite)
		}
	})
	if adminErr != nil {
		t.Fatalf("admin sign in: %v", adminErr)
	}
	return adminBrowser
}

// An admin invites without the CLI, sees the link once and revokes it, at both
// sizes without horizontal scroll.
func TestAdmin(t *testing.T) {
	tab := OpenIn(t, Admin(t), 1440, 900, "dark")
	tab.Go("/admin/users")
	tab.El(`.adm[data-admin="/admin/users"] .shell-link[aria-current="page"]`)
	email := "e2e-invitee-" + strconv.FormatInt(time.Now().UnixNano(), 36) + "@example.org"
	tab.Eval("document.getElementById('f-email').value = '" + email + "'; return true")
	tab.Click(`[data-invite]`)
	tab.WaitWithin(10*time.Second, "!!document.querySelector('[data-invite-link] input')")
	if link := tab.Eval("return document.querySelector('[data-invite-link] input').value"); !strings.Contains(link, "/invite/") {
		t.Fatalf("invitation link %s", link)
	}
	// Copy: the button confirms, or (no clipboard permission) selects the link for Ctrl+C.
	tab.Click(`[data-copy="invite-link"]`)
	tab.WaitWithin(5*time.Second, `(() => { const b = document.querySelector('[data-copy="invite-link"]');
		return b.querySelector('span').textContent === b.dataset.copied || document.activeElement.id === 'invite-link' })()`)
	tab.Axe("/admin/users")
	tab.Shot("invited")
	tab.Click(`[data-invite-row="` + email + `"] button[type="submit"]`)
	tab.WaitFor("document.getElementById('confirm-dialog').open")
	tab.Click("[data-confirm-ok]")
	tab.WaitWithin(10*time.Second, "location.search === '?done=revoked' && !document.querySelector('[data-invite-row=\""+email+"\"]')")

	tab.Go("/admin/system")
	tab.El(".adm-facts dd")
	// The dev stack runs the egress proxy: the probe reaches it. Its backups are not recorded, so the page warns.
	if tab.Eval(`return !!document.querySelector('[data-fact="admin.system.egress"] .ui-tone-success')`) != "true" {
		t.Error("egress proxy not shown as reachable")
	}
	tab.El(`[data-fact="admin.system.backup"] .ui-tone-warning`)
	tab.Axe("/admin/system")

	phone := OpenIn(t, Admin(t), 375, 812, "light")
	for _, p := range []string{"/admin/runners", "/admin/sources", "/admin/queue"} {
		tab.Go(p)
		tab.El("#set-title")
		tab.Axe(p)
	}
	tab.Shot("sources")

	for _, p := range []string{"/admin/users", "/admin/system", "/admin/sources", "/admin/queue"} {
		phone.Go(p)
		phone.El("#set-title")
		phone.NoOverflow(p)
	}
	phone.Shot("users")
}
