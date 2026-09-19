-- Fixes an unindexed correlated-subquery scan in NextUnreadChapterByMediaIDs (measured 80s+).
CREATE INDEX idx_reading_progress_chapter_completed ON reading_progress(chapter_id, completed);
CREATE INDEX idx_chapters_media_source_order_number ON chapters(media_id, source_order, number);
