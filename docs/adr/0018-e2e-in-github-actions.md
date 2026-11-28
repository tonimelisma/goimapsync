# 0018. The end-to-end test runs in GitHub Actions

- Status: accepted
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D90, D193, D199

## Context and problem

The end-to-end test needs Outlook.com's IMAP port, which cloud sessions cannot reach (ADR 0017).
The repository is public, so its workflow logs are public too.

## Decision drivers

- Reach IMAP; run before code changes merge, on `main`, and on a schedule.
- Forks never get credentials; logs never name a test account or a token.
- Unit tests stay the main safety net.

## Considered options

### Keep it in the cloud session, without IMAP

- Good: no CI credentials.
- Bad: half the test cannot run.
- Failure modes: Outlook.com writes untested.

### GitHub Actions on pushes, same-repository pull requests, weekly and on dispatch

- Good: reaches IMAP; each PR gets an `e2e` commit status; the weekly run keeps tokens in use.
- Bad: credentials in Actions; a PR runs its own copy of the workflow with them.
- Failure modes: a branch's workflow change could misuse the credentials, accepted because only
  people with write access push branches.

## Decision

Testing is unit tests following onedrive-go's rules (testify assertions, fakes at I/O seams,
failure injection, golden files, no `time.Sleep`) plus end-to-end runs against real throwaway
Gmail and Outlook.com test accounts (D90). The repository is public; the end-to-end test runs in
GitHub Actions (`e2e.yml`), as the cloud environment cannot reach Outlook.com's IMAP port; one run
at a time; the test accounts' emails are masked in the public log; the result is the tested
commit's `e2e` commit status (D193). It runs only in GitHub Actions, never on a developer machine
or a server: on every push to `main`, on pull requests whose head branch is in this repository
(never forks, never `pull_request_target`), weekly on a fixed off-the-hour UTC schedule, and on
dispatch. The weekly run re-enables its own workflow so GitHub does not disable the schedule, and
keeps one issue labelled `e2e-failure` open while it fails, closing it when it passes (D199,
revising D193). D193 first ran it only on pushes and dispatches, never for `pull_request`, with a
service-account key as a repository secret; D199 replaced the key with keyless Workload Identity
Federation; how the workflow reaches the tokens now is ADR 0019.

## Consequences

- Code PRs wait for the `e2e` status of their head (CLAUDE.md, Git, CI and PRs).
- Dependabot's PRs get no OIDC token; e2e is dispatched for their branches.

## Confirmation

`.github/workflows/e2e.yml`: its triggers, the same-repository condition, the concurrency group
and the masking step; `TestLive_BackupVerifyRestore` and `TestLive_HostileMailConverges` run
there.

## Retirement proof

A cloud session that reaches IMAP and can report a commit status, or a provider sandbox that
replaces real test accounts.

## Reversal plan

Drop the `pull_request` trigger to run e2e on `main` and dispatch only; the test code is the
same.
