# 0028. Nothing runs before the user starts it

- Status: accepted
- Date: 2026-11-14
- Deciders: maintainer
- Amends: [0016](0016-autonomy-levels-and-rule.md)

## Context and problem statement

With "fill in and approve" as the default level, the planner queued applications on real postings right after
the onboarding, before the user had looked at a single match. In the same way the worker crawled real sources as
soon as it started after the first deploy, while the maintainer was still setting up (#507).

## Decision outcome

The default level is "suggest": getafjob crawls and ranks, but queues nothing on the user's behalf. The last
onboarding step says so and offers the next steps: search now, apply to one job with "Apply now", or turn on
automatic applications in the settings. Users who saved a level keep it.

getafjob does not crawl on its own before the first crawl someone starts with a button (setup, job list, admin
sources) or `admin crawl`. That finished crawl arms the schedule (`GETAFJOB_CRAWL_INTERVAL`, default 6h, `0` turns
it off); a worker start or a deploy never starts a crawl. Board discovery only follows a crawl.

### Consequences

- Good, because the first application is always the user's own decision.
- Good, because a fresh instance fetches nothing until someone asks for it.
- Bad, because users who want automation have to turn it on once.
