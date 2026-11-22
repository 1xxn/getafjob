# 0010. Spec-first JSON API v1

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

The browser extension and later clients need a stable API; the web UI does not use it.

## Decision outcome

`api/openapi.yaml` (OpenAPI 3.1) is the source of truth under `/api/v1`; oapi-codegen generates models and a strict
`net/http` server (checked by the generate-diff job). Request models reject unknown fields. Errors are RFC 9457
problems with a stable `code` plus `params`, translated by the client. The spec is not embedded in the binary.

### Consequences

- Good, because the contract is reviewed before the code and stays in sync.
- Bad, because generator limits (e.g. union types) shape the schema.
