# Configuration

Every role reads the same environment variables. Each `GETAFJOB_<NAME>` can be given as `GETAFJOB_<NAME>_FILE`
instead; the value is then read from that file (Docker secrets; one trailing newline is removed). Setting both is
an error. Unknown `GETAFJOB_*` variables are reported as a warning (typos), with their names only.

| Variable | Default | Roles | Meaning |
|---|---|---|---|
| `GETAFJOB_HTTP_ADDR` | `:8080` | serve | address of web UI, API and gateway |
| `GETAFJOB_HEALTH_ADDR` | `:8081` | worker, runner | internal `/healthz` and `/readyz` |
| `GETAFJOB_PUBLIC_URL` | `https://localhost:8443` | serve, worker | public address behind Caddy (links, cookies, WebAuthn) |
| `GETAFJOB_DATABASE_URL` | none | serve, worker, migrate | `postgres://user@host:5432/db?sslmode=…` |
| `GETAFJOB_DATABASE_PASSWORD` | none | serve, worker, migrate | overrides the password in the URL; use `_FILE` |
| `GETAFJOB_DATABASE_APP_PASSWORD` | none | migrate | password migrate sets on the application role `getafjob_app` (serve and worker log in with it) |
| `GETAFJOB_LOG_LEVEL` | `info` | all | `debug`, `info`, `warn`, `error` |
| `GETAFJOB_LOG_FORMAT` | `json` | all | `json` or `text` |
| `GETAFJOB_BROWSER_BIN` | lookup | runner | Chromium/headless-shell binary |
| `GETAFJOB_EGRESS_PROXY` | none | runner, worker | egress proxy, e.g. `http://egress:4750` |
| `GETAFJOB_SHUTDOWN_TIMEOUT` | `20s` | all | graceful shutdown limit (1s to 5m) |

`getafjob admin env` lists all variables; `getafjob admin check-config` validates the current environment without
printing values. Secret values never appear in logs or error messages.
