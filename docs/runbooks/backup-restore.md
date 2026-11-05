# Runbook: backup and restore

Status: Phase 0 skeleton; restic backups and a rehearsed restore arrive in Phase 4 (#48).

## What to back up

| Data | Where | How |
|---|---|---|
| PostgreSQL | volume `pgdata` | `pg_dump --format=custom` from the `postgres` container |
| Blob store | volume `blobs` or S3 bucket (ciphertext only) | restic (encrypted); useless without the master key and `user_keys` from the same point in time |
| Secrets | `GETAFJOB_SECRETS_DIR` | separately and offline; without the master key, encrypted data is lost |

Backups are encrypted (restic) and stored off-site. A restore is only proven by restoring into a fresh stack and
running the smoke test (`deploy/smoke.sh`).
