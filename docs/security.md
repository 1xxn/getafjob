# Security

Threat model (STRIDE), controls and findings. Target: OWASP ASVS 4.0 level 2. Reporting: [SECURITY.md](../SECURITY.md).
This document grows with every module.

## Assets

| Asset | Where |
|---|---|
| Profiles, CVs, certificates, answers, application history | PostgreSQL, blob store (envelope-encrypted per user; search preferences stay readable for the crawler and hold no contact data) |
| Mail credentials, portal sessions, AI keys | PostgreSQL (envelope-encrypted per user) |
| Master key, database password, runner secret | Docker secrets (`*_FILE`) |
| Evidence (screenshots, filled values, timeline) | blob store (encrypted, 90-day retention) |
| User intent | never submit without the rule or approval, never twice, never invented data, no cover letters |

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
| S2 | Stolen runner token reused | 2 | Short-lived per-run tokens (60 min), lease bound to runner; stored as hash; single-use token invalidated on completion | `TestGateway`, `TestLeaseHonorsHostLimits`, `TestConcurrentLeasesRespectTheHost` |
| S3 | Account enumeration and mail flooding via sign-in links | 1 | Same answer for unknown addresses; at most 3 links per account and 15 minutes | `TestMagicLinkFlow`, `TestMagicLinksAreRateLimited` |
| S4 | Session cookie stolen or replayed | 1 | `__Host-` cookie, Secure, HttpOnly, SameSite=Lax; idle and absolute expiry; stored as hash | `TestInvitationAndSignOut`, `TestSessionIdleTimeoutAndTouch` |
| T1 | CSRF changes data | 1 | `http.CrossOriginProtection`, `__Host-` cookies | `TestProtectSetsHeadersAndBlocksCrossOriginPosts`, `TestCrossSitePostIsRejected` |
| T2 | Page tricks the engine into a wrong field or domain | 5 | Provenance verification against profile facts; field and origin checks; page content treated strictly as untrusted data; cover letters strictly refused | `TestPlannerProvenance`, `TestDomainOrigins`, `TestFillRefuses`, `TestProvenanceFactCheck` |
| T3 | Tampered images or binaries | 6 | Pinned actions/tools, cosign signatures, provenance, SBOM | CI, `.github/workflows/ci.yml` |
| T4 | Mail scanners use up sign-in links | 1 | Opening a link shows a button; only the POST signs in | `TestMagicLinkSignIn` |
| T5 | Script injection through user-controlled text | 1 | templ escapes all output; CSP with a nonce per response, no inline scripts, Trusted Types with only the `htmx` policy; htmx elements without the nonce are inert (`hx-csp`); vendored htmx checked against its sums | `TestInvitationAndSignOut`, `TestJobPages`, `TestProtectSetsHeadersAndBlocksCrossOriginPosts`, `TestVendoredFilesMatchChecksums` |
| T6 | Hostile source data poisons the job index or merges unrelated jobs | 1 | Descriptions reduced to plain text (`htmltext`), length checks, board slugs checked before they become host names or paths (`TestFromBoardRejectsUnsafeSlugs`), a job board never replaces a company board, no cross-source match without a company, a failing source closes nothing; JSON-LD with block, size and depth limits; sitemap job pages must stay on the sitemap's host; a discovered board is added only if it lists one of the company's jobs (same `MatchKey`), never replaces a known board, and guessed slugs pass the same checks; stored ETag and Last-Modified values are capped at 256 bytes and a 304 closes nothing | `FuzzText`, `FuzzSources`, `FuzzJobPostings`, `TestBoardFromURL`, `TestFindBoard`, `TestBoardChecks`, `TestConditionalFetch`, `TestCrawlNotModified`, `TestCrawl`, `TestShouldClose` |
| T7 | Hostile upload (HTML or SVG disguised as a CV, zip bomb, path tricks in names) | 1 | Type from magic bytes only (PDF, DOCX, PNG, JPEG); DOCX checked as zip with bounded entries and size; names cleaned and sealed; files sealed in the blob store; download only as attachment with `Content-Security-Policy: sandbox`; 10 MB limit on the upload route only, read in memory without temporary files | `TestSniff`, `FuzzSniff`, `TestCleanName`, `TestDocumentPages`, `TestProtectUploadLimit` |
| T8 | Duplicate or invented application (race, crash, retry, wrong field) | 2 | Submit register: a `submit_attempt` row unique per application, written with the state `submitting` in one transaction before the click; concurrent submits lose on the version or the unique key; crashes become `submit_unknown`, never retried; a parked application keeps its attempt, so it is never sent again; every value needs provenance, fields are checked against label and origin before filling and verified afterwards | `TestSubmitRegister`, `TestParkedAfterSubmitIsNeverSentAgain`, `TestCrashWhileSubmittingBecomesUnknown`, `TestTransitions`, `TestFillRefuses` |
| T9 | Easy Apply session hijacking or account ban by portal | 1 / 3 | Explicit user opt-in with terms acceptance; envelope-encrypted session cookies; daily quota limit (1–20, default 5); automatic handoff for interactive user re-auth without credential storage | `TestEasyApplyOptInRequired`, `TestPortalSessionsEncrypted`, `TestEasyApplyDailyLimit`, `TestReviewStepDetection` |
| T10 | Prompt injection via job description or AI budget exhaustion | 5 | Anthropic Messages API with tool-calling for strict JSON schema enforcement; operator key budget caps in integer millionths of cents; encrypted user keys; drafts always require approval; cover letters refused | `TestCalculateCostCents`, `TestBudgetCaps`, `TestCoverLetterRefused`, `TestEnforceApprovalOnDrafts`, `TestProvenanceFactCheck` |
| R1 | User disputes an application | n/a | Evidence preserved: filled values + sources, screenshots per step, confirmation page screenshot, timeline events; append-only audit log | `TestSubmitRegister`, `TestCheckpointScreenshot`, `TestApplicationTimeline`, `TestAuditEventsAreAppendOnly` |
| R2 | Security events cannot be traced or are altered | 4 | Append-only audit log (codes and params, no personal data, triggers prevent UPDATE/DELETE) | `TestAuditEventsAreAppendOnly`, `TestAuditLogHasNoPersonalData` |
| I1 | User reads another user's data | 4 | RLS for the application role `getafjob_app` (FORCE RLS, no bypass), tenant set per transaction ([0006](adr/0006-tenant-isolation-rls-and-envelope-encryption.md)) | `TestTenantSeparation`, `TestAppRoleCannotBypassRLS`, `TestDatabaseMigrationsEnforceRLS` |
| I2 | SSRF to internal services or cloud metadata | 3 | `netguard`: URLs read like Chromium (WHATWG, UTS 46), every connection checked after DNS, redirects checked; egress proxy denies private/loopback/link-local/cloud metadata (169.254.169.254); runner on internal networks only | `TestNonPublicURLs`, `TestClientRefusesPrivateTargetsPerConnection`, `FuzzPublicURL`, `deploy/smoke.sh` |
| I3 | Secrets or personal data in logs, traces or errors | all | `config.Secret` redaction, sensitive log attributes redacted (`log.Sensitive`), generic errors; spans carry the route template instead of the raw path and query, job spans no error text, no trace headers to third-party sites | `TestSecretIsRedactedEverywhere`, `TestErrorsNeverContainSecrets`, `TestSensitiveKeysAreRedacted`, `TestSensitiveKeywordsCoverage`, `TestInstrumentNamesSpansByRoute`, `TestTelemetryMiddleware` |
| I4 | Readiness details leak internals | 1 | `/readyz` returns ok/fail only; hidden by Caddy from the outside | `TestReadyzReportsEachCheckWithoutDetails`, `deploy/smoke.sh` |
| I5 | One-time tokens in access logs | 1 | Caddy without access log; tokens carried in query string are single-use and short-lived | `deploy/Caddyfile` |
| I6 | Database dump or blob storage exposes secrets | 4 | Envelope encryption: AES-256-GCM data key per user, wrapped with the master key (Docker secret, never in the database); associated data binds every ciphertext to user and purpose; `Forget` deletes the data key. Files are sealed before they reach the volume or bucket, under `users/<id>/`, bound to their key; keys are checked segment by segment and the directory store works through `os.Root` | `TestEnvelope`, `TestKeyring`, `FuzzOpen`, `TestSealed`, `TestSealedWithEnvelope`, `TestValidKey` |
| D1 | Slowloris / huge bodies | 1 | Server timeouts, header and body limits (1 MB default, 10 MB on upload paths) | `httpx.NewServer`, `TestProtectSetsHeadersAndBlocksCrossOriginPosts`, `TestProtectUploadLimit` |
| D2 | Hostile page exhausts the runner | 2/3 | Per-run timeouts, max 10 form pages, memory (2 GB) and pids (512) limits on container, browser wiped and restarted per run | `TestLaunchKeepsSandboxAndSiteIsolation`, `TestMaxPages` |
| D3 | Guessing or flooding on sign-in endpoints | 1 | Rate limit per client address (burst 10, then one request per 6 s); pages are `no-store` | `TestSignInIsRateLimited` |
| D4 | Huge or endless source responses | 1 | Body cap (`sources.MaxBody`), request timeouts, page caps per source, bounded fetch and detail parallelism, at most 3 attempts with backoff and `Retry-After` up to 30 s | `TestPagingIsBounded`, `TestClientErrors`, `FuzzSources` |
| D5 | Hostile CV (PDF or DOCX) crashes or exhausts `serve` during import | 1 | Text extraction runs in a child process of the binary without environment (no secrets), with address space, CPU time and file size limits (Linux), a 20 second timeout and bounded output; DOCX parts and PDF pages are bounded; parser panics are recovered | `TestExtractorChild`, `FuzzText`, `FuzzParse`, `TestCVImport` |
| E1 | Browser exploit escapes to the host | 2 | Chromium sandbox on (seccomp profile), site isolation, non-root, read-only rootfs, `cap_drop: ALL`, no DB access | `TestRendererRunsInItsOwnNamespaces`, `TestLaunchKeepsSandboxAndSiteIsolation` |
| E2 | Compromised runner reaches data | 2 | Runner has no database access and depends on no business module (depguard, `internal/archtest`); it leases one run at a time from the gateway with only that run's data and a token valid for 60 min, stored as a hash; documents only of that run; runners authenticate with a shared secret; one host gets no second run while one is active and at most one lease per 5 min | `TestRunnerHasNoDatabase`, `TestGateway`, `TestLeaseHonorsHostLimits`, `TestConcurrentLeasesRespectTheHost` |
| E3 | Malicious workflow change escalates | 6 | `permissions: {}`, read-only default token, SHA pinning enforced, zizmor, ruleset | CI, repository settings |

