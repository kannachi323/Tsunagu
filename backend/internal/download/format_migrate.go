package download

import (
	"archive/zip"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"

	"tsunagu/backend/internal/db/sqlcgen"
	"tsunagu/backend/internal/localsource"
)

type MigrateFormatResult struct {
	ChaptersMigrated int64
	PagesMigrated    int64
}

// MigrateMangaFormat repacks every already-downloaded manga chapter between
// loose image files and a single CBZ archive, to match a newly selected
// manga_download_format. Chapters already in the target format are skipped.
func (m *Manager) MigrateMangaFormat(ctx context.Context, target string) (MigrateFormatResult, error) {
	if target != "loose" && target != "cbz" {
		return MigrateFormatResult{}, fmt.Errorf("unknown manga download format %q", target)
	}

	rows, err := m.q.ListAllMangaPagePaths(ctx)
	if err != nil {
		return MigrateFormatResult{}, fmt.Errorf("listing manga pages: %w", err)
	}

	byChapter := map[int64][]sqlcgen.MangaPage{}
	for _, r := range rows {
		byChapter[r.ChapterID] = append(byChapter[r.ChapterID], r)
	}

	var res MigrateFormatResult
	for chapterID, pages := range byChapter {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if len(pages) == 0 || !pages[0].LocalPath.Valid {
			continue
		}
		sort.Slice(pages, func(i, j int) bool { return pages[i].PageNumber < pages[j].PageNumber })

		_, _, isArchive := localsource.ParseZipPagePath(pages[0].LocalPath.String)
		if (target == "cbz") == isArchive {
			continue
		}

		var migrateErr error
		if target == "cbz" {
			migrateErr = m.packChapterToCBZ(ctx, chapterID, pages)
		} else {
			migrateErr = m.unpackChapterFromCBZ(ctx, chapterID, pages)
		}
		if migrateErr != nil {
			log.Printf("download: migrating chapter %d to %s failed: %v", chapterID, target, migrateErr)
			continue
		}
		res.ChaptersMigrated++
		res.PagesMigrated += int64(len(pages))
	}

	return res, nil
}

func (m *Manager) packChapterToCBZ(ctx context.Context, chapterID int64, pages []sqlcgen.MangaPage) error {
	chapterDir := filepath.Dir(pages[0].LocalPath.String)
	archivePath := chapterDir + ".cbz"
	tmpPath := archivePath + ".part"

	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("creating archive: %w", err)
	}
	zw := zip.NewWriter(f)

	entries := make([]string, len(pages))
	for i, p := range pages {
		entryName := filepath.Base(p.LocalPath.String)
		entries[i] = entryName

		src, err := os.Open(p.LocalPath.String)
		if err != nil {
			zw.Close()
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("opening page %d: %w", p.PageNumber, err)
		}
		w, err := zw.CreateHeader(&zip.FileHeader{Name: entryName, Method: zip.Store})
		if err == nil {
			_, err = io.Copy(w, src)
		}
		src.Close()
		if err != nil {
			zw.Close()
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("packing page %d: %w", p.PageNumber, err)
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("closing archive: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("closing archive: %w", err)
	}
	if err := os.Rename(tmpPath, archivePath); err != nil {
		return fmt.Errorf("finalizing archive: %w", err)
	}

	for i, p := range pages {
		if err := m.q.SetMangaPagePath(ctx, sqlcgen.SetMangaPagePathParams{
			LocalPath:  sql.NullString{String: localsource.ZipPagePath(archivePath, entries[i]), Valid: true},
			ChapterID:  chapterID,
			PageNumber: p.PageNumber,
		}); err != nil {
			return fmt.Errorf("updating page %d: %w", p.PageNumber, err)
		}
	}

	for _, p := range pages {
		_ = os.Remove(p.LocalPath.String)
	}
	m.removeEmptyDirs(chapterDir)
	return nil
}

func (m *Manager) unpackChapterFromCBZ(ctx context.Context, chapterID int64, pages []sqlcgen.MangaPage) error {
	archivePath, _, ok := localsource.ParseZipPagePath(pages[0].LocalPath.String)
	if !ok {
		return fmt.Errorf("chapter %d page 1 is not archived", chapterID)
	}
	chapterDir := archivePath[:len(archivePath)-len(filepath.Ext(archivePath))]
	if err := os.MkdirAll(chapterDir, 0o755); err != nil {
		return fmt.Errorf("creating chapter dir: %w", err)
	}

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("opening archive: %w", err)
	}
	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byName[f.Name] = f
	}

	for _, p := range pages {
		_, entryName, ok := localsource.ParseZipPagePath(p.LocalPath.String)
		if !ok {
			continue
		}
		zf, ok := byName[entryName]
		if !ok {
			zr.Close()
			return fmt.Errorf("page %d missing from archive", p.PageNumber)
		}
		rc, err := zf.Open()
		if err != nil {
			zr.Close()
			return fmt.Errorf("reading page %d: %w", p.PageNumber, err)
		}
		localPath := filepath.Join(chapterDir, entryName)
		dst, err := os.Create(localPath)
		if err != nil {
			rc.Close()
			zr.Close()
			return fmt.Errorf("writing page %d: %w", p.PageNumber, err)
		}
		_, copyErr := io.Copy(dst, rc)
		rc.Close()
		dst.Close()
		if copyErr != nil {
			zr.Close()
			return fmt.Errorf("writing page %d: %w", p.PageNumber, copyErr)
		}

		if err := m.q.SetMangaPagePath(ctx, sqlcgen.SetMangaPagePathParams{
			LocalPath:  sql.NullString{String: localPath, Valid: true},
			ChapterID:  chapterID,
			PageNumber: p.PageNumber,
		}); err != nil {
			zr.Close()
			return fmt.Errorf("updating page %d: %w", p.PageNumber, err)
		}
	}

	if err := zr.Close(); err != nil {
		return fmt.Errorf("closing archive: %w", err)
	}
	_ = os.Remove(archivePath)
	return nil
}
