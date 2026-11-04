# getafjob

getafjob collects job postings on its own, ranks them for each user and fills out application forms in a real
browser: no stealth plugins, no fingerprint spoofing, no CAPTCHA services. Checks that are meant for
humans (CAPTCHAs, bot checks, security codes) go to the human through a remote browser view. AI is optional per
user and off by default; everything works without it.

**Status:** early development, Phase 0 (foundation). Nothing here applies to jobs yet. See the
[project board](https://github.com/users/1xxn/projects) and the [milestones](https://github.com/1xxn/getafjob/milestones).

## What it does

collect → deduplicate → rank per user → a rule decides → the browser fills in → submit → detect the confirmation →
read the inbox (received, rejection, invitation) → status + notification.

- **Provenance for every value:** each form value names its source (`profile.email`, `answer#123`, …); values
  without a source are refused. Every application keeps its evidence: fields with value and source, a screenshot
  before submitting, the confirmation page and a timeline.
- **Never twice:** a submit attempt is registered in the database before the click; a crash while submitting
  becomes `submit_unknown` and is never retried automatically.
- **Autonomy levels per user:** suggest · fill in + approve (default) · autonomous by rule.
- **Invite-only, multi-user,** self-hosted with Docker Compose on a Linux server; the same stack runs locally.

## Architecture in one paragraph

A modular monolith in Go: one binary with the roles `serve` (web UI, API, SSE, runner gateway), `worker` (River
queue on PostgreSQL), `runner` (Chromium via go-rod, no database access), `migrate` and `admin`. Caddy terminates
TLS; runners and source fetches reach the internet only through an egress proxy that blocks private networks and
cloud metadata. Details: [docs/architecture.md](docs/architecture.md), decisions: [docs/adr](docs/adr).

## Quickstart (local)

Requirements: Go 1.27.3, Docker with Compose. All tools are pinned in `go.mod` and run with `go tool`.

```bash
git clone https://github.com/1xxn/getafjob.git
cd getafjob
go tool task dev:up          # dev secrets, images, all services; waits until every service is healthy
curl -k https://localhost:8443/healthz
go tool task dev:down
```

Development:

```bash
go tool task --list          # every command
go tool task check           # what CI checks: fmt, vet, lint, tests (race), govulncheck, generated code
go tool task test:browser    # browser tests inside the runner image (real Chromium, sandbox on)
go tool task hooks           # git hooks (lefthook)
```

See [CONTRIBUTING.md](CONTRIBUTING.md) before your first change.

## Security

The threat model is in [docs/security.md](docs/security.md). Please report vulnerabilities privately, see
[SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE). Third-party notices: [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES).
