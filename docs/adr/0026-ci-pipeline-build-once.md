# 0026. CI pipeline: a fast pull request gate, end to end tests after the merge

- Status: accepted
- Date: 2026-11-12
- Deciders: maintainer

## Context and problem statement

`ci.yml` grew job by job. The app, runner and egress images are built up to five times per run (image matrix,
browser fixtures, e2e, compose stack, release snapshot). Before the e2e journeys a run took three to six minutes;
with them as a required check it takes seven minutes at best and often over twenty, because every flake blocks the
merge and needs a rerun. Main commits were also cancelled by concurrency groups (#265, #266), and the images
workflow builds the same images again on main, also for commits whose CI fails.

## Decision drivers

- Fast feedback: a pull request is checked in under three minutes, a documentation change in about one.
- Nothing slow or flaky in the gate; heavy checks run once per main commit.
- Build once: the images that are tested after the merge are the ones scanned, published and deployed.
- Every main commit gets every check and never shows as cancelled.
- Supply chain: caches written from main only, no Go cache in jobs that gate main, zizmor clean, pinned actions.

## Considered options

1. Keep e2e in the pull request gate and make it faster (shared images, shards, path filters).
2. A smoke e2e (pages render) in the gate, the full journeys after the merge.
3. No e2e in the gate: the gate runs static checks and tests, path filtered; images, e2e, publish and staging run
   after the merge.

## Decision outcome

Chosen option 3. Any e2e in the gate needs the whole stack (three images, Compose), at least two to three minutes
before the first page, and would set the duration of every pull request. Handler, template, catalog and CSP tests
and the browser fixture tests already cover what a smoke run finds.

**Pull request gate** (`ci.yml`, pull requests and pushes to main):

- Always: `verify` (mod, fmt, vet, generate, buf), `lint`, `govulncheck`, `zizmor`, `commits`,
  `test (linux 1/2)`, `test (linux 2/2)`, `test (windows)`, `test (macos)`, `fuzz`.
- A `changes` job decides the rest. Documentation-only changes skip all Go jobs. `images` (build and trivy),
  `browser fixtures`, `compose stack` and `release (snapshot)` run only when their inputs change (Dockerfile,
  `deploy/`, the runner, `.goreleaser.yaml`, `go.mod`) and on main. A skipped job counts as passed.
- Concurrency: one group per pull request that cancels outdated runs; a push to main gets a group per commit.

**After the merge** (`main.yml`, on `workflow_run` of a successful CI run on main, group per commit, never
cancelled): build the three images once (layer cache written here only), run the e2e journeys and the compose
rehearsals on them, then publish and sign `sha-<commit>`, move `main` and deploy staging only when the commit is
still the head of main (#266). A failure opens an issue (or comments on the open one); the next commit fixes it.

**Nightly**: full e2e and fuzz as before. **On demand**: the label `e2e` on a pull request runs the journeys for it.
Locally only the journey of the changed page runs.

### Consequences

- Good, because a pull request is checked in about the time it was before the journeys existed.
- Good, because staging only gets commits whose images passed e2e.
- Good, because the images are built once per main commit instead of up to six times.
- Bad, because an e2e failure shows after the merge, not before; deterministic journeys (#267) keep this rare.
- Bad, because required check names change once (ruleset update, allowed by the maintainer).

## More information

Issues #266, #267, #269. Before: main `f52a4b4`, e2e 6m51s, test (linux) 7m14s, browser fixtures 5m48s,
release snapshot 4m33s; runs 3 to 6 minutes before 2026-11-11, 7 to 98 minutes after.
