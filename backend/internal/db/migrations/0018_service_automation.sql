-- Queue ownership is per chapter; historical duplicate rows are superseded.
DELETE FROM downloads WHERE id NOT IN (SELECT MAX(id) FROM downloads GROUP BY chapter_id);
CREATE UNIQUE INDEX idx_downloads_unique_chapter ON downloads(chapter_id);
CREATE TABLE automation_policies (
    scope_id INTEGER PRIMARY KEY, -- 0 is global; other IDs refer to media
    policy_json TEXT NOT NULL
);
CREATE TABLE automation_actions (
    chapter_id INTEGER PRIMARY KEY REFERENCES chapters(id) ON DELETE CASCADE,
    completed_at INTEGER NOT NULL
);
CREATE TABLE service_schedule (
    name TEXT PRIMARY KEY,
    last_run INTEGER NOT NULL
);
