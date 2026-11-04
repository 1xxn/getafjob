# 0001. Record architecture decisions

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

getafjob starts from an agreed design. The reasons behind it must survive the people and sessions that implement
it, and a later change must be a visible decision, not an accident.

## Decision drivers

- Decisions stay findable next to the code.
- Deviations from the design are deliberate and reviewed.

## Considered options

1. ADRs in the repository (MADR)
2. Wiki pages
3. Only issue and pull request discussions

## Decision outcome

Chosen option: "ADRs in the repository (MADR)", because they are versioned with the code, reviewed in pull
requests and linked from `docs/architecture.md`.

### Consequences

- Good, because every structural change has a written reason.
- Bad, because writing ADRs costs time; small choices stay in code comments.

## More information

A real reason to deviate from the design is written up as a proposed ADR and confirmed by the maintainer before
the change is merged.
