package download

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"tsunagu/backend/internal/db"
	"tsunagu/backend/internal/db/sqlcgen"
)

func TestQueueDeduplicatesAndOnlyOneWorkerClaims(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, err = conn.Exec(`INSERT INTO media(id,external_id,content_type,title) VALUES(1,'series','anime','Series'); INSERT INTO chapters(id,media_id,external_id) VALUES(1,1,'episode')`)
	if err != nil {
		t.Fatal(err)
	}
	q := sqlcgen.New(conn)
	ctx := context.Background()
	job, err := q.EnqueueDownload(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			same, err := q.EnqueueDownload(ctx, 1)
			if err != nil {
				t.Error(err)
				return
			}
			if same.ID != job.ID {
				t.Error("duplicate queue row")
			}
			claimed, err := q.ClaimDownload(ctx, job.ID)
			if err != nil {
				t.Error(err)
			}
			if claimed {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("claims=%d", wins.Load())
	}
	m := New(q, nil, t.TempDir(), "", "loose")
	m.Shutdown()
	m.Shutdown()
}
