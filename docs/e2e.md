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
- Each run invites a fresh user (`admin invite`), so most journeys never depend on demo data.
- Journeys with real runs (`Demo(t)`) use the demo user of `admin demo -fixtures http://fixtures:8080 -autonomy suggest`, whose jobs
  point at the fixture site. It signs in with a mail link read from Mailpit (the test container joins the
  `devtools` network). Mail links are limited to 3 per 15 minutes; after many local reruns, wait.
- A journey opens a tab with `Open(t, width, height, scheme)` and fails on console errors, CSP violations, script
  exceptions and a document status other than 200.
- `tab.Axe(name)` runs axe-core (pinned 4.10.3, embedded from `internal/e2e/testdata/axe`, hash checked) and fails
  on every WCAG 2.0 to 2.2 A/AA violation. `TestPagesRender` checks every page at phone and desktop size,
  `TestAccessibility` the demo user's pages at tablet size; the bell menu and the admin pages are checked in their
  journeys.
- `Admin(t)` is a browser signed in as an invited admin (`GETAFJOB_E2E_ADMIN_INVITE`). Journeys never open a page
  that answers 404 on purpose: the harness counts it as a problem.

Write journeys against roles and visible texts where possible, keep them independent of each other, and take a
screenshot (`tab.Shot`) of every state worth looking at in review.
