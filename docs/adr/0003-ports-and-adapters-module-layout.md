# 0003. Ports and adapters, module layout, enforced boundaries

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

A modular monolith rots when modules reach into each other's internals and when business rules mix with I/O.

## Decision drivers

- Business rules testable with table tests and without a database or browser.
- Clear dependency direction; violations fail CI.

## Considered options

1. Layered packages (handlers/services/repositories) across the whole app
2. Ports and adapters per module, boundaries enforced by depguard and architecture tests
3. Convention only

## Decision outcome

Chosen option: "Ports and adapters per module", because it keeps the domain pure and makes boundaries checkable.

Each module under `internal/<module>/` has:

- `domain.go` – types and rules, no I/O (no `net/http`, `database/sql`, pgx, `os`, go-rod),
- `service.go` – use cases,
- `ports.go` – interfaces, declared by the consumer,
- `adapters/` – PostgreSQL, HTTP, browser, mail …,
- nested `internal/` for internals no other module may import.

Modules: `identity`, `profile`, `discovery`, `matching`, `application`, `runner`, `tracking`, `notify`, `assist`,
`ai`, `platform`, plus `web` for handlers and views. `platform` depends on no business module; `runner` has no
database access.

### Consequences

- Good, because golangci-lint depguard rules and architecture tests fail on violations.
- Bad, because some interfaces exist only to keep the direction; they stay small and consumer-owned.
