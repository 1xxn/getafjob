# 0025. Boosted navigation with a preserved live dock

- Status: proposed
- Date: 2026-11-11
- Deciders: maintainer

## Context and problem statement

Phase 8 rebuilds the UI (brief: [`docs/ui/phase-8-brief.md`](../ui/phase-8-brief.md)). Users must be able to watch a
running application while they move through the app, and counts and lists must update without reloads. Today every
link is a full page load: the live view and the event stream reconnect on each page, and nothing survives
navigation. [0009](0009-server-rendered-ui-templ-htmx.md) and [0024](0024-htmx-4-with-hx-csp.md) set the stack:
templ, htmx 4 with `hx-csp`, nonce CSP, Trusted Types.

How do pages navigate, and how does a live dock persist across pages?

## Decision drivers

- CSP stays as strict as it is: per-response nonce, Trusted Types, no inline code, no `unsafe-eval`.
- The live dock and the SSE connection keep running while the user moves between pages.
- Every page still works as a plain link and form without JavaScript (except live parts).
- Five users per instance, one maintainer: few moving parts, no second rendering stack.
- Accessible navigation: title, focus and announcements after a page change.

## Considered options

1. Full page loads; the dock is a TypeScript island that reconnects on every page
2. `hx-boost` on the app shell; the dock and the event connection live in an `hx-preserve` element
3. A single page app on `/api/v1`

## Decision outcome

Chosen option 2.

- The shell (`<body>`) carries `hx-boost` with `hx-target="#main"` and `hx-select="#main"`, so links and forms swap
  the main region and push the URL. Navigation, sidebar counters and the dock are outside `#main`.
- The dock (`#live-dock`) carries `hx-preserve`; its frame socket and state survive navigation. The SSE connection
  belongs to the shell script, not to a page, so it opens once per tab.
- Nonces: every response, full or fragment, sends its CSP header; `hx-csp` reads the response nonce and rewrites it
  to the page nonce before the swap. No change to the CSP is needed.
- Scripts: all islands load once in `<head>` as modules and bind to elements on `htmx:after:swap` and on load. No
  `<script>` inside swapped content.
- The server renders the whole page for normal requests and for history restores, and the main region plus an
  out of band update of the active navigation item for boosted requests (`HX-Request`, see
  `isFragmentRequest`). The history cache stays off (0024).
- After a swap the shell sets the title, moves focus to the `h1` in `#main` and announces it in a polite live
  region. View transitions stay as in 0024.
- Islands stay small and only where htmx cannot: live frame canvas and takeover, drag and drop in the pipeline,
  keyboard shortcuts, passkeys.
- Without JavaScript, links and forms do full loads and the dock is a link to the live center.

### Consequences

- Good, because the live view and the event stream keep running while the user works elsewhere.
- Good, because navigation is faster (only `#main` is rendered and swapped) and stays server rendered.
- Good, because the CSP and the `HX` helper rule stay unchanged; tested pages remain the source of truth.
- Bad, because islands must bind and unbind on swaps instead of on page load; a missed unbind leaks listeners.
- Bad, because focus, title and scroll after navigation are now our job; the E2E harness (#188) checks them.
- Bad, because a server error inside a boosted request needs an explicit error swap, or the user sees nothing.
- Option 1 drops frames and replays events on every page change and cannot keep a takeover alive across pages.
- Option 3 duplicates rendering, i18n and authorization in a second stack and needs a bundler and a router; too
  much for five users and one maintainer.

## More information

Sitemap, URL scheme, page templates and shortcuts: [`docs/ui/sitemap.md`](../ui/sitemap.md). Low fidelity
wireframes: [`docs/ui/wireframes.md`](../ui/wireframes.md).
