package localsource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tsunagu/backend/internal/db/sqlcgen"
)

// kindDirName is the canonical on-disk folder name for a content type —
// the reverse of dirKind, matching Moku's src/lib/core/localImport.ts.
var kindDirName = map[string]string{
	"manga": "manga",
	"anime": "anime",
	"novel": "novels",
}

// DeleteLocalSeries permanently removes a local-source title: its files on
// disk under {local_source_dir}/<kind>/<title>/ and every DB row tied to it
// (chapters, genres/tags links, metadata links and track links all cascade
// off the media row; downloads don't cascade so they're cleared first).
func (s *Scanner) DeleteLocalSeries(ctx context.Context, mediaID int64) error {
	media, err := s.q.GetMedia(ctx, mediaID)
	if err != nil {
		return fmt.Errorf("get media %d: %w", mediaID, err)
	}
	if media.ExtensionID.Valid || media.ExtensionName != "Local" {
		return fmt.Errorf("media %d is not a local-source series", mediaID)
	}

	chapters, err := s.q.ListChaptersByMedia(ctx, mediaID)
	if err != nil {
		return fmt.Errorf("list chapters for media %d: %w", mediaID, err)
	}
	if len(chapters) > 0 {
		ids := make([]int64, len(chapters))
		for i, c := range chapters {
			ids[i] = c.ID
		}
		if err := s.q.DeleteDownloadsByChapters(ctx, ids); err != nil {
			return fmt.Errorf("delete downloads for media %d: %w", mediaID, err)
		}
	}

	if err := s.q.DeleteMedia(ctx, mediaID); err != nil {
		return fmt.Errorf("delete media %d: %w", mediaID, err)
	}

	dirName, ok := kindDirName[media.ContentType]
	if !ok {
		return nil
	}
	titleDir := filepath.Join(s.LocalDir(), dirName, media.Title)
	if err := os.RemoveAll(titleDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %q: %w", titleDir, err)
	}
	return nil
}

func (s *Scanner) RenameLocalSeries(ctx context.Context, mediaID int64, newTitle string) error {
	media, err := s.q.GetMedia(ctx, mediaID)
	if err != nil {
		return fmt.Errorf("get media %d: %w", mediaID, err)
	}
	if media.ExtensionID.Valid || media.ExtensionName != "Local" {
		return fmt.Errorf("media %d is not a local-source series", mediaID)
	}
	oldTitle := media.Title
	if newTitle == "" || newTitle == oldTitle {
		return nil
	}

	dirName, ok := kindDirName[media.ContentType]
	if !ok {
		return fmt.Errorf("unknown content type %q", media.ContentType)
	}

	oldDir := filepath.Join(s.LocalDir(), dirName, oldTitle)
	newDir := filepath.Join(s.LocalDir(), dirName, newTitle)
	if _, err := os.Stat(newDir); err == nil {
		return fmt.Errorf("a title named %q already exists", newTitle)
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("rename %q to %q: %w", oldDir, newDir, err)
	}

	oldExternalID := media.ExternalID
	newExternalID := "local:" + strings.ToLower(dirName) + "/" + newTitle

	if _, err := s.q.RenameLocalMedia(ctx, sqlcgen.RenameLocalMediaParams{
		Title:      newTitle,
		ExternalID: newExternalID,
		ID:         mediaID,
	}); err != nil {
		return fmt.Errorf("rename media %d: %w", mediaID, err)
	}

	chapters, err := s.q.ListChaptersByMedia(ctx, mediaID)
	if err != nil {
		return fmt.Errorf("list chapters for media %d: %w", mediaID, err)
	}
	oldPrefix := oldExternalID + "/"
	for _, c := range chapters {
		if !strings.HasPrefix(c.ExternalID, oldPrefix) {
			continue
		}
		newChapterExternalID := newExternalID + "/" + strings.TrimPrefix(c.ExternalID, oldPrefix)
		if err := s.q.UpdateChapterExternalID(ctx, sqlcgen.UpdateChapterExternalIDParams{
			ExternalID: newChapterExternalID,
			ID:         c.ID,
		}); err != nil {
			return fmt.Errorf("rename chapter %d: %w", c.ID, err)
		}
	}
	return nil
}
