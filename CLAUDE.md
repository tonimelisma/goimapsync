# CLAUDE.md

## What this is

pimvault is a headless Go CLI for Linux that backs up personal Gmail and Outlook.com mail,
calendars, contacts and tasks to a local folder of standard files (`.eml`, `.ics`, `.vcf`), and
restores or migrates accounts from that folder. It ships as static Linux binaries (amd64, arm64)
published with a build provenance attestation on every merge to `main`. One Go module (Go 1.27
from `go.mod`), golangci-lint, GitHub Actions.

## Working style

- When it is clear what is wanted, do it. Ask only for what only the user can do, or for a real
  product choice nobody has made yet.
- Don't end a turn with a summary announcing the next step, an offer to continue, or a list of
  decisions that block nothing: take the next step.
- Stop only when nothing can move forward without the user, or when what blocks you is
  deliberately protected (a permission, a ruleset, a secret). Then say exactly what is needed.
- Still confirm destructive or irreversible actions outside the normal flow (deleting data,
  rewriting history, changing settings); the normal flow (branch, commit, PR, merge, release) is
  not one of them.
- Live tests clean up everything they create in their own teardown, even when they fail, and
  confirm the removal.

## Product invariants

- The account is the source of truth; the archive and the state are rebuildable projections.
  Losing state costs a full resync, never data. ([ADR 0006](docs/adr/0006-account-is-the-source-of-truth.md))
- The archive holds only standard files, one per item, folders as directories, file names that
  stay apart on case-insensitive filesystems. ([ADR 0007](docs/adr/0007-open-formats-one-file-per-item.md),
  [ADR 0008](docs/adr/0008-file-names-from-provider-ids.md))
- A backup never loses an item to a deletion in the account for 30 days, and withholds mass
  deletions. ([ADR 0010](docs/adr/0010-deleted-items-kept-aside.md))
- Restore and migrate never change the archive, never send invitations or notifications, and
  write nothing twice on a re-run. ([ADR 0011](docs/adr/0011-how-items-are-written.md),
  [ADR 0012](docs/adr/0012-dedupe-by-standard-keys.md))
- One run at a time per state directory. ([ADR 0013](docs/adr/0013-accounts-in-parallel-one-run-per-state.md))
- Tests and probes touch only the throwaway test accounts, never a real account; logs never show
  a test account's email or a token. ([ADR 0018](docs/adr/0018-e2e-in-github-actions.md),
  [ADR 0019](docs/adr/0019-test-tokens-in-azure-key-vault.md))
- Only `.github/workflows/release.yml` on `main` tags, releases or attests; its path and ref are
  the signing identity consumers verify. ([ADR 0020](docs/adr/0020-attested-releases-from-ci-green-main.md))
- New third-party dependencies, user-visible behavior and data-safety behavior are the
  maintainer's decisions; package layout is yours. ([ADR 0003](docs/adr/0003-few-chosen-dependencies.md),
  [ADR 0022](docs/adr/0022-shared-process-standard.md))

## Engineering philosophy

- No compatibility shims. When a structure is wrong, replace it and migrate its callers in the
  same change.
- The smallest correct design. Simplicity first; measure before optimizing, never optimize on a
  hunch.
- One owner per fact, per mutation and per side effect. Two places that know the same thing will
  disagree.
- Abstractions only at real boundaries (a process, a network, a storage format, a platform), not
  in anticipation.
- Don't guess and don't fabricate: an unknown API, path, value or result is looked up, measured or
  reported as unknown.
- You own the repository. Nothing is "pre-existing" or someone else's problem, and red CI is never
  waived: it is fixed first.
- Comments and docs describe current behavior and why it is that way, never its history.
- Blank slate: zero users, no backward compatibility for config, state, flags, output or internal
  APIs. Simple algorithms and data structures while n is small; get the data model right first.
- Unknown counts, dates and progress stay unknown in output until proven.
- One user action has one implementation path. Deterministic decision logic in the core, I/O at
  the edges; no type, package or layer without a real boundary to own; no ghost references to
  deleted code.

