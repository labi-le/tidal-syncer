-- 0006_collection_item_cache.sql caches each favorited album/playlist's expansion
-- to track ids so a sync cycle can skip re-fetching the item list from TIDAL when
-- it has not changed. Albums are immutable (version stays ''); playlists carry
-- their lastUpdated instant as the invalidation version.

CREATE TABLE IF NOT EXISTS collection_cache (
    kind          TEXT    NOT NULL,
    collection_id TEXT    NOT NULL,
    version       TEXT    NOT NULL DEFAULT '',
    n_tracks      INTEGER NOT NULL,
    cached_at     INTEGER NOT NULL,
    PRIMARY KEY (kind, collection_id)
);

CREATE TABLE IF NOT EXISTS collection_track (
    kind          TEXT    NOT NULL,
    collection_id TEXT    NOT NULL,
    track_id      INTEGER NOT NULL,
    PRIMARY KEY (kind, collection_id, track_id),
    FOREIGN KEY (kind, collection_id) REFERENCES collection_cache (kind, collection_id) ON DELETE CASCADE
) WITHOUT ROWID;
