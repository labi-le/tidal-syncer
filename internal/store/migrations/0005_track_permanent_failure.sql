-- 0005_track_permanent_failure.sql records whether a failed track failed
-- permanently: its track or album is gone (HTTP 404), its stream is DRM
-- encrypted, its manifest is an unsupported kind, or TIDAL grants only a
-- sub-lossless tier for it. The sync engine skips permanent failures instead of
-- re-attempting them every cycle; a higher quality.request or an explicit
-- --retry-failed re-attempts them. Transient failures (disk full, ffmpeg,
-- expired stream URL, 5xx) keep permanent = 0 and are retried on the next run.
--
-- Existing rows default to 0, so any pre-migration failure is retried once more
-- and then re-classified from its fresh failure cause.

ALTER TABLE tracks ADD COLUMN permanent INTEGER NOT NULL DEFAULT 0;
