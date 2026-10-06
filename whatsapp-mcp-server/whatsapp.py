import sqlite3
from datetime import datetime
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Dict, Optional, List, Tuple
import os
import os.path
import re
import requests
import json
import audio

# Configuration is environment-driven so installers can point the server at
# any bridge store. WHATSAPP_STORE_DIR must match the bridge's store directory.
_SERVER_DIR = os.path.dirname(os.path.abspath(__file__))
STORE_DIR = os.path.abspath(
    os.environ.get("WHATSAPP_STORE_DIR")
    or os.path.join(_SERVER_DIR, "..", "whatsapp-bridge", "store")
)
MESSAGES_DB_PATH = os.path.join(STORE_DIR, "messages.db")
OUTBOX_DIR = os.path.join(STORE_DIR, "outbox")
TOKEN_PATH = os.path.join(STORE_DIR, "bridge_token")
WHATSAPP_API_BASE_URL = os.environ.get("WHATSAPP_BRIDGE_URL", "http://127.0.0.1:8080/api")
REQUEST_TIMEOUT_SECONDS = 120

MAX_LIMIT = 100
MAX_CONTEXT = 20

# Ignore HTTP(S)_PROXY settings: the bearer token must never be sent to a proxy.
_http = requests.Session()
_http.trust_env = False


def _connect() -> sqlite3.Connection:
    """Open the message database read-only; the MCP server never writes to it."""
    return sqlite3.connect(Path(MESSAGES_DB_PATH).as_uri() + "?mode=ro", uri=True)


def _clamp(value: int, low: int, high: int) -> int:
    return max(low, min(int(value), high))


def _quote(text: Optional[str]) -> str:
    """JSON-quote untrusted text so a message can't fake extra lines/messages."""
    return json.dumps(text if text is not None else "", ensure_ascii=False)


def _bridge_token() -> str:
    token = os.environ.get("WHATSAPP_BRIDGE_TOKEN", "").strip()
    if token:
        return token
    try:
        with open(TOKEN_PATH, encoding="utf-8") as f:
            return f.read().strip()
    except FileNotFoundError:
        raise RuntimeError(
            f"Bridge token not found at {TOKEN_PATH}. Start the WhatsApp bridge first "
            "(it creates the token), or set WHATSAPP_BRIDGE_TOKEN."
        )


def _bridge_post(endpoint: str, payload: Dict[str, Any]) -> Tuple[int, Dict[str, Any]]:
    """POST to the authenticated bridge API. Returns (status, json-or-error dict)."""
    response = _http.post(
        f"{WHATSAPP_API_BASE_URL}/{endpoint}",
        json=payload,
        headers={"Authorization": f"Bearer {_bridge_token()}"},
        timeout=REQUEST_TIMEOUT_SECONDS,
    )
    try:
        body = response.json()
    except ValueError:
        body = {"success": False, "message": response.text.strip()}
    return response.status_code, body


def resolve_outbox_path(media_path: str) -> str:
    """Return the real path of a regular file inside the outbox, or raise ValueError.

    Mirrors the bridge's check so the caller gets a clear error before upload.
    Relative paths are resolved against the outbox; symlinks are resolved
    before the containment check.
    """
    if not media_path:
        raise ValueError("Media path must be provided")
    candidate = media_path if os.path.isabs(media_path) else os.path.join(OUTBOX_DIR, media_path)
    real = os.path.realpath(candidate)
    outbox = os.path.realpath(OUTBOX_DIR)
    if os.path.commonpath([outbox, real]) != outbox:
        raise ValueError(
            f"Files can only be sent from the outbox directory: {OUTBOX_DIR}. "
            "Copy the file there first."
        )
    if not os.path.isfile(real):
        raise ValueError(f"Media file not found: {media_path}")
    return real


def _normalize_phone(number: str) -> str:
    return re.sub(r"\D", "", number or "")

@dataclass
class Message:
    timestamp: datetime
    sender: str
    content: str
    is_from_me: bool
    chat_jid: str
    id: str
    chat_name: Optional[str] = None
    media_type: Optional[str] = None

@dataclass
class Chat:
    jid: str
    name: Optional[str]
    last_message_time: Optional[datetime]
    last_message: Optional[str] = None
    last_sender: Optional[str] = None
    last_is_from_me: Optional[bool] = None

    @property
    def is_group(self) -> bool:
        """Determine if chat is a group based on JID pattern."""
        return self.jid.endswith("@g.us")

@dataclass
class Contact:
    phone_number: str
    name: Optional[str]
    jid: str

@dataclass
class MessageContext:
    message: Message
    before: List[Message]
    after: List[Message]

