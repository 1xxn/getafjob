# Production Deployment & Operations

Production runs the same container topology as local development (`deploy/compose.yaml`), layered with [deploy/compose.prod.yaml](deploy/compose.prod.yaml) to pull signed GHCR images, enable Let's Encrypt/ZeroSSL ACME via Caddy, and apply memory/PID resource limits.

## 1. Prerequisites

- Linux server with Docker Engine and Docker Compose v2.
- Public DNS `A`/`AAAA` records pointing your `GETAFJOB_DOMAIN` to the host, with TCP `80`/`443` and UDP `443` reachable.
- An outbound SMTP server (`GETAFJOB_SMTP_URL`) for invitations, recovery links, and notifications.
- *(Optional for automated CD)* GitHub Actions environment secrets (`DEPLOY_SSH_HOST`, `DEPLOY_SSH_KEY`, `DEPLOY_SSH_USER`, `DEPLOY_SSH_PORT`, `DEPLOY_SSH_KNOWN_HOSTS`) used by [.github/workflows/deploy.yml](.github/workflows/deploy.yml).

### AppArmor on Ubuntu 23.10+ / Debian

Chromium's renderer namespace sandbox needs unprivileged user namespaces. Look at [deploy/seccomp/README.md](deploy/seccomp/README.md): on hosts with AppArmor `apparmor_restrict_unprivileged_userns=1`, add an AppArmor policy permitting `userns` for the runner container or configure the host profile accordingly instead of disabling `--no-sandbox`.

## 2. Host Setup & Secrets

Keep production secrets outside the repository directory (default `/etc/getafjob/secrets`, owned by `root:root`, mode `0600`):

```bash
sudo install -d -m 0700 -o root -g root /etc/getafjob/secrets /var/lib/getafjob/deploy
```

Required secret files in `$GETAFJOB_SECRETS_DIR`:

| File | Generator / Content |
|---|---|
| `db_password` | Random string (`getafjob admin gen-secret`) for Postgres owner role `getafjob` |
| `db_app_password` | Random string (`getafjob admin gen-secret`) for RLS-restricted runtime role `getafjob_app` |
| `master_key` | 32 random base64url bytes (`getafjob admin gen-secret`) wrapping user data keys |
| `runner_secret` | >= 32 character shared secret (`getafjob admin gen-secret`) for runner-to-gateway RPC |
| `smtp_url` | e.g. `smtps://user:pass@smtp.example.com:465` or `smtp://user:pass@smtp.example.com:587` |
| `backup_password` | *(Optional)* Restic repository encryption password (`deploy/backup.sh`) |

Set runtime environment variables (see [docs/configuration.md](configuration.md) for every option):

```bash
export GETAFJOB_ENV=production
export GETAFJOB_VERSION=v0.1.0
export GETAFJOB_DOMAIN=jobs.example.com
export GETAFJOB_ACME_EMAIL=ops@example.com
export GETAFJOB_SECRETS_DIR=/etc/getafjob/secrets
```

## 3. Deploying & Rolling Back

[deploy/deploy.sh](deploy/deploy.sh) handles full rollouts and automatic rollback on failure:

```bash
# Deploy a tagged release (verifies GHCR cosign signatures, runs migrate, starts stack, checks /healthz)
./deploy/deploy.sh --domain jobs.example.com v0.1.0
```

What `deploy.sh` runs in sequence:
1. Records the currently running tag as the rollback target (`$STATE_DIR/<project>.previous`).
2. Pulls `ghcr.io/1xxn/getafjob`, `ghcr.io/1xxn/getafjob-runner`, and `ghcr.io/1xxn/getafjob-egress` for the target version.
3. Verifies Cosign OIDC image signatures in production (pass `--skip-cosign` only if deploying custom/unsigned images).
4. Runs `docker compose run --rm migrate` (database migrations follow expand/contract patterns).
5. Starts updated containers (`up -d --remove-orphans`) and polls `https://$GETAFJOB_DOMAIN/healthz`.
6. Rolls back automatically to the previous version if `up` or health checks time out.

Manual rollback:

