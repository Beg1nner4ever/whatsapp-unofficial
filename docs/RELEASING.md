# Releasing

The plugin pins one binary version (`plugin/VERSION`) and its SHA-256 hashes
(`plugin/checksums.txt`). The launcher refuses any binary whose hash is not in
that file, so the committed checksums must equal what CI builds.

1. Bump the version in `plugin/VERSION`, `plugin/.claude-plugin/plugin.json`,
   `plugin/plugin.json` and `.claude-plugin/marketplace.json`
   (`scripts/check-manifests.sh` checks they agree).
2. Run `scripts/build-release.sh`. It uses the exact Go toolchain from
   `go.mod`, builds the five binaries into `dist/` and rewrites
   `plugin/checksums.txt`.
3. Commit `plugin/VERSION`, `plugin/checksums.txt` and the manifests, push,
   and wait for CI (it runs `build-release.sh --verify` when these files
   change).
4. Tag and push: `git tag v$(cat plugin/VERSION) && git push origin v$(cat plugin/VERSION)`.
5. The release workflow checks that the tag matches `plugin/VERSION`, rebuilds
   with `--verify` (hashes must match the commit), and publishes the release
   with the five binaries and `checksums.txt`.

Users installing from the marketplace between step 3 and the end of step 5
cannot download the binary yet, so tag right after the version commit lands.
