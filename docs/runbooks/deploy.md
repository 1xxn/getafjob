# Runbook: deploy

Status: Phase 0 skeleton – the automated workflow (staging on `main`, production on tags with approval, rollback)
arrives in Phase 4 (#49).

## Server prerequisites

- Linux with Docker Engine and the Compose plugin; DNS for `GETAFJOB_DOMAIN` points to the server; ports 80/443 open.
- Secrets directory (root-owned, `0700`, files `0600`): `db_password`, `master_key` (`getafjob admin gen-secret`).
- Chromium sandbox: hosts that restrict unprivileged user namespaces through AppArmor (Ubuntu 23.10+) may refuse the
  runner's sandbox ("No usable sandbox!" in the runner log). Check after the first start: the runner must turn
  healthy. If not, allow user namespaces for the runner with an AppArmor profile – never use `--no-sandbox`.

## Manual deploy

```bash
export GETAFJOB_VERSION=v0.1.0 GETAFJOB_DOMAIN=jobs.example.org GETAFJOB_ACME_EMAIL=ops@example.org \
       GETAFJOB_SECRETS_DIR=/etc/getafjob/secrets
cosign verify ghcr.io/1xxn/getafjob:$GETAFJOB_VERSION \
  --certificate-identity-regexp 'https://github.com/1xxn/getafjob/.github/workflows/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml pull
docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml up -d --wait
```

`migrate` runs before `serve` and `worker` start; both stay unready while migrations are pending.

## Rollback

Set `GETAFJOB_VERSION` to the previous tag and run `up -d --wait` again. Migrations are expand/contract, so the
previous version runs on the newer schema.
