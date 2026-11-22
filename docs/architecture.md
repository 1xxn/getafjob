# Architecture & Security Model

`getafjob` is a modular Go monolith backed by PostgreSQL 18. A single static binary (`cmd/getafjob`) runs in separate process roles with least-privilege database roles and isolated Docker networks.

## 1. Process Roles & Network Topology

```mermaid
flowchart LR
  User["Browser / PWA"] -->|HTTPS| Caddy["caddy"]
  Caddy -->|edge| Serve["serve (UI, API, Connect Gateway)"]
  Serve -->|backend| PG[("PostgreSQL 18 + River")]
  Worker["worker (River Jobs)"] -->|backend| PG
  Serve & Worker -->|filesystem / S3| Blobs[("Encrypted Blobs")]
  Runner["runner xN (Chromium + go-rod)"] -->|gateway (Connect RPC)| Serve
  Runner & Worker & Serve -->|proxy (HTTP CONNECT)| Egress["egress (smokescreen)"]
  Egress -->|internet| Web["ATS Boards, Job Feeds & Forms"]
```

- **No direct internet access for workers or runners**: Only `caddy` (inbound TLS) and `egress` (outbound HTTP/HTTPS proxy) attach to the external `internet` network in [deploy/compose.yaml](deploy/compose.yaml). `edge`, `backend`, `gateway`, `proxy`, and `observability` are Docker `internal: true` networks.
- **Database credential separation**: `migrate` uses the database owner role (`getafjob`) solely for schema migrations and maintenance. Long-running `serve` and `worker` processes authenticate as `getafjob_app`, which is subject to Row-Level Security. `runner` holds zero database or queue credentials.
- **Dev safety override**: [deploy/compose.dev.yaml](deploy/compose.dev.yaml) replaces the runner's egress proxy with `runner-egress`, restricting outbound browser traffic strictly to the local `fixtures` container (`testdata/fixtures/site`).

## 2. Package Layout & Dependency Boundaries

Business domains live under `internal/` and communicate through interfaces wired in [internal/app](internal/app):

| Package | Responsibility |
|---|---|
| `internal/identity` | Invitations, WebAuthn Passkeys, magic-link recovery, sessions, roles, audit log |
| `internal/profile` | Candidate profile, encrypted documents, CV text extraction child process (`extract-text`), answers, search preferences |
| `internal/discovery` | ATS board adapters, career-page crawlers, job feeds, conditional caching, snapshots, board auto-discovery |
| `internal/matching` | Constraint filters, deterministic weighted scoring, quantized local static embeddings (`internal/matching/embed`), match reasons |
| `internal/application` | Application state machine, autonomy rules, pacing/quotas, run leases, unique `submit_register`, evidence |
| `internal/gateway` | Connect RPC gateway connecting `serve` to remote or local `runner` instances (`api/proto/runner/v1`) |
| `internal/runner` | Browser lifecycle (`browser/`), step engine (`engine/`), DOM matching/filling (`executor/`), portal adapters |
| `internal/tracking` | Read-only IMAP polling and status suggestion rules |
| `internal/notify` | Web Push (VAPID), SMTP mail, ntfy notifications, daily summaries |
| `internal/assist` | Backend endpoints for assisted/extension workflows |
| `internal/portals` | Portal session management and opt-in Easy Apply flows |
| `internal/privacy` | GDPR data export (ZIP) and account erasure / crypto-shredding |
| `internal/web` | Server-rendered HTMX/templ web UI, TypeScript islands (`internal/web/ts`), OpenAPI JSON v1 handlers, live handoff WebSocket |
| `internal/platform` | Shared infrastructure (`config`, `crypto`, `db`, `queue`, `blob`, `httpx`, `netguard`, `otel`, `log`, `i18n`, `mail`, `webpush`, `maintenance`, `applymode`, `htmltext`, `vocab`, `loadtest`, `buildinfo`, `events`) |

Boundary invariants are enforced continuously by [.golangci.yml](.golangci.yml) (`depguard` and `forbidigo`) and whole-graph dependency tests in [internal/archtest](internal/archtest):

- Business modules cannot import one another (`TestModulesDoNotImportEachOther`).
- Only `internal/app` imports `internal/*/adapters/*` (`TestAdaptersOnlyWiredByApp`).
- Domain core packages (`internal/<module>`) have no DB, queue, or browser imports (`TestModuleCoreHasNoIO`).
- `internal/runner` has zero transitive dependencies on `pgx`, `river`, `internal/gateway`, `internal/platform/db`, or business modules (`TestRunnerHasNoDatabase`).
- `internal/platform` cannot import business modules, `web`, `runner`, or `app` (`TestPlatformKnowsNoModules`).

## 3. Core Runtime Flows

