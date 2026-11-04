# 0017. Docker Compose deployment and container hardening

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Decision outcome

One Linux server, Docker Compose: `caddy` (TLS), `serve`, `worker`, `runner`, `egress`, `postgres`, `migrate`,
optional observability. The same stack runs locally (`deploy/compose.yaml`); `deploy/compose.prod.yaml` switches to
signed GHCR images and ACME. App image `distroless/static:nonroot`, runner on headless-shell. All services read-only
with `cap_drop: ALL` and `no-new-privileges` where possible; secrets only as Docker secrets (`*_FILE`). Health via
`getafjob healthcheck` (no shell in distroless). `main` deploys to staging, tags to production after approval, with
automatic rollback on a failed health check (Phase 4).

### Consequences

- Good, because operation needs only Docker and the runbooks.
- Bad, because there is no automatic failover; backups and restore are tested instead.