## Controls in place

- **Configuration & Secrets**: Secrets redact themselves in `fmt`, `slog` and JSON; errors name variables, never values. Secret values are injected exclusively via Docker secrets (`*_FILE`).
- **Logs & Traces**: Logs redact attributes with sensitive names (email, phone, password, token, cookie, session, key, etc.). Distributed traces carry route templates, not raw parameters.
- **Security Headers**: Strict CSP (`script-src 'nonce-…'` fresh per response, Trusted Types limited to the `htmx` policy, `frame-ancestors 'none'`), HSTS, `nosniff`, `Referrer-Policy: no-referrer`, COOP/CORP (`same-origin`), Permissions-Policy.
- **Container Hardening**: Distroless static non-root, read-only root filesystems, `cap_drop: ALL`, `no-new-privileges: true`, internal isolated networks.
- **Browser Sandboxing**: Chromium user namespaces sandbox and custom seccomp profile, site isolation, downloads denied, proxy without loopback bypass, no direct internet access.
- **Repository & Supply Chain**: Ruleset on `main` (PRs, required status checks, rebase only, no force push), signed tags, pinned commit SHAs for GitHub Actions, container image digests pinned, govulncheck, and zizmor in CI.

---

## OWASP ASVS 4.0 / 5.0 Level 2 Verification Matrix

