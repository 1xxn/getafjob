# 0030. Approval on sites the job does not name

- Status: accepted
- Date: 2026-11-22
- Deciders: maintainer
- Amends: [0016](0016-autonomy-levels-and-rule.md)

## Context and problem statement

Job board postings often have no form; the form is behind a link to the original posting (#509). The runner
follows such links and, since `fef7b93`, allows profile values on the page it ends on. That page can be any site the
link names, so profile data and a submit could go to a host nobody checked (#545).

## Decision outcome

The runner may fill forms on such a page, but the server asks for approval before the submit. The rule reads the
origins of the form pages from the run's checkpoints and adds the reason `origin` when one of them is neither an
origin of the job (posting, apply URL, ATS hosts of the source) nor a known ATS host. Without an origin check wired
in, every form origin counts as foreign.

### Consequences

- Good, because following links stays useful and an unchecked site never gets an autonomous submit.
- Good, because the server decides from its own data, not from the runner's report.
- Bad, because the values are typed into the foreign form before the user approves; only the submit waits.