def get_sender_name(sender_jid: str) -> str:
    try:
        conn = _connect()
        cursor = conn.cursor()

        # Exact matches only: a substring match can attribute a message to the
        # wrong contact (e.g. "3361" matching "33612345678").
        candidates = [sender_jid]
        if '@' not in sender_jid:
            candidates.append(f"{sender_jid}@s.whatsapp.net")

        result = None
        for jid in candidates:
            cursor.execute("""
                SELECT name
                FROM chats
                WHERE jid = ?
                LIMIT 1
            """, (jid,))
            result = cursor.fetchone()
            if result:
                break
        
        if result and result[0]:
            return result[0]
        else:
            return sender_jid
        
    except sqlite3.Error as e:
        print(f"Database error while getting sender name: {e}")
        return sender_jid
    finally:
        if 'conn' in locals():
            conn.close()

def format_message(message: Message, show_chat_info: bool = True) -> str:
    """Format a single message as one line.

    Chat names, sender names and message text are untrusted (anyone can message
    you or name a group), so they are JSON-quoted: embedded newlines or fake
    "[timestamp] From: ..." text cannot impersonate other lines.
    """
    output = f"[{message.timestamp:%Y-%m-%d %H:%M:%S}] "

    if show_chat_info and message.chat_name:
        output += f"Chat: {_quote(message.chat_name)} "

    content_prefix = ""
    if hasattr(message, 'media_type') and message.media_type:
        content_prefix = f"[{message.media_type} - Message ID: {_quote(message.id)} - Chat JID: {_quote(message.chat_jid)}] "

    try:
        sender = "me" if message.is_from_me else _quote(get_sender_name(message.sender))
        output += f"From: {sender}: {content_prefix}{_quote(message.content)}\n"
    except Exception as e:
        print(f"Error formatting message: {e}")
    return output

def format_messages_list(messages: List[Message], show_chat_info: bool = True) -> str:
    output = ""
    if not messages:
        output += "No messages to display."
        return output
    
    for message in messages:
        output += format_message(message, show_chat_info)
    return output

def list_messages(
    after: Optional[str] = None,
    before: Optional[str] = None,
    sender_phone_number: Optional[str] = None,
    chat_jid: Optional[str] = None,
    query: Optional[str] = None,
    limit: int = 20,
    page: int = 0,
    include_context: bool = True,
    context_before: int = 1,
    context_after: int = 1
) -> List[Message]:
    """Get messages matching the specified criteria with optional context."""
    limit = _clamp(limit, 1, MAX_LIMIT)
    page = max(0, int(page))
    context_before = _clamp(context_before, 0, MAX_CONTEXT)
    context_after = _clamp(context_after, 0, MAX_CONTEXT)
    try:
        conn = _connect()
        cursor = conn.cursor()
        
        # Build base query
        query_parts = ["SELECT messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, chats.jid, messages.id, messages.media_type FROM messages"]
        query_parts.append("JOIN chats ON messages.chat_jid = chats.jid")
        where_clauses = []
        params = []
        
        # Add filters
        if after:
            try:
                after = datetime.fromisoformat(after)
            except ValueError:
                raise ValueError(f"Invalid date format for 'after': {after}. Please use ISO-8601 format.")
            
            where_clauses.append("messages.timestamp > ?")
            params.append(after)

        if before:
            try:
                before = datetime.fromisoformat(before)
            except ValueError:
                raise ValueError(f"Invalid date format for 'before': {before}. Please use ISO-8601 format.")
            
            where_clauses.append("messages.timestamp < ?")
            params.append(before)

        if sender_phone_number:
            where_clauses.append("messages.sender = ?")
            params.append(sender_phone_number)
            
        if chat_jid:
            where_clauses.append("messages.chat_jid = ?")
            params.append(chat_jid)
            
        if query:
            where_clauses.append("LOWER(messages.content) LIKE LOWER(?)")
            params.append(f"%{query}%")
            
        if where_clauses:
            query_parts.append("WHERE " + " AND ".join(where_clauses))
            
        # Add pagination
        offset = page * limit
        query_parts.append("ORDER BY messages.timestamp DESC")
        query_parts.append("LIMIT ? OFFSET ?")
        params.extend([limit, offset])
        
        cursor.execute(" ".join(query_parts), tuple(params))
        messages = cursor.fetchall()
        
        result = []
        for msg in messages:
            message = Message(
                timestamp=datetime.fromisoformat(msg[0]),
                sender=msg[1],
                chat_name=msg[2],
                content=msg[3],
                is_from_me=msg[4],
                chat_jid=msg[5],
                id=msg[6],
                media_type=msg[7]
            )
            result.append(message)
            
        if include_context and result:
            # Add context for each message
            messages_with_context = []
            for msg in result:
                context = get_message_context(msg.id, context_before, context_after)
                messages_with_context.extend(context.before)
                messages_with_context.append(context.message)
                messages_with_context.extend(context.after)
            
            return format_messages_list(messages_with_context, show_chat_info=True)
            
        # Format and display messages without context
        return format_messages_list(result, show_chat_info=True)    
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return []
    finally:
        if 'conn' in locals():
            conn.close()


