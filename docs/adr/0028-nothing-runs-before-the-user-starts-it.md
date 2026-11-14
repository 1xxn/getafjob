# 0028. Nothing runs before the user starts it

- Status: accepted
- Date: 2026-11-14
- Deciders: maintainer
- Amends: [0016](0016-autonomy-levels-and-rule.md)

## Context and problem statement

With "fill in and approve" as the default level, the planner queued applications on real postings right after
the onboarding, before the user had looked at a single match.

## Decision outcome

The default level is "suggest": getafjob crawls and ranks, but queues nothing on the user's behalf. The last
onboarding step says so and offers the next steps: search now, apply to one job with "Apply now", or turn on
automatic applications in the settings. Users who saved a level keep it.

### Consequences

- Good, because the first application is always the user's own decision.
- Bad, because users who want automation have to turn it on once.
