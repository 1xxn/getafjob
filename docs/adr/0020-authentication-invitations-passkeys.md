# 0020. Invitations, passkeys and magic links; no passwords

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Decision outcome

Accounts exist only by invitation: one-time links with expiry, created by an admin (`getafjob admin invite`). Users
sign in with passkeys (WebAuthn); a magic link is the fallback. getafjob stores no passwords. Sessions use a
`__Host-` cookie; state-changing requests are protected by `http.CrossOriginProtection`. Roles: admin, user.
Security-relevant events go to an audit log.

### Consequences

- Good, because there are no password databases to leak or reuse.
- Bad, because magic links depend on working mail delivery.
