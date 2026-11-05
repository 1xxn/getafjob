# Runbook: key rotation

| Secret | Rotation |
|---|---|
| Master key | see below; the data is never re-encrypted, only the per-user data keys are re-wrapped |
| Database password | `ALTER ROLE … PASSWORD`, update `db_password`, restart `serve`, `worker`, `migrate` |
| VAPID keys (Web Push) | rotate only when compromised; subscriptions must be renewed |

## Master key

Every user's data key is stored wrapped with the master key (`user_keys.master_key_id` names it by a hash).
`serve` and `worker` read data keys wrapped with `GETAFJOB_MASTER_KEY` or, during a rotation,
`GETAFJOB_MASTER_KEY_OLD`; new keys are always wrapped with the current one.

1. Create the new key: `getafjob admin gen-secret > master_key.new` in the secrets directory (root-owned, `0600`).
2. Move the current key to `master_key_old` and the new one to `master_key`; add a `master_key_old` secret to the
   Compose file for `serve`, `worker` and the admin container and set
   `GETAFJOB_MASTER_KEY_OLD_FILE: /run/secrets/master_key_old`.
3. Restart `serve` and `worker`. Both keys now work.
4. Run `getafjob admin rotate-master-key` (in the worker container). It re-wraps all data keys in batches and prints
   the count; running it again prints 0.
5. Remove `master_key_old` and the variable, restart `serve` and `worker`, then destroy the old key file.

If step 4 stops with "data key wrapped with an unknown master key", a key older than `GETAFJOB_MASTER_KEY_OLD` is
still in use: put that key into `master_key_old` and run the command again. Never delete the old key before the
command prints 0.

A backup made before the rotation still needs the old master key; keep it as long as such backups are kept.
