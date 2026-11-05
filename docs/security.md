# Security

Threat model (STRIDE), controls and findings. Target: OWASP ASVS 4.0 level 2. Reporting: [SECURITY.md](../SECURITY.md).
This document grows with every module.

## Assets

| Asset | Where |
|---|---|
| Profiles, CVs, certificates, answers, application history | PostgreSQL, blob store (envelope-encrypted per user; search preferences stay readable for the crawler and hold no contact data) |
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
| T7 | Hostile upload (HTML or SVG disguised as a CV, zip bomb, path tricks in names) | 1 | Type from magic bytes only (PDF, DOCX, PNG, JPEG); DOCX checked as zip with bounded entries and size; names cleaned and sealed; files sealed in the blob store; download only as attachment with `Content-Security-Policy: sandbox`; 10 MB limit on the upload route only, read in memory without temporary files | `TestSniff`, `FuzzSniff`, `TestCleanName`, `TestDocumentPages`, `TestProtectUploadLimit` |
| R1 | User disputes an application | n/a | Evidence: values + sources, screenshot, confirmation, timeline; audit log | Phase 2 |
| R2 | Security events cannot be traced or are altered | 4 | Append-only audit log (codes and params, no personal data) | `TestAuditEventsAreAppendOnly`, `TestAuditLogHasNoPersonalData` |
| I1 | User reads another user's data | 4 | RLS for the application role `getafjob_app` (no bypass), tenant set per transaction ([0006](adr/0006-tenant-isolation-rls-and-envelope-encryption.md)) | `TestTenantSeparation`, `TestAppRoleCannotBypassRLS` |
| I2 | SSRF to internal services or cloud metadata | 3 | `netguard`: URLs read like Chromium (WHATWG, UTS 46), every connection checked after DNS, redirects checked; egress proxy denies private/loopback/link-local; runner on internal networks only | `TestNonPublicURLs`, `TestClientRefusesPrivateTargetsPerConnection`, `FuzzPublicURL`, `deploy/smoke.sh` |
| I3 | Secrets or personal data in logs, traces or errors | all | `config.Secret` redaction, sensitive log attributes redacted, generic errors; spans carry the route template instead of the raw path and query, job spans no error text, no trace headers to third-party sites | `TestSecretIsRedactedEverywhere`, `TestErrorsNeverContainSecrets`, `TestSensitiveKeysAreRedacted`, `TestInstrumentNamesSpansByRoute`, `TestTelemetryMiddleware` |
| I4 | Readiness details leak internals | 1 | `/readyz` returns ok/fail only; hidden by Caddy | `TestReadyzReportsEachCheckWithoutDetails`, smoke test |
| I5 | One-time tokens in access logs | 1 | Caddy without access log | `deploy/Caddyfile` |
| I6 | Database dump or blob storage exposes secrets | 4 | Envelope encryption: AES-256-GCM data key per user, wrapped with the master key (Docker secret, never in the database); associated data binds every ciphertext to user and purpose; `Forget` deletes the data key. Files are sealed before they reach the volume or bucket, under `users/<id>/`, bound to their key; keys are checked segment by segment and the directory store works through `os.Root` | `TestEnvelope`, `TestKeyring`, `FuzzOpen`, `TestSealed`, `TestSealedWithEnvelope`, `TestValidKey` |
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

## ASVS 5.0 L2: authentication (V6) and sessions (V7)

getafjob has no passwords (ADR 0020), so the password requirements 6.1.2, 6.2.x and 6.5.2 for weak secrets do
not apply. Federation and IdPs (6.8.x, 7.1.3, 7.6.1) do not apply either.

Sign-in pathways (6.1.3, 6.3.4): invitation link (first sign-in only), magic link by mail, passkey. There are no
others; API v1 has no sign-in yet.

