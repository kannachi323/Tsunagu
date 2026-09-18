-- Tracks the last time an AniList search for a media item came back with no
-- confident match, so the backfill stops re-searching the same title every
-- single run and only retries it after a cooldown window.
ALTER TABLE media ADD COLUMN metadata_search_failed_at TIMESTAMP;
