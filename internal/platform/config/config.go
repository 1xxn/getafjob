// Package config loads the configuration of every role from environment variables.
//
// Every variable GETAFJOB_<NAME> can be given as GETAFJOB_<NAME>_FILE instead; the value is then read from that
// file (Docker secrets). Setting both is an error. Secrets use the [Secret] type, which redacts itself when
// printed or logged.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Prefix starts every variable name.
const Prefix = "GETAFJOB_"

// maxFileSize caps *_FILE files (secrets are small; guards against accidentally linked huge files).
const maxFileSize = 64 << 10

// Config is the complete configuration. Each role checks the fields it needs itself.
type Config struct {
	// HTTPAddr is the address of the web UI/API (serve).
	HTTPAddr string
	// HealthAddr is the internal address for /healthz and /readyz of the worker and runner roles.
	HealthAddr string
	// GatewayAddr is the internal address of the runner gateway (serve); only the gateway network reaches it.
	GatewayAddr string
	// RunnerSecret authenticates the server's runners at the gateway; empty: the gateway stays off.
	RunnerSecret Secret
	// RunnerKind is "server" (default) or "home" for a home runner.
	RunnerKind string
	// GatewayURL is where a runner reaches the gateway (http://serve:8090). Empty: the runner only self-tests.
	GatewayURL *url.URL
	// PublicURL is the public address behind Caddy (links in mails, WebAuthn origin, cookies).
	PublicURL *url.URL
	// DatabaseURL is the PostgreSQL connection (postgres://…); the password should rather come from DatabasePassword.
	DatabaseURL Secret
	// DatabasePassword overrides the password in DatabaseURL.
	DatabasePassword Secret
	// DatabaseAppPassword is set on the application role by migrate (serve and worker log in with it).
	DatabaseAppPassword Secret
	// LogLevel and LogFormat configure slog.
	LogLevel  slog.Level
	LogFormat string
	// BrowserBin is the path to Chromium/headless-shell (runner). Empty: look it up.
	BrowserBin string
	// EgressProxy is the egress proxy for the browser and source fetches (http://egress:4750). Empty: direct.
	EgressProxy *url.URL
	// OTelEndpoint is the OTLP/HTTP endpoint for traces and metrics (http://otel-collector:4318). Empty: off.
	OTelEndpoint *url.URL
	// MasterKey wraps the users' data keys (32 bytes, base64url); MasterKeyOld is the previous one during a
	// rotation (admin rotate-master-key).
	MasterKey, MasterKeyOld Secret
	// BlobURL names the file store: file:///var/lib/getafjob/blobs or s3://bucket/prefix?endpoint=…&region=….
	// BlobAccessKey and BlobSecretKey are the S3 credentials.
	BlobURL                      string
	BlobAccessKey, BlobSecretKey Secret
	// SMTPURL is the mail server (smtps://user:pass@host:465, smtp://…:587 with STARTTLS). Empty: no mail.
	SMTPURL Secret
	// MailFrom is the sender address of all mail.
	MailFrom string
	// VAPIDKey is the P-256 private key for Web Push notifications (32 bytes, base64url).
	VAPIDKey Secret
	// VAPIDSubject is the contact address in VAPID tokens (mailto: or https://).
	VAPIDSubject string
	// ShutdownTimeout bounds the graceful shutdown.
	ShutdownTimeout time.Duration
	// CrawlInterval is the time between two crawls (worker).
	CrawlInterval time.Duration
	// EasyApplyEnabled is an operator switch enabling the Easy Apply module (#43). Default: false.
	EasyApplyEnabled bool
	// DevFixtureHost is the host:port of the development fixture site whose plain http origin may take form values;
	// honored only when PublicURL is local.
	DevFixtureHost string
	// AIEnabled is an operator switch enabling the optional AI module (#44). Default: false.
	AIEnabled bool
	// AnthropicAPIKey is the operator's Anthropic API key. Empty: users must supply their own key.
	AnthropicAPIKey Secret
	// AnthropicModel is the default Anthropic model for AI operations. Default: claude-3-5-haiku-20241022.
	AnthropicModel string
	// AIBudgetPerUserCents is the budget cap per user in USD cents when using the operator key. Default: 500 (5 USD).
	AIBudgetPerUserCents int

	// Unknown lists GETAFJOB_* variables that are set but read by nobody (typos); names only, never values.
	Unknown []string
}

