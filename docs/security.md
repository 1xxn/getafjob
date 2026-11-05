# Security

Threat model (STRIDE), controls and findings. Target: OWASP ASVS 4.0 level 2. Reporting: [SECURITY.md](../SECURITY.md).
This document grows with every module.

## Assets

| Asset | Where |
|---|---|
| Profiles, CVs, certificates, answers, application history | PostgreSQL, blob store (encrypted) |
| Mail credentials, portal sessions, AI keys | PostgreSQL (envelope-encrypted) |
| Master key, database password | Docker secrets |
| Evidence (screenshots, filled values) | blob store (encrypted, 90-day retention) |
| User intent | never submit without the rule or approval, never twice, never invented data |

## Trust boundaries

1. Internet ↔ Caddy (users, extension, attackers)
2. serve ↔ runner / home runner (gateway)
3. runner/worker ↔ internet (egress proxy)
4. serve/worker ↔ PostgreSQL, blob store
5. Page content ↔ engine (untrusted HTML, prompt injection for the optional AI)
6. Build and release supply chain (GitHub Actions, modules, images)

## STRIDE

| # | Threat | Boundary | Control | Status / test |
|---|---|---|---|---|
| S1 | Account takeover by password attacks | 1 | No passwords: invitations, passkeys, magic links ([0020](adr/0020-authentication-invitations-passkeys.md)); one-time tokens stored as SHA-256 only | `TestInvitationFlow`, `TestMagicLinkFlow` |
| S3 | Account enumeration and mail flooding via sign-in links | 1 | Same answer for unknown addresses; at most 3 links per account and 15 minutes | `TestMagicLinkFlow`, `TestMagicLinksAreRateLimited` |
| S2 | Stolen runner token reused | 2 | Short-lived per-run tokens, lease bound to runner | Phase 2 |
| S4 | Session cookie stolen or replayed | 1 | `__Host-` cookie, Secure, HttpOnly, SameSite=Lax; idle and absolute expiry; stored as hash | `TestInvitationAndSignOut`, `TestSessionIdleTimeoutAndTouch` |
| T1 | CSRF changes data | 1 | `http.CrossOriginProtection`, `__Host-` cookies | `TestProtectSetsHeadersAndBlocksCrossOriginPosts`, `TestCrossSitePostIsRejected` |
| T4 | Mail scanners use up sign-in links | 1 | Opening a link shows a button; only the POST signs in | `TestMagicLinkSignIn` |
| T5 | Script injection through user-controlled text | 1 | templ escapes all output; CSP without inline scripts | `TestInvitationAndSignOut` |
| T2 | Page tricks the engine into a wrong field or domain | 5 | Provenance, field/domain checks, page content is data | Phase 2 |
| T3 | Tampered images or binaries | 6 | Pinned actions/tools, cosign signatures, provenance, SBOM | CI |
| T6 | Hostile source data poisons the job index or merges unrelated jobs | 1 | Descriptions reduced to plain text (`htmltext`), length checks, board slugs checked before they become host names or paths (`TestFromBoardRejectsUnsafeSlugs`), a job board never replaces a company board, no cross-source match without a company, a failing source closes nothing; JSON-LD with block, size and depth limits; sitemap job pages must stay on the sitemap's host; a discovered board is added only if it lists one of the company's jobs (same `MatchKey`), never replaces a known board,, and guessed slugs pass the same checks; stored ETag and Last-Modified values are capped at 256 bytes and a 304 closes nothing | `FuzzText`, `FuzzSources`, `FuzzJobPostings`, `TestBoardFromURL`, `TestFindBoard`, `TestBoardChecks`, `TestConditionalFetch`, `TestCrawlNotModified`, `TestCrawl`, `TestShouldClose` |
| R1 | User disputes an application | n/a | Evidence: values + sources, screenshot, confirmation, timeline; audit log | Phase 2 |
| R2 | Security events cannot be traced or are altered | 4 | Append-only audit log (codes and params, no personal data) | `TestAuditEventsAreAppendOnly`, `TestAuditLogHasNoPersonalData` |
| I1 | User reads another user's data | 4 | RLS for the application role `getafjob_app` (no bypass), tenant set per transaction ([0006](adr/0006-tenant-isolation-rls-and-envelope-encryption.md)) | `TestTenantSeparation`, `TestAppRoleCannotBypassRLS` |
| I2 | SSRF to internal services or cloud metadata | 3 | `netguard`: URLs read like Chromium (WHATWG, UTS 46), every connection checked after DNS, redirects checked; egress proxy denies private/loopback/link-local; runner on internal networks only | `TestNonPublicURLs`, `TestClientRefusesPrivateTargetsPerConnection`, `FuzzPublicURL`, `deploy/smoke.sh` |
| I3 | Secrets or personal data in logs or errors | all | `config.Secret` redaction, sensitive log attributes redacted, generic errors | `TestSecretIsRedactedEverywhere`, `TestErrorsNeverContainSecrets`, `TestSensitiveKeysAreRedacted` |
| I4 | Readiness details leak internals | 1 | `/readyz` returns ok/fail only; hidden by Caddy | `TestReadyzReportsEachCheckWithoutDetails`, smoke test |
| I5 | One-time tokens in access logs | 1 | Caddy without access log | `deploy/Caddyfile` |
| I6 | Database dump exposes secrets | 4 | Envelope encryption, master key outside the database | Phase 1 |
| D1 | Slowloris / huge bodies | 1 | Server timeouts, header and body limits | `httpx.NewServer`, `TestProtect…` |
| D3 | Guessing or flooding on sign-in endpoints | 1 | Rate limit per client address (burst 10, then one request per 6 s); pages are `no-store` | `TestSignInIsRateLimited` |
| D2 | Hostile page exhausts the runner | 2/3 | Per-run limits, memory/pids limits, browser restarted per run | Phase 2 |
| D4 | Huge or endless source responses | 1 | Body cap (`sources.MaxBody`), request timeouts, page caps per source, bounded fetch and detail parallelism, at most 3 attempts with backoff and `Retry-After` up to 30 s | `TestPagingIsBounded`, `TestClientErrors`, `FuzzSources` |
| E1 | Browser exploit escapes to the host | 2 | Chromium sandbox on (seccomp profile), site isolation, non-root, read-only rootfs, `cap_drop: ALL`, no DB access | `TestRendererRunsInItsOwnNamespaces`, `TestLaunchKeepsSandboxAndSiteIsolation` |
| E2 | Compromised runner reaches data | 2 | Runner has no database access, depguard rule | `.golangci.yml` |
| E3 | Malicious workflow change escalates | 6 | `permissions: {}`, read-only default token, SHA pinning enforced, zizmor, ruleset | CI, repository settings |

## Controls in place (Phase 0)

- Configuration secrets redact themselves in `fmt`, `slog` and JSON; errors name variables, never values.
- Logs redact attributes with sensitive names (e-mail, phone, password, token, cookie …).
- Security headers: strict CSP (`script-src 'self'`, Trusted Types, `frame-ancestors 'none'`), HSTS, `nosniff`,
  `Referrer-Policy: no-referrer`, COOP/CORP, Permissions-Policy.
- Containers: distroless/non-root, read-only, `cap_drop: ALL`, `no-new-privileges`, internal networks.
- Browser: sandbox and site isolation on, downloads denied, proxy without loopback bypass, no non-proxied UDP.
- Repository: ruleset on `main` (PR, required checks, rebase only, no force push), protected `v*` tags, secret
  scanning with push protection, private vulnerability reporting.

## Findings

| # | Severity | Finding | Fix | Status |
|---|---|---|---|---|
| P0-1 | High | Trivy: `kin-openapi` (embedded spec) and smokescreen dependencies with HIGH advisories in images | Spec no longer embedded; smokescreen built from a patched build module | PR #52 |

## Residual risks

- A page sees everything filled into its own form, by design.
- The operator (admin with server access) can read data when holding the master key.
