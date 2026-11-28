# 0016. Upstream golang.org/x/oauth2, with a token source that saves refreshed tokens

- Status: accepted
- Date: 2026-11-27
- Aliases: D203

## Context and problem

ADR 0015 kept a fork of `golang.org/x/oauth2` only for its `OnTokenChange` hook, which saves a
refreshed token (PV-235). The fork must be kept in step with upstream by hand. onedrive-go dropped
the same fork by wrapping upstream's token source.

## Decision drivers

- No forked dependency.
- Each refresh saved once, and an older token never saved over a newer one.
- A failed save must not fail the run.

## Considered options

### Keep the fork

- Good: no change.
- Bad: upstream fixes arrive only when the fork is rebased.
- Failure modes: a security fix upstream missing from the fork.

### Wrap `cfg.TokenSource` and save a token that differs from the last one returned

- Good: upstream only; upstream's reuse source returns the same token until it refreshes, so a
  changed token is a refreshed one.
- Bad: the wrapper must order saves itself.
- Failure modes: without a lock across the call, two requests could save an older token after a
  newer one.

### onedrive-go's full design (cross-process lock, lineage tracking, retried writes)

- Good: safe with several processes on one sign-in file.
- Bad: much more code; pimvault allows one run per state directory (ADR 0013).
- Failure modes: complexity without a matching need.

## Decision

pimvault uses upstream `golang.org/x/oauth2` with no `replace`. `oauthflow.TokenSource` wraps
`cfg.TokenSource` and saves each token whose access or refresh token differs from the last one it
returned, holding one lock across the wrapped call, so concurrent requests of an account save a
refresh once and saves happen in the order tokens were issued. A failed save is logged and the run
goes on with the refreshed token (D203, superseding D134). The fork repository is left as it is.

## Consequences

- Dependabot updates `golang.org/x/oauth2` like any module.
- The lock serializes callers of one account's token source, which upstream's reuse source does
  anyway.

## Confirmation

`internal/oauthflow` tests: a refresh is saved once, a valid token is never saved, concurrent use
saves one refresh, a failed save still returns the token (PV-235).

## Retirement proof

An upstream hook for refreshed tokens (golang/go#77502) would make the wrapper unnecessary.

## Reversal plan

Restore the `replace` and the `OnTokenChange` callback; the save callbacks are the same.
