#!/usr/bin/env bash
# Builds a release (D187): static Linux binaries for amd64 and arm64 from a
# clean checkout of a tag, with plain go build, plus SHA-256 sums, in dist/.
# Usage: script/release.sh v1.2.3
# The tag must exist and be checked out. The release workflow publishes the
# files in dist/ as the GitHub Release of that tag (D194).
#
# Builds are reproducible (D197): anyone running this on the same tag gets
# byte-identical binaries, as the Go toolchain is the exact version in go.mod
# and paths, caches and the machine leave no trace in the binary.
set -euo pipefail

cd "$(dirname "$0")/.."

version="${1:?usage: script/release.sh vMAJOR.MINOR.PATCH}"

if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	echo "release: version must look like v1.2.3, got $version" >&2
	exit 1
fi

if [[ -n "$(git status --porcelain)" ]]; then
	echo "release: the checkout has uncommitted changes" >&2
	exit 1
fi

if [[ "$(git rev-parse HEAD)" != "$(git rev-parse --verify --quiet "refs/tags/$version^{commit}")" ]]; then
	echo "release: check out tag $version first" >&2
	exit 1
fi

# The exact toolchain go.mod names, fetched through the module proxy if the
# installed Go differs.
GOTOOLCHAIN="go$(go mod edit -json | sed -n 's/^\t"Go": "\(.*\)",$/\1/p')"
export GOTOOLCHAIN

rm -rf dist
mkdir dist

for arch in amd64 arm64; do
	out="dist/pimvault-$version-linux-$arch"
	# Go records the checked-out tag as the version (pimvault --version).
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "-s -w" -o "$out" ./cmd/pimvault
done

(cd dist && sha256sum pimvault-* >SHA256SUMS)

echo "release: built $version in dist/"
ls -l dist
