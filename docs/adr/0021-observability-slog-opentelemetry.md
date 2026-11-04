# 0021. Observability with slog and OpenTelemetry

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Decision outcome

Structured logs with `log/slog` (JSON in containers); attributes with sensitive names are redacted and code never
logs personal data. Traces and metrics via OpenTelemetry; an optional Compose profile runs Grafana, Prometheus, Loki
and Tempo. Key metrics: success rate per ATS, duration per run step, queue depth, crawl statistics. SLO: dashboard
p95 < 200 ms.

### Consequences

- Good, because one standard covers local runs and the server.
- Bad, because the observability profile is extra infrastructure to operate (optional).
