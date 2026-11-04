//go:build ignore

// gen derives chromium.json from Docker's default seccomp profile: everything the default allows, plus the
// syscalls Chromium's namespace sandbox needs without CAP_SYS_ADMIN (user/pid/net namespaces and chroot).
// This keeps the browser sandbox on instead of running Chromium with --no-sandbox.
//
// Usage (upstream file from github.com/moby/profiles, seccomp/default.json at the commit noted in README.md):
//
//	go run deploy/seccomp/gen.go path/to/default.json > deploy/seccomp/chromium.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type profile struct {
	DefaultAction   json.RawMessage   `json:"defaultAction"`
	DefaultErrnoRet json.RawMessage   `json:"defaultErrnoRet,omitempty"`
	ArchMap         json.RawMessage   `json:"archMap"`
	Syscalls        []json.RawMessage `json:"syscalls"`
}

type rule struct {
	Names   []string `json:"names"`
	Action  string   `json:"action"`
	Comment string   `json:"comment"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run deploy/seccomp/gen.go <moby default.json>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var p profile
	if err := json.Unmarshal(raw, &p); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	extra, err := json.Marshal(rule{
		Names:   []string{"clone", "unshare", "setns", "chroot"},
		Action:  "SCMP_ACT_ALLOW",
		Comment: "getafjob: Chromium namespace sandbox (user, pid and net namespaces, chroot) without CAP_SYS_ADMIN",
	})
	if err != nil {
		panic(err)
	}
	p.Syscalls = append(p.Syscalls, extra)
	out, err := json.MarshalIndent(p, "", "\t")
	if err != nil {
		panic(err)
	}
	_, _ = os.Stdout.Write(append(out, '\n'))
}
