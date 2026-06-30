-- 0003_favorites_added_at.sql records when each favorite was added on TIDAL so the
-- snapshot can be read back in favorite-add order. Only the favorites-tracks path
-- carries a date; rows pulled in via an album or playlist stay empty.

ALTER TABLE favorites_snapshot ADD COLUMN added_at TEXT NOT NULL DEFAULT '';
