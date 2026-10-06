# WhatsApp MCP Server (hardened fork)

This is a Model Context Protocol (MCP) server for WhatsApp.

> This is a security-hardened fork of [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp). See [Security model](#security-model) for what changed and why.

With this you can search and read your personal Whatsapp messages (including images, videos, documents, and audio messages), search your contacts and send messages to either individuals or groups. You can also send media files including images, videos, documents, and audio messages.

It connects to your **personal WhatsApp account** directly via the Whatsapp web multidevice API (using the [whatsmeow](https://github.com/tulir/whatsmeow) library). All your messages are stored locally in a SQLite database and only sent to an LLM (such as Claude) when the agent accesses them through tools (which you control).

Here's an example of what you can do when it's connected to Claude.

![WhatsApp MCP](./example-use.png)

> To get updates on this and other projects I work on [enter your email here](https://docs.google.com/forms/d/1rTF9wMBTN0vPfzWuQa2BjfGKdKIpTbyeKxhPMcEzgyI/preview)

> *Caution:* as with many MCP servers, the WhatsApp MCP is subject to [the lethal trifecta](https://simonwillison.net/2025/Jun/16/the-lethal-trifecta/): it reads untrusted content (messages from anyone) and can send data out. This fork is **read-only by default** to break that chain; enable sending only if you need it.

## Security model

Changes from upstream:

| Issue in upstream | Fix in this fork |
| --- | --- |
| REST API listened on all interfaces (`:8080`) with no authentication: anyone on your network could send messages as you | Binds to `127.0.0.1` only (non-loopback requires an explicit opt-in) and requires a random bearer token stored in `store/bridge_token` (mode `0600`) |
| `/api/send` accepted any `media_path`, so any local file (SSH keys, credentials) could be sent to any number | Files can only be sent from `store/outbox/`; symlinks and `..` are resolved before the check |
| Any website could call the API via a "simple" cross-origin POST (no CORS preflight) | Requests with `Origin` / cross-site `Sec-Fetch-Site` headers are rejected, the body must be `application/json`, and the `Host` must be loopback (DNS rebinding) |
| Sender-controlled document filenames were used as-is when saving media (`../../Library/LaunchAgents/x.plist`) | Filenames are reduced to a sanitized base name prefixed with the message ID, the final path is checked to stay inside `store/media/`, and files are created with `O_EXCL` (no overwrite, no symlink following) |
| Session keys, messages and media were world-readable (`0755`/`0644`) | Umask `077`; store directories `0700`, files `0600`; existing installs are tightened on startup |
| Message text was logged; the media encryption key was printed on upload | Logs contain metadata only |
| Send tools always available alongside untrusted message content | Sending is disabled unless `WHATSAPP_ALLOW_SEND=1` is set (enforced in both the bridge and the MCP server) |
| Message text was returned raw, so a message could forge extra lines (e.g. a fake `From: Me:` line) | Chat names, sender names and message text are JSON-quoted; server instructions tell the model to treat them as untrusted data |
| Sender names were resolved with `LIKE '%number%'`, which could attribute a message to the wrong contact | Exact JID matching |
| No limits or timeouts | Request size limit, HTTP timeouts, result limits capped at 100, read-only database access from the MCP server, proxies ignored for bridge calls |

Remaining risks you should know about:

- This uses an unofficial WhatsApp client. WhatsApp may ban numbers that use one.
- Reading messages still exposes the model to prompt injection. Keep sending disabled unless you need it, and review every send the model proposes.
- Anything running as your user account can read the store, as with any local app data.

### Configuration

| Variable | Default | Used by | Purpose |
| --- | --- | --- | --- |
| `WHATSAPP_STORE_DIR` | `whatsapp-bridge/store` | bridge, MCP server | Session, messages, token, outbox, media. Use the same absolute path for both. |
| `WHATSAPP_ALLOW_SEND` | off | bridge, MCP server | Set to `1` in **both** to enable the send tools. |
| `WHATSAPP_BRIDGE_ADDR` | `127.0.0.1:8080` | bridge | Listen address. |
| `WHATSAPP_BRIDGE_ALLOW_NON_LOOPBACK` | off | bridge | Required to listen on a non-loopback address. Not recommended. |
| `WHATSAPP_BRIDGE_URL` | `http://127.0.0.1:8080/api` | MCP server | Bridge API base URL. |
| `WHATSAPP_BRIDGE_TOKEN` | generated | bridge, MCP server | Override the token file (32+ characters). |

## Installation

### Prerequisites

- Go
- Python 3.6+
- Anthropic Claude Desktop app (or Cursor)
- UV (Python package manager), install with `curl -LsSf https://astral.sh/uv/install.sh | sh`
- FFmpeg (_optional_) - Only needed for audio messages. If you want to send audio files as playable WhatsApp voice messages, they must be in `.ogg` Opus format. With FFmpeg installed, the MCP server will automatically convert non-Opus audio files. Without FFmpeg, you can still send raw audio files using the `send_file` tool.

### Steps

1. **Clone this repository**

   ```bash
   git clone https://github.com/<your-account>/whatsapp-mcp.git
   cd whatsapp-mcp
   ```

2. **Run the WhatsApp bridge**

   Navigate to the whatsapp-bridge directory and run the Go application:

   ```bash
   cd whatsapp-bridge
   go run .
   ```

   The first time you run it, you will be prompted to scan a QR code. Scan the QR code with your WhatsApp mobile app to authenticate.

   The bridge starts read-only. To allow sending, run `WHATSAPP_ALLOW_SEND=1 go run .` and set the same variable for the MCP server (step 3).

   After approximately 20 days, you will might need to re-authenticate.

3. **Connect to the MCP server**

   Copy the below json with the appropriate {{PATH}} values:

   ```json
   {
     "mcpServers": {
       "whatsapp": {
         "command": "{{PATH_TO_UV}}", // Run `which uv` and place the output here
         "args": [
           "--directory",
           "{{PATH_TO_SRC}}/whatsapp-mcp/whatsapp-mcp-server", // cd into the repo, run `pwd` and enter the output here + "/whatsapp-mcp-server"
           "run",
           "main.py"
         ]
       }
     }
   }
   ```

   For **Claude**, save this as `claude_desktop_config.json` in your Claude Desktop configuration directory at:

   ```
   ~/Library/Application Support/Claude/claude_desktop_config.json
   ```

   For **Cursor**, save this as `mcp.json` in your Cursor configuration directory at:

   ```
   ~/.cursor/mcp.json
   ```

4. **Restart Claude Desktop / Cursor**

   Open Claude Desktop and you should now see WhatsApp as an available integration.

   Or restart Cursor.

### Windows Compatibility

If you're running this project on Windows, be aware that `go-sqlite3` requires **CGO to be enabled** in order to compile and work properly. By default, **CGO is disabled on Windows**, so you need to explicitly enable it and have a C compiler installed.

#### Steps to get it working:

1. **Install a C compiler**  
   We recommend using [MSYS2](https://www.msys2.org/) to install a C compiler for Windows. After installing MSYS2, make sure to add the `ucrt64\bin` folder to your `PATH`.  
   → A step-by-step guide is available [here](https://code.visualstudio.com/docs/cpp/config-mingw).

2. **Enable CGO and run the app**

   ```bash
   cd whatsapp-bridge
   go env -w CGO_ENABLED=1
   go run .
   ```

Without this setup, you'll likely run into errors like:

> `Binary was compiled with 'CGO_ENABLED=0', go-sqlite3 requires cgo to work.`

## Architecture Overview

This application consists of two main components:

1. **Go WhatsApp Bridge** (`whatsapp-bridge/`): A Go application that connects to WhatsApp's web API, handles authentication via QR code, and stores message history in SQLite. It serves as the bridge between WhatsApp and the MCP server.

2. **Python MCP Server** (`whatsapp-mcp-server/`): A Python server implementing the Model Context Protocol (MCP), which provides standardized tools for Claude to interact with WhatsApp data and send/receive messages.

### Data Storage

- All message history is stored in a SQLite database within the `whatsapp-bridge/store/` directory (owner-only permissions)
- The database maintains tables for chats and messages
- Messages are indexed for efficient searching and retrieval

## Usage

Once connected, you can interact with your WhatsApp contacts through Claude, leveraging Claude's AI capabilities in your WhatsApp conversations.

### MCP Tools

Claude can access the following tools to interact with WhatsApp:

- **search_contacts**: Search for contacts by name or phone number
- **list_messages**: Retrieve messages with optional filters and context
- **list_chats**: List available chats with metadata
- **get_chat**: Get information about a specific chat
- **get_direct_chat_by_contact**: Find a direct chat with a specific contact
- **get_contact_chats**: List all chats involving a specific contact
- **get_last_interaction**: Get the most recent message with a contact
- **get_message_context**: Retrieve context around a specific message
- **download_media**: Download media from a WhatsApp message and get the local file path

Only available when `WHATSAPP_ALLOW_SEND=1`:

- **send_message**: Send a WhatsApp message to a specified phone number or group JID
- **send_file**: Send a file from the outbox (image, video, raw audio, document) to a specified recipient
- **send_audio_message**: Send an audio file from the outbox as a WhatsApp voice message (requires the file to be an .ogg opus file or ffmpeg must be installed)

### Media Handling Features

The MCP server supports both sending and receiving various media types:

#### Media Sending

You can send various media types to your WhatsApp contacts. Files must be placed in the outbox directory (`whatsapp-bridge/store/outbox/` by default) first; paths outside it are rejected. Paths can be absolute or relative to the outbox.

- **Images, Videos, Documents**: Use the `send_file` tool to share any supported media type.
- **Voice Messages**: Use the `send_audio_message` tool to send audio files as playable WhatsApp voice messages.
  - For optimal compatibility, audio files should be in `.ogg` Opus format.
  - With FFmpeg installed, the system will automatically convert other audio formats (MP3, WAV, etc.) to the required format.
  - Without FFmpeg, you can still send raw audio files using the `send_file` tool, but they won't appear as playable voice messages.

#### Media Downloading

By default, just the metadata of the media is stored in the local database. The message will indicate that media was sent. To access this media you need to use the download_media tool which takes the `message_id` and `chat_jid` (which are shown when printing messages containing the meda), this downloads the media into `store/media/<chat>/` and then returns the file path which can be then opened or passed to another tool. Downloaded files are named `<message id>_<sanitized original name>`.

## Technical Details

1. Claude sends requests to the Python MCP server
2. The MCP server queries the Go bridge for WhatsApp data or directly to the SQLite database
3. The Go accesses the WhatsApp API and keeps the SQLite database up to date
4. Data flows back through the chain to Claude
5. When sending messages, the request flows from Claude through the MCP server to the Go bridge and to WhatsApp

## Development

```bash
cd whatsapp-bridge && go test ./...
cd whatsapp-mcp-server && uv run pytest
```

## Troubleshooting

- If you encounter permission issues when running uv, you may need to add it to your PATH or use the full path to the executable.
- Make sure both the Go application and the Python server are running for the integration to work properly.
- **"Bridge token not found"**: start the bridge once so it creates `store/bridge_token`, and make sure `WHATSAPP_STORE_DIR` points to the same directory for both components.
- **"Sending is disabled"**: set `WHATSAPP_ALLOW_SEND=1` for both the bridge and the MCP server.

### Authentication Issues

- **QR Code Not Displaying**: If the QR code doesn't appear, try restarting the authentication script. If issues persist, check if your terminal supports displaying QR codes.
- **WhatsApp Already Logged In**: If your session is already active, the Go bridge will automatically reconnect without showing a QR code.
- **Device Limit Reached**: WhatsApp limits the number of linked devices. If you reach this limit, you'll need to remove an existing device from WhatsApp on your phone (Settings > Linked Devices).
- **No Messages Loading**: After initial authentication, it can take several minutes for your message history to load, especially if you have many chats.
- **WhatsApp Out of Sync**: If your WhatsApp messages get out of sync with the bridge, delete both database files (`whatsapp-bridge/store/messages.db` and `whatsapp-bridge/store/whatsapp.db`) and restart the bridge to re-authenticate.

For additional Claude Desktop integration troubleshooting, see the [MCP documentation](https://modelcontextprotocol.io/quickstart/server#claude-for-desktop-integration-issues). The documentation includes helpful tips for checking logs and resolving common issues.