## Engineering practices

- Test first. A change in behavior starts with a test that fails without it.
- Every bug, CI failure and review finding gets a regression test, seen failing before the fix;
  quote its red output in the PR.
- The instrumentation flywheel, every increment:
  - Never guess a bug's cause. If the logs and metrics can't explain it, add the instrumentation
    first, so this occurrence or the next one is diagnosable, then fix.
  - Never guess at performance: measure, keep a baseline, optimize, and report before and after.
  - Even when nothing is blocked, review the instrumentation on every path you touched: add what
    was missing, remove what is noise.
- Tests are hermetic: temporary directories and fixtures, never the developer's real data, config
  or accounts.
- Go conventions (standard Go practice and anything golangci-lint enforces are not repeated):
  - Wrap errors as `fmt.Errorf("verb noun: %w", err)`, once per boundary.
  - Bulk operations return results and errors together; never stop at the first failed item.
  - Write files to a temp file in the target directory, `fsync`, then rename.
  - Preserve provider fields you don't model; write back only fields you manage.
  - Always follow pagination (`nextPageToken`, `@odata.nextLink`).
  - No `time.Sleep` in tests; use explicit synchronization or injected clocks.
- Tests: testify assertions, fakes at the I/O seams (`internal/*/…test` packages serve the
  providers' JSON; `internal/outlookimap/outlookimaptest` is an IMAP server with Outlook.com's
  folders and XOAUTH2), failure injection and golden files. Each test that proves a requirement
  carries `// Validates: PV-NNN` above it. The end-to-end tests (`-tags e2e`, `internal/e2e`) run
  only in the e2e workflow. Test account names and credentials are never committed (`.env`,
  `.testdata/`); account emails are redacted in shown output and masked in workflow logs.
- Provider behavior: when a provider behaves differently from its docs or from what pimvault
  assumed, in a live run or a probe, record it in the same change: an F-row in
  `docs/research/2026-09-26-initial-research.md` with the date, account type and evidence, a line
  in `docs/research/api-quirks.md`, a fake that reproduces it and a test. Probe undocumented
  behavior live before relying on it (see Cloud sessions).
- Incidents: when a live run loses, duplicates or misreports data, or touches the wrong account,
  add an entry to `docs/incidents.md` in the fix's PR.
- Writing: plain, direct language, no marketing tone; lead with the answer.

## Commands

```sh
script/verify                         # everything CI checks (run before every push)
script/check.sh                       # tidy, format, lint, build, vet (also -tags e2e), race tests
go test -race ./...                   # the whole unit test suite
go test -race -run TestName ./internal/<package>/   # one test
golangci-lint fmt ./...               # format
script/process-check                  # requirements, Validates tags, aliases and doc links agree
script/stage start | pre-merge <PR> | post-merge <PR>
actionlint                            # after workflow changes (go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12)
```

## Where docs live

- `docs/index.md` maps every doc. Requirements live in `docs/requirements.md`: stable IDs, one
  EARS sentence each, current behavior only, each `[verified]` by a test tagged
  `Validates: <ID>` or `unverified: <why>`. Future work lives in GitHub issues, not in docs.
- `docs/architecture.md` says how the code is built and why; `docs/adr/NNNN-title.md` records each
  decision (superseded, never deleted); `docs/lessons.md` holds rules learned and open problems,
  and is pruned when they are resolved.
- The former decision log's row numbers (`D1`–`D203`, `O31`, `O138`, `O139`) are aliases on the
  requirement or ADR that carries each; cite either, and `script/process-check` resolves them.
- `docs/research/`: dated research notes with sources (facts, cited as F-numbers);
  `docs/research/api-quirks.md` indexes every provider quirk by API. `docs/incidents.md`: live
  incidents with cause, fix and prevention.
- Update the docs and this file in the same PR as the code they describe.

## Increment lifecycle

1. **Start**: `script/stage start`. Anything red on main or in the last scheduled run, and any
   unanswered review comment on recent PRs, is fixed before new work. Read the open issues.
