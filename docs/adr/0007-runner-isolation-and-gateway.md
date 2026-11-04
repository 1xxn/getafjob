# 0007. Runner isolation and the runner gateway

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

Runners load untrusted pages in Chromium. A compromised runner must not reach the database or other users' data.

## Considered options

1. Runners query the database directly
2. Runners lease work from a gateway in `serve` over Connect RPC

## Decision outcome

Chosen option 2. Runners have no database access (enforced by depguard). They connect outbound to the gateway
(Connect RPC, protobuf in `api/proto`, `buf lint` + `buf breaking`) and lease one run at a time with only the data
it needs plus a short-lived token. Per-host rate limits are applied centrally at leasing. The home runner is the
same binary with outbound connections only.

Container: non-root, read-only rootfs, `cap_drop: ALL`, Chromium sandbox on (seccomp profile, never
`--no-sandbox`), internet only through the egress proxy ([0008](0008-egress-proxy.md)).

### Consequences

- Good, because a runner compromise exposes one run's data at most.
- Bad, because every piece of data a run needs must be modelled in the protocol.
