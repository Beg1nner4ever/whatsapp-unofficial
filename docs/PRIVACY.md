# Privacy Policy - whatsapp-unofficial

Last updated: 2026-10-06

whatsapp-unofficial is an unofficial, community-built, open-source tool. It is
not affiliated with, endorsed by, or sponsored by WhatsApp or Meta. There is no
company or server behind it: the software runs entirely on your own computer.

## Summary

- Your WhatsApp data stays on your machine. The project operates no servers
  and receives none of your data.
- There is no telemetry, analytics, crash reporting or tracking of any kind.
- Message content reaches an AI model only when your AI assistant calls one of
  the plugin's tools, and only the results of that call.
- You can delete everything at any time with `whatsapp-unofficial logout --wipe`.

## How it works

The plugin links to your WhatsApp account as a "linked device", the same
mechanism WhatsApp uses for its desktop and web apps. It talks directly to
WhatsApp's servers using the open-source whatsmeow library, exactly like any
other linked device. The plugin itself never sends your data anywhere else.

## What is stored, and where

All files live in one private data directory, created with owner-only
permissions (directories 0700, files 0600 on macOS and Linux):

| Platform | Default location |
| --- | --- |
| macOS | `~/Library/Application Support/whatsapp-unofficial` |
| Linux | `~/.config/whatsapp-unofficial` (or `$XDG_CONFIG_HOME/whatsapp-unofficial`) |
| Windows | `%AppData%\whatsapp-unofficial` |

You can choose another location with the `WHATSAPP_UNOFFICIAL_HOME` environment
variable.

| File or folder | Contents |
| --- | --- |
| `whatsapp.db` | Linked-device session: encryption keys, your contact list and identity mappings. Anyone holding this file can act as your linked device. |
| `messages.db` | Chats and messages synced to this device (text, metadata, media references). |
| `media/` | Media files you asked the assistant to download. |
| `outbox/` | Files you place there to be sent. The plugin can only send files from this folder. |
| `daemon.sock`, `daemon.lock`, `daemon.log` | Local control socket, lock file and log of the background process. The socket is local-only; no network port is opened. |

The plugin launcher also caches the downloaded program binary (no personal
data) in the plugin data directory provided by Claude Code or Codex, or in
`~/.cache/whatsapp-unofficial`.

## What is sent to the AI model

Nothing is sent proactively. When your AI assistant (for example Claude Code or
Codex) calls a tool such as `list_messages` or `search_contacts`, the tool's
result - which can include message text, contact names and phone numbers - is
returned to the assistant and processed by the AI model provider you use, under
that provider's own privacy terms. You control this through your assistant's
tool-approval settings. Message content is marked as untrusted data in tool
results to reduce the risk of prompt injection.

Sending is off by default. If you enable it, the assistant can send messages
and files from the `outbox/` folder on your behalf; those go to WhatsApp like
any message you send yourself.

## What is sent elsewhere

- **WhatsApp / Meta**: the normal linked-device traffic needed to sync and,
  if enabled, send messages. This is governed by WhatsApp's own privacy policy.
- **GitHub**: on first start the launcher downloads the program binary from
  this project's GitHub Releases page. GitHub sees a normal download request
  (your IP address and user agent). No personal data is included.
- Nobody else. There is no telemetry.

## Retention and deletion

Data is kept until you delete it. To remove everything:

```sh
whatsapp-unofficial logout --wipe
```

This unlinks the device from your WhatsApp account and deletes the data
directory. `logout` without `--wipe` unlinks the device but keeps the local
files. You can also unlink the device from your phone (WhatsApp > Linked
devices) and delete the data directory manually. Uninstalling the plugin does
not by itself delete the data directory.

## Children

The tool is not directed at children and must be used only by the owner of the
WhatsApp account, in line with WhatsApp's own age requirements.

## Changes

Changes to this policy are published in this file in the project repository,
with the date above updated.

## Contact

Open an issue at
https://github.com/Beg1nner4ever/whatsapp-unofficial/issues. For security
reports, use GitHub's private vulnerability reporting on the repository rather
than a public issue.
