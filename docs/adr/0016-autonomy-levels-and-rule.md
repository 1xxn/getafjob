# 0016. Autonomy levels and the green rule

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Decision outcome

Per user: (1) suggest, (2) fill in and approve, (3) autonomous by rule. The rule is green only if the score
meets the threshold, there is no exclusion or duplicate, the company limit is free, every field has provenance,
free text comes only from text blocks or approved AI drafts, no question is open, the form shows no errors, the
quota is free and the ATS adapter's measured success rate (maturity) is sufficient. Otherwise the application goes
to the approval list. Quotas are spread over the day; per-host rate limits apply at leasing.

### Consequences

- Good, because autonomy is earned per ATS by measured results.
- Bad, because new adapters start in approval mode.

## Amendment 2026-11-11: limits off for self-hosting

The instance is hosted for a handful of people. `GETAFJOB_LIMITS=off` (the default) removes the daily quota from the
rule, the planner and leasing, and drops the per-host gap; runs lease as fast as runners allow. `on` keeps the
behaviour above. All other parts of the rule, the guards (exclusion, duplicate, company limit) and the submit
register are unchanged.
