package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// fakeSource builds an environment with files for tests.
func fakeSource(env map[string]string, files map[string]string) Source {
	return Source{
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		Environ: func() []string {
			out := make([]string, 0, len(env))
			for k, v := range env {
				out = append(out, k+"="+v)
			}
			return out
		},
		Open: func(name string) (io.ReadCloser, error) {
			v, ok := files[name]
			if !ok {
				return nil, fs.ErrNotExist
			}
			return io.NopCloser(strings.NewReader(v)), nil
		},
	}
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(fakeSource(nil, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTPAddr != ":8080" || c.HealthAddr != ":8081" {
		t.Errorf("addrs = %q %q", c.HTTPAddr, c.HealthAddr)
	}
	if c.PublicURL.String() != "https://localhost:8443" {
		t.Errorf("public url = %v", c.PublicURL)
	}
	if c.LogLevel != slog.LevelInfo || c.LogFormat != "json" || c.ShutdownTimeout != 20*time.Second {
		t.Errorf("log/shutdown defaults wrong: %v %q %v", c.LogLevel, c.LogFormat, c.ShutdownTimeout)
	}
	if c.DatabaseURL != "" || c.EgressProxy != nil {
		t.Errorf("optional values set without env")
	}
}

func TestLoadValuesAndFiles(t *testing.T) {
	env := map[string]string{
		"GETAFJOB_HTTP_ADDR":              "127.0.0.1:9000",
		"GETAFJOB_DATABASE_URL":           "postgres://getafjob@postgres:5432/getafjob?sslmode=disable",
		"GETAFJOB_DATABASE_PASSWORD_FILE": "/run/secrets/db_password",
		"GETAFJOB_LOG_LEVEL":              "debug",
		"GETAFJOB_LOG_FORMAT":             "text",
		"GETAFJOB_EGRESS_PROXY":           "http://egress:4750",
		"GETAFJOB_SHUTDOWN_TIMEOUT":       "5s",
		"GETAFJOB_HTTP_ADRR":              "typo",
		"OTHER":                           "ignored",
	}
	files := map[string]string{"/run/secrets/db_password": "s3cr3t\r\n"}
	c, err := Load(fakeSource(env, files))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTPAddr != "127.0.0.1:9000" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.DatabasePassword.Reveal() != "s3cr3t" {
		t.Errorf("password from file not trimmed: %q", c.DatabasePassword.Reveal())
	}
	if c.LogLevel != slog.LevelDebug || c.LogFormat != "text" || c.ShutdownTimeout != 5*time.Second {
		t.Errorf("parsed values wrong")
	}
	if c.EgressProxy.Host != "egress:4750" {
		t.Errorf("EgressProxy = %v", c.EgressProxy)
	}
	if len(c.Unknown) != 1 || c.Unknown[0] != "GETAFJOB_HTTP_ADRR" {
		t.Errorf("Unknown = %v", c.Unknown)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := []struct {
		name  string
		env   map[string]string
		files map[string]string
		want  string
	}{
		{"both value and file", map[string]string{"GETAFJOB_DATABASE_PASSWORD": "a", "GETAFJOB_DATABASE_PASSWORD_FILE": "/x"}, map[string]string{"/x": "b"}, "both set"},
		{"missing file", map[string]string{"GETAFJOB_DATABASE_PASSWORD_FILE": "/nope"}, nil, "does not exist"},
		{"empty file path", map[string]string{"GETAFJOB_DATABASE_PASSWORD_FILE": ""}, nil, "_FILE is empty"},
		{"huge file", map[string]string{"GETAFJOB_DATABASE_PASSWORD_FILE": "/big"}, map[string]string{"/big": strings.Repeat("x", maxFileSize+1)}, "larger than"},
		{"bad addr", map[string]string{"GETAFJOB_HTTP_ADDR": "8080"}, nil, "host:port"},
		{"bad public url", map[string]string{"GETAFJOB_PUBLIC_URL": "ftp://x"}, nil, "http(s) URL"},
		{"public url with credentials", map[string]string{"GETAFJOB_PUBLIC_URL": "https://a:b@x"}, nil, "credentials"},
		{"bad db url", map[string]string{"GETAFJOB_DATABASE_URL": "mysql://u:p@h/db"}, nil, "postgres://"},
		{"bad level", map[string]string{"GETAFJOB_LOG_LEVEL": "loud"}, nil, "GETAFJOB_LOG_LEVEL"},
		{"bad format", map[string]string{"GETAFJOB_LOG_FORMAT": "xml"}, nil, "json or text"},
		{"bad proxy", map[string]string{"GETAFJOB_EGRESS_PROXY": "socks5://x:1"}, nil, "http(s) URL"},
		{"bad timeout", map[string]string{"GETAFJOB_SHUTDOWN_TIMEOUT": "10m"}, nil, "between 1s and 5m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(fakeSource(tc.env, tc.files))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestErrorsNeverContainSecrets(t *testing.T) {
	// A broken port makes url.Parse fail with the complete URL (including the password).
	env := map[string]string{"GETAFJOB_DATABASE_URL": "postgres://u:hunter2@host:99999999999/db\x7f"}
	_, err := Load(fakeSource(env, nil))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Fatalf("error leaks the password: %v", err)
	}
}

func TestSecretIsRedactedEverywhere(t *testing.T) {
	s := Secret("hunter2")
	for _, out := range []string{
		fmt.Sprint(s), fmt.Sprintf("%v %s %#v %q", s, s, s, s), slog.AnyValue(s).Resolve().String(),
	} {
		if strings.Contains(out, "hunter2") {
			t.Errorf("secret leaked: %q", out)
		}
	}
	b, _ := s.MarshalText()
	if string(b) != redacted {
		t.Errorf("MarshalText = %q", b)
	}
	if s.Reveal() != "hunter2" {
		t.Errorf("Reveal = %q", s.Reveal())
	}
	c := Config{DatabaseURL: "postgres://u:hunter2@h/db", DatabasePassword: s}
	var sb strings.Builder
	slog.New(slog.NewTextHandler(&sb, nil)).Info("cfg", "config", c)
	if strings.Contains(sb.String(), "hunter2") {
		t.Errorf("config log leaks secret: %s", sb.String())
	}
}

func TestOpenErrorDoesNotLeakPath(t *testing.T) {
	src := fakeSource(map[string]string{"GETAFJOB_DATABASE_PASSWORD_FILE": "/secret/path"}, nil)
	src.Open = func(string) (io.ReadCloser, error) { return nil, errors.New("open /secret/path: permission denied") }
	_, err := Load(src)
	if err == nil || strings.Contains(err.Error(), "/secret/path") {
		t.Fatalf("err = %v", err)
	}
}

func TestNames(t *testing.T) {
	n := Names()
	if len(n) != len(specs) || n[0] != "GETAFJOB_HTTP_ADDR" {
		t.Fatalf("Names = %v", n)
	}
}

func TestCrawlSettings(t *testing.T) {
	c, err := Load(fakeSource(map[string]string{
		"GETAFJOB_CRAWL_INTERVAL": "2h",
		"GETAFJOB_CRAWL_QUERIES":  " Softwareentwickler , Go Developer,,",
		"GETAFJOB_CRAWL_CITIES":   "Berlin",
	}, nil))
	if err != nil || c.CrawlInterval != 2*time.Hour || len(c.CrawlQueries) != 2 || c.CrawlQueries[1] != "Go Developer" ||
		len(c.CrawlCities) != 1 {
		t.Fatalf("config = %v %v %v, %v", c.CrawlInterval, c.CrawlQueries, c.CrawlCities, err)
	}
	if d, _ := Load(fakeSource(nil, nil)); d.CrawlInterval != 6*time.Hour || d.CrawlQueries != nil {
		t.Fatalf("defaults = %v %v", d.CrawlInterval, d.CrawlQueries)
	}
	for name, v := range map[string]string{
		"GETAFJOB_CRAWL_INTERVAL": "5m",
		"GETAFJOB_CRAWL_QUERIES":  strings.Repeat("x", 101),
	} {
		if _, err := Load(fakeSource(map[string]string{name: v}, nil)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s=%q: err = %v", name, v, err)
		}
	}
}
