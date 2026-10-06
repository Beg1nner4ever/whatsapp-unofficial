import os
import sqlite3
import sys

import pytest

sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))

import whatsapp  # noqa: E402


@pytest.fixture
def store(tmp_path, monkeypatch):
    """A bridge-like store with a messages DB and an outbox, wired into the module."""
    outbox = tmp_path / "outbox"
    outbox.mkdir()
    db_path = tmp_path / "messages.db"

    conn = sqlite3.connect(db_path)
    conn.executescript(
        """
        CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP);
        CREATE TABLE messages (
            id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP,
            is_from_me BOOLEAN, media_type TEXT, filename TEXT, url TEXT, media_key BLOB,
            file_sha256 BLOB, file_enc_sha256 BLOB, file_length INTEGER,
            PRIMARY KEY (id, chat_jid)
        );
        """
    )
    conn.commit()
    conn.close()

    monkeypatch.setattr(whatsapp, "STORE_DIR", str(tmp_path))
    monkeypatch.setattr(whatsapp, "MESSAGES_DB_PATH", str(db_path))
    monkeypatch.setattr(whatsapp, "OUTBOX_DIR", str(outbox))
    monkeypatch.setattr(whatsapp, "TOKEN_PATH", str(tmp_path / "bridge_token"))
    return tmp_path


def insert_chat(store, jid, name, ts="2026-01-01 10:00:00"):
    with sqlite3.connect(store / "messages.db") as conn:
        conn.execute("INSERT INTO chats VALUES (?, ?, ?)", (jid, name, ts))
