# End to end journeys

`internal/e2e` drives the running stack like a user does. Every Phase 8 page gets a journey there (#188).

```bash
go tool task dev:up      # stack on https://localhost:8443
go tool task test:e2e    # journeys; screenshots in bin/e2e/out
go tool task test:e2e -- -test.run TestShell   # one journey
```

How it runs:

- The test binary runs in the runner image with the same hardening as the runner (sandbox, seccomp profile,
  read-only file system, no capabilities) on the internal `edge` network, next to Caddy.
- Chromium starts through `internal/runner/browser`, so no security switch can be added. It reaches Caddy as
  `https://localhost` by a host resolver rule and trusts only the key of Caddy's local intermediate CA
  (`--ignore-certificate-errors-spki-list`); every other certificate still fails.
- Each run invites a fresh user (`admin invite`), so journeys never depend on demo data and never touch real data.
- A journey opens a tab with `Open(t, width, height, scheme)` and fails on console errors, CSP violations, script
  exceptions and a document status other than 200.

Write journeys against roles and visible texts where possible, keep them independent of each other, and take a
screenshot (`tab.Shot`) of every state worth looking at in review.
