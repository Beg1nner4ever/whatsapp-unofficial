---
name: setup
description: Link WhatsApp for the whatsapp-unofficial plugin (first-time setup, re-linking, "not connected" errors) and explain how to enable sending. Use when the user wants to connect WhatsApp, a WhatsApp tool reports it is not paired or logged out, or the user asks how to turn on sending.
---

# Set up whatsapp-unofficial

This is an unofficial, community-built integration, not affiliated with WhatsApp
or Meta. It links to the user's own account as a WhatsApp "linked device".
Mention once that unofficial clients may violate WhatsApp's terms and could get
the account restricted, then proceed if the user agrees.

## Link the account

1. Call the `connection_status` tool. If it reports a linked, connected
   account, setup is done.
2. Ask for the user's phone number in international format. Pass it to
   `pair_with_phone` as digits only: country code first, no `+`, spaces,
   dashes or leading zeros. Example: `+44 7700 900123` becomes `447700900123`.
3. Show the returned 8-character code and tell the user, on their phone:
   WhatsApp > Settings (or the menu) > **Linked devices** > **Link a device** >
   **Link with phone number instead**, then type the code. Codes expire within
   a few minutes; request a new one if needed.
4. Call `connection_status` again to confirm. The first history sync can take a
   few minutes; older chats appear gradually.

Never ask for or handle WhatsApp verification SMS codes or passwords; only the
pairing code shown by `pair_with_phone` is needed.

**Terminal alternative** (QR code or pairing code): the user can run the
plugin's launcher, `scripts/launch` in the plugin directory (two levels above
this file), in their own terminal:

```sh
<plugin-dir>/scripts/launch login                      # shows a QR code to scan
<plugin-dir>/scripts/launch login --phone 447700900123 # prints a pairing code
<plugin-dir>/scripts/launch status
```

To unlink and delete all local data: `<plugin-dir>/scripts/launch logout --wipe`.

## Sending is off by default

Only read tools are available unless the user opts in. When enabled, the
`send_message`, `send_file` and `send_audio_message` tools appear; files can
only be sent from the `outbox` folder in the data directory.

- **Claude Code**: run `/plugin`, open whatsapp-unofficial, choose
  **Configure options** and turn on **Allow sending messages**, then restart
  the Claude Code session.
- **Codex**: plugins cannot set environment variables, so sending needs a
  separate server entry in `~/.codex/config.toml`. See "Enable sending in
  Codex" in
  https://github.com/Beg1nner4ever/whatsapp-unofficial/blob/main/docs/INSTALL.md

Recommend keeping sending off unless the user actually needs it.
