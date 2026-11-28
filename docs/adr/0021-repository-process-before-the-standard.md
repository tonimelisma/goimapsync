# 0021. The repository's own process, before the shared standard

- Status: superseded by [0022](0022-shared-process-standard.md)
- Date: 2026-11-27 (recorded from the decision log; decided earlier)
- Aliases: D92, D100, D101, D102, D103, D104, D105, D106, D107, D108, D118, D119, D130, D139

## Context and problem

pimvault's repository and process setup was decided once the product requirements were complete,
with the sister repositories Otos and onedrive-go as the sources.

## Decision drivers

- A light process for a single-binary project worked by agents.
- Decisions recorded where agents read them.

## Considered options

### Copy Otos' and onedrive-go's processes whole

- Good: proven.
- Bad: heavier than a small CLI needs.
- Failure modes: rules nobody follows.

### A lighter, adapted copy

- Good: only what applies.
- Bad: diverges from the sister repositories.
- Failure modes: each repository drifts its own way (what the shared standard later fixed).

## Decision

- Docs (D100): a lighter Otos layout: `docs/requirements.md` holding the decisions,
  `docs/research/` the facts, `docs/plans/` deleted when done; ADRs and `docs/architecture.md`
  once there was code and a real architectural decision to justify; `PRD.md` split into these.
- One local check script, `script/check.sh`: format, lint, build, race tests (D101). CI on GitHub
  Actions on every push and PR runs build, race tests, `go vet` and a linter (D92).
- `AGENTS.md` the real instruction file, `CLAUDE.md` a symlink to it, no `GEMINI.md` (D102).
  `.claude/settings.json` (hooks, such as the Go 1.27 setup hook) and `.claude/skills/` committed;
  only `.claude/settings.local.json` ignored, as cloud sessions clone fresh (D103).
- Principles copied into the instructions: blank slate and no backward compatibility; simplicity
  and Pike's rules; no guessing or fabricated facts; one user action, one implementation path; the
  account as the source of truth with archive and state as rebuildable projections;
  onedrive-go's engineering philosophy, anti-fizzbuzz guardrails and repository-ownership rules
  (D104). Only project-specific Go conventions: `verb noun: %w` wrapping, partial failure as a
  first-class result, atomic write with fsync, preserving fields you don't understand, mandatory
  pagination, no `time.Sleep` in tests (D105).
- Process (D106): increments; PR-only changes to `main`; a short task brief before coding
  (outcome, non-goals, verification, a data-safety note for anything touching live accounts); a
  fresh-context `/code-review` before each PR whose correctness and requirement findings are
  acted on; a red `main` blocks merges; a light path for doc-only changes; never deleting
  others' work. Done meant the check script green on the final tree with its output shown, docs
  updated, PR merged; the final report said what changed, what didn't and open risks. No branch
  prefixes or worktrees.
- Docs rules (D107): the maintainer's guidance captured in its doc in the same increment and
  non-trivial requirements restated for confirmation; dated research notes with sources; a
  live-incidents ledger once real-account tests existed (`docs/incidents.md`); no recent-PR
  review-comment sweep.
- Tooling copied and adapted (D108): `.golangci.yml` from onedrive-go; the CI workflow reduced
  to build, race tests, vet and lint; `dependabot.yml`; `.gitignore`; `.editorconfig`; a PR
  template from Otos without macOS items; `.env.example`; a test-credential bootstrap script.
- Decision rights: the maintainer decided anything user-visible, any data-safety behavior,
  package boundaries and dependencies, the agent everything else (D118); package and module layout
  then became the agent's (D139). The instructions said to research before asking and to ask
  decisions with options, facts and tradeoffs, the agent's recommendation first, and to show the
  check script's output from the final tree before saying work was done (D119).
- PRs squash-merged by the agent once CI was green, without asking: one commit per increment on
  `main` (D130).

## Consequences

Carried forward into ADR 0022: one check script (`script/check.sh`, run by `script/verify`), CI
on every push and PR, committed `.claude/settings.json`, the principles and Go conventions,
dated research and the incidents ledger, agent squash-merges on green CI, and the maintainer's
decision rights over product, data safety and dependencies. Replaced: decisions as requirement
rows, `docs/plans/`, `AGENTS.md` as the real file, the task brief and its PR template, the
pre-PR `/code-review` rule, the question-formatting rules and branches without prefixes.

## Confirmation

None now: superseded.

## Retirement proof

Shown by ADR 0022.

## Reversal plan

Not needed: superseded.