// Source abstracts environment and file system (tests).
type Source struct {
	LookupEnv func(string) (string, bool)
	Environ   func() []string
	Open      func(string) (io.ReadCloser, error)
}

// OS reads the real process environment.
func OS() Source {
	return Source{
		LookupEnv: os.LookupEnv,
		Environ:   os.Environ,
		Open:      func(name string) (io.ReadCloser, error) { return os.Open(name) }, //nolint:gosec // path is set by the operator (Docker secret)
	}
}

type spec struct {
	name string
	def  string
	set  func(c *Config, v string) error
}

var specs = []spec{
	{"HTTP_ADDR", ":8080", func(c *Config, v string) error { return setAddr(&c.HTTPAddr, v) }},
	{"HEALTH_ADDR", ":8081", func(c *Config, v string) error { return setAddr(&c.HealthAddr, v) }},
	{"GATEWAY_ADDR", ":8090", func(c *Config, v string) error { return setAddr(&c.GatewayAddr, v) }},
	{"RUNNER_SECRET", "", setRunnerSecret},
	{"RUNNER_KIND", "server", setRunnerKind},
	{"GATEWAY_URL", "", setGatewayURL},
	{"PUBLIC_URL", "https://localhost:8443", setPublicURL},
	{"DATABASE_URL", "", setDatabaseURL},
	{"DATABASE_PASSWORD", "", func(c *Config, v string) error { c.DatabasePassword = Secret(v); return nil }},
	{"DATABASE_APP_PASSWORD", "", func(c *Config, v string) error { c.DatabaseAppPassword = Secret(v); return nil }},
	{"LOG_LEVEL", "info", func(c *Config, v string) error { return c.LogLevel.UnmarshalText([]byte(v)) }},
	{"LOG_FORMAT", "json", setLogFormat},
	{"BROWSER_BIN", "", func(c *Config, v string) error { c.BrowserBin = v; return nil }},
	{"EGRESS_PROXY", "", setEgressProxy},
	{"OTEL_ENDPOINT", "", setOTelEndpoint},
	{"MASTER_KEY", "", func(c *Config, v string) error { return setKey(&c.MasterKey, v) }},
	{"MASTER_KEY_OLD", "", func(c *Config, v string) error { return setKey(&c.MasterKeyOld, v) }},
	{"BLOB_URL", "", setBlobURL},
	{"BLOB_ACCESS_KEY", "", func(c *Config, v string) error { c.BlobAccessKey = Secret(v); return nil }},
	{"BLOB_SECRET_KEY", "", func(c *Config, v string) error { c.BlobSecretKey = Secret(v); return nil }},
	{"SHUTDOWN_TIMEOUT", "20s", setShutdownTimeout},
	{"SMTP_URL", "", func(c *Config, v string) error { c.SMTPURL = Secret(v); return nil }},
	{"MAIL_FROM", "getafjob@localhost", setMailFrom},
	{"VAPID_KEY", "", func(c *Config, v string) error { return setKey(&c.VAPIDKey, v) }},
	{"VAPID_SUBJECT", "", setVAPIDSubject},
	{"CRAWL_INTERVAL", "6h", setCrawlInterval},
	{"EASY_APPLY_ENABLED", "false", func(c *Config, v string) error { return setBool(&c.EasyApplyEnabled, v) }},
	{"DEV_FIXTURE_HOST", "", func(c *Config, v string) error { c.DevFixtureHost = strings.ToLower(strings.TrimSpace(v)); return nil }},
	{"AI_ENABLED", "false", func(c *Config, v string) error { return setBool(&c.AIEnabled, v) }},
	{"ANTHROPIC_API_KEY", "", func(c *Config, v string) error { c.AnthropicAPIKey = Secret(v); return nil }},
	{"ANTHROPIC_MODEL", "claude-3-5-haiku-20241022", func(c *Config, v string) error { c.AnthropicModel = strings.TrimSpace(v); return nil }},
	{"AI_BUDGET_PER_USER_CENTS", "500", func(c *Config, v string) error {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return errors.New("must be a positive integer")
		}
		c.AIBudgetPerUserCents = n
		return nil
	}},
}

