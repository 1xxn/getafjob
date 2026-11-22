# 0008. Egress proxy for runners and source fetches

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

Job postings and pages are untrusted; a crafted link can point the browser or a fetcher at internal services or
cloud metadata (SSRF).

## Considered options

1. In-process checks only (`net.Dialer.Control`)
2. A dedicated egress proxy plus in-process checks
3. Own proxy implementation as an extra role

## Decision outcome

Chosen option 2 with [smokescreen](https://github.com/stripe/smokescreen): a maintained Go CONNECT/HTTP proxy that
resolves names itself and denies private, loopback, link-local (incl. `169.254.169.254`) and other special ranges.
Runner and worker sit on internal Compose networks; only `caddy` and `egress` reach the internet. Chromium uses the
proxy without a loopback exception and with non-proxied UDP disabled. In-process fetchers additionally check every
connection (`netguard`, Phase 1).

smokescreen is built from a pinned build module (`deploy/egress`) so its dependencies can be patched.

### Consequences

- Good, because a missed in-process check still cannot reach internal addresses.
- Bad, because local test sites need an explicit allow rule in the e2e setup.
