# 0023. Rule-based matching with stored reasons

- Status: accepted; scoring superseded by [0029](0029-hybrid-matching-with-static-embeddings.md)
- Date: 2026-11-05
- Deciders: maintainer

## Context and problem statement

Every user needs the open jobs of the shared index ranked and filtered for them, with a result they can follow.
Skills and languages sit in the sealed profile, the wanted titles and places in the readable preferences.

## Decision drivers

- Works without AI and gives the same result for the same input.
- Every number is explained; filters cannot be made slow by user input.
- Personal data stays sealed at rest.

## Considered options

1. Rules with dictionaries (role synonyms, weighted skills, regions), stored per user and job.
2. Full-text ranking in PostgreSQL only.
3. Embeddings or an AI model.

## Decision outcome

Chosen option: 1. A score of 0 to 100 from title, skills, seniority, location, language, salary and recency; hard
filters (excluded or unrelated title, location, language, salary below the minimum) decide on their own. Results
with their reasons go to `matches` (row-level security) and are recomputed by the worker after every crawl and
after a profile change. User terms are plain substrings, never patterns. The worker opens profiles with the user's
data key; the crawl reads only the preferences for its search terms.

### Consequences

- Good, because the list explains itself and runs offline.
- Good, because no regex from users means no catastrophic backtracking.
- Bad, because dictionaries need care; unknown skills only count as typed.
- Bad, because every crawl rewrites results that changed, one write per user and page of jobs.