def get_message_context(
    message_id: str,
    before: int = 5,
    after: int = 5
) -> MessageContext:
    """Get context around a specific message."""
    before = _clamp(before, 0, MAX_CONTEXT)
    after = _clamp(after, 0, MAX_CONTEXT)
    try:
        conn = _connect()
        cursor = conn.cursor()
        
        # Get the target message first
        cursor.execute("""
            SELECT messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, chats.jid, messages.id, messages.chat_jid, messages.media_type
            FROM messages
            JOIN chats ON messages.chat_jid = chats.jid
            WHERE messages.id = ?
        """, (message_id,))
        msg_data = cursor.fetchone()
        
        if not msg_data:
            raise ValueError(f"Message with ID {message_id} not found")
            
        target_message = Message(
            timestamp=datetime.fromisoformat(msg_data[0]),
            sender=msg_data[1],
            chat_name=msg_data[2],
            content=msg_data[3],
            is_from_me=msg_data[4],
            chat_jid=msg_data[5],
            id=msg_data[6],
            media_type=msg_data[8]
        )
        
        # Get messages before
        cursor.execute("""
            SELECT messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, chats.jid, messages.id, messages.media_type
            FROM messages
            JOIN chats ON messages.chat_jid = chats.jid
            WHERE messages.chat_jid = ? AND messages.timestamp < ?
            ORDER BY messages.timestamp DESC
            LIMIT ?
        """, (msg_data[7], msg_data[0], before))
        
        before_messages = []
        for msg in cursor.fetchall():
            before_messages.append(Message(
                timestamp=datetime.fromisoformat(msg[0]),
                sender=msg[1],
                chat_name=msg[2],
                content=msg[3],
                is_from_me=msg[4],
                chat_jid=msg[5],
                id=msg[6],
                media_type=msg[7]
            ))
        
        # Get messages after
        cursor.execute("""
            SELECT messages.timestamp, messages.sender, chats.name, messages.content, messages.is_from_me, chats.jid, messages.id, messages.media_type
            FROM messages
            JOIN chats ON messages.chat_jid = chats.jid
            WHERE messages.chat_jid = ? AND messages.timestamp > ?
            ORDER BY messages.timestamp ASC
            LIMIT ?
        """, (msg_data[7], msg_data[0], after))
        
        after_messages = []
        for msg in cursor.fetchall():
            after_messages.append(Message(
                timestamp=datetime.fromisoformat(msg[0]),
                sender=msg[1],
                chat_name=msg[2],
                content=msg[3],
                is_from_me=msg[4],
                chat_jid=msg[5],
                id=msg[6],
                media_type=msg[7]
            ))
        
        return MessageContext(
            message=target_message,
            before=before_messages,
            after=after_messages
        )
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        raise
    finally:
        if 'conn' in locals():
            conn.close()


def list_chats(
    query: Optional[str] = None,
    limit: int = 20,
    page: int = 0,
    include_last_message: bool = True,
    sort_by: str = "last_active"
) -> List[Chat]:
    """Get chats matching the specified criteria."""
    limit = _clamp(limit, 1, MAX_LIMIT)
    page = max(0, int(page))
    try:
        conn = _connect()
        cursor = conn.cursor()
        
        # Build base query
        query_parts = ["""
            SELECT 
                chats.jid,
                chats.name,
                chats.last_message_time,
                messages.content as last_message,
                messages.sender as last_sender,
                messages.is_from_me as last_is_from_me
            FROM chats
        """]
        
        if include_last_message:
            query_parts.append("""
                LEFT JOIN messages ON chats.jid = messages.chat_jid 
                AND chats.last_message_time = messages.timestamp
            """)
            
        where_clauses = []
        params = []
        
        if query:
            where_clauses.append("(LOWER(chats.name) LIKE LOWER(?) OR chats.jid LIKE ?)")
            params.extend([f"%{query}%", f"%{query}%"])
            
        if where_clauses:
            query_parts.append("WHERE " + " AND ".join(where_clauses))
            
        # Add sorting
        order_by = "chats.last_message_time DESC" if sort_by == "last_active" else "chats.name"
        query_parts.append(f"ORDER BY {order_by}")
        
        # Add pagination
        offset = (page ) * limit
        query_parts.append("LIMIT ? OFFSET ?")
        params.extend([limit, offset])
        
        cursor.execute(" ".join(query_parts), tuple(params))
        chats = cursor.fetchall()
        
        result = []
        for chat_data in chats:
            chat = Chat(
                jid=chat_data[0],
                name=chat_data[1],
                last_message_time=datetime.fromisoformat(chat_data[2]) if chat_data[2] else None,
                last_message=chat_data[3],
                last_sender=chat_data[4],
                last_is_from_me=chat_data[5]
            )
            result.append(chat)
            
        return result
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return []
    finally:
        if 'conn' in locals():
            conn.close()


