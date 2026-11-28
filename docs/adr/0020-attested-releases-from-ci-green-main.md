# 0020. Every merge to main releases an attested build; VERSION names the series

- Status: accepted
- Date: 2026-11-27 (series from VERSION; the rest recorded from the decision log, decided earlier)
- Aliases: D187, D194, D196, D197, D198, D200

## Context and problem

pimvault ships as static Linux binaries that installers poll for and verify themselves. Releases
should not depend on anyone tagging or publishing by hand, cloud sessions cannot push tags, and a
consumer must be able to prove a binary came from this repository's workflow on `main`.

## Decision drivers

- No manual release step; nothing published that CI did not pass.
- Verifiable provenance without a long-lived signing key.
- A new minor or major series without editing the workflow.

## Considered options

### Releases dispatched by a cloud session with a version

- Good: explicit control.
- Bad: a manual step per release; the version is typed by hand.
- Failure modes: merged changes sit unreleased.

### Every green CI run of a push to main releases the next patch, series from VERSION

- Good: no manual step; the version is computed; a series change is a one-line PR.
- Bad: many small releases.
- Failure modes: a release fails after merge; the workflow's rerun finishes it.

## Decision

Releases are tagged versions with static Linux binaries (amd64, arm64) built by
`script/release.sh` with plain `go build`, published as GitHub Releases; `pimvault --version`
prints the version (D187). The `release.yml` workflow publishes them: it was first dispatched by
a cloud session with a version, refusing one not higher than every tag and only finishing an
existing tag's release on a rerun (D194). Now every merge to `main` publishes a release by itself,
with no approval step: when the CI run of the push to `main` succeeds, `release.yml` tags and
builds that commit if it is still `main`'s head (else the newer commit's run releases both), with
the next patch version. Only `release.yml` run on `main` tags, releases or attests; a manual run
is refused on any other ref, nothing runs on tag pushes, and cloud sessions never tag or release
by hand (D198, revising D194).

The version is the next patch of the series `VERSION` names (`0.1` → `v0.1.9` after `v0.1.8`; a
new series starts at `.0`), above every existing tag. The repository was deleted and recreated
from one commit before it went public, so personal email addresses in earlier history are not
published; that history, its pull requests and the v0.1.0–v0.1.2 releases are gone, and new
versions stay above v0.1.2 (D196).

Releases are built at arm's length and can be pulled by anyone: from the tag, with no cache and
no secret beyond `GITHUB_TOKEN`; reproducibly (the exact Go version from `go.mod`, `-trimpath`);
each carries a Sigstore-signed GitHub build provenance attestation, also attached as
`pimvault-vX.Y.Z.sigstore.json`. The repository knows nothing of who installs it: consumers poll
the latest release and verify it themselves (D197). The repository was renamed from
`tonimelisma/goimapsync` to `tonimelisma/pimvault` once automatic releases and keyless e2e worked
under the old name; releases since are signed as `tonimelisma/pimvault`, and consumers verify
with `--repo tonimelisma/pimvault --signer-workflow
tonimelisma/pimvault/.github/workflows/release.yml --source-ref refs/heads/main` (D200).

## Consequences

- `.github/workflows/release.yml`'s path and the `main` ref are part of the signing identity
  consumers verify; changing either needs every consumer's policy updated first, accepting both
  during the switch.
- A minor or major series starts by editing `VERSION` in a PR.

## Confirmation

The release workflow's last step, tagged `Validates: PV-270`, downloads each published binary and
`SHA256SUMS`, checks the sums and verifies the attestation's signer workflow and source ref, and
checks `releases/latest`.

## Retirement proof

A packaging channel (a distribution's repository) that replaces GitHub Releases for every
consumer.

## Reversal plan

Re-add a `workflow_dispatch`-only release; the build script and attestation stay.
