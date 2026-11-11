package views

// sgColors are the color tokens shown as swatches; styleguide.css has one .sg-<token> rule per entry.
var sgColors = []string{
	"bg", "surface", "surface-2", "sunken", "border", "border-strong", "text", "text-2", "text-3",
	"accent", "accent-soft", "accent-text", "on-accent", "success", "success-soft", "success-text",
	"warning", "warning-soft", "warning-text", "danger", "danger-soft", "danger-text", "info", "info-soft",
	"info-text", "focus",
}

// sgIcons lists every symbol of static/icons.svg; TestIconSprite keeps both in sync.
var sgIcons = []string{
	"home", "search", "briefcase", "bookmark", "live", "inbox", "check-circle", "check", "x", "chevron-down",
	"chevron-up", "chevron-left", "chevron-right", "arrow-up", "arrow-down", "arrow-up-down", "list", "file-text",
	"file", "columns", "user", "users", "message", "type", "settings", "shield", "bell", "sun", "moon", "monitor",
	"menu", "plus", "external", "more", "logout", "globe", "eye", "upload", "download", "trash", "edit", "filter",
	"refresh", "play", "pause", "clock", "send", "pointer", "alert", "info", "command", "calendar", "building",
	"map-pin", "server", "database", "star", "lock", "ban",
}

// SGIcons returns the icon names of the sprite, for tests.
func SGIcons() []string { return sgIcons }

// ParseTheme accepts the manual theme values; anything else means "follow the system".
func ParseTheme(s string) string {
	if s == "light" || s == "dark" {
		return s
	}
	return ""
}
