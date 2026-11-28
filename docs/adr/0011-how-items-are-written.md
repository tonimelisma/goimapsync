# 0011. How mail and events are written into the accounts

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D55, D56, D62, D66, D163, D165, D166, O31, O138

## Context and problem

Restore and migrate write archived items into an account. Each provider offers several write
paths, and they differ in what they keep (dates, Message-IDs), what they trigger (spam
classification, invitations) and what permissions they need.

## Decision drivers

- Keep the original headers, so later runs recognise what is already there (ADR 0012).
- Never send invitations or notifications to other people.
- One sign-in per account (ADR 0004).

## Considered options

### Gmail: `messages.import` or `drafts.create`

- Good: standard client paths.
- Bad: `messages.import` runs spam classification; `drafts.create` replaces Date and
  Message-ID (F63).
- Failure modes: restored mail lands in Spam; drafts are written again on every run.

### Gmail `messages.insert` (with the DRAFT label for drafts); Outlook.com IMAP APPEND

- Good: `messages.insert` skips classification (F9, F53) and keeps a draft's headers; IMAP
  APPEND keeps the message bytes.
- Bad: Outlook.com needs IMAP, a second API with its own token audience (F26).
- Failure modes: IMAP refused when the account's IMAP setting is off (PV-185).

### Events: Gmail `events.insert`; Outlook without `isDraft`

- Good: simple.
- Bad: invitations go out to every attendee.
- Failure modes: a restore emails everyone in the calendar.

## Decision

- Mail into Gmail with `messages.insert`, no spam scanning or classification (D55). Mail for
  Gmail's Drafts was first to go through the drafts API with the compose permission (D163); it is
  now inserted with the DRAFT label, which keeps its Date and Message-ID, and the compose
  permission is not requested (D165, F63).
- Mail into Outlook.com with IMAP APPEND, with the IMAP OAuth scope on the same Microsoft app
  (D56); one browser sign-in consents to Graph and IMAP, and the refresh token is redeemed once
  per API (O138, built with D161 and D164).
- Events into Gmail with `events.import` as private copies: attendees kept, no notifications
  (D62).
- Events with attendees into Outlook.com with their attendees and Graph `isDraft: true`, which
  sends no invitations (D166). This decided O31 after prototypes against a test account, as D66
  required: EWS `SendToNone` is not available to pimvault (F48, F50, F57, F62).

## Consequences

PV-163, PV-167, PV-173 and PV-175 describe the behavior. Outlook.com users must turn IMAP on for
restore and migrate (README; PV-185).

## Confirmation

`internal/restore` tests against the Gmail, Graph and IMAP fakes check the endpoints, labels,
`isDraft` and flags used (PV-163, PV-167, PV-173, PV-175).

## Retirement proof

A Graph API that writes mail with its original headers into personal Outlook.com accounts, or a
Gmail change to what `messages.insert` keeps.

## Reversal plan

Each write path is one function in `internal/gmail`, `internal/gcal`, `internal/graph` or
`internal/outlookimap`; replacing one changes that package and its fake.
