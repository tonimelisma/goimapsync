# 0003. Few third-party dependencies, each chosen by the maintainer

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D84, D88, D120, D121

## Context and problem

pimvault needs IMAP for writing into Outlook.com, iCalendar and vCard parsing and writing, a CLI
framework and a TOML parser. Each dependency is code pimvault ships and trusts with account data.

## Decision drivers

- Correct handling of the standards (RFC 5545, vCard 4.0, IMAP4rev1/XOAUTH2).
- Few transitive dependencies; maintained upstreams.
- A new dependency is a product decision, made by the maintainer.

## Considered options

### Write everything in-house

- Good: no third-party code.
- Bad: IMAP and iCalendar parsing are large and easy to get subtly wrong.
- Failure modes: archive files other tools cannot read.

### A small, fixed set of focused libraries

- Good: well-tested parsers; one family (emersion) for mail, IMAP, iCalendar and vCard.
- Bad: go-imap v2 is still beta.
- Failure modes: a beta API change needs adapting at upgrade time.

## Decision

- IMAP (used only for APPEND into Outlook.com): `github.com/emersion/go-imap/v2` (beta), whose sign-in takes a `github.com/emersion/go-sasl` client (D84).
- iCalendar and vCard: `github.com/emersion/go-ical` and `github.com/emersion/go-vcard` (D88).
- CLI: `github.com/spf13/cobra` (v1.10.x, bringing `github.com/spf13/pflag`) (D120).
- TOML: `github.com/BurntSushi/toml` (v1.6.x) (D121).
- Tests: `github.com/stretchr/testify`. OAuth: `golang.org/x/oauth2` (ADR 0016).

Adding any other module needs the maintainer's approval first.

## Consequences

- Dependabot proposes updates weekly; a major or beta API change is adapted in its own PR.
- A new need is met with the standard library or existing modules before a new one is proposed.

## Confirmation

`.golangci.yml`'s depguard rule lists the allowed modules; an import of any other fails
`script/check.sh`.

## Retirement proof

An allowed module abandoned upstream, or a security advisory without a fix.

## Reversal plan

Replace the module behind the package that wraps it (`internal/outlookimap`, `internal/icalfile`,
`internal/vcardfile`, `internal/config`, `internal/cli`); the archive format does not change.
