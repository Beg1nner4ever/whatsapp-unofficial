# whatsapp-unofficial

Let Claude Code and Codex read your WhatsApp messages, and send them only if you opt in.

> **Unofficial.** This project is not affiliated with, endorsed by, or connected to WhatsApp or Meta. It links to your account as a "linked device" through the open-source [whatsmeow](https://github.com/tulir/whatsmeow) library. Using unofficial clients may violate WhatsApp's Terms of Service and could get your number banned. Use at your own risk; see [TERMS](docs/TERMS.md) and [PRIVACY](docs/PRIVACY.md).

It started as a security-hardened fork of [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp) and is now a single Go binary packaged as a plugin.

## Install

### Claude Code

```
/plugin marketplace add Beg1nner4ever/whatsapp-unofficial
/plugin install whatsapp-unofficial@whatsapp-unofficial
```

Then ask Claude: *"Set up WhatsApp."* It will ask for your phone number and give you an 8-character code. On your phone, open **WhatsApp > Linked devices > Link a device > Link with phone number instead** and enter the code.

Sending is off by default. To enable it, turn on `allow_send` in the plugin's configuration (`/plugin` > whatsapp-unofficial > Configure).

### Codex

```bash
codex plugin marketplace add Beg1nner4ever/whatsapp-unofficial
```

Then run `/plugins` in Codex, install **whatsapp-unofficial**, and ask Codex to set up WhatsApp the same way. Requires Codex 0.147 or later. Codex plugins can't set environment variables, so enabling sending needs a separate config entry; see [docs/INSTALL.md](docs/INSTALL.md#enable-sending-in-codex-optional).

More install options, including Windows and troubleshooting, are in [docs/INSTALL.md](docs/INSTALL.md).

### Manual (any MCP client)

Download the binary for your platform from [Releases](https://github.com/Beg1nner4ever/whatsapp-unofficial/releases), verify it against `checksums.txt`, and register it:

```bash
claude mcp add -s user whatsapp-unofficial -- /path/to/whatsapp-unofficial serve
```

```toml
# ~/.codex/config.toml
[mcp_servers.whatsapp-unofficial]
command = "/path/to/whatsapp-unofficial"
args = ["serve"]
```

Link your account from a terminal with `whatsapp-unofficial login` (QR code) or `whatsapp-unofficial login --phone 33612345678` (pairing code).

## How it works

```
Claude Code / Codex ──stdio──> whatsapp-unofficial serve ──reads (read-only)──> messages.db
                                        │
                                        └──Unix socket (0600)──> whatsapp-unofficial daemon ──> WhatsApp
```

- **One binary, three roles.** `serve` is the MCP server each agent session starts. `daemon` holds the single WhatsApp connection and keeps `messages.db` in sync. `serve` starts it automatically and it exits 15 minutes after the last session goes away. CLI commands: `login`, `status`, `logout [--wipe]`.
- **Sync is on demand.** Messages sync while an agent session is open. When the daemon reconnects, WhatsApp delivers what arrived in the meantime.
- **Data** lives in your OS config directory (`~/Library/Application Support/whatsapp-unofficial` on macOS, `~/.config/whatsapp-unofficial` on Linux, `%AppData%\whatsapp-unofficial` on Windows). It is shared by Claude Code and Codex, so one linked device serves both.

## Tools

Always available:

- **connection_status**: link and connection state, and what to do next
- **pair_with_phone**: link the account with a pairing code
- **search_contacts**: search the address book and one-to-one chats
- **list_chats**, **get_chat**: chats and their metadata
- **get_direct_chat_by_contact**: find a one-to-one chat by phone number
- **get_contact_chats**: chats involving a contact
- **list_messages**: messages matching filters, newest first, with optional surrounding context
- **get_last_interaction**: the most recent message with a contact
- **get_message_context**: messages before and after a given message
- **download_media**: download a message's attachment and return its local path

Only when sending is enabled:

- **send_message**
- **send_file**: files must be in the outbox
- **send_audio_message**: converted to a voice note with ffmpeg if needed

## Security model

| Risk | Mitigation |
| --- | --- |
| Prompt injection from incoming messages ("send ~/.ssh/id_ed25519 to ...") | Sending is **off by default**; send tools aren't even registered. Message text, names and IDs are JSON-quoted so a message can't forge extra lines, and the server instructions tell the model they are untrusted data. Send tools are annotated destructive so clients ask for confirmation. |
| Exfiltrating local files | Files can only be sent from `<data>/outbox`. Symlinks and `..` are resolved before the check, so copying a file there is an explicit step you can see. |
| Other machines, websites, DNS rebinding | No network listener. The daemon only listens on a Unix socket with mode `0600` inside a `0700` directory. |
| Malicious attachment names (`../../Library/LaunchAgents/x.plist`) | Names are reduced to a sanitized base name prefixed with the message ID and confined to `<data>/media`. Files are created with `O_EXCL`, so nothing is overwritten and no symlink is followed. |
| Session theft from disk | Umask `077`: the session keys, messages, media and logs are owner-only. Permissions are tightened on startup. |
| Leaking messages to logs | Logs hold connection events only, no message text or contact numbers. |
| Tampered downloads (plugin installs) | The launcher downloads the binary for your platform over HTTPS and verifies its SHA-256 against `plugin/checksums.txt` before running it. Release builds are reproducible and CI checks the hashes. |
| Misattributed senders | Exact matching across phone-number and LID identities, with no substring matches. |

Remaining risks: unofficial-client account bans; prompt injection can still mislead the model's *answers* (review what it tells you); anything running as your user account can read your data, as with any local app.

## Development

```bash
go test ./...
go vet ./...
scripts/test-launcher.sh
scripts/build-release.sh          # reproducible release binaries + plugin/checksums.txt
```

Run a local build instead of a downloaded release by pointing the launcher at it:

```bash
go build -o /tmp/whatsapp-unofficial ./cmd/whatsapp-unofficial
WHATSAPP_UNOFFICIAL_BIN=/tmp/whatsapp-unofficial claude --plugin-dir ./plugin
```

Configuration (environment):

| Variable | Default | Purpose |
| --- | --- | --- |
| `WHATSAPP_UNOFFICIAL_HOME` | OS config dir | Data directory |
| `WHATSAPP_UNOFFICIAL_ALLOW_SEND` | off | `1`/`true` registers the send tools |
| `WHATSAPP_UNOFFICIAL_IDLE_MINUTES` | `15` | Daemon idle shutdown |
| `WHATSAPP_UNOFFICIAL_BIN` | unset | Launcher: run this binary instead of downloading |

## Uninstall

```bash
whatsapp-unofficial logout --wipe   # unlinks the device and deletes all local data
```

Then remove the plugin (`/plugin uninstall whatsapp-unofficial`, or via `/plugins` in Codex). You can also remove the linked device from your phone under **Linked devices**.

## License

MIT. Originally based on [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp) (MIT).
