package sync

import (
	"context"
	"path/filepath"
	"testing"
	"tsunagu/backend/internal/contentfilter"
	"tsunagu/backend/internal/db"
	"tsunagu/backend/internal/db/sqlcgen"
)

func TestLibrarySourceOverridesApplyBeforePagination(t *testing.T) {
	ctx := context.Background()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = conn.Exec(`PRAGMA foreign_keys=OFF;
 INSERT INTO media(id,extension_id,external_id,content_type,title,added_at,content_block_rank) VALUES
 (1,11,'one','manga','One',CURRENT_TIMESTAMP,1),
 (2,22,'two','manga','Two',CURRENT_TIMESTAMP,1),
 (3,33,'three','manga','Three',CURRENT_TIMESTAMP,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	value := contentfilter.SourceOverrides{Enabled: true, Allowed: []int64{11}, Blocked: []int64{33}}
	if err = contentfilter.SaveSourceOverrides(ctx, conn, value); err != nil {
		t.Fatal(err)
	}
	sy := New(conn, sqlcgen.New(conn), t.TempDir(), t.TempDir())
	rows, total, err := sy.QueryLibrary(ctx, LibraryQuery{ContentFilterRank: 1, Limit: 1})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != 1 {
		t.Fatalf("overrides: rows=%+v total=%d err=%v", rows, total, err)
	}
	value.Enabled = false
	if err = contentfilter.SaveSourceOverrides(ctx, conn, value); err != nil {
		t.Fatal(err)
	}
	rows, total, err = sy.QueryLibrary(ctx, LibraryQuery{ContentFilterRank: 1, Limit: 1})
	if err != nil || total != 1 || len(rows) != 1 || rows[0].ID != 3 {
		t.Fatalf("disabled: rows=%+v total=%d err=%v", rows, total, err)
	}
	value.Allowed = []int64{33}
	if err = contentfilter.SaveSourceOverrides(ctx, conn, value); err == nil {
		t.Fatal("accepted conflicting allow and block")
	}
}
