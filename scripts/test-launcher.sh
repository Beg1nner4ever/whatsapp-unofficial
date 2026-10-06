#!/usr/bin/env bash
# Tests for plugin/scripts/launch using a temp plugin root, a file:// download
# base and fake release assets (shell scripts standing in for the Go binary).
# A fake `uname` on PATH pins the platform so results are host-independent.
# Set LAUNCHER_SHELL=/bin/dash (etc.) to run the launcher under another shell.
set -uo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd -P)
launcher_src="$repo/plugin/scripts/launch"
version="9.8.7"

work=$(mktemp -d "${TMPDIR:-/tmp}/wau-launcher-test.XXXXXX")
trap 'rm -rf "$work"' EXIT

pass=0
fail=0
ok() {
	pass=$((pass + 1))
	echo "ok   - $1"
}
not_ok() {
	fail=$((fail + 1))
	echo "FAIL - $1"
	[ -n "${2:-}" ] && printf '       %s\n' "$2"
}
check() { # check <description> <command...>
	local desc=$1
	shift
	if "$@"; then ok "$desc"; else not_ok "$desc"; fi
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		shasum -a 256 "$1" | awk '{print $1}'
	fi
}

# fake_uname <os> <arch>: create a bin dir whose `uname` reports the platform.
fake_uname() {
	local dir="$work/uname-$1-$2"
	mkdir -p "$dir"
	cat >"$dir/uname" <<EOF
#!/bin/sh
case "\${1:-}" in
-s) echo "$1" ;;
-m) echo "$2" ;;
*) echo "$1" ;;
esac
EOF
	chmod +x "$dir/uname"
	echo "$dir"
}

# new_plugin <name>: fresh plugin root with launcher, VERSION and an empty
# checksums file. Prints its path.
new_plugin() {
	local root="$work/$1/plugin"
	mkdir -p "$root/scripts"
	if [ -n "${LAUNCHER_SHELL:-}" ]; then
		{ echo "#!$LAUNCHER_SHELL"; tail -n +2 "$launcher_src"; } >"$root/scripts/launch"
	else
		cp "$launcher_src" "$root/scripts/launch"
	fi
	chmod +x "$root/scripts/launch"
	printf '%s\n' "$version" >"$root/VERSION"
	: >"$root/checksums.txt"
	echo "$root"
}

releases="$work/releases"
mkdir -p "$releases"

# Fake binary: prints a marker and each argument in brackets.
make_asset() {
	local name=$1
	cat >"$releases/$name" <<'EOF'
#!/bin/sh
echo "FAKE-BINARY"
for a in "$@"; do printf '[%s]\n' "$a"; done
EOF
	# Deliberately not executable: the launcher must set the mode itself.
	chmod 0644 "$releases/$name"
}

asset=whatsapp-unofficial_linux_amd64
make_asset "$asset"
good_hash=$(sha256 "$releases/$asset")
linux_path="$(fake_uname Linux x86_64):$PATH"
base="file://$releases"

run_launcher() { # run_launcher <plugin_root> <data_dir> [args...]; sets out/err/rc
	local root=$1 data=$2
	shift 2
	out=$(PATH="$linux_path" CLAUDE_PLUGIN_DATA="$data" \
		WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE="$base" \
		"$root/scripts/launch" "$@" 2>"$work/stderr")
	rc=$?
	err=$(cat "$work/stderr")
}

# --- 1. success path ---------------------------------------------------------
root=$(new_plugin success)
printf '%s  %s\n' "$good_hash" "$asset" >"$root/checksums.txt"
data="$work/success/data"
run_launcher "$root" "$data" serve
cached="$data/bin/$version/$asset"
check "success: exit 0" test "$rc" -eq 0
check "success: stdout is only the binary's output" test "$out" = "$(printf 'FAKE-BINARY\n[serve]')"
check "success: download note on stderr" grep -q "downloading $asset" "$work/stderr"
check "success: binary cached" test -f "$cached"
check "success: cached binary executable" test -x "$cached"
check "success: no temp files left" test -z "$(find "$data" -name '.*' -type f)"

# Second run uses the cache even if the download source disappears.
mv "$releases/$asset" "$releases/$asset.away"
run_launcher "$root" "$data" status
check "cache: second run works offline" test "$rc" -eq 0 -a "$out" = "$(printf 'FAKE-BINARY\n[status]')"
check "cache: second run silent on stderr" test -z "$err"
mv "$releases/$asset.away" "$releases/$asset"

# --- 2. args pass-through ----------------------------------------------------
# shellcheck disable=SC2016 # literal $HOME must reach the binary unexpanded
run_launcher "$root" "$data" login --phone "+1 555 0100" "" '*' '$HOME' "a'b\"c"
# shellcheck disable=SC2016
expected=$(printf '%s\n' 'FAKE-BINARY' '[login]' '[--phone]' '[+1 555 0100]' '[]' '[*]' '[$HOME]' "[a'b\"c]")
check "args: passed through verbatim" test "$out" = "$expected"

