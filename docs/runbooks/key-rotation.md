# Runbook: key rotation

Status: Phase 0 skeleton – envelope encryption arrives in Phase 1 (#12).

| Secret | Rotation |
|---|---|
| Master key | new key as a second secret, re-wrap all user data keys (`getafjob admin rotate-master-key`, Phase 1), then remove the old key |
| Database password | `ALTER ROLE … PASSWORD`, update `db_password`, restart `serve`, `worker`, `migrate` |
| VAPID keys (Web Push) | rotate only when compromised – subscriptions must be renewed |