### V1: Architecture, Design and Threat Modeling

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 1.1.1 | Secure software development lifecycle | Threat model, quality gates, automated testing, CI security checks | `docs/security.md`, `.github/workflows/ci.yml` |
| 1.1.2 | Threat model for application and assets | Documented assets, trust boundaries, STRIDE threats and controls | `docs/security.md` |
| 1.4.1 | Principle of least privilege | Database role `getafjob_app` cannot bypass RLS; runner has no database access; containers drop all capabilities | `TestAppRoleCannotBypassRLS`, `TestRunnerHasNoDatabase`, `deploy/compose.yaml` |
| 1.4.2 | Defense in depth across all architectural tiers | Caddy TLS + HSTS -> httpx protection -> domain validation -> RLS -> envelope encryption | `deploy/Caddyfile`, `internal/platform/httpx/secure.go`, `TestTenantSeparation` |
| 1.14.1 | Build and dependency verification | Pinned Go modules (`go.sum`), pinned GitHub Actions SHAs, signed images via cosign | `Taskfile.yml`, `ci.yml`, `docs/runbooks/deploy.md` |

### V2: Authentication (ASVS 5.0 V6)

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 2.1.1, 2.3.1 | Documented and enforced rate limits | Token bucket per client IP; at most 3 magic links per account in 15 minutes | `TestSignInIsRateLimited`, `TestMagicLinksAreRateLimited` |
| 2.1.2 | No default passwords or accounts | No passwords exist (ADR 0020); user accounts created only by explicit invitation | `TestInvitationFlow` |
| 2.2.1 | Multi-factor authentication (passkeys) | FIDO2 / WebAuthn passkeys with user verification (PIN/biometrics); mail link acts as delayed recovery once passkey exists | `TestPasskeys`, `TestRecoveryInsteadOfMagicLink`, `TestPasskeyNudgeAndRecovery` |
| 2.4.1 | One-time initial secrets short-lived | Invitation tokens carry 256 random bits, valid 7 days, single use, stored as SHA-256 | `TestInvitationFlow`, `TestExpiredInvitation` |
| 2.5.1 | Out-of-band codes single use & 10 min TTL | Magic links consumed in transaction, expire after 10 minutes | `TestMagicLinkFlow` |
| 2.7.2 | Cryptographic nonce of at least 64 bits | WebAuthn challenges use 256-bit CSPRNG nonces, single use, 5 min TTL | `TestPasskeys`, `TestPasskeyPages` |

