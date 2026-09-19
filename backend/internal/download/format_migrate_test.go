package download

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"tsunagu/backend/internal/db"
	"tsunagu/backend/internal/db/sqlcgen"
	"tsunagu/backend/internal/localsource"
)

func TestMigrateMangaFormatConvertsAllChapters(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "media")

	const numChapters = 5
	const pagesPerChapter = 3

	conn, err := db.Open(filepath.Join(tmp, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if _, err := conn.Exec(`INSERT INTO media (id, external_id, content_type, title) VALUES (1, 'ext:a', 'manga', 'TitleA')`); err != nil {
		t.Fatal(err)
	}

	for c := 1; c <= numChapters; c++ {
		if _, err := conn.Exec(`INSERT INTO chapters (id, media_id, external_id) VALUES (?, 1, ?)`, c, "ch"+string(rune('0'+c))); err != nil {
			t.Fatal(err)
		}
		for p := 1; p <= pagesPerChapter; p++ {
			path := filepath.Join(root, "manga", "ExtA", "TitleA", "Ch"+string(rune('0'+c)), string(rune('0'+p))+".jpg")
			writeFile(t, path, "page-data")
			if _, err := conn.Exec(`INSERT INTO manga_pages (chapter_id, page_number, local_path) VALUES (?, ?, ?)`, c, p, path); err != nil {
				t.Fatal(err)
			}
		}
	}

	q := sqlcgen.New(conn)
	m := &Manager{q: q, mediaDir: root, downloadsDir: root}

	res, err := m.MigrateMangaFormat(context.Background(), "cbz")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if res.ChaptersMigrated != numChapters {
		t.Fatalf("chaptersMigrated = %d, want %d (failed=%d alreadyTarget=%d)", res.ChaptersMigrated, numChapters, res.ChaptersFailed, res.ChaptersAlreadyTarget)
	}
	if res.PagesMigrated != numChapters*pagesPerChapter {
		t.Fatalf("pagesMigrated = %d, want %d", res.PagesMigrated, numChapters*pagesPerChapter)
	}
	if res.ChaptersFailed != 0 {
		t.Fatalf("chaptersFailed = %d, want 0", res.ChaptersFailed)
	}

	pages, err := q.ListAllMangaPagePaths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != numChapters*pagesPerChapter {
		t.Fatalf("got %d page rows after migration, want %d", len(pages), numChapters*pagesPerChapter)
	}
	for _, p := range pages {
		archivePath, _, ok := localsource.ParseZipPagePath(p.LocalPath.String)
		if !ok {
			t.Fatalf("chapter %d page %d not archived: %q", p.ChapterID, p.PageNumber, p.LocalPath.String)
		}
		if _, err := os.Stat(archivePath); err != nil {
			t.Fatalf("archive %q missing: %v", archivePath, err)
		}
	}

	// Migrating again with the same target should be a no-op (everything
	// already at target), not a re-migration.
	res2, err := m.MigrateMangaFormat(context.Background(), "cbz")
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if res2.ChaptersMigrated != 0 || res2.ChaptersAlreadyTarget != numChapters {
		t.Fatalf("second migrate = %+v, want 0 migrated / %d already-target", res2, numChapters)
	}
}