2. **Ready**: write the Definition of Ready in the PR body: the problem, requirement IDs or
   issue, acceptance criteria, what is out of scope.
3. **Test first**, then implement with the instrumentation flywheel.
4. **Verify** with the one verify command; it must be green before pushing.
5. **Docs** (requirements, architecture, ADRs, lessons, this file) in the same PR.
6. **Land**: PR, wait for `CI gate`, squash-merge, which releases (see Git, CI and PRs).
7. **Report**: after the merge, one PR comment with the Definition of Done checklist and the final
   increment report (below).

A plan that spans several increments lives in a GitHub tracking issue with a checklist, never in
a file. Never committed: per-PR report or readiness files, session state or resume files, ledgers
of merged PRs. That information belongs in the PR and its comments.

## Git, CI and PRs

- One branch per increment, cut from a fresh `origin/main`, named `feat/`, `fix/`, `docs/`,
  `chore/`, `refactor/` or `perf/` plus a short topic. Never reuse a branch after its PR merged;
  never name a branch for the tool that wrote it.
- Every change lands through a PR whose body fills `.github/pull_request_template.md`. CI checks
  the body, and that a PR titled `fix...` changes a test.
- `CI gate` is the one required check: a job that needs every other job. Wait for it
  (`script/stage pre-merge <PR>`), and fix every failure; never bypass it.
- Squash-merge as soon as `CI gate` is green, without asking: in a cloud session with
  `gh api -X PUT repos/<owner>/<repo>/pulls/<N>/merge -f merge_method=squash -f sha=<head sha>`
  (`script/stage post-merge <PR>` does this); locally with `gh pr merge --auto --squash`.
- Merging to main releases; nobody tags or publishes by hand.
- The cloud sessions' GitHub proxy refuses tag pushes, branch deletion and GraphQL: use the REST
  API (`gh api`), and leave branch deletion to GitHub's delete-on-merge and the branch-cleanup
  workflow.
- Commit messages: an imperative subject saying what changed; the body says why.
- A PR that changes code also waits for the `e2e` commit status of its head to be `success`
  before merging (the e2e workflow runs on every PR from a branch of this repository; see Cloud
  sessions). A red `main` blocks every merge except its fix.
- Releases (ADR 0020): when the CI run of a push to `main` succeeds, `release.yml` (run on `main`
  only) checks the tested commit if it is still `main`'s head (else the newer commit's run
  releases both), tags it with the next patch version of the series in `VERSION` (above every
  tag and v0.1.2), runs `script/release.sh` and uploads the binaries, `SHA256SUMS` and a signed
  build provenance attestation, then verifies the published release. Builds are reproducible and
  never use a cache; the repository knows nothing of who installs it. A minor or major series
  starts by editing `VERSION` in a PR (`0.2` releases `v0.2.0` next). A rerun for a tagged commit
  only finishes that release; a manual `workflow_dispatch` on `main` (optional version) is the
  fallback. Read the outcome with `gh api repos/tonimelisma/pimvault/releases/latest`. Never
  change `release.yml`'s path or the `main` ref: consumers verify releases with
  `--signer-workflow tonimelisma/pimvault/.github/workflows/release.yml --source-ref refs/heads/main`.
- CI (`ci.yml`) runs `script/verify` and the `process` job on every PR (also when its description
  is edited), on pushes to `main` and weekly (Tuesdays 06:23 UTC); `CI gate` needs both.

## Cloud sessions

Work happens in Claude Code cloud sessions; nothing in this repository needs another machine. The
session's GitHub proxy cannot push tags, create releases or reach IMAP, so those run in GitHub
Actions: releases after a successful CI run on `main`, e2e on pushes, pull requests and
dispatches.

- `.claude/hooks/session-start.sh` runs at session start and does nothing unless
  `CLAUDE_CODE_REMOTE=true`. It installs gcc, curl and git if missing (race tests need cgo), the
  Go release from `go.mod` (through the module proxy, or from go.dev when no Go 1.21+ is
  present), golangci-lint built with that Go and the module cache, then exports a Key Vault token
  for live probes (below). Cold: about a minute on a 4-core cloud container; warm: a few seconds.
