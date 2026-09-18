package download

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"tsunagu/backend/internal/db/sqlcgen"
	"tsunagu/backend/internal/localsource"
)

type RelocateResult struct {
	MovedFiles int64
	MovedBytes int64
	NewPath    string
}

var downloadSubdirs = []string{"manga", "anime", "novels"}

func (m *Manager) Relocate(ctx context.Context, newPath string, migrate bool) (RelocateResult, error) {
	trimmed := strings.TrimSpace(newPath)
	if trimmed == "" {
		trimmed = m.mediaDir
	}
	newAbs, err := filepath.Abs(trimmed)
	if err != nil {
		return RelocateResult{}, fmt.Errorf("resolve path: %w", err)
	}
	cur := filepath.Clean(m.DownloadsDir())
	if newAbs == cur {
		return RelocateResult{NewPath: newAbs}, nil
	}
	if isSubpath(cur, newAbs) || isSubpath(newAbs, cur) {
		return RelocateResult{}, fmt.Errorf("new downloads path must not be nested inside the current one")
	}
	if err := os.MkdirAll(newAbs, 0o755); err != nil {
		return RelocateResult{}, fmt.Errorf("create %s: %w", newAbs, err)
	}
	probe := filepath.Join(newAbs, ".tsunagu-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return RelocateResult{}, fmt.Errorf("path not writable: %w", err)
	}
	_ = os.Remove(probe)

	res := RelocateResult{NewPath: newAbs}
	if !migrate {
		return res, nil
	}

	for _, sub := range downloadSubdirs {
		src := filepath.Join(cur, sub)
		if info, statErr := os.Stat(src); statErr != nil || !info.IsDir() {
			continue
		}
		files, bytes, cpErr := copyTree(src, filepath.Join(newAbs, sub))
		res.MovedFiles += files
		res.MovedBytes += bytes
		if cpErr != nil {
			return res, fmt.Errorf("copy %s: %w", sub, cpErr)
		}
	}

	if err := m.rewritePaths(ctx, cur, newAbs); err != nil {
		return res, fmt.Errorf("rewrite database paths: %w", err)
	}

	for _, sub := range downloadSubdirs {
		_ = os.RemoveAll(filepath.Join(cur, sub))
	}
	return res, nil
}

func isSubpath(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyTree(src, dst string) (files int64, bytes int64, err error) {
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if st, statErr := os.Stat(target); statErr == nil && st.Size() == info.Size() {
			files++
			bytes += info.Size()
			return nil
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		if cpErr := copyFile(p, target); cpErr != nil {
			return cpErr
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (m *Manager) rewritePaths(ctx context.Context, oldRoot, newRoot string) error {
	remap := func(p string) (string, bool) {
		clean := filepath.Clean(p)
		for _, sub := range downloadSubdirs {
			base := filepath.Join(oldRoot, sub)
			if clean == base || strings.HasPrefix(clean, base+string(filepath.Separator)) {
				rel, err := filepath.Rel(oldRoot, clean)
				if err != nil {
					return "", false
				}
				return filepath.Join(newRoot, rel), true
			}
		}
		return "", false
	}

	pages, err := m.q.ListAllMangaPagePaths(ctx)
	if err != nil {
		return err
	}
	for _, r := range pages {
		lp := r.LocalPath.String
		if archivePath, entryName, ok := localsource.ParseZipPagePath(lp); ok {
			np, ok := remap(archivePath)
			if !ok {
				continue
			}
			if err := m.q.SetMangaPagePath(ctx, sqlcgen.SetMangaPagePathParams{
				LocalPath:  sql.NullString{String: localsource.ZipPagePath(np, entryName), Valid: true},
				ChapterID:  r.ChapterID,
				PageNumber: r.PageNumber,
			}); err != nil {
				return err
			}
			continue
		}
		np, ok := remap(lp)
		if !ok {
			continue
		}
		if err := m.q.SetMangaPagePath(ctx, sqlcgen.SetMangaPagePathParams{
			LocalPath:  sql.NullString{String: np, Valid: true},
			ChapterID:  r.ChapterID,
			PageNumber: r.PageNumber,
		}); err != nil {
			return err
		}
	}

	novels, err := m.q.ListAllNovelContentPaths(ctx)
	if err != nil {
		return err
	}
	for _, r := range novels {
		np, ok := remap(r.LocalPath.String)
		if !ok {
			continue
		}
		if err := m.q.SetNovelChapterContentPath(ctx, sqlcgen.SetNovelChapterContentPathParams{
			LocalPath: sql.NullString{String: np, Valid: true},
			ChapterID: r.ChapterID,
		}); err != nil {
			return err
		}
	}

	streams, err := m.q.ListAllEpisodeStreamPaths(ctx)
	if err != nil {
		return err
	}
	for _, r := range streams {
		np, ok := remap(r.LocalPath.String)
		if !ok {
			continue
		}
		if err := m.q.SetAnimeEpisodeStreamPath(ctx, sqlcgen.SetAnimeEpisodeStreamPathParams{
			LocalPath: sql.NullString{String: np, Valid: true},
			ChapterID: r.ChapterID,
		}); err != nil {
			return err
		}
	}
	return nil
}
