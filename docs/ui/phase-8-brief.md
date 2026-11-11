# Phase 8 brief: UI and UX rebuild

Input for the Phase 8 work packages (tracking #169). It records where the UI stands after Phase 7, what users need,
and the constraints every package has to keep. Decisions go into ADR 0025 (#170); this file is the brief, not the
decision.

## Users and jobs to be done

At most five people on one self-hosted instance. Each of them wants to:

1. See new matching jobs quickly and decide: apply, save, skip, block.
2. Let getafjob apply, and step in only when a form needs a human (a question, a captcha, an approval).
3. Watch any running application live and take over at once.
4. Know where every application stands and what to do next.
5. Keep profile, CVs and answers complete without hunting through pages.

The product promise: no fabricated data, every value has a source, the user stays in control. The UI has to make
that visible (provenance, approvals, evidence) without slowing the user down.

## State after Phase 7

Backend and runtime are in place and verified in the dev stack:

- Live view: frames for the whole run, any number of viewers, takeover while the run waits (`/applications/{id}/screen`).
- Live events: `GET /events` (SSE) sends application changes; `events.js` re-dispatches them as `getafjob:app` on
  `document.body`, so htmx elements refresh with `hx-trigger="getafjob:app from:body"`.
- No throughput limits (`GETAFJOB_LIMITS=off`), 3 parallel runs per runner, short browser hold for form questions.
- Typed questions (checkbox, radio, select, date, file hint), fill before asking, portal and email postings.
- Static URLs are content versioned (`views.Asset`), long cached.

The view layer itself is still the proof of concept: about 2,900 lines of templ in `internal/web/views`, 1,000 lines
of `app.css`, two TypeScript islands. It is small and is expected to be replaced, not patched.

## Page inventory and known problems

| Route | Today | Problems seen in the browser |
|---|---|---|
| `/` | greeting, setup nudge | no dashboard: nothing about running, waiting or new jobs |
| `/jobs` | filters, list, crawl, URL import, board add, blocklist | one long column; selects have no labels (a11y); source filter lists only ATS; import errors say nothing useful (#213) |
| `/jobs/{id}` | fit inspector, CV switcher, apply preview, external apply | horizontal scroll at 800 px; emoji and slogans in the preview ("getafjob Guarantee"); hardcoded English in the preview |
| `/applications/{id}` | live view, questions, fields, screenshots, timeline | raw reasons like "run: submit not registered, no click: unknown"; evidence tables hard to read on small screens |
| `/tracker` | five columns, stage select, notes | stage names are raw states (`needs_input`); no drag and drop; cards dense |
| `/approvals` | list with reasons | no batch approval; no review of the filled fields |
| `/profile`, `/documents`, `/answers`, `/texts` | four separate pages | dates as free text; no completeness view; CV review shows little (#214) |
| `/preferences`, `/account`, `/privacy` | long forms | autonomy options not explained by their effect |
| `/setup` | wizard | no progress, no "skip for now" |
| none | admin | users, invites, runners, sources, queue only through the CLI |

Cross-cutting:

- Navigation: ten text links in a top bar, no grouping, no counters, wraps on small screens.
- Live: the top bar shows "N live"; there is no live center and no dock that survives navigation.
- Texts: push notification texts in `static/sw.js` and `internal/app/applications.go` are English only.
- Accessibility: unlabeled selects, focus styles uneven, some colors fixed instead of tokens.
- Content Security Policy: inline handlers never run (nonce CSP). A test now forbids `on*=` in templates; keep it.

## Information architecture (starting point for #170)

- Home: dashboard
- Discover: Jobs, Saved searches
- Apply: Live, Needs input, Approvals, Queue
- Track: Applications, Pipeline
- Me: Profile, Documents, Answers, Texts
- Settings: Search, Automation, Notifications, Account and security
- Admin (admins only): Users, Runners, Sources, Queue, System

Page templates: list, list with detail side by side, detail with tabs, wizard, settings sections.

## Design principles

- One primary action per screen, named by what it does ("Apply now", "Answer and continue").
- Show the next step for every state; never show raw state names or internal error text.
- Live by default: counts, statuses and lists update from `/events`, no polling, no manual reload.
- Provenance stays visible but quiet: a source chip per value, details on demand.
- Plain texts in EN and DE from the catalogs; no slogans, no emoji as icons, short sentences.
- Works at 375 px and 1440 px, light and dark, keyboard only.
- Fast: server time under 150 ms p95 for list pages with 2,000 demo jobs, JS and CSS under 100 KB compressed.

## Technical constraints

- Server rendered templ plus htmx 4 with `hx-csp` (ADR 0009, 0024). Every element with `hx-*` attributes uses the
  `HX(ctx, ...)` helper so it carries the nonce. No inline scripts, styles or handlers.
- Persistent parts across navigation (live dock) need boosted navigation with `hx-preserve` or an island; decide in
  ADR 0025.
- TypeScript islands live in `internal/web/ts`, built by `internal/web/tsbuild` (add new entry points there).
- Static files only through `views.Asset("path")`.
- Texts only through the catalogs `internal/platform/i18n/catalog/{en,de}.json`; both languages in the same PR.
- Page content from employers is data: escape it, never execute it, never put it in `href` without `templ.SafeURL`
  checks.
- Guard rails stay as they are: provenance, approval rule, one submit path, humans solve checks.

## Definition of done for every Phase 8 package

- Works in `go tool task dev:up` with `admin demo -fixtures http://fixtures:8080`, checked in a real browser, light
  and dark, 375 px and 1440 px.
- An end to end journey test covers it (#188 builds the harness first).
- No new console errors (CSP, 404, JS); no inline handlers; texts in EN and DE.
- WCAG 2.2 AA on the changed pages: labels, focus, contrast, keyboard.
- Before and after screenshots in the PR, from demo data only.
