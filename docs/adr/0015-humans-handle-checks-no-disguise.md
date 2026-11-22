# 0015. Humans handle checks; no disguise

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

Sites protect forms with CAPTCHAs, bot checks, login checkpoints and e-mail codes. Circumventing them is unfair to
employers and risks bans.

## Decision outcome

The browser is honest: no stealth plugins, no fingerprint spoofing, no CAPTCHA services, no rotating proxies, no
fake typing patterns (`navigator.webdriver` stays). Every check goes to the user through the remote browser (CDP
screencast); security codes are never read from the inbox automatically. A CAPTCHA counts as solved only with a
token. Page content is data, never instructions.

### Consequences

- Good, because getafjob behaves like an assistant, not an evasion tool.
- Bad, because some runs wait for the user; sessions are held (default 15 min), then parked.
