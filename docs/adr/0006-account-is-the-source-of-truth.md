# 0006. The account is the source of truth; archive and state are rebuildable

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D35, D51, D52, D72, D96

## Context and problem

pimvault keeps three kinds of data on disk: the archive (the items), sync state (change tokens,
label maps) and settings with sign-ins. If state and archive disagree with the account, which one
wins, and where does each live?

## Decision drivers

- Losing state must cost a full resync, never data.
- The archive should be usable by other tools without pimvault's metadata in it.
- Standard locations on Linux.

## Considered options

### State inside the archive (a hidden directory)

- Good: one directory to copy.
- Bad: the archive is no longer only standard files; copying an archive copies stale state.
- Failure modes: a restored old archive with its old tokens skips changes.

### Archive, state and config in the XDG directories, the account authoritative

- Good: the archive holds only `.eml`, `.ics` and `.vcf` files; state is disposable.
- Bad: three directories instead of one.
- Failure modes: a lost state directory triggers a full listing (ADR 0009), no loss.

## Decision

The account is the source of truth; the archive mirrors it one file per item, with no dedupe
inside it (D35), and the archive and state are projections that can be rebuilt. The archive
defaults to `$XDG_DATA_HOME/pimvault/` (D96), state to `$XDG_STATE_HOME/pimvault/` as JSON
files (D51, D52), and the config file to `$XDG_CONFIG_HOME/pimvault/config.toml` (D72), each
replacing the earlier choice of a hidden directory in the archive.

## Consequences

- PV-100, PV-104 and PV-225 describe the behavior.
- Dedupe happens only when writing into an account (ADR 0012).

## Confirmation

`internal/config` tests (precedence, defaults) and `internal/state` tests (save, load, a corrupt
file is treated as empty) for PV-104 and PV-225.

## Retirement proof

A need to back up something the account no longer has (pimvault keeps deleted items only 30 days,
ADR 0010) would make the archive a source of truth of its own.

## Reversal plan

Moving state into the archive would be a path change in `internal/config` and `internal/state`; a
full listing rebuilds it.
