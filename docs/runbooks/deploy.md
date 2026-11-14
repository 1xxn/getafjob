# Runbook: Deployment, Staging, Approval & Rollback

Status: Production-ready (Phase 4, #49).

For the first installation on a bare server (DNS, mail, hardening, Docker, secrets, first admin) see
[install.md](install.md).

getafjob uses a hardened, automated Docker Compose deployment architecture with separate Compose projects for staging
and production, GitHub Environment approval gates for production releases, and automatic rollback on health check failure.

---

## 1. Architecture & Environments

getafjob runs as a containerized stack orchestrated by Docker Compose:
`caddy` (TLS/reverse proxy), `serve` (web UI, API, runner gateway), `worker` (River background jobs), `runner` (Chromium sandbox), `egress` (egress proxy), `postgres` (database), and `migrate` (schema migrations).

### Environment Separation

| Attribute | Staging | Production |
| :--- | :--- | :--- |
| **Trigger** | Push to `main` branch (`main.yml`, after the stack checks) | Push tag `v*` (`release.yml`) |
| **Approval Gate** | None (continuous deployment) | Required Reviewer approval in GitHub |
| **Compose Project** | `getafjob-staging` | `getafjob` |
| **Default Ports** | `8080` (HTTP), `8443` (HTTPS) | `80` (HTTP), `443` (HTTPS) |
| **Secrets Directory** | `/etc/getafjob/staging/secrets/` | `/etc/getafjob/secrets/` |
| **State Directory** | `/var/lib/getafjob/deploy/` | `/var/lib/getafjob/deploy/` |
| **State File** | `getafjob-staging.version` | `getafjob.version` |
| **Volume Names** | `getafjob-staging_pgdata`, etc. | `getafjob_pgdata`, etc. |

Because Compose project names isolate all containers, networks, and volumes, staging and production can safely run on the same Docker host or on separate servers without collision.

---

## 2. Deployment Sequence & Safety

Every deployment (via `deploy/deploy.sh`) follows an automated 4-step sequence:

```
[1/4] Pull Images & Verify Cosign
          │
          ▼
[2/4] Database Migrations (migrate) ──► (on error) ──► Abort (schema intact, running stack untouched)
          │
          ▼
[3/4] Start Containers (up -d)      ──► (on error) ──► Automatic Rollback
          │
          ▼
[4/4] Health Check (/healthz)       ──► (on error) ──► Automatic Rollback
          │
          ▼
     Success: Update Active Version State
```

1. **Pull Images:** Pulls pinned container images from GHCR (`ghcr.io/1xxn/getafjob`, `getafjob-runner`, `getafjob-egress`). The keyless OIDC signatures that GitHub Actions issues for all three images are verified with cosign. Production requires it: install cosign on the host (a pinned release from github.com/sigstore/cosign/releases, checksum verified); a deploy without it stops before migrations unless `--skip-cosign` is passed on purpose. Staging verifies when cosign is installed (`COSIGN_VERIFY=true` makes it required there too).
2. **Database Migrations:** Executes `docker compose run --rm migrate`. All schema migrations follow expand/contract rules (ADR 0004); old code can run against new schema. If migrations fail, the deployment immediately halts without modifying running services.
3. **Start Containers:** Launches updated containers with `docker compose up -d --remove-orphans`.
4. **Health Check Verification:** Polls the public health check endpoint (`https://${DOMAIN}:${PORT}/healthz`) until healthy (default timeout: 60s).
5. **Automatic Rollback:** If container startup or the health check fails, `deploy/deploy.sh` automatically rolls back:
   - Reads the recorded previous version.
   - Re-runs `docker compose up -d --remove-orphans --wait` with the previous image version.
   - Restores the previous version to serving traffic and exits with code 1 to alert operators.

---

## 3. Server Prerequisites & Access Setup (Maintainer Guide)

### Host Requirements

- Linux (Ubuntu 24.04 LTS or Debian 12 recommended).
- Docker Engine 26+ with Docker Compose v2 plugin (`docker compose version`).
- AppArmor / unprivileged user namespaces enabled for the Chromium runner sandbox (Ubuntu 23.10+): check that `runner` starts without requiring `--no-sandbox`.
- Repository cloned at `/opt/getafjob`.

### Dedicated Deployment User

Create a non-root user `deploy` and add it to the `docker` group:

```bash
sudo useradd -m -s /bin/bash deploy
sudo usermod -aG docker deploy
sudo mkdir -p /opt/getafjob /var/lib/getafjob/deploy
sudo chown -R deploy:deploy /opt/getafjob /var/lib/getafjob/deploy
```

### SSH Access Key Setup

Generate a dedicated Ed25519 SSH key pair for GitHub Actions deployments:

```bash
ssh-keygen -t ed25519 -C "getafjob-deploy-actions" -f ~/.ssh/id_ed25519_deploy -N ""
```

On the deployment server, add the public key to `/home/deploy/.ssh/authorized_keys`:

```bash
mkdir -p /home/deploy/.ssh
chmod 0700 /home/deploy/.ssh
cat id_ed25519_deploy.pub >> /home/deploy/.ssh/authorized_keys
chmod 0600 /home/deploy/.ssh/authorized_keys
chown -R deploy:deploy /home/deploy/.ssh
```

---

## 4. Secrets Setup

All secrets are mounted as Docker secrets from a root-owned directory (`0700`, files `0600`). Secrets are never committed to git or baked into images.

### Directory Structure

```
/etc/getafjob/secrets/               # Production secrets (root:root, 0700)
├── db_password                      # Postgres superuser password (0600)
├── db_app_password                  # Application role password (0600)
├── master_key                       # 32-byte envelope encryption key (0600)
├── runner_secret                    # Runner-to-gateway shared secret (0600)
├── smtp_url                         # SMTP credentials URL (0600)
└── backup_password                  # Restic repository password (0600)

/etc/getafjob/staging/secrets/       # Staging secrets (separate keys)
└── ...
```

### Generating Secrets

Generate random secrets using the admin CLI:

```bash
sudo mkdir -p /etc/getafjob/secrets /etc/getafjob/staging/secrets
sudo chmod 0700 /etc/getafjob/secrets /etc/getafjob/staging/secrets

# Production secrets
getafjob admin gen-secret | sudo tee /etc/getafjob/secrets/db_password > /dev/null
getafjob admin gen-secret | sudo tee /etc/getafjob/secrets/db_app_password > /dev/null
getafjob admin gen-secret | sudo tee /etc/getafjob/secrets/master_key > /dev/null
getafjob admin gen-secret | sudo tee /etc/getafjob/secrets/runner_secret > /dev/null
getafjob admin gen-secret | sudo tee /etc/getafjob/secrets/backup_password > /dev/null
printf 'smtp://user:pass@smtp.example.org:587' | sudo tee /etc/getafjob/secrets/smtp_url > /dev/null

sudo chmod 0600 /etc/getafjob/secrets/*

# Staging secrets (generate separately)
getafjob admin gen-secret | sudo tee /etc/getafjob/staging/secrets/db_password > /dev/null
getafjob admin gen-secret | sudo tee /etc/getafjob/staging/secrets/db_app_password > /dev/null
getafjob admin gen-secret | sudo tee /etc/getafjob/staging/secrets/master_key > /dev/null
getafjob admin gen-secret | sudo tee /etc/getafjob/staging/secrets/runner_secret > /dev/null
getafjob admin gen-secret | sudo tee /etc/getafjob/staging/secrets/backup_password > /dev/null
printf 'smtp+insecure://mailpit:1025' | sudo tee /etc/getafjob/staging/secrets/smtp_url > /dev/null

sudo chmod 0600 /etc/getafjob/staging/secrets/*
```

---

## 5. GitHub Repository & Environment Setup

To enable automated deployments and production approval gates in GitHub Actions:

### 1. Configure Environments

In the GitHub repository:
1. Navigate to **Settings** → **Environments**.
2. Click **New environment** and enter `staging`:
   - No protection rules (continuous deployment from `main`).
3. Click **New environment** and enter `production`:
   - Under **Deployment protection rules**, enable **Required reviewers**.
   - Add the maintainer username (`1xxn`).
   - Save protection rules.

### 2. Configure Environment Secrets

Add the following secrets to each environment (or as repository secrets if sharing a server):

| Secret Name | Value | Description |
| :--- | :--- | :--- |
| `DEPLOY_SSH_HOST` | `203.0.113.10` or `jobs.example.org` | Deployment server host/IP |
| `DEPLOY_SSH_KEY` | Contents of `id_ed25519_deploy` | Private SSH key for deploy user |
| `DEPLOY_SSH_USER` | `deploy` | SSH username (default: `deploy`) |
| `DEPLOY_SSH_PORT` | `22` | SSH port (default: `22`) |
| `DEPLOY_SSH_KNOWN_HOSTS` | `jobs.example.org ssh-ed25519 ...` | Host key from `ssh-keyscan` |

*Note:* If `DEPLOY_SSH_HOST` or `DEPLOY_SSH_KEY` is not configured, the deploy workflow logs a notice and skips execution without failing the image build or release job.

---

## 6. Deployment Operations

### Automated Workflows

- **Staging Deployment:** When a pull request merges to `main`, `.github/workflows/main.yml` runs the stack checks (e2e included), builds and signs container images (`ghcr.io/1xxn/getafjob:sha-<commit>`, plus `main` for the head), and when the commit is still the head of `main` dispatches to `deploy.yml` with `environment: staging`.
- **Production Deployment:** When the maintainer pushes a tag (`v0.1.0`), `.github/workflows/release.yml` creates binaries, publishes signed container images, and requests approval for the `production` environment. Upon approval, `deploy.yml` deploys the release.

### Manual Deployment via GitHub Actions UI

1. Open repository **Actions** tab.
2. Select **Deploy** workflow in the sidebar.
3. Click **Run workflow**:
   - **Target environment:** `staging` or `production`.
   - **Action:** `deploy`.
   - **Version tag:** e.g. `v0.1.0` or `sha-<full commit hash>`.
4. Click **Run workflow**.

### Manual Deployment via CLI

On the deployment server:

```bash
cd /opt/getafjob

# Deploy specific version to production
GETAFJOB_ENV=production deploy/deploy.sh v0.1.0

# Deploy specific version to staging
GETAFJOB_ENV=staging deploy/deploy.sh sha-<full commit hash>

# Using task
go tool task deploy -- --env production v0.1.0
```

---

## 7. Rollback Procedures

### Automatic Rollback

If a newly deployed container fails to start, crashes, or fails the `/healthz` check within `HEALTHCHECK_TIMEOUT` (default: 60s):
1. `deploy/deploy.sh` halts and catches the failure.
2. It retrieves the previously active version from `/var/lib/getafjob/deploy/<project>.version`.
3. It runs `docker compose up -d --remove-orphans --wait` with the previous version.
4. It re-verifies `/healthz`.
5. It exits with code 1 and outputs instructions to inspect logs.

### Manual Rollback via GitHub Actions UI

1. Open repository **Actions** tab.
2. Select **Deploy** workflow.
3. Click **Run workflow**:
   - **Target environment:** `staging` or `production`.
   - **Action:** `rollback`.
   - **Version tag:** (Optional; leave empty to automatically restore the previous successful version).

### Manual Rollback via CLI

```bash
cd /opt/getafjob

# Roll back to the previous successful version
deploy/rollback.sh --env production

# Roll back to an explicit target version
deploy/rollback.sh --env production v0.1.0

# Using task
go tool task rollback -- --env production
```

### Why Database Schema Rollback Is Not Required

Database migrations in getafjob are designed according to **expand/contract** principles (ADR 0004):
- Additions (new tables, columns, indexes) are non-destructive and nullable or carry defaults.
- Removals and renames happen across separate release milestones.
- When an application rolls back to the previous binary version, it continues operating safely against the newer schema without schema rollbacks.

---

## 8. Verification & Rehearsal

To verify deployment mechanics, health checks, and automatic rollback locally:

```bash
go tool task test:deploy
```

The automated rehearsal script (`deploy/test-deploy.sh`):
1. Deploys version `v-test-1` into an isolated project on port `18443`.
2. Verifies health and state recording.
3. Simulates a broken deployment of `v-test-2` (health check failure).
4. Verifies automatic rollback restores `v-test-1` to a healthy state.
5. Verifies manual rollback command execution.
6. Cleans up all test containers and volumes.