def search_contacts(query: str) -> List[Contact]:
    """Search contacts by name or phone number."""
    try:
        conn = _connect()
        cursor = conn.cursor()
        
        # Split query into characters to support partial matching
        search_pattern = '%' +query + '%'
        
        cursor.execute("""
            SELECT DISTINCT 
                jid,
                name
            FROM chats
            WHERE 
                (LOWER(name) LIKE LOWER(?) OR LOWER(jid) LIKE LOWER(?))
                AND jid NOT LIKE '%@g.us'
            ORDER BY name, jid
            LIMIT 50
        """, (search_pattern, search_pattern))
        
        contacts = cursor.fetchall()
        
        result = []
        for contact_data in contacts:
            contact = Contact(
                phone_number=contact_data[0].split('@')[0],
                name=contact_data[1],
                jid=contact_data[0]
            )
            result.append(contact)
            
        return result
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return []
    finally:
        if 'conn' in locals():
            conn.close()


def get_contact_chats(jid: str, limit: int = 20, page: int = 0) -> List[Chat]:
    """Get all chats involving the contact.
    
    Args:
        jid: The contact's JID to search for
        limit: Maximum number of chats to return (default 20)
        page: Page number for pagination (default 0)
    """
    limit = _clamp(limit, 1, MAX_LIMIT)
    page = max(0, int(page))
    try:
        conn = _connect()
        cursor = conn.cursor()
        
        cursor.execute("""
            SELECT DISTINCT
                c.jid,
                c.name,
                c.last_message_time,
                m.content as last_message,
                m.sender as last_sender,
                m.is_from_me as last_is_from_me
            FROM chats c
            JOIN messages m ON c.jid = m.chat_jid
            WHERE m.sender = ? OR c.jid = ?
            ORDER BY c.last_message_time DESC
            LIMIT ? OFFSET ?
        """, (jid, jid, limit, page * limit))
        
        chats = cursor.fetchall()
        
        result = []
        for chat_data in chats:
            chat = Chat(
                jid=chat_data[0],
                name=chat_data[1],
                last_message_time=datetime.fromisoformat(chat_data[2]) if chat_data[2] else None,
                last_message=chat_data[3],
                last_sender=chat_data[4],
                last_is_from_me=chat_data[5]
            )
            result.append(chat)
            
        return result
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return []
    finally:
        if 'conn' in locals():
            conn.close()


def get_last_interaction(jid: str) -> str:
    """Get most recent message involving the contact."""
    try:
        conn = _connect()
        cursor = conn.cursor()
        
        cursor.execute("""
            SELECT 
                m.timestamp,
                m.sender,
                c.name,
                m.content,
                m.is_from_me,
                c.jid,
                m.id,
                m.media_type
            FROM messages m
            JOIN chats c ON m.chat_jid = c.jid
            WHERE m.sender = ? OR c.jid = ?
            ORDER BY m.timestamp DESC
            LIMIT 1
        """, (jid, jid))
        
        msg_data = cursor.fetchone()
        
        if not msg_data:
            return None
            
        message = Message(
            timestamp=datetime.fromisoformat(msg_data[0]),
            sender=msg_data[1],
            chat_name=msg_data[2],
            content=msg_data[3],
            is_from_me=msg_data[4],
            chat_jid=msg_data[5],
            id=msg_data[6],
            media_type=msg_data[7]
        )
        
        return format_message(message)
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return None
    finally:
        if 'conn' in locals():
            conn.close()