- Live probes over HTTPS (Gmail, Graph) run in the session itself (ADR 0019): the hook exports a
  Key Vault token for the test vault as `PIMVAULT_E2E_ACCESS_TOKEN` (with
  `PIMVAULT_E2E_KEY_VAULT`), so `go run ./script/live/tokens -out DIR` signs pimvault in as the
  test accounts; then `go build -o /tmp/pimvault ./cmd/pimvault` and run it with
  `XDG_CONFIG_HOME=DIR`. The token lasts about an hour; renew it with
  `export PIMVAULT_E2E_ACCESS_TOKEN="$(script/live/vault-token)"`. Without the environment's
  `MELISMA_SECRETS_CLIENT_SECRET` or the network to Entra ID and the vault, the hook says why and
  exports nothing. IMAP (port 993) is refused in cloud sessions; anything needing it runs through
  the end-to-end workflow.
- End-to-end test (ADR 0018): `.github/workflows/e2e.yml` runs
  `go test -tags e2e -timeout 40m ./internal/e2e/` only in GitHub Actions: on every push to
  `main`, on every pull request from a branch of this repository (never forks), weekly (Tuesdays
  04:23 UTC) and on dispatch. Each push to a PR runs it again. Dependabot's PRs get no Key Vault
  token, so they skip it; dispatch it for their branch. To rerun it for a branch, e.g. after an
  `error`:
  `gh api repos/tonimelisma/pimvault/dispatches -f event_type=e2e -f 'client_payload[ref]=BRANCH'`.
  The result is the `e2e` commit status of the branch's head:
  `gh api repos/tonimelisma/pimvault/commits/SHA/status --jq '.statuses[]|select(.context=="e2e")|.state'`
  (pending, then success or failure; its `target_url` is the run). One run at a time uses the
  test accounts; GitHub keeps only the newest waiting run, and a dropped one ends as `error`:
  dispatch again. Only commits on this repository's branches are tested. Logs:
  `gh api repos/tonimelisma/pimvault/actions/runs/RUN/jobs`, then `.../actions/jobs/JOB/logs`.
  The log is public; the workflow masks the test accounts' emails and stored tokens
  (`script/live/tokens -mask`), and nothing else it prints may name an account or a token. The
  test tokens are the secrets `e2e-gmail-token` and `e2e-outlook-token` in Azure Key Vault
  `kv-pimvault-ci`, reached keylessly: the `e2e` job runs in the GitHub environment `e2e`
  (variables `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_KEY_VAULT`) and exchanges GitHub's OIDC
  token at Entra ID for a Key Vault token of the app `gh-pimvault-e2e`, whose only federated
  credential trusts this repository's immutable OIDC subject for the environment `e2e`. The Go
  code reads `PIMVAULT_E2E_ACCESS_TOKEN` and `PIMVAULT_E2E_KEY_VAULT`; each stored refresh adds a
  secret version. The weekly run re-enables its own workflow so GitHub does not disable the
  schedule, and keeps one issue labelled `e2e-failure` open while it fails; it closes it when it
  passes. Dispatch the workflow with `scheduled` set to test that path.
- Re-sign-in when a test token fails (ADR 0019). The Google app is in production, so sign-ins do
  not expire weekly; sign a test account in again only when its stored token was revoked or the
  e2e run says it fails. It runs in a cloud session, with the Key Vault token the session-start
  hook exported:
  1. `go run ./script/live/e2elogin -in /tmp/e2e-redirect gmail >/tmp/e2e-login.log 2>&1 &`
     (or `outlook`; background; the same process keeps the PKCE state).
  2. Give the maintainer the URL from `/tmp/e2e-login.log`. They open it on a phone in a private
     tab, sign in as the test account, allow access and paste back the address the browser
     failed to load (`http://127.0.0.1:53682/?…` for Gmail, `http://localhost:53682/?…` for
     Outlook).
  3. `printf '%s\n' 'ADDRESS' >/tmp/e2e-redirect`, then read the log: it says it stored the
     tokens, or why not. It refuses any account other than the one the secret already holds. On
     first setup, when the secret holds no token yet, add `-account EMAIL` with the test
     account's address (from the maintainer; never committed).
  4. Dispatch the end-to-end workflow for `main` to confirm.
