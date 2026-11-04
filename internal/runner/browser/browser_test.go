//go:build browser

package browser

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/proto"
)

// Browser tests run inside the runner image (go tool task test:browser) with the same seccomp profile as
// production; GETAFJOB_BROWSER_BIN points to headless-shell there.
func launch(t *testing.T, opts Options) *Browser {
	t.Helper()
	if opts.Bin == "" {
		opts.Bin = os.Getenv("GETAFJOB_BROWSER_BIN")
	}
	b, err := Launch(context.Background(), opts)
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(func() {
		if err := b.Close(context.Background()); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return b
}

func TestLaunchKeepsSandboxAndSiteIsolation(t *testing.T) {
	b := launch(t, Options{})
	args := b.Args()
	for _, f := range []string{"no-sandbox", "disable-site-isolation-trials", "single-process", "no-zygote"} {
		if HasFlag(args, f) {
			t.Errorf("forbidden flag --%s in %v", f, args)
		}
	}
	if !HasFlag(args, "site-per-process") {
		t.Errorf("site isolation not enforced: %v", args)
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--disable-features=") && strings.Contains(a, "site-per-process") {
			t.Errorf("site-per-process disabled: %s", a)
		}
	}
	v, err := b.Version()
	if err != nil || !strings.Contains(v, "Chrome/") {
		t.Fatalf("version = %q, %v", v, err)
	}
}

// TestRendererRunsInItsOwnNamespaces checks the sandbox from the outside: Chromium's namespace sandbox puts
// every renderer into its own PID namespace, so /proc/<pid>/status shows two PIDs on the NSpid line.
func TestRendererRunsInItsOwnNamespaces(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("namespace sandbox is Linux only")
	}
	b := launch(t, Options{})
	page, err := b.Page(proto.TargetCreateTarget{URL: "about:blank"})
	if err != nil {
		t.Fatal(err)
	}
	if err := page.Timeout(10 * time.Second).SetDocumentContent("<p>sandbox probe</p>"); err != nil {
		t.Fatal(err)
	}
	renderers := 0
	procs, _ := filepath.Glob("/proc/[0-9]*/cmdline")
	for _, cmdPath := range procs {
		cmd, err := os.ReadFile(cmdPath)
		if err != nil || !strings.Contains(string(cmd), "--type=renderer") {
			continue
		}
		renderers++
		if strings.Contains(string(cmd), "--no-sandbox") {
			t.Errorf("renderer without sandbox: %s", cmdPath)
		}
		status, err := os.ReadFile(filepath.Join(filepath.Dir(cmdPath), "status"))
		if err != nil {
			t.Fatalf("read status: %v", err)
		}
		for _, line := range strings.Split(string(status), "\n") {
			if strings.HasPrefix(line, "NSpid:") && len(strings.Fields(line)) < 3 {
				t.Errorf("renderer %s shares the PID namespace (%q) – namespace sandbox off", cmdPath, line)
			}
		}
	}
	if renderers == 0 {
		t.Fatal("no renderer process found")
	}
}

func TestForbiddenExtraFlagsAreRejected(t *testing.T) {
	for _, f := range []string{"no-sandbox", "disable-web-security", "single-process"} {
		_, err := Launch(context.Background(), Options{
			Bin: os.Getenv("GETAFJOB_BROWSER_BIN"), ExtraFlags: map[string]string{f: ""},
		})
		if err == nil {
			t.Errorf("extra flag %s accepted", f)
		}
	}
}
