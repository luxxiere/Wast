import sqlite3
import os
from typing import List, Optional

try:
    from src.config import cfg
except ImportError:
    from config import cfg


def get_connection() -> sqlite3.Connection:
    os.makedirs(os.path.dirname(cfg.db_path), exist_ok=True)
    conn = sqlite3.connect(cfg.db_path)
    conn.row_factory = sqlite3.Row
    conn.execute("""
        CREATE TABLE IF NOT EXISTS clients (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            name TEXT NOT NULL,
            country TEXT NOT NULL,
            client_uuid TEXT NOT NULL,
            email TEXT NOT NULL,
            token TEXT NOT NULL,
            created_at TEXT NOT NULL
        )
    """)
    conn.commit()
    return conn


def add_client(name: str, country: str, client_uuid: str, email: str, token: str) -> int:
    with get_connection() as conn:
        cursor = conn.execute(
            """
            INSERT INTO clients (name, country, client_uuid, email, token, created_at)
            VALUES (?, ?, ?, ?, ?, datetime('now'))
            """,
            (name, country, client_uuid, email, token)
        )
        conn.commit()
        return cursor.lastrowid


def get_clients() -> List[sqlite3.Row]:
    with get_connection() as conn:
        return conn.execute(
            "SELECT id, name, country, client_uuid, email, token, created_at FROM clients ORDER BY id"
        ).fetchall()


def get_client_by_id(client_id: int) -> Optional[sqlite3.Row]:
    with get_connection() as conn:
        return conn.execute(
            "SELECT id, name, country, client_uuid, email, token, created_at FROM clients WHERE id=?",
            (client_id,)
        ).fetchone()


def delete_client(client_id: int) -> bool:
    with get_connection() as conn:
        cursor = conn.execute("DELETE FROM clients WHERE id=?", (client_id,))
        conn.commit()
        return cursor.rowcount > 0


def count_clients() -> int:
    with get_connection() as conn:
        row = conn.execute("SELECT COUNT(*) FROM clients").fetchone()
        return row[0] if row else 0