### V3: Session Management (ASVS 5.0 V7)

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 3.1.1 | Documented idle and absolute session timeouts | Idle timeout 7 days, absolute timeout 30 days (ADR 0020) | `TestSessionValidity`, `TestSessionIdleTimeoutAndTouch`, `TestSessionRotation` |
| 3.2.1 | Backend-generated cryptographically secure tokens | 256 bits from `crypto/rand`, stored as SHA-256 hashes in PostgreSQL | `TestSessionRotation` |
| 3.2.3 | New session token on authentication | New session on every sign-in; hourly token rotation with 1-minute overlap grace | `TestSessionRotationRule`, `TestSessionCookieRotates` |
| 3.4.1 | Session termination on logout | Sign-out revokes session and rotated tokens immediately; expired rows deleted | `TestSignOutEndsRotatedTokens`, `TestSessionCookieRotates` |
| 3.4.2 | Session termination on account disable/delete | Disabled users fail authentication; account deletion cascades to all sessions | `TestDisabledUser` |
| 3.4.5 | Administrative session revocation | `getafjob admin sign-out -email …` revokes all sessions of a user | `TestSignOutUser` |
| 3.5.1 | Re-authentication for sensitive actions | Passkey registration/deletion and session revocation require sign-in within last 15 min | `TestPasskeyChangesNeedFreshSignIn`, `TestSignOutOthersNeedsFreshSignIn` |

### V4: Access Control

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 4.1.1 | Enforcement on a trusted server | All authorizations evaluated in Go backend and enforced in PostgreSQL via RLS | `TestTenantSeparation`, `TestAppRoleCannotBypassRLS` |
| 4.1.2 | Multi-tenant isolation | Row-Level Security on every tenant table; application role connects with `getafjob_app` without bypass rights | `TestTenantSeparation`, `TestDatabaseMigrationsEnforceRLS` |
| 4.2.1 | Prevention of IDOR / indirect object reference | Object lookups always require user ID context; blob store paths prefixed with `users/<id>/` | `TestDocumentPages`, `TestValidKey`, `TestSealedWithEnvelope` |
| 4.3.1 | Administrative interface separation | Administration performed exclusively via CLI (`getafjob admin`), no administrative web routes | `cmd/getafjob`, `internal/app/admin.go` |
| 4.4.1 | Extension / assist token scoping | Browser extension tokens scoped specifically to assist actions with limited TTL | `TestAssistPairingAndToken`, `TestAssistScope` |