# --- 3. checksum mismatch refusal --------------------------------------------
root=$(new_plugin mismatch)
printf '%s  %s\n' "$(printf '0%.0s' $(seq 1 64))" "$asset" >"$root/checksums.txt"
data="$work/mismatch/data"
run_launcher "$root" "$data" serve
check "mismatch: non-zero exit" test "$rc" -ne 0
check "mismatch: stdout empty" test -z "$out"
check "mismatch: clear error" grep -q "checksum mismatch for $asset" "$work/stderr"
check "mismatch: binary not cached" test ! -e "$data/bin/$version/$asset"
check "mismatch: no leftover files" test -z "$(find "$data" -type f)"
check "mismatch: nothing executable left" test -z "$(find "$data" -type f -perm -u+x)"

# A tampered cached binary is detected and replaced by a verified download.
root=$(new_plugin tamper)
printf '%s  %s\n' "$good_hash" "$asset" >"$root/checksums.txt"
data="$work/tamper/data"
mkdir -p "$data/bin/$version"
printf '#!/bin/sh\necho EVIL\n' >"$data/bin/$version/$asset"
chmod 0700 "$data/bin/$version/$asset"
run_launcher "$root" "$data" serve
check "tamper: cached file replaced and verified" test "$rc" -eq 0 -a "$out" = "$(printf 'FAKE-BINARY\n[serve]')"
check "tamper: cached content now matches checksum" test "$(sha256 "$data/bin/$version/$asset")" = "$good_hash"

# --- 4. WHATSAPP_UNOFFICIAL_BIN override -------------------------------------
root=$(new_plugin override)
override="$work/override/custom-bin"
make_asset custom-bin
cp "$releases/custom-bin" "$override"
chmod +x "$override"
out=$(PATH="$linux_path" CLAUDE_PLUGIN_DATA="$work/override/data" \
	WHATSAPP_UNOFFICIAL_BIN="$override" WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE="http://invalid" \
	"$root/scripts/launch" serve "x y" 2>"$work/stderr")
rc=$?
check "override: runs the given binary with args" test "$rc" -eq 0 -a "$out" = "$(printf 'FAKE-BINARY\n[serve]\n[x y]')"
check "override: no download or cache" test ! -e "$work/override/data"

# --- 5. unsupported platform -------------------------------------------------
root=$(new_plugin unsupported)
for plat in "FreeBSD amd64" "Linux i686" "Linux armv7l"; do
	read -r p_os p_arch <<<"$plat"
	out=$(PATH="$(fake_uname "$p_os" "$p_arch"):$PATH" CLAUDE_PLUGIN_DATA="$work/unsupported/data" \
		"$root/scripts/launch" serve 2>"$work/stderr")
	rc=$?
	check "unsupported $p_os/$p_arch: non-zero exit, empty stdout" test "$rc" -ne 0 -a -z "$out"
	check "unsupported $p_os/$p_arch: clear message" grep -q "unsupported platform: $p_os/$p_arch" "$work/stderr"
done

# --- 6. other refusals -------------------------------------------------------
root=$(new_plugin insecure)
printf '%s  %s\n' "$good_hash" "$asset" >"$root/checksums.txt"
out=$(PATH="$linux_path" CLAUDE_PLUGIN_DATA="$work/insecure/data" \
	WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE="http://example.com/x" \
	"$root/scripts/launch" serve 2>"$work/stderr")
rc=$?
check "insecure base: refused" test "$rc" -ne 0 -a -z "$out"
check "insecure base: clear message" grep -q "only https:// URLs are allowed" "$work/stderr"

root=$(new_plugin nochecksum)
run_launcher "$root" "$work/nochecksum/data" serve
check "missing checksum entry: refused" test "$rc" -ne 0 -a -z "$out"
check "missing checksum entry: clear message" grep -q "no checksum for $asset" "$work/stderr"

root=$(new_plugin missingasset)
printf '%s  %s\n' "$good_hash" "$asset" >"$root/checksums.txt"
WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE_SAVE=$base
base="file://$work/does-not-exist"
run_launcher "$root" "$work/missingasset/data" serve
base=$WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE_SAVE
check "download failure: refused with message" test "$rc" -ne 0 -a -z "$out"
check "download failure: no leftover files" test -z "$(find "$work/missingasset/data" -type f 2>/dev/null)"

# --- 7. concurrent first runs ------------------------------------------------
root=$(new_plugin concurrent)
printf '%s  %s\n' "$good_hash" "$asset" >"$root/checksums.txt"
data="$work/concurrent/data"
pids=()
for i in 1 2 3 4 5 6 7 8; do
	PATH="$linux_path" CLAUDE_PLUGIN_DATA="$data" WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE="$base" \
		"$root/scripts/launch" serve "$i" >"$work/concurrent/out.$i" 2>/dev/null &
	pids+=($!)
done
concurrent_ok=1
for i in "${!pids[@]}"; do
	wait "${pids[$i]}" || concurrent_ok=0
	n=$((i + 1))
	[ "$(cat "$work/concurrent/out.$n")" = "$(printf 'FAKE-BINARY\n[serve]\n[%s]' "$n")" ] || concurrent_ok=0
done
check "concurrent: all 8 runs succeed with correct output" test "$concurrent_ok" -eq 1
check "concurrent: exactly one cached file, no temp files" test "$(find "$data" -type f | wc -l | tr -d ' ')" -eq 1
check "concurrent: cached binary verified" test "$(sha256 "$data/bin/$version/$asset")" = "$good_hash"

echo
echo "launcher tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
