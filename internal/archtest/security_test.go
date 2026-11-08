package archtest

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/log"
)

var tableRx = regexp.MustCompile(`(?i)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z0-9_]+)`)
var rlsRx = regexp.MustCompile(`(?i)ALTER\s+TABLE\s+([a-z0-9_]+)\s+ENABLE\s+ROW\s+LEVEL\s+SECURITY`)

// publicTables are catalog or discovery tables that hold public postings and boards, not tenant data.
var publicTables = map[string]bool{
	"boards":             true,
	"crawl_runs":         true,
	"jobs":               true,
	"job_snapshots":      true,
	"board_checks":       true,
	"schema_migrations":  true,
	"river_job":          true,
	"river_leader":       true,
	"river_client":       true,
	"river_client_queue": true,
	"river_migration":    true,
}

// identityLoopTables are enabled in migration 00003 via DO $$ loop.
var identityLoopTables = map[string]bool{
	"magic_links":         true,
	"sessions":            true,
	"passkeys":            true,
	"webauthn_challenges": true,
}

func TestDatabaseMigrationsEnforceRLS(t *testing.T) {
	files, err := filepath.Glob("../../db/migrations/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob migrations: %v (count %d)", err, len(files))
	}
	allSql := ""
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		allSql += "\n" + string(b)
	}

	created := map[string]bool{}
	for _, m := range tableRx.FindAllStringSubmatch(allSql, -1) {
		name := strings.ToLower(m[1])
		created[name] = true
	}

	enabled := map[string]bool{}
	for _, m := range rlsRx.FindAllStringSubmatch(allSql, -1) {
		name := strings.ToLower(m[1])
		enabled[name] = true
	}
	for tbl := range identityLoopTables {
		enabled[tbl] = true
	}

	for tbl := range created {
		if publicTables[tbl] {
			continue
		}
		if !enabled[tbl] {
			t.Errorf("tenant table %q created without ENABLE ROW LEVEL SECURITY", tbl)
		}
	}
}

func TestConfigSecretsUseSecretType(t *testing.T) {
	cfgType := reflect.TypeOf(config.Config{})
	secretType := reflect.TypeOf(config.Secret(""))

	for i := 0; i < cfgType.NumField(); i++ {
		field := cfgType.Field(i)
		name := field.Name
		if strings.Contains(strings.ToLower(name), "secret") ||
			strings.Contains(strings.ToLower(name), "password") ||
			strings.Contains(strings.ToLower(name), "key") {
			// Skip public keys or non-secret fields
			if strings.Contains(strings.ToLower(name), "public") || strings.Contains(strings.ToLower(name), "match") {
				continue
			}
			if field.Type != secretType {
				t.Errorf("config field %s has type %v, must be config.Secret", name, field.Type)
			}
		}
	}
}

func TestSensitiveKeywordsCoverage(t *testing.T) {
	required := []string{
		"password", "secret", "token", "cookie", "api_key", "private_key",
		"email", "phone", "address", "session", "credential", "iban",
	}
	for _, kw := range required {
		if !log.Sensitive(kw) {
			t.Errorf("log.Sensitive(%q) = false, must be true for ASVS compliance", kw)
		}
	}
}
