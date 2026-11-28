# 0009. Change tokens where offered, full listing as the fallback

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D50, D53, D141, D146

## Context and problem

A daily backup should download only what changed. Providers offer change tokens for some data
and not for other data, and the tokens expire: Gmail history start IDs usually after a week,
rarely after hours (F4, F42).

## Decision drivers

- Few API calls and downloads per run (Gmail quota, F77, F80).
- Correctness when a token expires or a change leaves no history.

## Considered options

### List everything on every run

- Good: always correct, no state.
- Bad: lists every message ID of every label each run.
- Failure modes: slow and quota-hungry for large accounts.

### Change tokens with a full listing as the fallback

- Good: small incremental runs; the full listing is the same code path as the first backup.
- Bad: two ways to find changes per provider.
- Failure modes: a change a token misses (a Gmail label rename) needs detecting explicitly.

## Decision

At first, Gmail mail backup listed all message IDs plus those of Inbox, Drafts, Sent and each user
label on every run and stored no change token (D141). Now change detection uses the provider's
change tokens where offered: Gmail history, Graph delta (Outlook mail per folder, contacts, To Do
lists), Google Calendar and People sync tokens. Data without tokens is fully listed each run
(Google Tasks; Outlook calendars, whose recurring series are re-listed in full every run, D53).
An expired token, or a Gmail label rename (which produces no history), triggers the full listing:
list everything, download what is missing, mirror what is gone, without downloading existing
files again (D50). Tokens are stored in the state directory (D146, revising D141).

## Consequences

PV-102 and PV-103 describe the behavior. Losing state costs one full listing (ADR 0006).

## Confirmation

`internal/backup` tests for incremental runs, expired tokens and label renames on every provider
(PV-102, PV-103).

## Retirement proof

A provider dropping its change token API, or a measurement showing full listing is cheap enough
for the largest accounts.

## Reversal plan

Remove the token path for a provider and keep its full listing, which already exists.
