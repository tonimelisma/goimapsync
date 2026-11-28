# 0017. End-to-end tests in the agent's cloud environment, tokens in Secret Manager

- Status: superseded by [0018](0018-e2e-in-github-actions.md) and [0019](0019-test-tokens-in-azure-key-vault.md)
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D94, D157, D158, D179

## Context and problem

pimvault's behavior depends on provider quirks that only real accounts show, so it needs an
end-to-end test against throwaway test accounts. The question was where that test runs and where
the test accounts' tokens live.

## Decision drivers

- Real accounts' tokens never in CI logs or in the repository.
- The test runs before changes merge.

## Considered options

### Locally only

- Good: no secrets anywhere but a developer machine.
- Bad: depends on one machine; not run on every change.
- Failure modes: regressions merge untested.

### In the agent's cloud environment, tokens in Google Secret Manager

- Good: runs where the agent works, before every PR.
- Bad: the cloud environment cannot reach Outlook.com's IMAP port (F66), and it holds a
  long-lived service-account key.
- Failure modes: the IMAP half of the test cannot run.

## Decision

First, real-account end-to-end tests ran locally only, and CI ran build, vet, lint and the tests
that need no accounts (D94). Then they ran in the agent's cloud environment with the test-account
tokens stored as environment secrets, never in GitHub CI (D157), before every PR; only the
throwaway test accounts were used, never a real account, and account emails were redacted in
logged output (D179, refining D157). The tokens were kept in Google Secret Manager in pimvault's
Google Cloud project; each environment (local, the agent's cloud environment, CI if ever used)
held one service-account credential as an environment variable, and the tests read tokens from
the store and saved refreshed ones back (D158).

## Consequences

The IMAP port being unreachable from cloud sessions forced the move to GitHub Actions (ADR 0018);
the service-account key was retired with the move to Azure Key Vault (ADR 0019).

## Confirmation

None now: superseded.

## Retirement proof

Shown by ADR 0018 (the test runs in GitHub Actions, which reaches IMAP) and ADR 0019 (no key
anywhere).

## Reversal plan

Not needed: superseded.
