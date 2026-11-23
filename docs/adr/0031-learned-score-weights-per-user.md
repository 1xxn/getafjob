# 0031. Learned score weights per user

- Status: accepted
- Date: 2026-11-23
- Deciders: maintainer
- Amends: [0029](0029-hybrid-matching-with-static-embeddings.md)

## Context and problem statement

ADR 0029 fixed the weights of the core score factors (similarity 0.40, title 0.20, skills 0.25, level 0.15) and
planned to learn them per user once there is feedback. Users differ: one cares about the stack, another about the
role name. How do we adapt the weights without losing a score that the reasons explain?

## Decision drivers

- Deterministic and explainable: the score stays weight times factor, each part shown as a reason.
- No LLM, no new service, no data outside the user's own rows.
- Never drift on little or one-sided data, and never learn from the system's own choices.

## Considered options

1. Fixed weights (ADR 0029).
2. Logistic regression per user over the four core factors, blended with the defaults.
3. A learning-to-rank model over all features (gradient boosting).

## Decision outcome

Chosen option: 2.

- Feedback is what the user did with a job: skip is negative; bookmark, queue, approval or an external
  application is positive. Applications the planner queued count only after an own action of the user, and a
  rejected writing suggestion is no rating of the job. The planner's queue events name the worker as actor.
- Before each matching run, the latest 500 rated jobs are scored with the default weights to get their raw factors
  (0 to 1). Learning needs at least 20 of them and 5 per side; otherwise the defaults stay.
- Batch gradient descent on the L2-regularized logistic loss from zero (fixed rounds and rate). Positive
  coefficients, normalized, are the learned weights. Without the embedding model similarity keeps its default
  share.
- The learned weights are blended with the defaults by n / (n + 50) and every factor keeps at least 0.05.
- The weights are computed in each run and not stored; a reason "Weighted by your own choices (n rated jobs)"
  says when they apply.

### Consequences

- Good: the score follows the user's choices, stays explainable and is reproducible from their data.
- Good: nothing new to store, export or delete; it disappears with the applications.
- Bad: what the user bookmarks depends on what the list showed first, so the feedback is biased toward the current
  ranking. The blend and the floor limit the effect.
- Bad: each run scores up to 500 extra jobs per user (about 2.5 ms of scoring plus their embedding).
