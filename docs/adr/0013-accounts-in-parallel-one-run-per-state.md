# 0013. Accounts back up in parallel, items one at a time, one run per state directory

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D186, D191

## Context and problem

A backup of several accounts took the sum of their times. Gmail throttles parallel downloads:
one raw download at a time runs at about 6.5 a second, four at once are throttled within seconds
(F82, F83), and each account has its own quota (F80). Two runs on one state directory at the same
time would race on state and archive files.

## Decision drivers

- Wall time for several accounts.
- Stay under per-account throttling.
- No two writers of one state file.

## Considered options

### Everything sequential

- Good: simple.
- Bad: slow with several accounts.
- Failure modes: a nightly run overlaps the next one.

### Parallel within an account

- Good: faster in theory.
- Bad: throttled at once (F83).
- Failure modes: retries make it slower than sequential.

### Accounts in parallel, items one at a time within each

- Good: each account has its own quota, archive directory, state file and sign-in.
- Bad: output from several accounts must be ordered.
- Failure modes: none beyond those of one account.

## Decision

`backup` backs up the selected accounts at the same time; within an account items still go one
at a time (F83). Summary lines print in the accounts' order, each once it and those before it are
done; in a terminal the status line shows every account in progress (D191). One pimvault run at
a time per state directory: a second `backup`, `verify`, `restore` or `migrate` exits 1 at
once, naming the running process; a lock left by a crashed run is taken over (D186).

## Consequences

PV-109 and PV-110 describe the behavior. Two schedules on one host must use separate state
directories or not overlap.

## Confirmation

`internal/cli` tests for concurrent accounts and the refused second run, `internal/state` lock
tests (PV-109, PV-110).

## Retirement proof

Providers allowing parallel downloads per account without throttling, measured.

## Reversal plan

Set the account concurrency to one in `internal/cli`; output ordering already handles it.