### V5: Validation, Sanitization and Encoding

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 5.1.1 | Input validation on length, type, and format | Strict input length limits (names, URLs, text blocks); numeric bounds validated | `TestProtectUploadLimit`, `TestMaxPages`, `TestCatalogValidation` |
| 5.2.1 | Context-aware output encoding (XSS) | All templates compiled with `templ` providing automatic HTML context escaping | `TestJobPages`, `TestProfileView` |
| 5.2.2 | Content-Security-Policy & Trusted Types | Per-response CSP nonce, Trusted Types restricted to `htmx` policy, no inline scripts | `TestProtectSetsHeadersAndBlocksCrossOriginPosts`, `internal/platform/httpx/secure.go` |
| 5.3.1 | SQL injection prevention | 100% parameterized SQL queries compiled via `sqlc`, zero dynamic SQL concatenation | `sqlc.yaml`, `db/queries/`, `go test ./...` |
| 5.5.1 | Safe deserialization | Bounded JSON decoders, structured JSON-LD validation, strict schema for AI output | `FuzzJobPostings`, `TestStructuredOutputs` |

### V6: Stored Cryptography

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 6.1.1 | Standard industry-accepted cryptography | AES-256-GCM for envelope encryption, SHA-256 for hashes, ed25519 for VAPID | `internal/platform/crypto`, `internal/platform/webpush` |
| 6.2.1 | Cryptographic key management | Per-user AES-256-GCM data keys wrapped with master key; master key in Docker secrets; key rotation via keyring | `TestEnvelope`, `TestKeyring`, `TestKeyRotation` |
| 6.3.1 | Cryptographically secure random numbers | `crypto/rand` CSPRNG used for all nonces, session tokens, challenges, and keys | `TestSessionRotation`, `newNonce` |
| 6.4.1 | Associated data binding | Ciphertexts bind user ID and purpose string in GCM additional authenticated data | `TestEnvelope`, `TestPortalSessionsEncrypted` |

### V7: Error Handling and Logging

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 7.1.1 | No sensitive data in log files | `internal/platform/log` automatically intercepts and redacts sensitive attributes | `TestSensitiveKeysAreRedacted`, `TestSensitiveKeywordsCoverage` |
| 7.1.2 | Configuration secret masking | `config.Secret` masks plain text in `String()`, `fmt`, and `slog` | `TestSecretIsRedactedEverywhere`, `TestConfigSecretsUseSecretType` |
| 7.2.1 | Generic user error messages | User-facing errors never expose database details, file paths, or stack traces | `TestReadyzReportsEachCheckWithoutDetails`, `internal/web/setup.go` |
| 7.3.1 | Tamper-evident audit logging | `audit_events` table is append-only; database trigger strictly blocks UPDATE and DELETE | `TestAuditEventsAreAppendOnly`, `TestAuditLogHasNoPersonalData` |

### V8: Data Protection & Privacy

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 8.1.1 | Personal data minimization | Search preferences hold no contact data; cover letters strictly refused | `TestProfileItems`, `TestCoverLetterRefused` |
| 8.2.1 | Evidence & screenshot retention limits | Checkpoint screenshots automatically expired and purged from blob store after 90 days | `TestExpiredCheckpointScreenshots`, `TestBlobPurge` |
| 8.3.1 | Secure data destruction | `Forget` zeroes and deletes user data key rendering stored ciphertexts unrecoverable | `TestEnvelope` |

### V9: Communication Security

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 9.1.1 | TLS 1.2+ mandatory for all external traffic | Caddy front door terminates TLS with HTTP/2 and modern ciphers | `deploy/Caddyfile`, `deploy/smoke.sh` |
| 9.1.2 | Strict-Transport-Security (HSTS) | HSTS header with 2-year max-age and `includeSubDomains` | `TestProtectSetsHeadersAndBlocksCrossOriginPosts` |
| 9.2.1 | Network segmentation | Backend, edge, gateway, proxy, devtools, and observability networks isolated with `internal: true` | `deploy/compose.yaml`, `deploy/smoke.sh` |
| 9.3.1 | Egress filtering & SSRF protection | `egress` proxy blocks private IP ranges, loopback, and cloud metadata (169.254.169.254); `netguard` verifies public IPs post-DNS | `TestNonPublicURLs`, `TestClientRefusesPrivateTargetsPerConnection`, `deploy/smoke.sh` |

