# 0027. Remove the AI module

- Status: accepted
- Date: 2026-11-13
- Deciders: maintainer
- Supersedes: [0012](0012-no-ai-by-default.md)

## Context and problem statement

The optional `ai` module ([0012](0012-no-ai-by-default.md)) had settings (switch, own API key, budget) but no
caller: the planner's drafter was never set, and CV import, mail tracking and form planning all run on rules. The
settings promised something that did nothing, and the module kept encrypted API keys, an outbound provider and
about 2,300 lines of code to maintain (#370).

## Decision outcome

Remove the module: the `internal/ai` package, its configuration (`GETAFJOB_AI_*`, `GETAFJOB_ANTHROPIC_*`), its
tables (migration 00041), the settings section, the planner's drafter hook, the runner question's `draft` field
(proto field 3 reserved) and the `ai_draft#…` provenance. Unknown fields keep ending in a question to the user.

If drafts for unknown fields are wanted later, they come back as a new feature with its own ADR: the gateway
calls the provider for the runner, the draft is only a prefill, and the user approves every value.

### Consequences

- Good, because nothing in the product talks to an AI provider, which is simpler to explain and to secure.
- Good, because less code, configuration and stored secrets.
- Bad, because unusual forms keep ending in a question to the user.
