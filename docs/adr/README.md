# Architecture decision records

Decisions are recorded in the [MADR](https://adr.github.io/madr/) format: one file per decision, numbered, never
rewritten once accepted: a changed decision gets a new ADR that supersedes the old one. Copy
[`template.md`](template.md) to start a new one.

| # | Decision | Status |
|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | accepted |
| [0002](0002-modular-monolith-one-binary-with-roles.md) | Modular monolith, one binary with roles | accepted |
| [0003](0003-ports-and-adapters-module-layout.md) | Ports and adapters, module layout, enforced boundaries | accepted |
| [0004](0004-postgresql-pgx-sqlc-goose.md) | PostgreSQL 18 with pgx, sqlc and goose (expand/contract) | accepted |
| [0005](0005-river-job-queue.md) | River as the job queue | accepted |
| [0006](0006-tenant-isolation-rls-and-envelope-encryption.md) | Tenant isolation with RLS, envelope encryption | accepted |
| [0007](0007-runner-isolation-and-gateway.md) | Runner isolation and the runner gateway | accepted |
| [0008](0008-egress-proxy.md) | Egress proxy for runners and source fetches | accepted |
| [0009](0009-server-rendered-ui-templ-htmx.md) | Server-rendered UI with templ, htmx and SSE | accepted, partly superseded by 0024 |
| [0010](0010-spec-first-json-api.md) | Spec-first JSON API v1 | accepted |
| [0011](0011-browser-automation-go-rod.md) | Browser automation with go-rod | accepted |
| [0012](0012-no-ai-by-default.md) | Works without AI; optional AI module | superseded by 0027 |
| [0013](0013-provenance-for-every-form-value.md) | Provenance for every form value | accepted |
| [0014](0014-submit-register-never-twice.md) | One submit path and a submit register | accepted |
| [0015](0015-humans-handle-checks-no-disguise.md) | Humans handle checks; no disguise | accepted |
| [0016](0016-autonomy-levels-and-rule.md) | Autonomy levels and the green rule | accepted |
| [0017](0017-compose-deployment-and-hardening.md) | Docker Compose deployment and container hardening | accepted |
| [0018](0018-supply-chain.md) | Supply chain: pinned tools, signed artifacts | accepted |
| [0019](0019-english-first-ui-en-de.md) | English first; UI in English and German | accepted |
| [0020](0020-authentication-invitations-passkeys.md) | Invitations, passkeys and magic links; no passwords | accepted |
| [0021](0021-observability-slog-opentelemetry.md) | Observability with slog and OpenTelemetry | accepted |
| [0022](0022-mail-link-as-delayed-recovery.md) | Mail link only as delayed recovery once a passkey exists | accepted |
| [0023](0023-rule-based-matching.md) | Rule-based matching with stored reasons | scoring superseded by 0029 |
| [0024](0024-htmx-4-with-hx-csp.md) | htmx 4 with hx-csp under a nonce CSP and Trusted Types | accepted |
| [0025](0025-boosted-navigation-and-live-dock.md) | Boosted navigation with a preserved live dock | accepted |
| [0026](0026-ci-pipeline-build-once.md) | CI pipeline: a fast pull request gate, end to end tests after the merge | accepted |
| [0027](0027-remove-the-ai-module.md) | Remove the AI module | accepted |
| [0028](0028-nothing-runs-before-the-user-starts-it.md) | Nothing runs before the user starts it | accepted |
| [0029](0029-hybrid-matching-with-static-embeddings.md) | Hybrid matching with a skill taxonomy and static embeddings | accepted |
