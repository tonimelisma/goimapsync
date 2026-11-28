# 0010. Deleted items are kept aside for 30 days and mass deletions are withheld

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D11, D181, D182

## Context and problem

The archive mirrors the account (ADR 0006). At first, deletions in the account were mirrored in
the archive immediately, and recovering deleted items was declared not pimvault's job (D11). That
means an accidental or malicious mass deletion in the account wipes the backup at the next run.

## Decision drivers

- A backup must survive a mistake in the account for long enough to notice it.
- The archive should still mirror the account.

## Considered options

### Mirror deletions at once

- Good: the archive equals the account.
- Bad: no protection against deletions.
- Failure modes: a deleted mailbox empties the backup the next night.

### Never delete

- Good: nothing is lost.
- Bad: the archive grows without bound and no longer mirrors the account.
- Failure modes: restore writes back items the user deleted on purpose.

### Keep deleted files aside for 30 days, and withhold mass deletions

- Good: the main archive mirrors the account; recent deletions are recoverable by copying a file
  back; a mass deletion needs confirmation.
- Bad: one more directory to explain.
- Failure modes: a legitimate large cleanup needs `--allow-deletions` once.

## Decision

When an item disappears from the account, backup moves its file to
`<archive>/.deleted/<account>/<YYYY-MM-DD>/` (same type and folder directories below it) and
deletes it from there 30 days later; restore, migrate and verify ignore that area (D181, revising
D11). If one run would remove more than 20% of a collection holding at least 50 items, it removes
none of them, backs up everything else, warns and exits 2; `backup --allow-deletions` lets such
removals through; moves between folders do not count (D182, revising D11).

## Consequences

PV-106 and PV-107 describe the behavior; the README tells users how to recover a file and when to
pass `--allow-deletions`.

## Confirmation

`internal/backup` and `internal/archive` tests for kept-aside files, pruning after 30 days and
withheld mass deletions, for each provider (PV-106, PV-107).

## Retirement proof

Provider-side recovery that covers every data type for longer than 30 days.

## Reversal plan

Change the retention or threshold constants, or delete files at once again; the `.deleted` area
would simply stop filling.
