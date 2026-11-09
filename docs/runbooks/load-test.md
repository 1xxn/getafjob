# Runbook: load test & latency SLO

Status: accepted (ADR 0021, Architecture Section 10).

## Objective & SLO Target

As defined in **ADR 0021** and **docs/architecture.md (Section 10 Quality Requirements)**:
- **Target SLO**: The web service must answer requests with a **95th percentile latency (p95) strictly under 200 ms** on the standard demo dataset (2000 job postings).
- **Error Budget**: Zero server errors (5xx) and request error rate < 1.0 %.

## Load Test Architecture

The load testing framework (`internal/platform/loadtest`) simulates concurrent virtual users executing realistic user traffic:

1. **Traffic Profile**:
   - `/jobs`: 30 % (primary dashboard and ranked job postings)
   - `/jobs?q=kubernetes`: 20 % (full-text search across postings)
   - `/jobs?workplace=remote`: 15 % (facet filtering)
   - `/jobs?decision=all&page=10`: 15 % (deep pagination)
   - `/jobs/{id}`: 10 % (job posting detail view and match explanation)
   - `/profile`: 5 % (candidate profile view)
   - `/healthz`: 5 % (platform health probe)

2. **Metric Collection**:
   - Exact percentiles calculated over recorded latencies: `Min`, `p50`, `p75`, `p90`, `p95`, `p99`, `Max`.
   - Throughput (requests/sec) and HTTP status code distribution.
   - Automatic pass/fail evaluation against the target SLO (`p95 <= TargetSLO && ErrorRate < 1%`).

## Running the Load Test

### Option 1: Automated Integration Test

Run the automated integration test against an ephemeral PostgreSQL testcontainer:

```bash
go tool task test:load
```

Or via `go test` directly:

```bash
go test -v -run TestConcurrentLoadSLO ./internal/web/...
```

The test boots the full web application, seeds 2000 demo job postings, executes matching for demo candidates, runs 8 concurrent virtual user workers for 2 seconds, and outputs a formatted summary table verifying that `p95 < 200ms`.

### Option 2: Live Local Stack with Observability

To inspect live metrics in Grafana while running load:

1. **Start the stack with the observability profile**:
   ```bash
   go tool task obs:up
   ```

2. **Seed 2000 demo postings and user profiles**:
   ```bash
   go run ./cmd/getafjob admin demo -jobs 2000 -seed 1
   ```

3. **Generate load**:
   ```bash
   go tool task obs:load
   ```

   Or customize concurrency, duration, and target URL using the administrative CLI command:
   ```bash
   go run ./cmd/getafjob admin load-test \
     -url https://localhost:8443 \
     -insecure \
     -users 10 \
     -duration 30s \
     -slo 200ms
   ```

## Verifying in Grafana

When running against the Compose stack with the observability profile:
1. Open Grafana at `http://127.0.0.1:3000`.
2. Navigate to the **getafjob / Overview** dashboard.
3. Observe the **HTTP p95 Latency (SLO < 200 ms)** panel:
   - **PromQL**: `histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_seconds_bucket[5m]))) * 1000`
   - Ensure the p95 latency curve remains safely below the red 200 ms threshold.
4. Check the **Service SLO & HTTP Latency** panel for throughput and overall request rates.

## Capacity & Performance Hardening

Key factors ensuring sub-200ms p95 latency under load:
1. **PostgreSQL Indexes**:
   - `jobs`: Full-text search and filtering rely on indexed columns (`fts_document`, `workplace`, `created_at`).
   - `matches`: Composite index on `(user_id, score DESC)` ensures fast pagination (`LIMIT 25 OFFSET 225`).
2. **Row-Level Security (RLS)**:
   - Tenant isolation policies use `current_setting('app.current_user_id', true)` session variables, avoiding redundant joins.
3. **Database Connection Pool**:
   - `pgxpool.Pool` maintains persistent connections and avoids handshake latency under concurrency.