### Discovery & Indexing
Scheduled (`GETAFJOB_CRAWL_INTERVAL`, started once an initial crawl has been triggered) or manual (`admin crawl`) River jobs fetch sources concurrently. Company ATS boards are upserted before aggregator job boards so canonical company URLs take precedence and duplicate aggregator listings are merged into `alt_urls`. Conditional headers (`ETag`, `Last-Modified`) avoid re-downloading unchanged `Greenhouse`, `Ashby`, `Lever`, and `Personio` boards. Jobs unseen after complete crawls (or beyond the staleness window on capped feeds) are automatically closed.

### Matching & Scoring
`matching.Prepare` extracts normalized skills, seniority, experience years, salary figures, and language once per posting, optionally computing a 256-dimensional unit vector using `internal/matching/embed`. That embedding table is built deterministically at image build time from a SHA-256-verified snapshot of `static-similarity-mrl-multilingual-v1` quantized to `float16` ([Dockerfile](Dockerfile)), loaded read-only into memory without network calls or GPU dependencies. Every candidate match stores both the numeric score and the exact contributing rules.

### Application State Machine & Browser Runner
Applications progress along:

```text
discovered -> matched -> queued -> running <-> needs_input -> submitting -> submitted -> confirmed -> (interview | rejected | offer | withdrawn)
                                  \-> parked / failed / skipped / submit_unknown
```

1. **Autonomy & Guardrails**: Depending on user autonomy settings (`suggest`, `approve`, `autonomous`), runs either require manual start, pause for human approval in `/approvals`, or submit only when all verification rules pass.
2. **Step Engine & Provenance**: Runners lease one run at a time from `internal/gateway` over Connect RPC (`Lease`), receiving only the fields needed for that application. Before writing any DOM field, `executor` verifies value provenance (profile field, approved answer, or explicit user input during handoff) and refuses unattributed fields or cross-origin form hijacks.
3. **Single-Submit Guarantee**: Before clicking the final submit control, the runner requests `ReadyToSubmit`. `serve` records a unique row in `submit_register` atomically with transitioning to `submitting`. Crashes during submission move the run to `submit_unknown` rather than retrying a submit click.
4. **Human Handoff**: Anti-bot challenges, logins, verification codes, or unmapped questions pause the run (`needs_user_action`), notify the user, and stream the live Chromium tab over an ephemeral token-scoped WebSocket (`/applications/{id}`) so the user completes the step directly.

## 4. Security Model

- **Authentication & Sessions**: Accounts are invitation-only. Primary sign-in uses discoverable WebAuthn Passkeys with user verification. Once a passkey is registered, emailed one-time magic links enforce a recovery delay (`identity.RecoveryDelay`) before granting a session. Session tokens rotate hourly, privilege-sensitive operations (managing passkeys, revoking other sessions) require recent authentication, and write requests validate Fetch Metadata / CSRF protections plus CSP nonces (`internal/platform/httpx`).
- **Row-Level Security (RLS)**: Tenant-owned PostgreSQL tables enforce RLS policies tied to transaction-scoped `app.user_id` (or explicit system transactions via `app.system`). [TestDatabaseMigrationsEnforceRLS](internal/archtest/security_test.go) fails CI if any new migration creates a non-catalog table without enabling RLS.
- **Envelope Encryption & Crypto-Shredding**: `internal/platform/crypto` encrypts sensitive profile fields, portal sessions, and uploaded/archived artifacts (`internal/platform/blob` over `os.Root` or S3) with per-user AES-256-GCM data keys (`DEK`) using user/purpose associated data. Data keys are wrapped with `GETAFJOB_MASTER_KEY` and stored in `user_keys`. Running `getafjob admin delete-user` removes the user's wrapped data key, cryptographically shredding historical blobs and backups.
- **SSRF & Egress Control**: [internal/platform/netguard](internal/platform/netguard) rejects private, loopback, link-local, and non-public URLs both at validation time and inside the dialer. Outbound HTTP from `serve`, `worker`, and `runner` routes through `smokescreen` (`egress`), configured in [Dockerfile](Dockerfile) to block RFC 1918, loopback, link-local (`169.254.169.254`), CGNAT (`100.64.0.0/10`), multicast, and IPv6 ULA/link-local ranges.
- **Browser Sandbox & Container Hardening**: Containers run with read-only root filesystems, `cap_drop: [ALL]`, and `no-new-privileges:true`. The `runner` image removes `apt` after patching, disables `--no-sandbox` via `forbidigo` in [.golangci.yml](.golangci.yml), enables Chromium site isolation, and uses [deploy/seccomp/chromium.json](deploy/seccomp/chromium.json) to permit renderer user namespaces without `CAP_SYS_ADMIN`. CV PDF/DOCX parsing runs in an isolated subprocess (`getafjob extract-text`).
- **Redacted Structured Logging**: Secrets use `config.Secret` (`TestConfigSecretsUseSecretType`), structured fields pass key redaction (`TestSensitiveKeywordsCoverage`), and Caddy strips one-time invite/login path tokens and query strings from access logs ([deploy/Caddyfile](deploy/Caddyfile)).
