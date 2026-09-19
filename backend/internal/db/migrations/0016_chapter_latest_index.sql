-- Avoids an O(n^2) full sort per chapter in LatestChapterByMediaIDs.
CREATE INDEX idx_chapters_media_number_order ON chapters(media_id, number DESC, source_order DESC);
