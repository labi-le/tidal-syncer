-- 0004_track_genre.sql records each track's genres as a semicolon-joined string
-- so the library can be queried by genre via SQL (e.g. WHERE genre LIKE '%Metal%').
-- The FLAC files keep their separate Vorbis GENRE comments unchanged; this column
-- mirrors them only for querying. Existing rows are backfilled in Go from each
-- file's tags on the next sync, because SQL cannot read the audio files.

ALTER TABLE tracks ADD COLUMN genre TEXT NOT NULL DEFAULT '';
