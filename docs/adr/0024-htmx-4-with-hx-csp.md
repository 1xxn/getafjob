# 0024. htmx 4 with hx-csp under a nonce CSP and Trusted Types

- Status: accepted
- Date: 2026-11-06
- Deciders: maintainer

## Context and problem statement

[0009](0009-server-rendered-ui-templ-htmx.md) chose htmx 2 for partial updates and SSE. The CSP enforces Trusted
Types; htmx 2 parses responses with plain strings and has no policy of its own, so it only runs with a permissive
default policy. htmx 4.0 (stable since 2026-08-28, npm tag `next`) ships the `hx-csp` extension for exactly this.

## Decision drivers

- Keep Trusted Types, no `unsafe-eval`, no inline scripts.
- Server-rendered templ pages that work without JavaScript; htmx only adds partial updates and live data.
- As current as possible without trading security for it.

## Considered options

1. htmx 2 with a default Trusted Types policy limited to the parser sinks
2. htmx 4 with `hx-csp` (named policy `htmx`, nonce on every htmx element) and `hx-sse`
3. Own small TypeScript module with a named policy
4. Datastar
5. No JavaScript at all

## Decision outcome

Chosen option 2.

- Every response gets a fresh nonce: `script-src 'nonce-…'`, `require-trusted-types-for 'script'`,
  `trusted-types htmx`. No other policy, not even `default`, can be created.
- Every element with `hx-*` attributes carries `hx-nonce` (templ helper `views.HX`); `hx-csp` makes elements
  without it inert and blocks `javascript:` URLs in htmx attributes.
- Config: only the extensions `hx-csp` and `hx-sse`, no indicator styles from htmx, view transitions on swaps. No
  JS expressions are used; the history cache extension stays off, so no page with personal data is stored locally.
- htmx requests get a fragment (`HX-Request`, not a history restore); everything else the whole page.
- The files are vendored unmodified with their npm integrity and SHA-256 sums (`internal/web/static/vendor/htmx`),
  checked by a test.
- Native browser features carry the rest: cross-document view transitions (CSS, off with reduced motion) and
  speculation rules sent as a header (prefetch of job pages).

### Consequences

- Good, because Trusted Types and the strict CSP stay fully in force and get stricter (nonce instead of `'self'`).
- Good, because injected markup cannot trigger requests: it lacks the nonce.
- Bad, because htmx 4 is young (4.0.1); fewer examples than 2.x, and updates need their release notes read.
- Bad, because every htmx element must use the helper; a test checks all rendered pages.
- Option 1 left HTML sinks open for all code; option 3 means maintaining our own framework; option 4 needs
  `unsafe-eval`; option 5 rules out live status for runs.
