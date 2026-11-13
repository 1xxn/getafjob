package views

import (
	"context"
	"strings"

	"github.com/1xxn/getafjob/internal/web/views/ui"
)

// NavCounts are the numbers next to navigation items.
type NavCounts struct {
	NeedsInput int
	Approvals  int
	Running    int
}

// Shell is what the app frame needs besides the page: who is signed in, the theme, the current path and the
// counters. The page middleware stores it in the request context.
type Shell struct {
	UserName  string
	Admin     bool
	Theme     string            // light, dark or empty (system)
	Path      string            // current path, for the active navigation item
	Back      string            // current path and query, where the theme and language forms return to
	Collapsed bool              // sidebar shows icons only
	Flash     string            // catalog key of a one-time message, shown as a toast
	Counts    func() *NavCounts // nil or returning nil: no counters
	Dock      string            // live dock layout "<side>:<size>", empty for the default
}

// DockSide and DockSize split the dock layout; right and md by default.
func (s Shell) DockSide() string {
	if side, _, ok := strings.Cut(s.Dock, ":"); ok {
		return side
	}
	return "right"
}

func (s Shell) DockSize() string {
	if _, size, ok := strings.Cut(s.Dock, ":"); ok {
		return size
	}
	return "md"
}

type shellKey struct{}

// WithShell stores the frame data for Layout.
func WithShell(ctx context.Context, s Shell) context.Context {
	return context.WithValue(ctx, shellKey{}, s)
}

// ShellFrom returns the frame data of the request (zero value if none).
func ShellFrom(ctx context.Context) Shell {
	s, _ := ctx.Value(shellKey{}).(Shell)
	return s
}

type navItem struct {
	Key   string // catalog key of the label
	Href  string
	Icon  string
	Count string // data-count name; empty: no counter
}

type navGroup struct {
	Key   string // catalog key of the heading; empty: no heading
	Items []navItem
	Admin bool // shown to admins only
}

// navGroups follow docs/ui/sitemap.md; pages that do not exist yet join their group when they are built.
var navGroups = []navGroup{
	{Items: []navItem{{Key: "nav.home", Href: "/", Icon: "home"}}},
	{Key: "nav.group.discover", Items: []navItem{{Key: "nav.jobs", Href: "/jobs", Icon: "briefcase"},
		{Key: "nav.searches", Href: "/searches", Icon: "bookmark"}}},
	{Key: "nav.group.apply", Items: []navItem{
		{Key: "nav.live", Href: "/live", Icon: "live", Count: "running"},
		{Key: "nav.needs_input", Href: "/needs-input", Icon: "inbox", Count: "needs_input"},
		{Key: "nav.approvals", Href: "/approvals", Icon: "check-circle", Count: "approvals"},
	}},
	{Key: "nav.group.track", Items: []navItem{
		{Key: "nav.applications", Href: "/applications", Icon: "send"},
		{Key: "nav.pipeline", Href: "/pipeline", Icon: "columns"},
	}},
	{Key: "nav.group.me", Items: []navItem{
		{Key: "nav.profile", Href: "/profile", Icon: "user"},
		{Key: "nav.documents", Href: "/documents", Icon: "file"},
		{Key: "nav.answers", Href: "/answers", Icon: "message"},
		{Key: "nav.texts", Href: "/texts", Icon: "type"},
	}},
	{Key: "nav.group.settings", Items: []navItem{
		{Key: "nav.preferences", Href: "/settings/search", Icon: "settings"},
		{Key: "nav.account", Href: "/settings/account", Icon: "lock"},
	}},
	{Key: "nav.group.admin", Admin: true, Items: []navItem{
		{Key: "admin.users", Href: "/admin/users", Icon: "users"},
		{Key: "admin.runners", Href: "/admin/runners", Icon: "monitor"},
		{Key: "admin.sources", Href: "/admin/sources", Icon: "database"},
		{Key: "admin.queue", Href: "/admin/queue", Icon: "list"},
		{Key: "admin.system", Href: "/admin/system", Icon: "server"},
	}},
}

// bottomNav are the items of the phone tab bar; "Menu" opens the full navigation.
var bottomNav = []navItem{
	{Key: "nav.home", Href: "/", Icon: "home"},
	{Key: "nav.jobs", Href: "/jobs", Icon: "briefcase"},
	{Key: "nav.approvals", Href: "/approvals", Icon: "check-circle", Count: "approvals"},
	{Key: "nav.pipeline", Href: "/pipeline", Icon: "columns"},
}

// active reports whether the navigation item is the current page or one of its sub pages.
func (n navItem) active(path string) bool {
	if n.Href == "/" {
		return path == "/"
	}
	if n.Href == "/settings/search" { // the settings sections share one entry; the account has its own
		return strings.HasPrefix(path, "/settings/") && path != "/settings/account"
	}
	return path == n.Href || strings.HasPrefix(path, n.Href+"/")
}

func (n navItem) count(c *NavCounts) int {
	if c == nil {
		return 0
	}
	switch n.Count {
	case "approvals":
		return c.Approvals
	case "needs_input":
		return c.NeedsInput
	case "running":
		return c.Running
	}
	return 0
}

// layoutTheme is the page's forced theme, else the reader's choice.
func layoutTheme(ctx context.Context, p Page) string {
	if p.Theme != "" {
		return p.Theme
	}
	return ShellFrom(ctx).Theme
}

func themeIcon(t string) string {
	switch t {
	case "light":
		return "sun"
	case "dark":
		return "moon"
	}
	return "monitor"
}

func themeOptions() []string { return []string{"", "light", "dark"} }

func themeLabel(t string) string {
	if t == "" {
		return "nav.theme.system"
	}
	return "nav.theme." + t
}

func boolAttr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// shellFor returns the frame data of the request; the page's user name wins when the context has none.
func shellFor(ctx context.Context, p Page) Shell {
	s := ShellFrom(ctx)
	if s.UserName == "" {
		s.UserName = p.UserName
	}
	if s.Back == "" {
		s.Back = "/"
	}
	return s
}

// flashTone shows partial results as a warning.
func flashTone(key string) ui.Tone {
	if strings.HasSuffix(key, "_partial") || strings.Contains(key, ".not_queued.") {
		return ui.Warning
	}
	return ui.Success
}