### V10: Malicious Code & Sandboxing

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 10.1.1 | Browser sandbox enforcement | Chromium runner starts with user namespaces sandbox enabled; verified on startup; custom seccomp profile | `TestLaunchKeepsSandboxAndSiteIsolation`, `TestRendererRunsInItsOwnNamespaces` |
| 10.2.1 | Isolated document parser execution | PDF and DOCX text extraction executes in isolated child process with Linux resource limits | `TestExtractorChild`, `FuzzParse` |
| 10.3.1 | Container security baseline | Distroless static non-root containers, `read_only: true`, `cap_drop: [ALL]`, `no-new-privileges` | `deploy/compose.yaml` |

### V11: Business Logic

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 11.1.1 | Idempotent transaction execution | `submit_attempts` unique constraint ensures an application cannot be submitted twice | `TestSubmitRegister`, `TestParkedAfterSubmitIsNeverSentAgain` |
| 11.2.1 | Anti-automation & abuse controls | Easy Apply rate limits (max 5/day default); portal account concurrency limits | `TestEasyApplyDailyLimit`, `TestLeaseHonorsHostLimits` |
| 11.3.1 | Mandatory human approval for high-risk actions | AI application drafts and automated answers require explicit user approval | `TestEnforceApprovalOnDrafts`, `TestAutonomyRuleApproval` |

### V12: File and Resource Handling

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 12.1.1 | Upload size limits enforced | 10 MB limit on document upload routes; 1 MB on general web routes | `TestProtectUploadLimit` |
| 12.2.1 | MIME type verified via magic bytes | Content-Type header ignored; magic bytes inspected (PDF, DOCX, PNG, JPEG); DOCX verified as safe zip | `TestSniff`, `FuzzSniff` |
| 12.4.1 | Safe file download serving | Documents served with `Content-Security-Policy: sandbox`, `Content-Disposition: attachment`, `nosniff` | `TestDocumentDownloadSandbox` |

### V13: API and Web Service

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 13.1.1 | API authentication | Connect RPC runner gateway authenticated with bearer token or shared secret; API v1 authenticated | `TestGateway`, `TestAssistAPI` |
| 13.2.1 | Payload size caps | Request message size strictly bounded (`MaxMessage = 12 MB`, `maxScreenshot = 8 MB`, `maxData = 256 KB`) | `TestGateway` |
| 13.3.1 | CSRF defense | `http.CrossOriginProtection` validates origin/fetch metadata on all mutating requests | `TestProtectSetsHeadersAndBlocksCrossOriginPosts` |

### V14: Configuration

| Req. | Requirement (short) | How getafjob satisfies it | Proof / Test |
|---|---|---|---|
| 14.1.1 | Production secrets protection | Passwords and keys provided as Docker secret files (`*_FILE`), never committed | `deploy/compose.yaml`, `config.go` |
| 14.2.1 | Strict configuration parsing | Unknown configuration variables generate warnings; invalid types fail fast | `TestConfigUnknownVariables`, `TestConfigValidation` |
| 14.4.1 | Secure defaults | AI disabled by default (`GETAFJOB_AI_ENABLED=false`); Easy Apply disabled by default; telemetry disabled by default | `TestConfigDefaults` |

---

## Findings

| # | Severity | Finding | Fix | Status |
|---|---|---|---|---|
| P0-1 | High | Trivy: `kin-openapi` (embedded spec) and smokescreen dependencies with HIGH advisories in images | Spec no longer embedded; smokescreen built from a patched build module | Resolved (PR #52) |
| P4-1 | Info | Sensitive keyword coverage in logging | Added automated architecture verification test `TestSensitiveKeywordsCoverage` to enforce ongoing compliance | Verified (PR #123) |
| P4-2 | Info | Database migrations RLS consistency | Added automated architecture verification test `TestDatabaseMigrationsEnforceRLS` ensuring every future tenant table has RLS enabled | Verified (PR #123) |

## Residual risks

- **Employer Visibility**: A job application form and employer career site sees everything submitted into its own fields by design.
- **Operator Access**: An operator with root access to the host server and knowledge of the master key secret file can decrypt stored tenant data.
- **Portal Account Policies**: Third-party portal platforms (LinkedIn, Indeed) may modify anti-automation policies or terms; getafjob mitigates this via explicit user opt-in, daily rate limits, and interactive handoff for re-authentication.
