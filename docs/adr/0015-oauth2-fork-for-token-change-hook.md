# 0015. OAuth through a fork of golang.org/x/oauth2 for its OnTokenChange hook

- Status: superseded by [0016](0016-upstream-oauth2-with-saving-token-source.md)
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D134

## Context and problem

A refreshed OAuth token must be saved, or the next run may hold a refresh token the provider has
already replaced. Upstream `golang.org/x/oauth2` gives no callback when its token source
refreshes.

## Decision drivers

- Save every refreshed token exactly when it is issued.
- Share onedrive-go's approach.

## Considered options

### The fork `github.com/tonimelisma/oauth2` with a `replace`

- Good: a `Config.OnTokenChange` hook fires on every refresh.
- Bad: a forked dependency to keep in step with upstream.
- Failure modes: the fork falls behind upstream fixes.

### Wrap upstream's token source

- Good: no fork.
- Bad: pimvault detects refreshes itself.
- Failure modes: an older token saved over a newer one if done carelessly.

## Decision

OAuth used the fork `github.com/tonimelisma/oauth2` (a `replace` for `golang.org/x/oauth2`, as
onedrive-go did) for its `OnTokenChange` hook, until golang/go#77502 landed upstream (D134).

## Consequences

`go.mod` carried a `replace`; `internal/oauthflow` set `OnTokenChange` on a copy of the
config.

## Confirmation

`internal/oauthflow`'s token source tests.

## Retirement proof

Shown by ADR 0016: a wrapper around upstream's token source saves each refresh once, tested.

## Reversal plan

Not needed: superseded.
