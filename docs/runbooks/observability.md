# Runbook: observability

Status: accepted (ADR 0021). The stack is optional and runs alongside the standard Compose deployment.

## Architecture

The observability profile provides metrics, traces, and logs across all services:
- **OpenTelemetry Collector**: Ingests OTLP traces and metrics on port `4318` (HTTP) / `4317` (gRPC).
- **Prometheus**: Scrapes metrics from the collector (`:8889`) and evaluates metrics.
- **Tempo**: Receives and stores distributed traces via OTLP gRPC.
- **Loki**: Stores structured container logs with OTLP ingestion.
- **Grafana**: Web UI on `http://127.0.0.1:3000` with pre-provisioned Prometheus, Tempo, and Loki datasources and the overview dashboard.

## Starting the Observability Stack

### Local Development

Start the stack with the observability profile enabled:

```bash
go tool task obs:up
```

Or with Docker Compose directly:

```bash
export GETAFJOB_OTEL_ENDPOINT=http://otel-collector:4318
docker compose -f deploy/compose.yaml --profile dev --profile observability up -d --build --wait
```

Open Grafana: `http://127.0.0.1:3000` (anonymous admin access enabled for local inspection).

To stop:

```bash
go tool task obs:down
```

### Production

In production, activate the `--profile observability` flag:

```bash
export GETAFJOB_OTEL_ENDPOINT=http://otel-collector:4318
docker compose -f deploy/compose.yaml -f deploy/compose.prod.yaml --profile observability up -d --wait
```

## Key Metrics & SLOs

The pre-provisioned dashboard (`getafjob / Overview`) tracks the 4 core business metrics and the HTTP latency SLO:

1. **Service SLO (< 200 ms p95)**:
   - Query: `histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket[5m]))) * 1000`
   - Target: p95 latency under 200 ms.
2. **ATS Application Success Rate**:
   - `getafjob_ats_maturity`: Measured submission success rate per ATS adapter [0, 1].
   - `getafjob_ats_runs_total`: Run counts by adapter and outcome (`submitted`, `asked`, `parked`, `guard`, `expired`).
3. **Run Step Durations**:
   - `getafjob_run_step_duration_seconds`: Step execution latency histogram by step (`prepare`, `extract`, `plan`, `execute`, `verify`, `submit`) and adapter.
4. **Queue Depth & Background Workers**:
   - `getafjob_queue_jobs`: River queue depth by queue and state (`available`, `running`, `retryable`, `scheduled`).
   - `getafjob_queue_job_duration_seconds`: Execution duration by background job kind and outcome.
5. **Crawl & Discovery Statistics**:
   - `getafjob_crawl_postings_total`: Processed postings by step (`fetched`, `invalid`, `duplicate`, `seen`, `details`, `details_failed`, `closed`).
   - `getafjob_crawl_sources_total`: Fetched sources by outcome (`ok`, `not_modified`, `error`).
   - `getafjob_crawl_duration_seconds`: Total crawl time.
   - `getafjob_boards_added_total`: Discovered company career boards added to catalog.

## Trace to Log Correlation

Logs emitted with `internal/platform/log` automatically include `trace_id` and `span_id`. In Grafana, Loki logs contain clickable links navigating directly to the corresponding trace in Tempo, and Tempo spans link back to the container logs.
