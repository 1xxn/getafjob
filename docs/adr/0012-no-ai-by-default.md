# 0012. Works without AI; optional AI module

- Status: superseded by [0027](0027-remove-the-ai-module.md)
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

AI can help with unusual forms and drafts, but costs money, may invent data and must not be required.

## Decision outcome

Everything works without AI: deterministic planner, rule-based matching, CV variants instead of tailoring, cover
letters from templates. The optional `ai` module (Planner, Drafter, Extractor, Classifier) uses the Anthropic API
with an operator key and per-user budget or the user's own encrypted key; off by default per user. Output is
schema-validated and checked for provenance ([0013](0013-provenance-for-every-form-value.md)); drafts need
approval. Tests use a fake provider only.

### Consequences

- Good, because the product is predictable and cheap to run.
- Bad, because unusual forms more often end in a question to the user.
