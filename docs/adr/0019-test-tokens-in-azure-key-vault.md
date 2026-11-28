# 0019. Test tokens in Azure Key Vault, reached keylessly from Actions and from cloud sessions

- Status: accepted
- Date: 2026-11-27
- Aliases: D195, D201, D202

## Context and problem

The end-to-end test (ADR 0018) and live probes need the test accounts' OAuth tokens, and store
refreshed ones back. A test account sometimes has to be signed in again. No long-lived key should
exist anywhere, and no session should need a particular machine.

## Decision drivers

- No key or client secret for CI; no secret in the repository or its logs.
- Live probes and re-sign-in from a cloud session.
- A re-sign-in can never store a real account as a test account.

## Considered options

### Google Secret Manager with a service-account key

- Good: worked from every environment.
- Bad: a long-lived key in each environment.
- Failure modes: a leaked key reads and writes the tokens.

### Google Secret Manager with Workload Identity Federation, gcloud on a maintainer's machine

- Good: keyless in Actions.
- Bad: live probes and re-sign-in need a maintainer's machine with a gcloud sign-in.
- Failure modes: no live access from a cloud session.

### Azure Key Vault: OIDC federation in Actions, the shared cloud sign-in in sessions

- Good: keyless in Actions; cloud sessions reach the vault with the client secret the shared
  cloud environment already holds, read only by the session-start hook.
- Bad: the shared cloud sign-in may read and set this vault's secrets.
- Failure modes: without the environment's secret or its network allowances, a session has no
  live access, and says so.

## Decision

The tokens (`e2e-gmail-token`, `e2e-outlook-token`) live in Azure Key Vault `kv-pimvault-ci`;
Secret Manager is no longer used and no fallback exists. GitHub Actions reaches the vault
keylessly: the `e2e` job runs in the GitHub environment `e2e`, exchanges GitHub's OIDC token at
Entra ID for a Key Vault token of the app `gh-pimvault-e2e` (one federated credential for this
repository's immutable OIDC subject of the environment `e2e`; Key Vault Secrets Officer on the
vault) and passes it as `PIMVAULT_E2E_ACCESS_TOKEN` with `PIMVAULT_E2E_KEY_VAULT`. Stored
refreshes add a secret version. The Workload Identity pool and the `pimvault-e2e` service
account are no longer used (D202).

Cloud sessions reach the same vault: the session-start hook signs in as the shared cloud app
`cloud-sessions-reader` (client-credentials flow with the environment's
`MELISMA_SECRETS_CLIENT_SECRET`; Key Vault Secrets Officer on the vault) through
`script/live/vault-token` and exports the token and vault name the same way, so live probes and
re-sign-in run in a cloud session; IMAP stays CI-only, as sessions cannot reach port 993 (D202).

The Google app is in production (ADR 0005), so there is no weekly test sign-in; a test account is
signed in again only when its stored token is revoked or fails (D201). The re-sign-in runs in a
cloud session: the agent starts `script/live/e2elogin` in the background, the maintainer opens
the printed URL in a private tab on a phone, signs in as the test account and pastes the failed
`127.0.0.1:53682/…` or `localhost:53682/…` address back; the tool stores the tokens only if the
signed-in account is the one the secret already holds (D195; once weekly with Secret Manager,
then on a maintainer's machine, now in the cloud session).

## Consequences

- CLAUDE.md, "Cloud sessions", gives the commands; the maintainer's one-time setup is listed there
  too.
- The session's token lasts about an hour; `script/live/vault-token` renews it.

## Confirmation

`internal/e2e` tests for `FromEnv` and the Key Vault client against a fake (secrets_test); the
`e2e` workflow's credentials step; the session-start hook prints whether it exported a token.

## Retirement proof

A provider sandbox without real tokens, or a vault-less way for Actions and sessions to share
refreshed tokens.

## Reversal plan

Point `PIMVAULT_E2E_KEY_VAULT` at another vault, or swap the Key Vault client in `internal/e2e`
for another store; the workflow's and hook's sign-in steps change with it.
