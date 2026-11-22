# 0004. PostgreSQL 18 with pgx, sqlc and goose (expand/contract)

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

getafjob needs durable state for many users: jobs with history, applications with a strict state machine, a
queue, full-text search and row-level tenant isolation.

## Decision drivers

- Transactions that cover state change, submit register and enqueued jobs together.
- Row-level security, full-text search (English/German) and trigram similarity in the database.
- Typed SQL without an ORM; migrations that never break the running version.

## Considered options

1. PostgreSQL 18 + pgx v5 + sqlc + goose
2. PostgreSQL + an ORM (GORM, ent)
3. SQLite

## Decision outcome

Chosen option: "PostgreSQL 18 + pgx v5 + sqlc + goose", because SQL stays explicit and reviewed, sqlc generates
typed code (checked by the generate-diff job), and goose migrations are embedded in the binary and run by the
`migrate` role.

Migrations follow expand/contract: add first, switch the code, remove in a later release. Readiness of `serve` and
`worker` fails while migrations are pending. An advisory lock prevents concurrent migration runs.

### Consequences

- Good, because one database covers data, queue ([0005](0005-river-job-queue.md)), search and isolation
  ([0006](0006-tenant-isolation-rls-and-envelope-encryption.md)).
- Bad, because SQLite-style "copy one file" backups are gone; backups use restic (runbook).