// Names returns all known variable names (with prefix), e.g. for the docs.
func Names() []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, Prefix+s.name)
	}
	return out
}

// Load reads and validates the configuration. Errors name variables only, never values.
func Load(src Source) (Config, error) {
	var c Config
	var errs []error
	known := map[string]bool{}
	for _, s := range specs {
		known[Prefix+s.name] = true
		known[Prefix+s.name+"_FILE"] = true
		v, ok, err := lookup(src, s.name)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !ok {
			v = s.def
		}
		if err := s.set(&c, v); err != nil {
			errs = append(errs, fmt.Errorf("%s%s: %w", Prefix, s.name, err))
		}
	}
	if src.Environ != nil {
		for _, kv := range src.Environ() {
			name, _, _ := strings.Cut(kv, "=")
			if strings.HasPrefix(name, Prefix) && !known[name] {
				c.Unknown = append(c.Unknown, name)
			}
		}
		slices.Sort(c.Unknown)
	}
	return c, errors.Join(errs...)
}

// lookup reads GETAFJOB_<name> or GETAFJOB_<name>_FILE.
func lookup(src Source, name string) (string, bool, error) {
	key := Prefix + name
	v, ok := src.LookupEnv(key)
	path, fileOK := src.LookupEnv(key + "_FILE")
	switch {
	case ok && fileOK:
		return "", false, fmt.Errorf("%s and %s_FILE are both set", key, key)
	case !fileOK:
		return v, ok, nil
	}
	if path == "" {
		return "", false, fmt.Errorf("%s_FILE is empty", key)
	}
	f, err := src.Open(path)
	if err != nil {
		// Only the kind of error, not the path.
		if errors.Is(err, fs.ErrNotExist) {
			return "", false, fmt.Errorf("%s_FILE: file does not exist", key)
		}
		return "", false, fmt.Errorf("%s_FILE: cannot open file", key)
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return "", false, fmt.Errorf("%s_FILE: cannot read file", key)
	}
	if len(b) > maxFileSize {
		return "", false, fmt.Errorf("%s_FILE: file larger than %d bytes", key, maxFileSize)
	}
	s := string(b)
	s = strings.TrimSuffix(s, "\n")
	s = strings.TrimSuffix(s, "\r")
	return s, true, nil
}

// setRunnerSecret takes a secret of at least 32 characters (admin gen-secret gives 43).
func setRunnerSecret(c *Config, v string) error {
	if v != "" && len(v) < 32 {
		return errors.New("must be at least 32 characters (getafjob admin gen-secret)")
	}
	c.RunnerSecret = Secret(v)
	return nil
}

func setRunnerKind(c *Config, v string) error {
	v = strings.ToLower(strings.TrimSpace(v))
	if v != "" && v != "server" && v != "home" && v != "home_runner" {
		return fmt.Errorf("GETAFJOB_RUNNER_KIND: unknown runner kind %q (must be server or home)", v)
	}
	if v == "home" || v == "home_runner" {
		c.RunnerKind = "home"
	} else {
		c.RunnerKind = "server"
	}
	return nil
}

func setAddr(dst *string, v string) error {
	if _, _, err := net.SplitHostPort(v); err != nil {
		return errors.New("must be host:port, e.g. :8080")
	}
	*dst = v
	return nil
}

func setPublicURL(c *Config, v string) error {
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("must be an absolute http(s) URL")
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("must not contain credentials, query or fragment")
	}
	c.PublicURL = u
	return nil
}

func setDatabaseURL(c *Config, v string) error {
	if v == "" {
		return nil
	}
	// url.Parse errors contain the URL including the password, hence our own message.
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Host == "" {
		return errors.New("must be a postgres:// URL")
	}
	c.DatabaseURL = Secret(v)
	return nil
}

