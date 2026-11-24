# pimvault agent instructions

pimvault is a headless Go CLI for Linux that backs up Gmail and Outlook.com
mail, calendars, contacts and tasks to a local folder in standard formats
(`.eml`, `.ics`, `.vcf`), and restores or migrates accounts from that folder.

- `docs/requirements.md`: the owner's decisions (D-rows) and open decisions (O-rows). Source of truth for scope.
- `docs/research/`: dated research notes with sources. Facts, not decisions.
  `docs/research/api-quirks.md` indexes every provider quirk by API.
- `docs/incidents.md`: live incidents (data lost, duplicated or misreported, or
  a wrong account touched), with cause, fix and prevention.
- `docs/plans/`: active work plans, numbered. Delete a plan when its work is done.

## Working with the owner

- When the owner asks a question, answer it in the first sentence. No
  options, no follow-up questions, no tool calls or edits unless asked.
- The owner's latest words override this file. When they conflict, follow
  the owner and update this file in the same change.
- If unsure whether something is the owner's decision, it isn't: decide it
  and report it.
- Owner decisions for a feature are collected in one batch before the work
  starts. During development, ask only when reality contradicts a recorded
  decision.
- When the owner objects or interrupts, stop. Answer or explain, then wait
  for them to say continue.

## Decisions

- The owner decides anything user-visible, any data-safety behavior and
  third-party dependencies. Everything else is yours: decide it and report it
  in the PR.
- Package and module layout is yours and grows with the code: one module,
  few packages, split one out when the code shows a real boundary. Never ask
  the owner about it.
- Research before asking. Then ask decisions with the AskUserQuestion tool:
  options with facts and tradeoffs, your recommendation first and marked
  "(Recommended)" with the reason, one decision per question. Say what is
  verified and what is not.
- `docs/requirements.md` holds only decisions the owner explicitly made.
  Capture new owner guidance there (or in research/plans) in the same change,
  and restate non-trivial requirements to the owner for confirmation.
- Stay within the documented scope. Do not add features, topics or non-goals
  the owner has not asked for.

## Principles

- Blank slate: zero users, no backward compatibility for config, state, flags,
  output or internal APIs. No compatibility shims.
- Simplicity is a requirement. Measure before optimizing; simple algorithms and
  data structures while n is small; get the data model right first.
- Do not guess: verify with code, docs, tests or runtime evidence. Unknown
  counts, dates and progress stay unknown in output until proven.
- One user action has one implementation path.
- The account is the source of truth. The archive and the tool's state are
  rebuildable projections; losing state costs a full resync, never data.
- Every persisted fact and side effect has one owner. Deterministic decision
  logic in the core, I/O at the edges. No type, package or layer without a real
  boundary to own; no ghost references to deleted code.
- You own the repo: never leave it broken, never call a problem
  "pre-existing", fix what you find.

## Go conventions

Standard Go practice and anything golangci-lint enforces are not repeated here.

- Wrap errors as `fmt.Errorf("verb noun: %w", err)`, once per boundary.
- Bulk operations return results and errors together; never stop at the first
  failed item.
- Write files to a temp file in the target directory, `fsync`, then rename.
- Preserve provider fields you don't model; write back only fields you manage.
- Always follow pagination (`nextPageToken`, `@odata.nextLink`).
- No `time.Sleep` in tests; use explicit synchronization or injected clocks.

## Layout and commands

- One Go module: `cmd/pimvault/` and `internal/<package>/`.
- `script/check.sh` is the single check: tidy, format, lint, build, vet, race
  tests.