| Req. | Requirement (short) | How | Test |
|---|---|---|---|
| 6.1.1, 6.3.1 | Documented and enforced rate limits | Sign-in and invitation endpoints: token bucket per client address; at most 3 magic links per account in 15 minutes | `TestSignInIsRateLimited`, `TestMagicLinksAreRateLimited` |
| 6.3.2 | No default accounts | Accounts only by invitation; no built-in users | `TestInvitationFlow` |
| 6.3.3 | MFA or combined factors | Passkeys require user verification (device plus PIN or biometrics, two factors). Once a passkey exists, a mail link alone no longer signs in ([0022](adr/0022-mail-link-as-delayed-recovery.md)). Accounts without a passkey still use the mail link alone and are asked on every visit to add one | `TestPasskeys`, `TestRecoveryInsteadOfMagicLink`, `TestPasskeyNudgeAndRecovery` |
| 6.4.1 | Initial secrets random, short-lived, one-time | Invitation links: 256 random bits, 7 days, used once, stored as SHA-256 | `TestInvitationFlow`, `TestExpiredInvitation` |
| 6.4.2 | No hints or secret questions | None exist | n/a |
| 6.4.3, 6.4.4 | Recovery does not bypass stronger factors | Recovery by mail link only after 24 hours, announced by mail, shown on the account page, canceled by any passkey sign-in; a notice after use | `TestRecoveryInsteadOfMagicLink`, `TestRecoveryWindow` |
| 6.5.1, 6.6.2 | Out-of-band links used once, for their request only | A magic link is consumed in the transaction that creates the session and names one account | `TestMagicLinkFlow` |
| 6.5.3, 6.5.4 | Random out-of-band codes with enough entropy | 256 bits from `crypto/rand` | `TestMagicLinkFlow` |
| 6.5.5 | Out-of-band lifetime at most 10 minutes | Magic links expire after 10 minutes | `TestMagicLinkFlow` |
| 6.6.3 | Brute force protection for codes | 256-bit tokens plus rate limits | `TestSignInIsRateLimited` |
| 6.7.2 (L3) | Challenge nonce of at least 64 bits, unique | WebAuthn with go-webauthn: 256-bit random challenge per ceremony, single use, 5 minutes; relying party is the public host, the only origin the public URL; user verification required; counter going backwards (cloned key) refused; a credential ID registers once | `TestPasskeys`, `TestPasskeyPages` |
| 7.1.1, 7.3.1, 7.3.2 | Documented idle and absolute timeout | Idle 7 days, absolute 30 days from sign-in, kept across rotations. Deviation from NIST SP 800-63B AAL2 (30 minutes idle, 24 hours absolute): getafjob works in the background for days and holds no payment data; sensitive changes need a fresh sign-in (7.5.x) | `TestSessionValidity`, `TestSessionIdleTimeoutAndTouch`, `TestSessionRotation` |
| 7.1.2 | Concurrent sessions documented | No limit; the account page lists them (up to 100) and ends the others | `TestAccountSessions` |
| 7.2.1, 7.2.2 | Tokens checked by the backend, generated per session | Reference tokens, looked up on every request | `TestInvitationAndSignOut` |
| 7.2.3 | Reference tokens from a CSPRNG, at least 128 bits | 256 bits from `crypto/rand`, stored as SHA-256 | `TestSessionRotation` |
| 7.2.4 | New token on (re-)authentication | Every sign-in creates a new session; tokens also rotate hourly, the old one stays valid for one minute | `TestSessionRotationRule`, `TestSessionCookieRotates` |
| 7.4.1 | Logout and expiry end the session in the backend | Sign-out deletes the session with all its rotated tokens; expired rows are removed | `TestSignOutEndsRotatedTokens`, `TestSessionCookieRotates` |
| 7.4.2 | Sessions end when an account is disabled or deleted | Disabled accounts fail authentication; deleting a user cascades to its sessions | `TestDisabledUser` |
| 7.4.3 | Option to end other sessions after a factor change | After adding or removing a passkey the account page says so and offers to sign out all other sessions | `TestPasskeyPages`, `TestAccountSessions` |
| 7.4.4 | Visible logout on every page | Sign-out button in the top bar of every signed-in page | `TestInvitationAndSignOut` |
| 7.4.5 | Admins end sessions of a user | `getafjob admin sign-out -email …` | `TestSignOutUser` |
| 7.5.1 | Re-authentication before changing sign-in methods | Adding and removing passkeys and ending other sessions need a sign-in in the last 15 minutes | `TestPasskeyChangesNeedFreshSignIn`, `TestPasskeyRegistrationNeedsFreshSignIn`, `TestSignOutOthersNeedsFreshSignIn` |
| 7.5.2 | View and end sessions after re-authentication | Account page lists sessions; ending the others needs a sign-in in the last 15 minutes | `TestAccountSessions`, `TestSignOutOthersAsksForFreshSignIn` |
| 7.6.2 | Sessions only by explicit action | Magic and invitation links show a button; only the POST signs in | `TestMagicLinkSignIn` |

## Findings

| # | Severity | Finding | Fix | Status |
|---|---|---|---|---|
| P0-1 | High | Trivy: `kin-openapi` (embedded spec) and smokescreen dependencies with HIGH advisories in images | Spec no longer embedded; smokescreen built from a patched build module | PR #52 |

## Residual risks

- A page sees everything filled into its own form, by design.
- The operator (admin with server access) can read data when holding the master key.
