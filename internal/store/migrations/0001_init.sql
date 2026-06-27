-- 0001_init.sql initial schema for the tidal-syncer local cache.
-- schema_migrations is bootstrapped by the Go migration runner, not here.

CREATE TABLE IF NOT EXISTS tokens (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    access_token  TEXT NOT NULL,
    refresh_token TEXT NOT NULL,
    expires_at    INTEGER NOT NULL,
    user_id       TEXT NOT NULL,
    country_code  TEXT NOT NULL,
    session_id    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tracks (
    tidal_id         TEXT PRIMARY KEY,
    isrc             TEXT NOT NULL DEFAULT '',
    album_id         TEXT NOT NULL DEFAULT '',
    path             TEXT NOT NULL DEFAULT '',
    obtained_quality TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL CHECK (status IN ('pending', 'done', 'failed')),
    updated_at       INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_tracks_status ON tracks (status);

CREATE TABLE IF NOT EXISTS favorites_snapshot (
    kind     TEXT NOT NULL,
    tidal_id TEXT NOT NULL,
    name     TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (kind, tidal_id)
);
