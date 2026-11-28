# 0005. The Google app is published "In production" without verification

- Status: accepted
- Date: 2026-10-31
- Aliases: D156, D159, D201

## Context and problem

A Google OAuth app in "Testing" status gets refresh tokens that expire after 7 days; "In
production" without Google's verification shows an unverified-app warning before consent and has
a 100-user lifetime cap, but sign-ins do not expire weekly (F1, F30). Gmail's scopes are
restricted, so verification would need a security assessment.

## Decision drivers

- Sign-ins that do not expire for unattended backups.
- No security assessment for a personal-use tool.

## Considered options

### Stay in Testing, test users added, signed in again weekly

- Good: no unverified-app warning.
- Bad: every account, test accounts included, signs in again every week.
- Failure modes: a missed weekly sign-in stops backups.

### Publish "In production" without verification

- Good: refresh tokens do not expire after 7 days.
- Bad: sign-in shows an unverified-app warning; at most 100 users ever.
- Failure modes: past the cap, new users cannot sign in.

## Decision

The app is published "In production" without verification (D156). For a while it stayed in
Testing with the test accounts as test users, signed in again weekly (D159), until pimvault was
used for real accounts; it has been "In production" since 2026-10-31, so D156 applies again,
D159 is superseded and there is no weekly test sign-in (D201). A test account is signed in again
only when its stored token is revoked or fails (ADR 0019).

## Consequences

- The README tells users about the warning and how to use their own client instead (PV-230).
- The end-to-end test's Google tokens last until revoked.

## Confirmation

The weekly end-to-end run (ADR 0018) fails, and opens an `e2e-failure` issue, if a stored Google
token stops working.

## Retirement proof

More than about 80 users, or a reason to remove the warning, would mean seeking verification.

## Reversal plan

Move the app back to Testing in the Google Cloud console and sign test accounts in weekly; nothing
in the code changes.
