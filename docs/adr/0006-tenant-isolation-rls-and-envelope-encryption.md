# 0006. Tenant isolation with RLS, envelope encryption

- Status: accepted
- Date: 2026-11-04
- Deciders: maintainer

## Context and problem statement

Several invited users share one instance. Their CVs, answers, mailbox access and portal sessions are personal data;
a bug in one query must not leak another user's data, and a stolen database dump must not reveal secrets.

## Decision drivers

- Defense in depth below the application code.
- Secrets and documents unreadable without the master key.
- Per-user deletion (crypto-shredding) and key rotation.

## Considered options

1. Application-level `WHERE user_id = …` only
2. Row-level security plus application filters
3. One database or schema per user

## Decision outcome

Chosen option: "Row-level security plus application filters". Every user table has `user_id`; policies compare it
with `current_setting('app.user_id')`, which the data layer sets per transaction (`SET LOCAL`). The application
connects with a role that cannot bypass RLS. An integration test reads another user's data and must fail.

Sensitive data uses envelope encryption: a data key per user encrypts documents, portal sessions, mail
credentials, screenshots and AI keys; the master key (Docker secret) encrypts the data keys. Rotation re-wraps data
keys without touching the data.

### Consequences

- Good, because a missing filter becomes an empty result, not a leak.
- Good, because deleting a user's data key makes their encrypted blobs unreadable.
- Bad, because cross-user jobs (global job index, admin views) need explicit, reviewed code paths.
