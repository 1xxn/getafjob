# 0026. CI pipeline: build the images once, clear stages, every main commit checked

- Status: accepted
- Date: 2026-11-12
- Deciders: maintainer

## Context and problem statement

`ci.yml` grew job by job. The app, runner and egress images are built up to five times per run (image matrix,
browser fixtures, e2e, compose stack, release snapshot), so the e2e job alone spends about three minutes building
before the first journey. A run takes about eight minutes, the slowest jobs are `test (linux)` (7 min) and `e2e`
(7 min). Main commits were cancelled by concurrency groups (#265, #266), and the images workflow builds the same
images a sixth time on main, also for commits whose CI fails.

## Decision drivers

- Fast feedback: a broken pull request fails within three minutes; the whole run stays under eight minutes.
- Build once: every check tests the same image that gets scanned and published.
- Every main commit gets every check and never shows as cancelled.
- Supply chain: caches written from main only, no Go cache in jobs that gate main, zizmor clean, pinned actions.
- Required checks stay as strict as now; only names may change.

## Considered options

1. Keep `ci.yml`, tune the slow jobs one by one.
2. One pipeline in stages with one image build shared as artifacts; publish and deploy only after green CI.
3. Docker Bake plus a registry cache in GHCR for pull requests too.

## Decision outcome

Chosen option 2, because it removes the repeated builds without new credentials in pull requests (option 3 would
need `packages: write` or a public cache for untrusted code).

Stages of `ci.yml` (pull requests and pushes to main):

1. **Static** (parallel, no Docker): `verify` (mod, fmt, vet, generate, buf), `lint`, `govulncheck`, `zizmor`,
   `commits`, `fuzz`, `test (windows, macos)`.
2. **Build**: `images` builds app, runner and egress once (one job, one Buildx builder, layer cache from main
   only), runs trivy on each, and uploads them as one `docker save` artifact (`images`, kept one day).
3. **Test** (needs `images`, loads the artifact, never builds): `test (linux)` split in two shards by package,
   `browser fixtures`, `e2e journeys`, `compose stack` (`dev:up --no-build`).

E2E stays a required check only if it is cheap and deterministic; otherwise it moves out of the gate:

- It runs only when a pull request touches what it tests (`internal/web`, `internal/runner`, `internal/application`,
  `db/migrations`, `deploy`, the e2e package); other pull requests skip it.
- No builds and no waiting in the job: images from the artifact, `compose up --no-build`, demo data without
  matching every job (small `-jobs`), fresh fixture jobs per journey (#267), no fixed sleeps.
- The journeys run in two or three parallel shards (`-test.run` by group), each with its own stack, so wall time
  is stack start (about one minute) plus the slowest shard.
- Budget: the e2e check ends within four minutes wall time. If the measured time stays above that after the
  rebuild, e2e leaves the required checks and runs on main after merge and nightly, where a failure opens an issue
  and the next main commit must fix it.
4. **Release snapshot**: only when release inputs change (`.goreleaser.yaml`, `Dockerfile`, `go.mod`) or on main;
   otherwise the job is skipped, which counts as passed.

A `changes` job decides which stages a pull request needs; documentation-only changes skip build and test.

Concurrency: pull requests share one group per branch and cancel outdated runs; a push to main gets a group per
commit and is never cancelled.

`images.yml` no longer runs on `push`: it runs on `workflow_run` of a successful CI run on main, publishes and signs
`sha-<commit>` per commit (group per commit), and a final job moves `main` and deploys staging only when its commit
is still the head of main (#266). Release and nightly keep their own workflows.

### Consequences

- Good, because the images are built once per run and the tested image is the scanned one.
- Good, because e2e starts with the stack after about one minute instead of four, and most pull requests skip it.
- Bad, because sharded e2e needs one stack per shard (more runner minutes for less wall time).
- Good, because staging only ever gets commits with green CI.
- Bad, because the artifact upload and download add about 30 seconds to each test job.
- Bad, because required check names change once (ruleset update, allowed by the maintainer).

## More information

Issues #266 and #263 (cache from main, `changes` job). Measurements before: run on main `f52a4b4`, about eight
minutes, e2e 6m51s, test (linux) 7m14s, browser fixtures 5m48s, release snapshot 4m33s.
