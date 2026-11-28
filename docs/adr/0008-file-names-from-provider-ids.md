# 0008. Archive file names come from provider IDs, with capitals marked

- Status: accepted
- Date: 2026-10-25
- Aliases: D36, D45, D178

## Context and problem

Each archive file needs a stable name, so a backup can tell which items it already has and move
a message between directories. Outlook item IDs are case-sensitive and often differ only in
case; on a case-insensitive filesystem (macOS, exFAT, most SMB shares) such files overwrote each
other and an Outlook backup silently lost about 5–10% of its items (F69; `docs/incidents.md`,
2026-10-25).

## Decision drivers

- Stable names derived from the account, so a full listing can compare without downloading.
- No collisions on case-insensitive filesystems.

## Considered options

### Content hashes

- Good: unique.
- Bad: a name cannot be known without downloading the item.
- Failure modes: every full listing downloads everything.

### Provider IDs as they are

- Good: known from a listing.
- Bad: collide on case-insensitive filesystems.
- Failure modes: silent loss (the 2026-10-25 incident).

### Provider IDs with each capital written as `~` plus its lowercase letter

- Good: known from a listing; distinct on any filesystem.
- Bad: longer, less readable names.
- Failure modes: none known.

## Decision

`.eml` files are named by the provider's message ID (Gmail message ID, Outlook immutable ID)
(D36); event, contact and task files by the provider's ID like mail (D45). Each capital letter is
written as `~` plus its lowercase letter (`AAkALg…` → `~a~ak~alg…`); collection directories whose
names differ only in case get ` (2)` suffixes as duplicates do (D178, revising D36).

## Consequences

PV-055 and PV-056 describe the behavior. Mail folders need no suffix: Gmail and Outlook.com refuse
folder names that differ only in case (requirement audit, 2026-10-25).

## Confirmation

`internal/archive` tests for IDs and folders differing only in case (PV-055, PV-056).

## Retirement proof

Providers whose IDs are case-insensitive, or an archive format that does not use file names as
keys.

## Reversal plan

A one-time rename of archive files; state is rebuilt by a full listing.