def get_chat(chat_jid: str, include_last_message: bool = True) -> Optional[Chat]:
    """Get chat metadata by JID."""
    try:
        conn = _connect()
        cursor = conn.cursor()
        
        query = """
            SELECT 
                c.jid,
                c.name,
                c.last_message_time,
                m.content as last_message,
                m.sender as last_sender,
                m.is_from_me as last_is_from_me
            FROM chats c
        """
        
        if include_last_message:
            query += """
                LEFT JOIN messages m ON c.jid = m.chat_jid 
                AND c.last_message_time = m.timestamp
            """
            
        query += " WHERE c.jid = ?"
        
        cursor.execute(query, (chat_jid,))
        chat_data = cursor.fetchone()
        
        if not chat_data:
            return None
            
        return Chat(
            jid=chat_data[0],
            name=chat_data[1],
            last_message_time=datetime.fromisoformat(chat_data[2]) if chat_data[2] else None,
            last_message=chat_data[3],
            last_sender=chat_data[4],
            last_is_from_me=chat_data[5]
        )
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return None
    finally:
        if 'conn' in locals():
            conn.close()


def get_direct_chat_by_contact(sender_phone_number: str) -> Optional[Chat]:
    """Get chat metadata by sender phone number."""
    try:
        conn = _connect()
        cursor = conn.cursor()
        
        cursor.execute("""
            SELECT 
                c.jid,
                c.name,
                c.last_message_time,
                m.content as last_message,
                m.sender as last_sender,
                m.is_from_me as last_is_from_me
            FROM chats c
            LEFT JOIN messages m ON c.jid = m.chat_jid 
                AND c.last_message_time = m.timestamp
            WHERE c.jid = ?
            LIMIT 1
        """, (f"{_normalize_phone(sender_phone_number)}@s.whatsapp.net",))
        
        chat_data = cursor.fetchone()
        
        if not chat_data:
            return None
            
        return Chat(
            jid=chat_data[0],
            name=chat_data[1],
            last_message_time=datetime.fromisoformat(chat_data[2]) if chat_data[2] else None,
            last_message=chat_data[3],
            last_sender=chat_data[4],
            last_is_from_me=chat_data[5]
        )
        
    except sqlite3.Error as e:
        print(f"Database error: {e}")
        return None
    finally:
        if 'conn' in locals():
            conn.close()

def _send(payload: Dict[str, Any]) -> Tuple[bool, str]:
    try:
        status, body = _bridge_post("send", payload)
        if status == 200:
            return body.get("success", False), body.get("message", "Unknown response")
        return False, f"Error: HTTP {status} - {body.get('message', '')}"
    except requests.RequestException as e:
        return False, f"Request error (is the bridge running?): {e}"
    except Exception as e:
        return False, f"Unexpected error: {e}"


def send_message(recipient: str, message: str) -> Tuple[bool, str]:
    if not recipient:
        return False, "Recipient must be provided"
    return _send({"recipient": recipient, "message": message})


def send_file(recipient: str, media_path: str) -> Tuple[bool, str]:
    if not recipient:
        return False, "Recipient must be provided"
    try:
        real_path = resolve_outbox_path(media_path)
    except ValueError as e:
        return False, str(e)
    return _send({"recipient": recipient, "media_path": real_path})


def send_audio_message(recipient: str, media_path: str) -> Tuple[bool, str]:
    if not recipient:
        return False, "Recipient must be provided"
    try:
        real_path = resolve_outbox_path(media_path)
    except ValueError as e:
        return False, str(e)

    if real_path.endswith(".ogg"):
        return _send({"recipient": recipient, "media_path": real_path})

    # Convert inside the outbox (the only place the bridge sends from) and
    # remove the temporary file afterwards.
    try:
        converted = audio.convert_to_opus_ogg_temp(real_path, temp_dir=OUTBOX_DIR)
    except Exception as e:
        return False, f"Error converting file to opus ogg. You likely need to install ffmpeg: {e}"
    try:
        return _send({"recipient": recipient, "media_path": converted})
    finally:
        try:
            os.unlink(converted)
        except OSError:
            pass


def download_media(message_id: str, chat_jid: str) -> Optional[str]:
    """Download media from a message and return the local file path.

    Args:
        message_id: The ID of the message containing the media
        chat_jid: The JID of the chat containing the message

    Returns:
        The local file path if download was successful, None otherwise
    """
    try:
        status, body = _bridge_post("download", {"message_id": message_id, "chat_jid": chat_jid})
        if status == 200 and body.get("success", False):
            return body.get("path")
        print(f"Download failed: HTTP {status} - {body.get('message', 'Unknown error')}")
        return None
    except requests.RequestException as e:
        print(f"Request error (is the bridge running?): {e}")
        return None
    except Exception as e:
        print(f"Unexpected error: {e}")
        return None
