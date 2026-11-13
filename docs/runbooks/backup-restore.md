# Runbook: backup and restore

Status: Operational (restic encrypted off-site backups with automated rehearsal, #48).

## Overview

getafjob uses [restic](https://restic.net) for client-side encrypted, deduplicated, off-site backups of both the
PostgreSQL database and the blob store. Backups run without downtime while services are active. A restore can be
performed into an existing stack or onto a bare-metal server, and is proven by the automated rehearsal script
(`deploy/test-restore.sh`) and smoke test suite (`deploy/smoke.sh`).

## What to back up

| Component | Location | Backup method | Notes |
|---|---|---|---|
| PostgreSQL | volume `pgdata` | `pg_dump --format=custom` via postgres container | Complete schema, goose migration state, user data, jobs, matches, queues |
| Blob store | volume `blobs` or S3 | restic snapshot (`/var/lib/getafjob/blobs`) | User CVs and documents; already ciphertext (sealed with per-user data keys) |
| Manifest | staging `/manifest.json` | Captured in restic snapshot | Records schema version, project name, UTC timestamp and flags |
| Secrets | `GETAFJOB_SECRETS_DIR` | Offline & separate (or optional `--include-secrets`) | Contains `master_key`, `db_password`, `runner_secret`. Without `master_key`, encrypted user blobs cannot be decrypted |

> [!IMPORTANT]
> **Data key dependency:** The blob store and user settings are protected with envelope encryption. A restored
> database and blob store are completely unreadable without the corresponding `master_key` in `GETAFJOB_SECRETS_DIR`.
> Store your master key in a secure offline password manager or secret vault (see [key-rotation.md](key-rotation.md)).

## Supported storage backends

Restic supports multiple off-site storage targets. Set `RESTIC_REPOSITORY` and the corresponding credentials:

- **Local / Mounted directory:** `RESTIC_REPOSITORY=/mnt/backups/getafjob`
- **Amazon S3 / MinIO / Ceph:** `RESTIC_REPOSITORY=s3:s3.amazonaws.com/my-bucket/getafjob`
  (Requires `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, optional `AWS_DEFAULT_REGION` / `AWS_ENDPOINT`)
- **Backblaze B2:** `RESTIC_REPOSITORY=b2:my-bucket:getafjob`
  (Requires `B2_ACCOUNT_ID`, `B2_ACCOUNT_KEY`)
- **SFTP:** `RESTIC_REPOSITORY=sftp:backupuser@backup.example.com:/backups/getafjob`
- **REST server:** `RESTIC_REPOSITORY=rest:https://user:pass@rest.example.com/`

## Automated tooling

The repository provides three self-contained scripts in `deploy/`:

1. `deploy/backup.sh`: Dumps PostgreSQL, stages blobs, runs `restic backup`, verifies integrity, and applies retention rules.
2. `deploy/restore.sh`: Pulls snapshot from restic, recreates database, restores blobs, and restarts services.
3. `deploy/test-restore.sh`: End-to-end disaster rehearsal (seeds data, backs up, wipes state, restores, and verifies data integrity + smoke test).

Restic runs either natively if `restic` is installed on the host, or automatically inside a pinned official container
(`restic/restic:0.17.3@sha256:8f5a62b422a2cb1277ea0dd6e826fe1acf649e5b9f02d60e5268d5fd1976255a`). No external binary
installation on the host is required.

## Quick reference commands

```bash
# Development / Local tasks
go tool task backup                          # Create a backup using deploy/secrets/backup_password
go tool task restore                         # Restore latest snapshot
go tool task test:restore                    # Run complete disaster recovery rehearsal

# Custom repository and flags
RESTIC_REPOSITORY=s3:s3.amazonaws.com/my-bucket/getafjob \
RESTIC_PASSWORD_FILE=/etc/getafjob/secrets/backup_password \
deploy/backup.sh --tag "manual-release"

# Restore a specific snapshot ID
deploy/restore.sh 8f371bc8   # asks for the project name; --yes in scripts
```

---

## Step-by-step: taking a backup

### 1. Manual backup

To take an immediate backup:

```bash
export RESTIC_REPOSITORY="s3:s3.amazonaws.com/my-company-backups/getafjob"
export RESTIC_PASSWORD_FILE="/etc/getafjob/secrets/backup_password"
export AWS_ACCESS_KEY_ID="AKIA..."
export AWS_SECRET_ACCESS_KEY="..."

deploy/backup.sh --tag "daily"
```

The script performs the following:
1. Verifies PostgreSQL health via `pg_isready`.
2. Dumps the database using `pg_dump -Fc` to a consistent custom-format archive.
3. Captures the contents of the `blobs` volume.
4. Generates a metadata manifest with schema version and timestamp.
5. Initializes the restic repository if first run (`restic init`).
6. Creates the encrypted snapshot tagged with `getafjob`, `schema:<version>`, and user tags.
7. Verifies repository integrity (`restic check --quiet`).
8. Prunes obsolete snapshots according to retention policy (`--keep-daily 7 --keep-weekly 4 --keep-monthly 12`).

### 2. Scheduled backup (Cron / Systemd Timer)

Example `/etc/cron.d/getafjob-backup`:

```cron
# Run daily at 02:30 AM UTC
30 2 * * * root cd /opt/getafjob && RESTIC_REPOSITORY="s3:s3.amazonaws.com/bucket/getafjob" RESTIC_PASSWORD_FILE="/etc/getafjob/secrets/backup_password" AWS_ACCESS_KEY_ID="..." AWS_SECRET_ACCESS_KEY="..." deploy/backup.sh >> /var/log/getafjob-backup.log 2>&1
```

---

## Step-by-step: disaster recovery & restore

### Scenario A: Rehearsal & automated verification

Before trusting any backup system, recovery must be proven. Run:

```bash
deploy/test-restore.sh
# or:
go tool task test:restore
```

This automated test:
1. Verifies the running stack.
2. Injects a canary token into the blob store.
3. Performs a restic backup into an ephemeral repository.
4. Drops PostgreSQL database tables and wipes the blob store volume.
5. Executes `deploy/restore.sh`.
6. Asserts exact match of user, job, and match counts, and verifies the blob canary.
7. Executes `deploy/smoke.sh` against the restored stack.

### Scenario B: Restoring onto a fresh host (bare metal rebuild)

1. **Provision host:** Linux server with Docker Engine and Docker Compose.
2. **Clone repository or deploy Compose stack:**
   ```bash
   git clone https://github.com/1xxn/getafjob.git /opt/getafjob
   cd /opt/getafjob
   ```
3. **Restore secrets:**
   Place `db_password`, `db_app_password`, `master_key`, and `runner_secret` into `/etc/getafjob/secrets` (permissions `0600`).
4. **Restore data with `deploy/restore.sh`:**
   ```bash
   export GETAFJOB_SECRETS_DIR="/etc/getafjob/secrets"
   export RESTIC_REPOSITORY="s3:s3.amazonaws.com/bucket/getafjob"
   export RESTIC_PASSWORD_FILE="/etc/getafjob/secrets/backup_password"
   export AWS_ACCESS_KEY_ID="..."
   export AWS_SECRET_ACCESS_KEY="..."

   deploy/restore.sh latest
   ```
5. **Verify health:**
   ```bash
   deploy/smoke.sh
   ```

### Scenario C: Rolling back to a specific snapshot

1. List available snapshots:
   ```bash
   docker run --rm -it \
     -v /opt/getafjob/deploy/backups/restic:/repo \
     -e RESTIC_REPOSITORY=/repo \
     -e RESTIC_PASSWORD_FILE=/run/secrets/backup_password \
     restic/restic:0.17.3 snapshots
   ```
2. Restore selected snapshot:
   ```bash
   deploy/restore.sh <snapshot-id>
   ```

---

## Verification checklist

After any restore, check:

- [ ] `deploy/smoke.sh` passes without errors (healthz, API metadata, login, egress isolation).
- [ ] Database record counts match expectations (`users`, `jobs`, `matches`).
- [ ] Migrations status is up to date (`max(version_id)` in `goose_db_version`).
- [ ] User documents (CVs) can be downloaded in the Web UI.
- [ ] `docker compose ps` shows all containers healthy.
