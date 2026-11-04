# 0013. Provenance for every form value

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

An application must never contain invented data.

## Decision outcome

Every planned value carries its source: `profile.email`, `answer#123`, `textblock#4`, `ai_draft#9 (approved)`.
The executor refuses values without provenance. Each application keeps evidence: every field with value and
source, a screenshot before submitting, the confirmation page and a timeline. Free text in autonomous runs comes
only from text blocks or approved AI drafts.

### Consequences

- Good, because every submitted value is explainable.
- Bad, because unknown questions stop the run until the user answers (the answer goes into the answer bank).
