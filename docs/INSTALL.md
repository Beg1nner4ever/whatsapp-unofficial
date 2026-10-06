# Installing whatsapp-unofficial

whatsapp-unofficial is an unofficial, community-built plugin. It is not
affiliated with WhatsApp or Meta. Read [TERMS.md](TERMS.md) first: unofficial
clients may violate WhatsApp's terms and your account could be restricted.

The plugin ships a small launcher. On first start it downloads the release
binary for your platform from this repository's GitHub Releases, verifies its
SHA-256 against `plugin/checksums.txt`, and caches it. Supported: macOS (Apple
Silicon, Intel), Linux (x86_64, arm64), Windows (x86_64).

## Claude Code

```sh
claude plugin marketplace add Beg1nner4ever/whatsapp-unofficial
claude plugin install whatsapp-unofficial@whatsapp-unofficial
```

Or inside Claude Code: `/plugin marketplace add Beg1nner4ever/whatsapp-unofficial`,
then `/plugin install whatsapp-unofficial@whatsapp-unofficial`.

Then ask Claude to "link my WhatsApp" - the bundled `setup` skill walks through
pairing.

**Enable sending (optional):** `/plugin` > whatsapp-unofficial > **Configure
options** > **Allow sending messages**, or at install time
`claude plugin install whatsapp-unofficial@whatsapp-unofficial --config allow_send=true`
(newer Claude Code versions also offer `claude plugin configure`). Restart the
session afterwards. Turning it off again removes the send tools.

## Codex

Requires Codex CLI **0.147.0 or newer** (the first release that reads the
portable `plugin.json` / `mcp.json` format). Older versions fall back to the
Claude Code manifest and cannot start the server.

```sh
codex plugin marketplace add Beg1nner4ever/whatsapp-unofficial
codex plugin add whatsapp-unofficial@whatsapp-unofficial
```

(or install it from `/plugins` inside Codex).

### Enable sending in Codex (optional)

Codex plugins have no user settings, and Codex does not let you set environment
variables for a plugin's MCP server. To enable sending, disable the plugin's
read-only server and add your own server entry that runs the same launcher with
sending turned on, in `~/.codex/config.toml`:

```toml
[plugins."whatsapp-unofficial@whatsapp-unofficial".mcp_servers.whatsapp-unofficial]
enabled = false

[mcp_servers.whatsapp-unofficial-send]
# Path of the installed plugin; `codex plugin add` prints it ("Installed plugin root").
command = "/Users/YOU/.codex/plugins/cache/whatsapp-unofficial/whatsapp-unofficial/0.1.0/scripts/launch"
args = ["serve"]
env = { WHATSAPP_UNOFFICIAL_ALLOW_SEND = "1" }
```

The installed path contains the version, so update `command` after upgrading
the plugin (or point it at `plugin/scripts/launch` in a clone of this
repository). To turn sending off again, delete both blocks.

## Manual / other MCP clients

Download the binary for your platform from the Releases page, check it with
`sha256sum -c checksums.txt --ignore-missing` (macOS: `shasum -a 256 -c`), then
configure your client to run `whatsapp-unofficial serve` over stdio.
Link the account with `whatsapp-unofficial login` (QR code) or
`whatsapp-unofficial login --phone 447700900123` (pairing code).

## Environment variables

| Variable | Purpose |
| --- | --- |
| `WHATSAPP_UNOFFICIAL_HOME` | Data directory (default: OS config dir + `/whatsapp-unofficial`) |
| `WHATSAPP_UNOFFICIAL_ALLOW_SEND` | `1`/`true` enables the send tools |
| `WHATSAPP_UNOFFICIAL_IDLE_MINUTES` | Background daemon idle shutdown (default 15) |
| `WHATSAPP_UNOFFICIAL_BIN` | Launcher: run this binary instead of downloading one |
| `WHATSAPP_UNOFFICIAL_DOWNLOAD_BASE` | Launcher: alternative https download base (for mirrors) |

## Uninstall

Run `<plugin-dir>/scripts/launch logout --wipe` first to unlink the device and
delete local data, then uninstall the plugin from Claude Code or Codex.
