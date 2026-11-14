# Configuration

Every role reads the same environment variables. Each `GETAFJOB_<NAME>` can be given as `GETAFJOB_<NAME>_FILE`
instead; the value is then read from that file (Docker secrets; one trailing newline is removed). Setting both is
an error. Unknown `GETAFJOB_*` variables are reported as a warning (typos), with their names only.

| Variable | Default | Roles | Meaning |
|---|---|---|---|
| `GETAFJOB_HTTP_ADDR` | `:8080` | serve | address of web UI, API and gateway |
| `GETAFJOB_HEALTH_ADDR` | `:8081` | worker, runner | internal `/healthz` and `/readyz` |
| `GETAFJOB_GATEWAY_ADDR` | `:8090` | serve | runner gateway (Connect RPC), only on the internal gateway network |
| `GETAFJOB_RUNNER_SECRET` | none | serve, runner | shared secret of the server's runners, at least 32 characters (`admin gen-secret`); empty keeps the gateway off |
| `GETAFJOB_GATEWAY_URL` | none | runner | where the runner reaches the gateway, e.g. `http://serve:8090`; empty: the runner only self-tests |
| `GETAFJOB_DEV_FIXTURE_HOST` | none | serve | development only: `host:port` of the fixture site whose plain http origin may take form values (`deploy/compose.dev.yaml`); ignored unless `GETAFJOB_PUBLIC_URL` is local |
| `GETAFJOB_DEV_STYLEGUIDE` | `false` | serve | development only: serve the design system style guide at `/dev/styleguide` (signed in users) |
| `GETAFJOB_LIMITS` | `off` | serve, worker | `off`: no daily quota and no per-host gap, runs lease as fast as runners allow (self-host for a few people); `on`: daily limit per user and one run per host every 5 minutes. Guard rails stay on either way |
| `GETAFJOB_RUNNER_PARALLEL` | `3` | runner | runs one runner works on at the same time (1 to 16); a run waiting for the user frees its slot, open browsers stay below twice this number. Size memory at about 500 MB per browser |
| `GETAFJOB_QUESTION_HOLD` | `2m` | runner | how long a run keeps its browser when it only asks form questions (30s to 15m); then the browser closes, the application waits for the answers and the answer starts a new run. Captchas, codes and sign-ins keep the 15 minute hold |
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
| `GETAFJOB_BLOB_URL` | none | serve, worker | file store: `file:///var/lib/getafjob/blobs` (volume) or `s3://bucket/prefix?endpoint=https://host&region=…`; plain `http` only for a service name in the Compose network; the endpoint must be reachable from the internal networks |
| `GETAFJOB_BLOB_ACCESS_KEY` | none | serve, worker | S3 access key; use `_FILE` |
| `GETAFJOB_BLOB_SECRET_KEY` | none | serve, worker | S3 secret key; use `_FILE` |
| `GETAFJOB_SMTP_URL` | none | serve, worker, admin | mail server: `smtps://user:pass@host:465` or `smtp://user:pass@host:587` (STARTTLS); `smtp+insecure://host:1025` for local development only; use `_FILE` |
| `GETAFJOB_MAIL_FROM` | `getafjob@localhost` | serve, worker, admin | sender address of all mail |
| `GETAFJOB_VAPID_KEY` | none | serve, worker | P-256 private key for Web Push notifications, 32 bytes base64url (`admin gen-vapid-key`); use `_FILE` |
| `GETAFJOB_VAPID_SUBJECT` | `mailto:<MAIL_FROM>` | worker | contact URI (`mailto:` or `https://`) in VAPID tokens |
| `GETAFJOB_TIMEZONE` | `Europe/Berlin` | serve, worker | IANA time zone of daily summaries and quiet hours; the notification settings name it |
| `GETAFJOB_EMBED_MODEL` | `/usr/share/getafjob/embed` | worker, admin | directory of the static embedding model for matching (shipped in the app image, about 55 MB in memory); empty or missing: matching uses the rules alone (ADR 0029) |
| `GETAFJOB_CRAWL_INTERVAL` | `6h` | worker | time between two crawls (30m to 168h); `0` schedules none (the development stack's default, `GETAFJOB_DEV_CRAWL_INTERVAL` overrides it) |

`getafjob admin env` lists all variables; `getafjob admin check-config` validates the current environment without
printing values. Secret values never appear in logs or error messages.
