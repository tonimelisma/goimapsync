# 0014. One Go module, internal packages, code adapted from onedrive-go

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D91, D109, D110, D111, D112, D113, D114, D126, D129, D137, D139, D145

## Context and problem

pimvault reuses code from the sister project onedrive-go (token files, retry, Graph transport and
auth, error classes, log files). The question was how to lay out the repository and whether the
copied code lives in separate modules.

## Decision drivers

- Simple builds and dependency updates.
- No package or module without a real boundary.
- Copied code that does not depend on pimvault, so it could move to its own repository.

## Considered options

### Copied packages as separate modules under `pkg/<name>/`, joined by `go.work`

- Good: a later split into its own repository is a move.
- Bad: several `go.mod` files to update; `go.work` in every build.
- Failure modes: version skew between the modules.

### One module, everything under `internal/`

- Good: one `go.mod`; one build.
- Bad: moving a package out later needs a module split then.
- Failure modes: none for a single binary.

## Decision

The repository started as `cmd/pimvault/` plus `internal/<package>/`, with code copied from
onedrive-go under `pkg/<name>/` as separate Go modules used via `go.work` (D91, D110), only
`tokenfile` in the foundation increment and the rest with the increments that used them (D129).
Selected for copying: `tokenfile`, `retry`, `graphtransport`, Graph auth and client core,
`errclass`, `logfile` (D109); the `perf` timing hook in `retry` and `graphtransport` dropped
(D111); from onedrive-go's `graph` only the auth flows, token source and client core, not the
drive endpoints (D112); `fsroot` not copied, its calls replaced with plain `os`/`filepath`
code (D113); `config` written from scratch (D114). The first packages were `internal/cli`,
`internal/config` and `internal/account` (D126), and the login packages `oauthflow` and
`internal/login` (D137).

pimvault is now one Go module: copied code lives in `internal/` like everything else and moves to
its own module only when it actually moves to its own repository (D145, revising D91, D110, D129,
D137). Package and module layout grows with the code and is the agent's decision, reported in the
PR (D139).

## Consequences

- `internal/oauthflow`, `internal/tokenfile` and `internal/httpretry` import no other pimvault
  package, so they can move out.
- New packages appear when the code shows a real boundary.

## Confirmation

`go.mod` is the only module file; `go list ./...` shows only `cmd/`, `internal/` and
`script/` packages.

## Retirement proof

A second consumer of a copied package, which would justify its own module and repository.

## Reversal plan

Split a package into its own module with a `go.mod` and a `replace` during the move.
