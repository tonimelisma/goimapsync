# Lessons

Rules learned the hard way that are not obvious from the code, and problems still open. When an
open problem is solved, or a rule moves into a test, an ADR or CLAUDE.md, its entry is deleted.
The incidents behind them are in [incidents](incidents.md).

## Rules learned

- Run the end-to-end test on a case-insensitive filesystem when a change touches archive names:
  Outlook IDs that differ only in case once overwrote each other there without an error
  (incident 2026-10-25, ADR 0008).
- A count that does not converge on a re-run is a provider quirk until proven otherwise:
  Outlook.com silently removes contacts it takes for duplicates, so "written" stayed at 26 on
  every run (incident 2026-10-25, PV-159, PV-160).
- Map provider errors to their real cause before showing them: Outlook.com's refusal of IMAP for
  a disabled account setting looked like an expired sign-in (incident 2026-10-25, PV-185).
- Sign test accounts in from a private browser window and check the `signed in as` line: a
  browser with several Microsoft sessions signs in the active one (incident 2026-10-25).
- golangci-lint must be built with the project's Go version, or it cannot type-check the
  language version `go.mod` names (F27b); CI and the session-start hook build it from source.

## Open problems

- None tracked here; open work is in GitHub issues.