- Go 1.27 comes from the go line in `go.mod`.
- Releases (D187, D194, D198): every merge to `main` publishes one by
  itself. When the CI run of a push to `main` succeeds, `release.yml` (run
  on `main` only) checks the tested commit if it is still `main`'s head
  (else the newer commit's run releases both), tags it with the next patch
  version (above every tag and v0.1.2, D196), runs `script/release.sh` and
  uploads the binaries, `SHA256SUMS` and a signed build provenance
  attestation (D197). Builds are reproducible and never use a cache; the
  repo knows nothing of who installs it. Cloud sessions never tag or release
  by hand. A rerun for a tagged commit only finishes that release; a manual
  `workflow_dispatch` on `main` (optional version) is the fallback. Read the
  outcome with `gh api repos/tonimelisma/pimvault/releases/latest`.

## Cloud sessions

All work happens in Claude Code cloud sessions; nothing in this repo needs
another machine. The session's GitHub proxy cannot push tags, create releases or
reach IMAP, so those run in GitHub Actions: releases after a successful CI
run on `main`, e2e on pushes, pull requests or a `repository_dispatch`
(`gh api` REST works; GraphQL, so `gh pr create`, does not).

- `.claude/hooks/session-start.sh` runs at session start and does nothing
  unless `CLAUDE_CODE_REMOTE=true`. It installs gcc, curl and git if missing
  (race tests need cgo), the Go release from `go.mod` (through the module
  proxy, or from go.dev when no Go 1.21+ is present), golangci-lint built with
  that Go, and the module cache. Cold: about a minute on a 4-core cloud
  container; warm: under a second.
- Full check: `script/check.sh`. Validate workflow changes with `actionlint`
  (`go install github.com/rhysd/actionlint/cmd/actionlint@v1.7.12`).
- Closing the loop needs no Docker: the unit tests drive the real code against
  in-process fakes (`internal/outlookimap/outlookimaptest` is an IMAP server
  with Outlook.com's folders and XOAUTH2). Live probes over HTTPS (Gmail,
  Graph) run here: `go run ./script/live/tokens -out DIR`, then
  `go build -o /tmp/pimvault ./cmd/pimvault` and run it with
  `XDG_CONFIG_HOME=DIR`. IMAP (port 993) is refused here; anything needing it
  runs through the end-to-end workflow.
- End-to-end test (D193, D199): `.github/workflows/e2e.yml` runs
  `go test -tags e2e -timeout 40m ./internal/e2e/` only in GitHub Actions,
  never on a developer machine or the owner's server. It runs on every push
  to `main`, on every pull request from a branch of this repository (never
  forks), weekly, and on dispatch. A branch gets e2e by opening its PR; each
  push to the PR runs it again. Dependabot's PRs get no Google token, so
  they skip it; dispatch it for their branch. To rerun it for a branch, e.g.
  after an `error`: `gh api repos/tonimelisma/pimvault/dispatches -f event_type=e2e -f 'client_payload[ref]=BRANCH'`.
  The result is the `e2e` commit status of the branch's head, the merge
  check for code changes:
  `gh api repos/tonimelisma/pimvault/commits/SHA/status --jq '.statuses[]|select(.context=="e2e")|.state'`
  (pending, then success or failure; its `target_url` is the run). One run
  at a time uses the test accounts; GitHub keeps only the newest waiting
  run, and a dropped one ends as `error`: dispatch again. Only commits on
  this repository's branches are tested. Logs:
  `gh api repos/tonimelisma/pimvault/actions/runs/RUN/jobs`, then
  `.../actions/jobs/JOB/logs`. The log is public; the workflow masks the
  test accounts' emails and stored tokens (`script/live/tokens -mask`), and
  nothing else it prints may name an account or a token. Google
  credentials are keyless: Workload Identity Federation (pool `github`,
  provider `goimapsync`, restricted to this repository's and owner's IDs)
  gives the run a
  one-hour token for the `pimvault-e2e` service account. The weekly run
  re-enables its own workflow so GitHub does not disable the schedule, and
  keeps one issue labelled `e2e-failure` open while it fails; it closes it
  when it passes. Dispatch the workflow with `scheduled` set to test that
  path.
- Weekly test sign-in (D159, D195), for `gmail` (and `outlook` when its
  sign-in fails):
  1. `go run ./script/live/e2elogin -in /tmp/e2e-redirect gmail >/tmp/e2e-login.log 2>&1 &`
     (background; the same process keeps the PKCE state).
  2. Give the owner the URL from `/tmp/e2e-login.log`. The owner opens it on
     the phone in a private tab, signs in as the test account, allows access
     and pastes back the address the browser failed to load
     (`http://127.0.0.1:53682/?…` for Gmail, `http://localhost:53682/?…` for
     Outlook).
  3. `printf '%s\n' 'ADDRESS' >/tmp/e2e-redirect`, then read the log: it
     says it stored the tokens, or why not. It refuses any account other than
     the one the secret already holds. On first setup, when the secret holds
     no token yet, add `-account EMAIL` with the test account's address
     (from the owner; never committed).
  4. Dispatch the end-to-end workflow for `main` to confirm.
- Network hosts a cloud session needs: `proxy.golang.org`,
  `sum.golang.org`, `go.dev`, `dl.google.com`, `github.com` and Ubuntu's apt
  mirrors for setup; for live probes and sign-ins also
  `oauth2.googleapis.com`, `secretmanager.googleapis.com`,
  `www.googleapis.com`, `gmail.googleapis.com`, `people.googleapis.com`,
  `tasks.googleapis.com`, `openidconnect.googleapis.com`, `login.microsoftonline.com`
  and `graph.microsoft.com`.

## Owner setup

One-time steps only the owner can do; agents never handle secret values.

- `script/live/google-setup.sh` (Google Cloud Shell): the Secret Manager
  secrets, the `pimvault-e2e` service account, the Workload Identity pool
  the e2e workflow signs in with, and a service account key.
- That key only as the "goimapsync" cloud environment's variable
  `PIMVAULT_E2E_SERVICE_ACCOUNT`, for the weekly sign-in (`e2elogin`) and
  live probes; Workload Identity works only inside Actions, so the key
  stays. The code reads the key itself, so it cannot be a proxy-injected
  secret. Plus the network hosts above. GitHub holds no Google secret.
- The weekly sign-in: open the URL the agent gives you in a private tab,
  sign in as the test account, paste back the failed address.

## Workflow

- Before coding, write a short task brief: outcome, non-goals, how it will be
  verified, and a data-safety note for anything that can touch a live account.
- Changes reach `main` through PRs (`gh api repos/tonimelisma/pimvault/pulls`).
  CI (`ci.yml`, `script/check.sh`) is a required gate: squash-merge your own
  PR (`gh api -X PUT .../pulls/N/merge -f merge_method=squash`) without
  asking once `script/check.sh` is green on its final tree here, CI is green
  on the PR, and, for code changes, the e2e status of its head is success.
  A red `main` blocks merges except the fix. Doc-only changes skip the
  build/test cycle. Merged branches are deleted automatically.
- Tests: unit tests with testify, fakes at I/O seams, failure injection and
  golden files. End-to-end tests against the real test accounts run in the
  e2e workflow before each PR that changes code merges (D179, D193, D199);
  tokens come from Secret Manager (D158).
  Test account names and credentials are never committed (`.env`,
  `.testdata/`), and account emails are redacted in shown output and masked
  in workflow logs.
- Provider behaviour: when a provider behaves differently from its docs or
  from what pimvault assumed, in a live run or a probe, record it in the same
  change: an F-row with the date, account type and evidence, a line in
  `docs/research/api-quirks.md`, a fake that reproduces it and a test. Probe
  undocumented behaviour live before relying on it (`go run
  ./script/live/tokens -out DIR` signs in as the test accounts).
- Incidents: when a live run loses, duplicates or misreports data, or touches
  the wrong account, add an entry to `docs/incidents.md` in the fix's PR.
- Before each PR, run `/code-review` on the diff in a fresh context. Fix
  findings that affect correctness or the requirements; the rest are optional.
- Done means: `script/check.sh` here and CI green on the final tree, for
  code changes the e2e status green on it, with their output shown, docs
  updated, PR merged. A green run followed by any edit proves nothing.
- Keep going from one increment to the next until the product is complete.
  Stop only for an owner decision or for access only the owner can give.
- Final report: what changed, what didn't, open risks, and any Owner setup
  still missing.
- Never delete worktrees, branches, stashes or files you didn't create.

## Writing

- Plain, direct language. No marketing tone.
- Keep answers and documents short; lead with the answer.
