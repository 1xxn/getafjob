# Security policy

getafjob stores personal data of its users (CVs, contact details, application answers, optionally mailbox access
and portal sessions) and drives a browser on their behalf. The threat model, the controls and the findings of
reviews are in [docs/security.md](docs/security.md).

## Supported versions

| Version | Supported |
|---|---|
| `main` and the latest release | yes, security fixes |
| older releases | no, update to the latest release |

## Reporting a vulnerability

Please report privately through GitHub's
[private vulnerability reporting](https://github.com/1xxn/getafjob/security/advisories/new), not in a public issue,
discussion or pull request. Include:

- what an attacker can do and what they need (a malicious job page, a crafted mail, another user of the same
  instance, a compromised runner …),
- steps to reproduce with fictional data only (never send real CVs or personal data),
- the affected version (`getafjob version`) and how it was deployed.

You get an answer within a week. Fixes are developed in a private advisory, released, and the advisory is published
with credit (unless you prefer not to be named).
