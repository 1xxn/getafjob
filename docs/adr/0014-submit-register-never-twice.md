# 0014. One submit path and a submit register

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

A duplicate application is worse than a missed one; crashes can happen between click and confirmation.

## Decision outcome

- Exactly one code path submits (`Submit`); the final submit uses the button inside the form, never a button of the
  same name elsewhere.
- Before the click, a `submit_attempt` row (unique per application) is written in a transaction.
- A crash while `submitting` becomes `submit_unknown`; it is reconciled via the confirmation page or the inbox and
  never retried automatically.
- Every click is verified (navigation, new tab, DOM change, request, scroll, native validation). A submit without
  effect is retried once, then parked with a screenshot and a message.
- Guards before every application: exclusions, duplicates across sources, company window.

### Consequences

- Good, because "applied twice" requires bypassing a unique constraint.
- Bad, because some real submissions end as `submit_unknown` and need a human look.
- Bad, because an application with a submit attempt is never queued again, even when it was proven not sent (no
  effect, form refused, reconciled as not sent): the user applies on the employer's site and marks it as applied.
