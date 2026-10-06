#!/usr/bin/env bash
# Reproducible cross-build of the whatsapp-unofficial release binaries.
#
#   scripts/build-release.sh           build dist/* and update plugin/checksums.txt
#   scripts/build-release.sh --verify  rebuild dist/* and fail if the hashes differ
#                                      from the committed plugin/checksums.txt
#
# Version comes from plugin/VERSION. Reproducibility requires the same Go
# toolchain (go.mod `go`/`toolchain` lines) and the same module sources.
set -euo pipefail

NAME="whatsapp-unofficial"
MAIN_PKG="./cmd/whatsapp-unofficial"
TARGETS=(darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64)

root=$(cd "$(dirname "$0")/.." && pwd -P)
dist="$root/dist"
committed="$root/plugin/checksums.txt"

usage() {
	echo "usage: $0 [--verify]" >&2
	exit 2
}

mode=build
case "${1:-}" in
"") ;;
--verify) mode=verify ;;
-h | --help) usage ;;
*) usage ;;
esac
[ $# -le 1 ] || usage

die() {
	echo "build-release: error: $*" >&2
	exit 1
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$@"
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$@"
	else
		die "need sha256sum or shasum"
	fi
}

command -v go >/dev/null 2>&1 || die "go is not installed"
version=$(tr -d ' \t\r\n' <"$root/plugin/VERSION")
[[ $version =~ ^[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.-]+)?$ ]] ||
	die "plugin/VERSION '$version' is not a semantic version"
[ -d "$root/${MAIN_PKG#./}" ] || die "main package $MAIN_PKG not found"

cd "$root"

# Use exactly the toolchain go.mod names (toolchain line, else go line) so a
# newer local Go does not produce different bytes than CI.
[ -f go.mod ] || die "go.mod not found"
gotoolchain=$(awk '$1 == "toolchain" { print $2; exit }' go.mod)
if [ -z "$gotoolchain" ]; then
	gotoolchain=$(awk '$1 == "go" { print "go" $2; exit }' go.mod)
fi
[[ $gotoolchain =~ ^go[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || die "cannot determine Go toolchain from go.mod"
# "go 1.N" means release go1.N.0 for Go >= 1.21.
[[ $gotoolchain =~ ^go[0-9]+\.[0-9]+$ ]] && gotoolchain="$gotoolchain.0"
export GOTOOLCHAIN=$gotoolchain
echo "build-release: $NAME v$version with $(go version)" >&2

rm -rf "$dist"
mkdir -p "$dist"
printf '*\n' >"$dist/.gitignore"

# Pin everything that changes code generation so local and CI builds match.
export CGO_ENABLED=0 GOAMD64=v1 GOARM64=v8.0
ldflags="-s -w -buildid= -X main.version=$version"

for target in "${TARGETS[@]}"; do
	goos=${target%/*}
	goarch=${target#*/}
	out="${NAME}_${goos}_${goarch}"
	[ "$goos" = windows ] && out="$out.exe"
	echo "build-release: building $out" >&2
	GOOS=$goos GOARCH=$goarch go build -trimpath -buildvcs=false \
		-ldflags "$ldflags" -o "$dist/$out" "$MAIN_PKG"
done

(
	cd "$dist"
	assets=()
	for f in "${NAME}"_*; do assets+=("$f"); done
	sha256 "${assets[@]}" | LC_ALL=C sort -k2
) >"$dist/checksums.txt"

cat "$dist/checksums.txt" >&2

if [ "$mode" = verify ]; then
	[ -f "$committed" ] || die "plugin/checksums.txt is missing"
	if ! diff -u "$committed" "$dist/checksums.txt" >&2; then
		die "rebuilt binaries do not match plugin/checksums.txt (see diff above). Rebuild with the go.mod toolchain and commit the updated checksums."
	fi
	echo "build-release: verified, hashes match plugin/checksums.txt" >&2
else
	cp "$dist/checksums.txt" "$committed"
	echo "build-release: wrote plugin/checksums.txt; commit it together with plugin/VERSION" >&2
fi
