# 0009. Server-rendered UI with templ, htmx and SSE

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

The UI needs live updates (runs, handoffs) and a strict CSP, without a Node toolchain.

## Considered options

1. SPA (React/Svelte) with a JSON API
2. templ + htmx 2 + SSE, small TypeScript islands

## Decision outcome

Chosen option 2. Pages are rendered by templ components (`Section`, `StickyBar`, `Field`, `StatusLabel`), htmx 2
with `allowEval=false`, live updates via SSE. TypeScript only for the remote-browser viewer and keyboard shortcuts,
bundled with the esbuild Go API, no Node/npm. Modern CSS (tokens, cascade layers, nesting), no framework, one
accent colour for "your turn". PWA with Web Push; WCAG 2.2 AA. The UI calls services directly; the JSON API is for
other clients ([0010](0010-spec-first-json-api.md)).

### Consequences

- Good, because CSP stays `script-src 'self'` and the build is pure Go.
- Bad, because rich client interactions need deliberate islands.
