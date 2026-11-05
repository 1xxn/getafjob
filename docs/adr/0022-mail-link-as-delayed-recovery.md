# 0022. Mail link only as delayed recovery once a passkey exists

- Status: accepted
- Date: 2026-11-05
- Deciders: maintainer

## Context and problem statement

A magic link signs in with mail access alone. For an account with a passkey that undoes the second factor:
whoever reads the mailbox takes over the account (ASVS 6.3.3, 6.4.3). People still lose devices and need a way
back in without an admin.

## Decision drivers

- Mail access alone must never be enough for an account with a passkey.
- The owner must notice an attempt and be able to stop it without help.
- Recovery must work without support tickets; signing in day to day stays one tap.
- No account enumeration: the sign-in form answers the same for every address.

## Considered options

1. Keep the magic link as it is.
2. Disable the magic link once a passkey exists; recovery only through an admin.
3. Mail link as delayed recovery: a request starts a waiting period, any passkey sign-in cancels it.
4. Mail link plus a second factor (TOTP or recovery codes).

## Decision outcome

Chosen option 3, because it closes the takeover by mail access alone, keeps recovery self-service and adds no new
secret to keep.

- Accounts without a passkey sign in with the mail link as before; the home and account pages ask them to add a
  passkey.
- For an account with a passkey, a mail link request starts a recovery: a mail tells the owner, a link works only
  after 24 hours, then for 48 hours, once. Requests during the wait mail nothing new.
- Any passkey sign-in cancels a pending recovery; the account page shows it with a cancel button.
- A sign-in through recovery sends a notice mail. Links issued before the passkey existed are refused.

### Consequences

- Good, because a stolen mailbox gives a day of warning instead of instant access.
- Good, because day-to-day sign-in stays a passkey tap and recovery needs no admin.
- Bad, because someone who lost every passkey waits a day.
- Bad, because an attacker who controls the mailbox can also delete the notice; the account page and the next
  passkey sign-in still show or cancel the request.

## More information

Issue #76; ASVS table in [security.md](../security.md).
