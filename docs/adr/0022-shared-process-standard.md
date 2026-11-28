# 0022. The shared process standard

- Status: accepted
- Date: 2026-11-27
- Aliases: D92, D101, D103, D104, D105, D107, D130, D139

## Context and problem

pimvault's process (ADR 0021) was its own: decisions as rows in `docs/requirements.md`, plans as
files, a task brief per PR, `AGENTS.md` as the real instruction file and no required check on
`main` (CI was described as a required gate, but no rule enforced it). The maintainer's
repositories now share one process standard.

## Decision drivers

- Requirements traceable to the tests that prove them; decisions as ADRs.
- One required check, enforced by a ruleset, and the same checkpoints in every repository.
- Agents land their own PRs on green CI.

## Considered options

### Keep the repository's own process

- Good: no migration.
- Bad: no traceability from behavior to tests; nothing enforces the gate.
- Failure modes: untested requirements and red merges go unnoticed.

### Adopt the shared standard, keeping the decision log as aliases

- Good: requirements, tests and docs checked in CI; one `CI gate`; old D-numbers in code, tests
  and PRs keep resolving.
- Bad: a one-time migration of about 200 decision rows.
- Failure modes: an alias cited but not declared fails `script/process-check`.

## Decision

- `CLAUDE.md` is the instruction file, `AGENTS.md` a symlink to it.
- Behavior is in `docs/requirements.md` as `PV-NNN` EARS requirements, each `[verified]` by a test
  tagged `Validates: PV-NNN` or `unverified:` with the reason; decisions about how pimvault is
  built are ADRs. Each former decision row (D1–D203) and open decision (O31, O138, O139, all
  decided) is an alias on the requirement or ADR that carries it; `script/process-check` checks
  that every alias cited in code, tests and docs is declared. Plans live in GitHub tracking
  issues.
- Carried forward from ADR 0021: one check script (`script/check.sh`, now run by `script/verify`
  with `script/process-check`) (D101); CI on every push and PR runs it (D92); `.claude/settings.json`
  with the session-start hook committed (D103); the principles (D104) and project-specific Go
  conventions (D105) in `CLAUDE.md`; dated research and the incidents ledger (D107); agents
  squash-merge their own PRs once `CI gate` is green (D130); package layout is the agent's
  decision, while user-visible behavior, data safety and new dependencies are the maintainer's
  (D139).
- CI has the `process` job and the `CI gate` job that needs every other job; a ruleset on `main`
  requires `CI gate`, squash merges only, no force-push or deletion, no bypass. A weekly run
  re-checks `main`. Code PRs also wait for their `e2e` status (ADR 0018).

## Consequences

- PR bodies follow `.github/pull_request_template.md`, checked by `script/pr-check`.
- `script/stage start | pre-merge | post-merge` are the increment's checkpoints.

## Confirmation

`script/process-check` and `script/pr-check` in the `process` job; the ruleset's required check.

## Retirement proof

A replacement standard adopted across the maintainer's repositories.

## Reversal plan

Remove the `process` job and the ruleset; the docs stay readable as they are.
