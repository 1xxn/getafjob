# Secrets

This directory holds the **development** secrets for `deploy/compose.yaml`. Everything here except this file is
ignored by git. Create them with:

```bash
go tool task dev:secrets
```

| File | Used by | Content |
|---|---|---|
| `db_password` | postgres, migrate, serve, worker | password of the `getafjob` database role |
| `master_key` | serve, worker | master key for envelope encryption (32 random bytes, base64url) |

Production keeps its secrets outside the repository (`GETAFJOB_SECRETS_DIR`, root-owned, mode 0600); see
`docs/runbooks/deploy.md` and `docs/runbooks/key-rotation.md`.
