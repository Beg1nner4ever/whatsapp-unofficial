#!/usr/bin/env bash
# Checks that every plugin/marketplace manifest parses and that all versions
# agree with plugin/VERSION. Requires jq.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd -P)
cd "$root"
command -v jq >/dev/null 2>&1 || {
	echo "check-manifests: jq is required" >&2
	exit 1
}

version=$(tr -d ' \t\r\n' <plugin/VERSION)
status=0

expect() { # expect <file> <jq filter> <value>
	local got
	if ! got=$(jq -er "$2" "$1" 2>/dev/null); then
		echo "FAIL $1: cannot read $2" >&2
		status=1
	elif [ "$got" != "$3" ]; then
		echo "FAIL $1: $2 is '$got', expected '$3'" >&2
		status=1
	else
		echo "ok   $1: $2 = $got"
	fi
}

for f in plugin/.claude-plugin/plugin.json plugin/plugin.json plugin/mcp.json \
	.claude-plugin/marketplace.json .agents/plugins/marketplace.json; do
	jq empty "$f" || {
		echo "FAIL $f: invalid JSON" >&2
		status=1
	}
done

expect plugin/.claude-plugin/plugin.json .version "$version"
expect plugin/plugin.json .version "$version"
expect .claude-plugin/marketplace.json '.plugins[] | select(.name == "whatsapp-unofficial") | .version' "$version"
expect plugin/.claude-plugin/plugin.json .name whatsapp-unofficial
expect plugin/plugin.json .name whatsapp-unofficial

# Codex must not see Claude-only variables, and the plugin must not ship a
# Claude-style .mcp.json that Codex could pick up.
if grep -q 'CLAUDE_' plugin/mcp.json plugin/plugin.json; then
	echo "FAIL plugin/mcp.json or plugin/plugin.json references a CLAUDE_ variable" >&2
	status=1
fi
if [ -e plugin/.mcp.json ]; then
	echo "FAIL plugin/.mcp.json must not exist (MCP server is inline in plugin.json)" >&2
	status=1
fi

exit "$status"
