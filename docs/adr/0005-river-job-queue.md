# 0005. River as the job queue

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

Crawls, matching, rule evaluation, inbox checks, push notifications, cleanup and planning run in the background.
A job must never be lost or run twice because a transaction committed but the message did not (or vice versa).

## Decision drivers

- Enqueue in the same transaction as the state change (no outbox needed).
- No extra infrastructure to operate.
- Cron, retries, uniqueness and per-queue limits.

## Considered options

1. River (PostgreSQL-backed)
2. Redis-based queue (asynq)
3. NATS / RabbitMQ
4. Transactional outbox + broker

## Decision outcome

Chosen option: "River", because it lives in PostgreSQL, so `InsertTx` enqueues atomically with the business
change. Queues: `crawl`, `apply`, `mail`; periodic jobs via River's cron. `run_events` is append-only and published
with LISTEN/NOTIFY to SSE.

### Consequences

- Good, because there is no second system and no dual-write problem.
- Bad, because queue load lands on PostgreSQL; limits per queue and worker keep it bounded.