func setLogFormat(c *Config, v string) error {
	if v != "json" && v != "text" {
		return errors.New("must be json or text")
	}
	c.LogFormat = v
	return nil
}

func setGatewayURL(c *Config, v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return errors.New("must be an http(s) URL without path or credentials")
	}
	c.GatewayURL = u
	return nil
}

func setEgressProxy(c *Config, v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("must be an http(s) URL")
	}
	c.EgressProxy = u
	return nil
}

func setMailFrom(c *Config, v string) error {
	if strings.ContainsAny(v, "\r\n <>") || !strings.Contains(v, "@") {
		return errors.New("must be a plain e-mail address")
	}
	c.MailFrom = v
	return nil
}

func setShutdownTimeout(c *Config, v string) error {
	d, err := time.ParseDuration(v)
	if err != nil || d < time.Second || d > 5*time.Minute {
		return errors.New("must be a duration between 1s and 5m")
	}
	c.ShutdownTimeout = d
	return nil
}

func setCrawlInterval(c *Config, v string) error {
	d, err := time.ParseDuration(v)
	if err != nil || d < 30*time.Minute || d > 7*24*time.Hour {
		return errors.New("must be a duration between 30m and 168h")
	}
	c.CrawlInterval = d
	return nil
}

func setVAPIDSubject(c *Config, v string) error {
	if v == "" {
		return nil
	}
	if !strings.HasPrefix(v, "mailto:") && !strings.HasPrefix(v, "https://") {
		return errors.New("must be a mailto: or https:// URL")
	}
	c.VAPIDSubject = v
	return nil
}

// LogValue logs harmless fields only.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("http_addr", c.HTTPAddr),
		slog.String("health_addr", c.HealthAddr),
		slog.String("public_url", urlString(c.PublicURL)),
		slog.Bool("database_configured", c.DatabaseURL != ""),
		slog.Bool("runner_secret_set", c.RunnerSecret != ""),
		slog.String("runner_kind", c.RunnerKind),
		slog.String("log_level", c.LogLevel.String()),
		slog.String("log_format", c.LogFormat),
		slog.Bool("egress_proxy_set", c.EgressProxy != nil),
		slog.String("otel_endpoint", urlString(c.OTelEndpoint)),
		slog.Bool("smtp_configured", c.SMTPURL != ""),
		slog.Bool("vapid_configured", c.VAPIDKey != ""),
		slog.Bool("master_key_set", c.MasterKey != ""),
		slog.Bool("old_master_key_set", c.MasterKeyOld != ""),
		slog.String("blob_store", blobScheme(c.BlobURL)),
		slog.Duration("shutdown_timeout", c.ShutdownTimeout),
	)
}

func urlString(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.String()
}

func setOTelEndpoint(c *Config, v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return errors.New("must be an http(s) URL without credentials")
	}
	c.OTelEndpoint = u
	return nil
}

// setKey accepts 32 bytes in base64url (admin gen-secret); the error never shows the value.
func setKey(dst *Secret, v string) error {
	v = strings.TrimRight(strings.TrimSpace(v), "=")
	if v == "" {
		return nil
	}
	if b, err := base64.RawURLEncoding.DecodeString(v); err != nil || len(b) != 32 {
		return errors.New("must be 32 bytes, base64url encoded (admin gen-secret)")
	}
	*dst = Secret(v)
	return nil
}

func setBlobURL(c *Config, v string) error {
	if v == "" {
		return nil
	}
	u, err := url.Parse(v)
	if err != nil || (u.Scheme != "file" && u.Scheme != "s3") || u.User != nil {
		return errors.New("must be file:///path or s3://bucket/prefix?endpoint=…, without credentials")
	}
	c.BlobURL = v
	return nil
}

// blobScheme logs only the kind of store.
func blobScheme(v string) string {
	scheme, _, _ := strings.Cut(v, ":")
	return scheme
}

func setBool(dst *bool, v string) error {
	v = strings.ToLower(strings.TrimSpace(v))
	switch v {
	case "true", "1", "yes", "on":
		*dst = true
	case "false", "0", "no", "off", "":
		*dst = false
	default:
		return errors.New("must be a boolean (true or false)")
	}
	return nil
}
