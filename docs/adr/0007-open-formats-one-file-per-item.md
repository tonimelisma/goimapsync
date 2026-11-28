# 0007. Open formats, one file per item, folders as directories

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D7, D8, D25, D33, D34, D42, D43, D150

## Context and problem

The archive must stay readable without pimvault, for decades, and restore and migrate must be
able to write it back into either provider.

## Decision drivers

- Formats other tools read: mail clients, calendar and contacts apps, vdir tools (F20).
- Incremental backups that touch only what changed.
- No pimvault-specific metadata files.

## Considered options

### A database or a single archive file (mbox, one `.ics` per calendar)

- Good: fewer files.
- Bad: one changed item rewrites a large file; a damaged file loses many items.
- Failure modes: an interrupted write corrupts the whole collection.

### Standard per-item files in directories

- Good: one file per item; folder membership is the directory; the vdir convention for
  calendars and contacts.
- Bad: many small files.
- Failure modes: a directory name the filesystem refuses (handled by PV-062).

## Decision

Mail as `.eml` (RFC 5322), events as `.ics` (iCalendar), contacts as vCard 4.0 `.vcf` (D7, D42);
folder information as directories, with no per-item sidecar files (D8). Tasks are iCalendar
`VTODO`, one `.ics` per task in a directory per task list (D25). Mail folders are flat, every
`.eml` directly in its folder's directory with no date subdirectories (D33); one `.ics` per event
and one `.vcf` per contact in a directory per calendar or address book (D34). A recurring event
and its exceptions share one `.ics` file (D43). Only standard properties are stored (RFC 5545,
RFC 7986, vCard 4.0); provider fields without one are not (D150).

## Consequences

- PV-050 to PV-054 and PV-013 describe the behavior; some provider fields are lost on purpose
  (README, "What is not kept").
- Restore reads the same files any other tool could have written.

## Confirmation

Golden-file and round-trip tests in `internal/gcal`, `internal/graph`, `internal/gpeople` and
`internal/vcardfile`; `verify` parses every `.ics` and `.vcf` (PV-121).

## Retirement proof

A provider feature users need restored that no standard property can carry, shown by a real
restore that loses it.

## Reversal plan

Add standard extension properties (RFC 9073, RFC 9554) or `X-` properties in the encoders; the
layout stays.
