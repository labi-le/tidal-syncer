-- 0002_track_requested_quality.sql records the quality tier requested at download
-- time so the sync engine can skip a track whose best available master was already
-- requested, instead of re-downloading every sub-request track on each cycle.

ALTER TABLE tracks ADD COLUMN requested_quality TEXT NOT NULL DEFAULT '';

-- Backfill existing rows: assume a done track was requested at the tier it
-- obtained, so already-hi-res downloads are never re-fetched even once.
UPDATE tracks SET requested_quality = obtained_quality WHERE status = 'done';
