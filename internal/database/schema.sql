-- Schema for goober-bot persistence.
-- This file is for documentation; the schema is also embedded in db.go
-- and applied automatically on startup.

CREATE TABLE IF NOT EXISTS schedules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    chat_id         INTEGER NOT NULL UNIQUE,
    cron_expression TEXT    NOT NULL,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    last_run        TEXT
);

CREATE TABLE IF NOT EXISTS user_config (
    chat_id         INTEGER PRIMARY KEY,
    station_code    TEXT    NOT NULL DEFAULT 'KBWI',
    city            TEXT    NOT NULL DEFAULT 'Baltimore',
    state           TEXT    NOT NULL DEFAULT 'MD',
    timezone_name   TEXT    NOT NULL DEFAULT 'ET',
    timezone_offset INTEGER NOT NULL DEFAULT -14400
);
