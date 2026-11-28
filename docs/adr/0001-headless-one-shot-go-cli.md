# 0001. A headless, one-shot Go CLI for Linux

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D3, D4, D48, D49, D86, D89, D97, D147

## Context and problem

pimvault backs up personal Gmail and Outlook.com accounts and writes them back (PV-001). It is
meant to run unattended on a server, so it needs no GUI, and it must reach four Google APIs,
Microsoft Graph and Outlook.com IMAP. The question is what kind of program it is and how it is
scheduled.

## Decision drivers

- Runs unattended on a Linux server; installs as one file.
- Few moving parts: no daemon to supervise, no state that only a running process holds.
- Full control over provider quirks (see `docs/research/api-quirks.md`), which wrappers around
  other tools hide.

## Considered options

### A wrapper around existing tools (imapsync, vdirsyncer, GYB)

- Good: less code at first.
- Bad: no tool covers mail, calendars, contacts and tasks for both providers, for backup,
  restore and migrate (initial research, "Prior art"); quirks would be fought through several
  layers.
- Failure modes: a wrapped tool's change breaks pimvault in ways it cannot fix.

### A long-running daemon

- Good: could react to changes quickly.
- Bad: needs supervision, health checks and its own scheduling; a crash mid-run is harder to
  reason about.
- Failure modes: a stuck daemon silently stops backing up.

### A one-shot Go binary with its own provider adapters

- Good: one static file, cross-compiled; a run starts, syncs and exits; cron or a systemd timer
  schedules it.
- Bad: every adapter is pimvault's to write and keep up.
- Failure modes: a missed schedule goes unnoticed by pimvault itself.

## Decision

A single Go binary named `pimvault`, with its own provider adapters (D4, D97), headless on Linux
(D3), built with Go 1.27 for Linux only (D86). Each run syncs once and exits; scheduling is
external (D48). The earlier idea of warning when more than 6 days passed since the last
successful run (D49) is dropped (D147): expired change tokens are handled by a full listing
(ADR 0009) and missed runs are the scheduler's concern. The code is MIT-licensed (D89).

## Consequences

- No background process, no IPC; state between runs lives in files (ADR 0006).
- Operators schedule it themselves (README, "Back up").
- Every provider behavior pimvault relies on is pimvault's to record and test.

## Confirmation

- `go.mod` names Go 1.27; `script/release.sh` builds static `linux-amd64` and `linux-arm64`
  binaries only.
- `cmd/pimvault` returns from `cli.Run` with an exit code; there is no serve or watch command
  (PV-224).

## Retirement proof

A real need for continuous sync (minutes, not a daily timer) that external scheduling cannot meet,
shown by a user's workload.

## Reversal plan

A daemon mode would wrap the same `backup` entry point in a loop with its own lock handling
(PV-110); the archive and state formats would not change.