```bash
./deploy/rollback.sh              # roll back to previous recorded version
./deploy/rollback.sh v0.0.9       # roll back to an explicit version
```

After initial startup, import the built-in employer board catalog and generate your first admin invitation:

```bash
docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml exec -T serve \
  /usr/local/bin/getafjob admin boards import

docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml exec -T serve \
  /usr/local/bin/getafjob admin invite -email you@example.com -role admin
```

## 4. Backup & Restore

[deploy/backup.sh](deploy/backup.sh) and [deploy/restore.sh](deploy/restore.sh) create and restore encrypted `restic` snapshots of PostgreSQL (`pg_dump`) and the blob volume (using host `restic` if installed or the pinned `restic/restic` container):

```bash
# Local repository (default deploy/backups/restic) or remote (s3:, b2:, sftp:, rest:, azure:, gs:)
export RESTIC_REPOSITORY=s3:https://s3.example.com/getafjob-backups
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...
export RESTIC_PASSWORD_FILE=/etc/getafjob/secrets/backup_password

# Create snapshot (excludes secrets by default; add --include-secrets to bundle secrets into the encrypted snapshot)
./deploy/backup.sh

# Record backup size in the UI / system status page
docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml exec -T serve \
  /usr/local/bin/getafjob admin backup-done -bytes 12345678
```

Restore latest (or specific) snapshot:

```bash
./deploy/restore.sh --snapshot latest --yes
```

Verify backup/restore end-to-end against a clean local stack with `./deploy/test-restore.sh` (`go tool task test:restore`).

## 5. Day-2 Operations

### Master Key Rotation

`GETAFJOB_MASTER_KEY` wraps per-user AES-256-GCM data encryption keys in PostgreSQL (`user_keys`). Rotating it re-wraps only the data keys without touching encrypted blobs or database rows:

1. Generate a new key:
   ```bash
   docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml exec -T serve /usr/local/bin/getafjob admin gen-secret
   ```
2. Move the old key to a temporary file or `GETAFJOB_MASTER_KEY_OLD`, put the new key into `$GETAFJOB_SECRETS_DIR/master_key`.
3. Recreate `serve` and `worker` with `GETAFJOB_MASTER_KEY_OLD` pointing to the previous key while `master_key` holds the new one, then re-wrap all data keys:
   ```bash
   docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml exec -T \
     -e GETAFJOB_MASTER_KEY_OLD="$(cat /etc/getafjob/secrets/master_key.old)" \
     serve /usr/local/bin/getafjob admin rotate-master-key
   ```
4. Once rotation finishes cleanly, remove `GETAFJOB_MASTER_KEY_OLD` and delete `master_key.old`.

### Maintenance Mode

Blocks non-admin requests (`503 Service Unavailable`) and pauses background queue dispatch:

```bash
docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml exec -T serve \
  /usr/local/bin/getafjob admin maintenance on -message "Upgrading PostgreSQL, back in 10 minutes."

docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml exec -T serve \
  /usr/local/bin/getafjob admin maintenance off
```

When `serve` is stopped or restarting, Caddy serves its static 503 maintenance page automatically from `deploy/maintenance/index.html` ([deploy/Caddyfile](deploy/Caddyfile)).

### User & Data Management

```bash
admin sign-out -email user@example.com
admin disable -email user@example.com
admin enable -email user@example.com
admin export-user -email user@example.com -out /tmp/export.zip
admin delete-user -email user@example.com   # crypto-shreds DEK
```

## 6. Observability (Optional)

Enable the `observability` Compose profile (`otel-collector`, `prometheus`, `tempo`, `loki`, `grafana`) and set `GETAFJOB_OTEL_ENDPOINT=http://otel-collector:4318`:

```bash
GETAFJOB_OTEL_ENDPOINT=http://otel-collector:4318 \
  docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml --profile observability up -d
```

Grafana (`127.0.0.1:3000`), Prometheus (`127.0.0.1:9090`), Tempo (`127.0.0.1:3200`), and Loki (`127.0.0.1:3100`) bind strictly to localhost so you can tunnel via SSH without exposing telemetry ports publicly.
