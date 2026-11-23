#!/bin/bash
# Prepares Claude Code cloud sessions (CLAUDE_CODE_REMOTE=true): a C compiler
# for race tests, the Go toolchain from go.mod and the pinned golangci-lint.
# Idempotent; a warm rerun takes seconds. Does nothing on other machines.
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

GOLANGCI_LINT_VERSION="v2.13.2"

cd "${CLAUDE_PROJECT_DIR:-$(dirname "$0")/../..}"

# go test -race needs cgo, so gcc; a fresh image may also lack curl and git.
missing=()
for pkg in gcc:gcc curl:curl git:git; do
  command -v "${pkg%%:*}" >/dev/null 2>&1 || missing+=("${pkg#*:}")
done
if [ "${#missing[@]}" -gt 0 ]; then
  sudo=""
  [ "$(id -u)" -eq 0 ] || sudo="sudo"
  ${sudo} apt-get update -qq
  DEBIAN_FRONTEND=noninteractive ${sudo} apt-get install -y -qq \
    "${missing[@]}" ca-certificates libc6-dev >/dev/null
fi

# Pin the exact release go.mod names (its go line is only a minimum), as CI
# does. A go command of 1.21 or newer fetches it from the module proxy;
# without one, install that release from go.dev into ~/.local/go.
want="go$(awk '/^toolchain /{print substr($2,3); f=1} /^go /{g=$2} END{if(!f) print g}' go.mod)"
if [ -x "${HOME}/.local/go/bin/go" ]; then
  export PATH="${HOME}/.local/go/bin:${PATH}"
fi
if ! command -v go >/dev/null 2>&1 || ! go version | grep -qE 'go1\.(2[1-9]|[3-9][0-9])'; then
  rm -rf "${HOME}/.local/go"
  mkdir -p "${HOME}/.local"
  curl -fsSL "https://go.dev/dl/${want}.linux-amd64.tar.gz" | tar -xz -C "${HOME}/.local"
  export PATH="${HOME}/.local/go/bin:${PATH}"
fi
export GOTOOLCHAIN="${want}"
go version
project_go="$(go env GOVERSION)"
if [ "${project_go}" != "${want}" ]; then
  echo "session-start: got ${project_go}, go.mod wants ${want}" >&2
  exit 1
fi

# golangci-lint must be built with the project's Go version, or it cannot
# type-check newer language features. `go install tool@version` would otherwise
# use the tool's own minimum Go version.
gobin="$(go env GOPATH)/bin"
if ! "${gobin}/golangci-lint" version 2>/dev/null | grep -q "version ${GOLANGCI_LINT_VERSION#v} built with ${project_go} "; then
  GOTOOLCHAIN="${project_go}" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"
fi
"${gobin}/golangci-lint" version

# Warm the module cache so the first check does not pay for it.
go mod download

if [ -n "${CLAUDE_ENV_FILE:-}" ]; then
  {
    echo "export GOTOOLCHAIN=${want}"
    echo "export PATH=\"${gobin}:$(dirname "$(command -v go)"):\$PATH\""
  } >> "${CLAUDE_ENV_FILE}"
fi
