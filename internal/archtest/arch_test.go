// Package archtest checks the module boundaries on the whole dependency graph (depguard only sees direct imports).
package archtest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const mod = "github.com/1xxn/getafjob/"

// modules are the business modules; each owns internal/<name>/...
var modules = []string{"identity", "discovery", "profile", "matching", "application", "tracking", "notify", "assist", "ai"}

type pkg struct {
	ImportPath string
	Imports    []string
	Deps       []string
}

var (
	once    sync.Once
	loaded  []pkg
	loadErr error
)

// load lists all packages with their imports and transitive dependencies once per test binary.
func load(t *testing.T) []pkg {
	t.Helper()
	once.Do(func() { loaded, loadErr = list() })
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	return loaded
}

func list() ([]pkg, error) {
	// go test caches results by the files the test itself opens; go list runs in a child process, so stat every
	// Go file here to invalidate the cache whenever code changes.
	if err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		_, err = os.Stat(path)
		return err
	}); err != nil {
		return nil, err
	}
	cmd := exec.Command("go", "list", "-json=ImportPath,Imports,Deps", mod+"...")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, stderr.String())
	}
	var pkgs []pkg
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p pkg
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, err
		}
		p.ImportPath = strings.TrimPrefix(p.ImportPath, mod)
		for i := range p.Imports {
			p.Imports[i] = strings.TrimPrefix(p.Imports[i], mod)
		}
		for i := range p.Deps {
			p.Deps[i] = strings.TrimPrefix(p.Deps[i], mod)
		}
		pkgs = append(pkgs, p)
	}
	if len(pkgs) < 20 {
		return nil, fmt.Errorf("only %d packages", len(pkgs))
	}
	return pkgs, nil
}

// moduleOf returns the module a package belongs to, or "".
func moduleOf(path string) string {
	for _, m := range modules {
		if path == "internal/"+m || strings.HasPrefix(path, "internal/"+m+"/") {
			return m
		}
	}
	return ""
}

func under(path, prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }

func TestModulesDoNotImportEachOther(t *testing.T) {
	for _, p := range load(t) {
		own := moduleOf(p.ImportPath)
		if own == "" {
			continue
		}
		for _, d := range p.Deps {
			if m := moduleOf(d); m != "" && m != own {
				t.Errorf("%s depends on module %s (%s); talk through a port wired in internal/app", p.ImportPath, m, d)
			}
		}
	}
}

func TestAdaptersOnlyWiredByApp(t *testing.T) {
	for _, p := range load(t) {
		for _, d := range p.Imports {
			m := moduleOf(d)
			if m == "" || !strings.Contains(d, "/adapters/") {
				continue
			}
			if !under(p.ImportPath, "internal/app") && moduleOf(p.ImportPath) != m {
				t.Errorf("%s imports adapter %s; only internal/app wires adapters", p.ImportPath, d)
			}
		}
	}
}

// ioDeps are dependencies that mean database, queue or browser access.
var ioDeps = []string{"github.com/jackc/pgx", "internal/platform/db", "github.com/riverqueue/river", "github.com/go-rod/rod"}

func usesIO(deps []string) string {
	for _, d := range deps {
		for _, x := range ioDeps {
			if under(d, x) {
				return d
			}
		}
	}
	return ""
}

func TestModuleCoreHasNoIO(t *testing.T) {
	for _, p := range load(t) {
		if m := moduleOf(p.ImportPath); m == "" || p.ImportPath != "internal/"+m {
			continue // only the core package; adapters do the I/O
		}
		if d := usesIO(p.Deps); d != "" {
			t.Errorf("%s depends on %s; I/O belongs in adapters/", p.ImportPath, d)
		}
	}
}

func TestRunnerHasNoDatabase(t *testing.T) {
	n := 0
	for _, p := range load(t) {
		if !under(p.ImportPath, "internal/runner") {
			continue
		}
		n++
		for _, d := range p.Deps {
			if under(d, "github.com/jackc/pgx") || under(d, "internal/platform/db") || under(d, "github.com/riverqueue/river") ||
				(moduleOf(d) != "" && strings.Contains(d, "/adapters/")) {
				t.Errorf("%s depends on %s; the runner has no database or queue access (ADR 0007)", p.ImportPath, d)
			}
		}
	}
	if n == 0 {
		t.Fatal("no runner packages found")
	}
}

func TestPlatformKnowsNoModules(t *testing.T) {
	for _, p := range load(t) {
		if !under(p.ImportPath, "internal/platform") {
			continue
		}
		for _, d := range p.Deps {
			if moduleOf(d) != "" || under(d, "internal/web") || under(d, "internal/runner") || under(d, "internal/app") {
				t.Errorf("%s depends on %s; platform is below the modules", p.ImportPath, d)
			}
		}
	}
}
