# seccomp profile for the runner

`chromium.json` is Docker's default seccomp profile plus one rule: `clone`, `unshare`, `setns` and `chroot` are
allowed without `CAP_SYS_ADMIN`, so Chromium can build its namespace sandbox (own user, PID and network namespace
per renderer). This keeps the sandbox on; the runner never starts Chromium with `--no-sandbox`. Without the profile
Chromium refuses to start ("No usable sandbox!").

The browser tests (`go tool task test:browser`) verify that renderers really run in their own PID namespace.

Regenerate after a Docker update:

```bash
# upstream: github.com/moby/profiles, seccomp/default.json
# pinned commit: 6fe7deb1b9fb7c0397a4593480d7d22b9ee8caef (2026-09-17)
go run deploy/seccomp/gen.go path/to/default.json > deploy/seccomp/chromium.json
```

Hosts with AppArmor restrictions for unprivileged user namespaces (Ubuntu 23.10+) additionally need an AppArmor
profile that allows `userns` for the runner container, see `docs/runbooks/deploy.md`.
