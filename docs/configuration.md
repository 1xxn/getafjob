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
| `GETAFJOB_OTEL_ENDPOINT` | none | serve, worker | OTLP/HTTP endpoint for traces and metrics, e.g. `http://otel-collector:4318`; empty: telemetry off |
| `GETAFJOB_SHUTDOWN_TIMEOUT` | `20s` | all | graceful shutdown limit (1s to 5m) |
| `GETAFJOB_MASTER_KEY` | none | serve, worker, admin | master key for envelope encryption, 32 bytes base64url (`admin gen-secret`); use `_FILE` |
| `GETAFJOB_MASTER_KEY_OLD` | none | serve, worker, admin | previous master key, only during a rotation ([key-rotation.md](runbooks/key-rotation.md)); use `_FILE` |
| `GETAFJOB_SMTP_URL` | none | serve, worker, admin | mail server: `smtps://user:pass@host:465` or `smtp://user:pass@host:587` (STARTTLS); `smtp+insecure://host:1025` for local development only; use `_FILE` |
| `GETAFJOB_MAIL_FROM` | `getafjob@localhost` | serve, worker, admin | sender address of all mail |
| `GETAFJOB_CRAWL_INTERVAL` | `6h` | worker | time between two crawls (30m to 168h) |
| `GETAFJOB_CRAWL_QUERIES` | none | worker | comma-separated search terms for job boards and amazon.jobs, e.g. `Softwareentwickler,Go Developer`; empty skips those sources |
| `GETAFJOB_CRAWL_CITIES` | none | worker | comma-separated cities for the Bundesagentur search, e.g. `Berlin,Hamburg` |

`getafjob admin env` lists all variables; `getafjob admin check-config` validates the current environment without
printing values. Secret values never appear in logs or error messages.
