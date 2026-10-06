# Submission kit - Anthropic (Claude plugin directory)

Status: **not submitted.** Read "Known blockers" first; approval is unlikely
under the current directory policy.

Sources (checked 2026-10-06):

- Pre-submission checklist: https://claude.com/docs/plugins/pre-submission-checklist
- Submitting a plugin: https://claude.com/docs/plugins/submit
- Publishing to the directory: https://claude.com/docs/directory/publish
- Anthropic Software Directory Policy: https://support.claude.com/en/articles/13145358-anthropic-software-directory-policy
- Submission portal: https://claude.ai/directory/manage
- Plugin manifest reference: https://code.claude.com/docs/en/plugins-reference

## Listing copy

- **Name:** whatsapp-unofficial
- **Short description:** Unofficial, community-built WhatsApp integration:
  read your own chats from Claude Code via a local linked device. Read-only by
  default. Not affiliated with WhatsApp or Meta.
- **Long description:**
  whatsapp-unofficial lets Claude Code read your own WhatsApp messages. A small
  local program links to your account as a WhatsApp linked device (like
  WhatsApp Web) and keeps all data on your computer, with no servers and no
  telemetry. Claude can list chats, search contacts, read messages with
  context and download media. Sending is off by default and must be enabled in
  the plugin's options. Incoming messages are treated as untrusted data, data
  files are owner-only, the control channel is a local socket, and files can
  only be sent from a dedicated outbox folder. This is an independent
  open-source project, not affiliated with, endorsed by, or sponsored by
  WhatsApp or Meta. Unofficial clients may violate WhatsApp's terms; use at
  your own risk.
- **Category:** Productivity
- **Example prompts (policy 3E requires at least 3):**
  1. "Link my WhatsApp account."
  2. "Summarize the WhatsApp messages I got today."
  3. "When did I last talk to Alex on WhatsApp, and what about?"
  4. "Find the address Sam sent me on WhatsApp last week."

## Data-handling answers for the form

- Personal data: yes - WhatsApp messages, contacts and phone numbers of the
  user and their contacts, processed locally.
- Sent to other services: only to WhatsApp (normal linked-device traffic) and
  to the Claude model when a tool is called. Binary download from GitHub
  Releases. No developer-operated servers, no telemetry.
- Retention: until the user runs `logout --wipe` or deletes the data directory.
- Users under 18: not directed at children.

## Reviewer test plan

Requires a real WhatsApp account on a phone (there is no sandbox). Policy 3D
asks for a test account with sample data; provide a spare phone number with a
few seeded chats, or explain that the reviewer must link their own test number.

1. Install from the marketplace; ask "Link my WhatsApp account"; follow the
   pairing-code steps; `connection_status` shows connected.
2. "List my 5 most recent chats" - `list_chats` returns them.
3. "Show the last 10 messages from <contact>" - `list_messages` returns quoted
   message content.
4. Ask it to send a message with sending disabled - no send tool exists; Claude
   explains how to enable it.
5. Enable **Allow sending messages**, restart, send a test message to yourself
   after confirming.

## Checklist

- [ ] Public GitHub repository `Beg1nner4ever/whatsapp-unofficial` (must be
      public before the listing goes live)
- [ ] README of at least 40 words describing the plugin (README.md is owned by
      the core rewrite; confirm it states "unofficial" prominently)
- [x] `license: MIT` in plugin.json and a LICENSE file
- [x] Privacy policy: https://github.com/Beg1nner4ever/whatsapp-unofficial/blob/main/docs/PRIVACY.md
- [x] Terms: https://github.com/Beg1nner4ever/whatsapp-unofficial/blob/main/docs/TERMS.md
- [x] Support: https://github.com/Beg1nner4ever/whatsapp-unofficial/issues
- [ ] Verified contact email for the submission form (policy 3B)
- [x] No credentials in files; send option is a boolean `userConfig`
- [x] Launcher pins an exact binary version and verifies SHA-256 before running
- [x] Icon: `plugin/assets/icon.svg`, `plugin/assets/icon.png` (512x512,
      original artwork, no WhatsApp/Meta marks)
- [ ] MCP tool annotations (`readOnlyHint`, `destructiveHint`, `title`;
      policy 5E) - must be set by the Go core for every tool
- [x] `claude plugin validate plugin` passes (with `--strict`, Claude Code 2.1.220 flags the directory-only link fields documentationUrl, privacyPolicyUrl, termsOfServiceUrl and supportUrl as unknown; they are read by the directory and ignored at load time)
- [ ] Release `v<VERSION>` published with all five binaries

## What you must do yourself

1. Use an account on a paid plan (Pro, Max, Team or Enterprise). On Team or
   Enterprise, an Owner or a member with the Directory permission must submit.
2. Make the repository public and publish the first release.
3. Open https://claude.ai/directory/manage, choose a plugin submission from a
   GitHub repository, fill in the data-handling questions and contact email,
   and accept the acknowledgements. Those are legal attestations; Claude
   cannot accept them for you.
4. Answer reviewer questions; every new version goes through automated
   validation and a security scan.

## Known blockers (honest assessment)

- **Policy 3F (endpoint ownership):** developers must own or control the
  endpoints their software connects to. This plugin connects to WhatsApp's
  servers, which the developer does not control. The plugin exception seems
  to cover only connections to approved directory connectors, not arbitrary
  third-party services.
- **Policy 1E (intellectual property) and brand rules:** a name containing
  "whatsapp" matches a known brand. Per the pre-submission checklist this
  holds the submission for manual review and may be rejected as a brand
  look-alike, even with "unofficial" in the name.
- **Policy 1C (third-party privacy):** the plugin processes messages from
  people who did not consent to AI processing.
- **WhatsApp Terms of Service:** unofficial clients may violate them, which
  reviewers may treat as facilitating a ToS violation.
- **Binaries:** the launcher downloads a binary at runtime. The checklist holds
  plugins with binaries for manual review.
- **No test account:** WhatsApp has no sandbox, so policy 3D (test account with
  sample data) needs a dedicated phone number.

Realistic path: distribute through this repository's own marketplace
(`claude plugin marketplace add Beg1nner4ever/whatsapp-unofficial`), which needs
no directory approval.