- Network hosts a cloud session needs: `proxy.golang.org`, `sum.golang.org`, `go.dev`,
  `dl.google.com`, `github.com` and Ubuntu's apt mirrors for setup; `login.microsoftonline.com`
  and `kv-pimvault-ci.vault.azure.net` for the Key Vault token; the Google and Microsoft API
  hosts for live probes.

## Maintainer setup

One-time steps only the maintainer can do; agents never handle secret values.

- `script/live/google-setup.sh` (Google Cloud Shell): enables the Google APIs the Google app
  uses.
- Azure: Key Vault `kv-pimvault-ci` with the test token secrets; the Entra ID app
  `gh-pimvault-e2e` (Key Vault Secrets Officer on that vault, one federated credential for this
  repository's immutable OIDC subject of the environment `e2e`) and the GitHub environment `e2e`
  holding its IDs as variables. No key or client secret exists for it; GitHub holds no secret.
- The cloud sessions' shared Entra ID app `cloud-sessions-reader` (its client secret is the cloud
  environment's `MELISMA_SECRETS_CLIENT_SECRET`) has Key Vault Secrets Officer on
  `kv-pimvault-ci`, and the cloud environment allows `login.microsoftonline.com` and
  `kv-pimvault-ci.vault.azure.net`.
- A re-sign-in, when a test token fails: open the URL the agent gives you in a private tab, sign
  in as the test account, paste back the failed address.

## Privacy review

Before every commit, read your own staged diff (`git diff --cached`) and remove anything private
about the maintainer, their family or their computers: people's names, addresses, personal email
addresses and accounts, hostnames, home-directory paths, IP addresses, tenant, client or vault
IDs, real personal documents or photos. Use neutral stand-ins in tests and examples. There is no
list of private terms to check against: judge the diff.

## Definition of Done

An increment is done when every line holds; the final report marks each ✅ or ❌ with evidence:

- [ ] Start stage clean: main CI, the last scheduled run and recent review comments were green or
      fixed first.
- [ ] Tests first; the regression test's red output before the fix is quoted.
- [ ] The verify command is green, and CI is green on the final commit.
- [ ] Traceability and doc checks (`script/process-check`, `script/pr-check`) are green.
- [ ] Docs and CLAUDE.md are updated in the same PR.
- [ ] Instrumentation reviewed on touched paths: what was added, what removed, or why it is
      adequate.
- [ ] Privacy review done on every commit's staged diff.
- [ ] Every review thread is answered or resolved.
- [ ] Merged; main CI green; the release it produced is linked.
- [ ] Clean up: only main and open PRs' branches remain on the remote; the working tree is clean.

### Final increment report

One PR comment after the merge, with these parts:

1. **Done / not done**: what the increment delivered, and anything left out.
2. **Definition of Ready**: as written, and whether it held.
3. **Requirements, ADRs and docs**: IDs added, changed or retired; ADRs; docs touched.
4. **Design decisions**: each with the alternatives considered and why they lost.
5. **Verification evidence**: commands, CI run links and durations, release link.
6. **Regression test**: name and red output before the fix (or "no bug fixed").
7. **Diagnosis**: for a bug, the root cause and how it was established.
8. **Instrumentation**: added, removed, and gaps still open.
9. **Performance**: before and after numbers, or "not touched".
10. **Privacy review**: done on every commit; anything removed.
11. **Smells**: what looked wrong but was out of scope.
12. **Follow-ups**: each filed as an issue, linked.
13. **Definition of Done**: the checklist above, each line ✅ or ❌ with evidence.
