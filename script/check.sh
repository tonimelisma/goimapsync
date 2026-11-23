#!/usr/bin/env bash
# Single check for local work and CI: tidy, format, lint, build, vet and race
# tests.
set -euo pipefail

cd "$(dirname "$0")/.."

go mod tidy -diff
golangci-lint fmt --diff ./...
golangci-lint run ./...
go build ./...
go vet ./...
# The end-to-end tests need real accounts; compiling them keeps them current.
go vet -tags e2e ./internal/e2e/
go test -race ./...

echo "check: ok"
