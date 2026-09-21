package automation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
	"tsunagu/backend/internal/db"
	"tsunagu/backend/internal/db/sqlcgen"
	"tsunagu/backend/internal/download"
)

func fixture(t *testing.T) (*Manager, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	conn, err := db.Open(filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_, err = conn.Exec(`INSERT INTO media(id,external_id,content_type,title,added_at) VALUES(1,'series','manga','Series',CURRENT_TIMESTAMP); INSERT INTO chapters(id,media_id,external_id,source_order) VALUES(1,1,'one',0),(2,1,'two',1),(3,1,'three',2)`)
	if err != nil {
		t.Fatal(err)
	}
	q := sqlcgen.New(conn)
	return New(conn, q, download.New(q, nil, root, "", "loose")), conn
}
func TestInheritedPolicyAndIdempotentAhead(t *testing.T) {
	m, conn := fixture(t)
	ctx := context.Background()
	if err := m.SetPolicy(ctx, 0, []byte(`{"downloadAhead":2,"deleteOnRead":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := m.SetPolicy(ctx, 1, []byte(`{"deleteOnRead":false}`)); err != nil {
		t.Fatal(err)
	}
	p, err := m.Policy(ctx, 1)
	if err != nil || p.DownloadAhead != 2 || p.DeleteOnRead {
		t.Fatalf("%+v %v", p, err)
	}
	for i := 0; i < 3; i++ {
		if err := m.ChapterEvent(ctx, 1, 1, false); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := conn.QueryRow("SELECT count(*) FROM downloads").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("queued=%d", count)
	}
	if err := m.SetPolicy(ctx, 1, []byte(`{"downloadAhead":-1}`)); err == nil {
		t.Fatal("accepted invalid policy")
	}
	if err := m.ChapterEvent(ctx, 999, 1, true); err == nil {
		t.Fatal("accepted unrelated media")
	}
}
func TestDelayedDeletionSurvivesManagerRestartAndRechecksPolicy(t *testing.T) {
	m, conn := fixture(t)
	ctx := context.Background()
	now := time.Unix(100000, 0)
	m.Now = func() time.Time { return now }
	if err := m.SetPolicy(ctx, 0, []byte(`{"deleteOnRead":true,"deleteDelayHours":1}`)); err != nil {
		t.Fatal(err)
	}
	_, err := conn.Exec(`INSERT INTO reading_progress(media_id,chapter_id,progress,completed) VALUES(1,1,1,1)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.ChapterEvent(ctx, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	conn.QueryRow("SELECT count(*) FROM automation_actions").Scan(&count)
	if count != 1 {
		t.Fatal("deleted too early")
	}
	restarted := New(conn, m.q, m.dm)
	restarted.Now = func() time.Time { return now.Add(2 * time.Hour) }
	if err := restarted.SetPolicy(ctx, 1, []byte(`{"deleteOnRead":false}`)); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	conn.QueryRow("SELECT count(*) FROM automation_actions").Scan(&count)
	if count != 0 {
		t.Fatal("stale action retained")
	}
}

func TestRefreshCatchupRespectsPauseAndFailureBackoff(t *testing.T) {
	m, _ := fixture(t)
	ctx := context.Background()
	now := time.Unix(100000, 0)
	m.Now = func() time.Time { return now }
	if err := m.SetPolicy(ctx, 0, []byte(`{"refreshInterval":"daily"}`)); err != nil {
		t.Fatal(err)
	}
	count := 0
	m.Refresh = func(context.Context, int64) error { count++; return nil }
	if err := m.RefreshDue(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.RefreshDue(ctx); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate refreshes: %d", count)
	}
	now = now.Add(48 * time.Hour)
	if err := m.SetPolicy(ctx, 1, []byte(`{"pauseUpdates":true}`)); err != nil {
		t.Fatal(err)
	}
	m.RefreshDue(ctx)
	if count != 1 {
		t.Fatal("paused title refreshed")
	}
	if err := m.SetPolicy(ctx, 1, []byte(`{"refreshInterval":"global"}`)); err != nil {
		t.Fatal(err)
	}
	m.RefreshDue(ctx)
	if count != 2 {
		t.Fatal("overdue refresh did not catch up")
	}
	now = now.Add(48 * time.Hour)
	m.Refresh = func(context.Context, int64) error { count++; return errors.New("offline") }
	if err := m.RefreshDue(ctx); err == nil {
		t.Fatal("provider failure was lost")
	}
	if err := m.RefreshDue(ctx); err != nil || count != 3 {
		t.Fatal("failure retried without backoff")
	}
	now = now.Add(5 * time.Minute)
	if err := m.RefreshDue(ctx); err == nil || count != 4 {
		t.Fatal("failed refresh did not retry")
	}
}

func TestFutureDeletionActionsDoNotStarveDueActions(t *testing.T) {
	m, conn := fixture(t)
	ctx := context.Background()
	m.Now = func() time.Time { return time.Unix(100000, 0) }
	if err := m.SetPolicy(ctx, 0, []byte(`{"deleteOnRead":true,"deleteDelayHours":100}`)); err != nil {
		t.Fatal(err)
	}
	for id := 4; id < 110; id++ {
		if _, err := conn.Exec(`INSERT INTO chapters(id,media_id,external_id) VALUES(?,1,?);`, id, fmt.Sprintf("c%d", id)); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`INSERT INTO reading_progress(media_id,chapter_id,progress,completed) VALUES(1,?,1,1);`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(`INSERT INTO automation_actions(chapter_id,completed_at) VALUES(?,90000)`, id); err != nil {
			t.Fatal(err)
		}
	}
	// An unread title has a stale action that must be discarded even when more
	// than the catch-up limit of earlier actions is not yet due.
	if _, err := conn.Exec(`INSERT INTO automation_actions(chapter_id,completed_at) VALUES(1,99999)`); err != nil {
		t.Fatal(err)
	}
	if err := m.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(`SELECT count(*) FROM automation_actions WHERE chapter_id=1`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("stale action starved behind future actions")
	}
}
