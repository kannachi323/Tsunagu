package download

import (
	"context"
	"database/sql"
	"fmt"
	"google.golang.org/grpc"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"tsunagu/backend/internal/db"
	"tsunagu/backend/internal/db/sqlcgen"
	"tsunagu/backend/internal/sandbox"
	pb "tsunagu/backend/internal/sandbox/gen/sandbox/v1"
)

type videoSourceFixture struct {
	pb.UnimplementedExtensionServiceServer
	resolutions atomic.Int32
}

func (f *videoSourceFixture) GetVideoStream(context.Context, *pb.EpisodeRequest) (*pb.StreamInfo, error) {
	return &pb.StreamInfo{StreamUrl: fmt.Sprintf("https://source.example/video?token=%d", f.resolutions.Add(1)), Headers: map[string]string{"Referer": "https://source.example/"}}, nil
}

type engineFunc func(context.Context, VideoRequest, func(VideoProgress)) error

func (f engineFunc) Download(ctx context.Context, r VideoRequest, p func(VideoProgress)) error {
	return f(ctx, r, p)
}

func TestNativeQueueCancellationInterruptionAndFreshResolution(t *testing.T) {
	for _, outcome := range []string{"cancel", "interrupt", "disk-error"} {
		t.Run(outcome, func(t *testing.T) {
			interrupt := outcome == "interrupt"
			root := t.TempDir()
			conn, err := db.Open(filepath.Join(root, "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			_, err = conn.Exec(`INSERT INTO repositories(id,index_url) VALUES(1,'fixture'); INSERT INTO extensions(id,repository_id,package_name,name,version,content_type,lang,apk_url) VALUES(1,1,'fixture','Fixture','1','anime','en','fixture'); INSERT INTO media(id,extension_id,external_id,content_type,title) VALUES(1,1,'series','anime','Series'); INSERT INTO chapters(id,media_id,external_id) VALUES(1,1,'episode')`)
			if err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer()
			defer server.Stop()
			source := &videoSourceFixture{}
			pb.RegisterExtensionServiceServer(server, source)
			go server.Serve(listener)
			sc, err := sandbox.NewEmbedded(listener.Addr().String(), "01234567890123456789012345678901")
			if err != nil {
				t.Fatal(err)
			}
			defer sc.Shutdown()
			q := sqlcgen.New(conn)
			job, err := q.EnqueueDownload(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			entered := make(chan VideoRequest, 1)
			manager := New(q, sc, root, root, "loose")
			manager.SetVideoEngine(engineFunc(func(ctx context.Context, r VideoRequest, p func(VideoProgress)) error {
				if err := os.WriteFile(r.OutputPath, []byte("partial"), 0600); err != nil {
					return err
				}
				entered <- r
				if outcome == "disk-error" {
					return syscall.ENOSPC
				}
				<-ctx.Done()
				return ctx.Err()
			}))
			manager.Start()
			defer manager.Shutdown()
			manager.Wake()
			var request VideoRequest
			select {
			case request = <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker never started")
			}
			if request.Headers["Referer"] != "https://source.example/" {
				t.Fatal("source headers lost")
			}
			if outcome == "disk-error" {
				deadline := time.Now().Add(5 * time.Second)
				var status, failure string
				for time.Now().Before(deadline) {
					_ = conn.QueryRow("SELECT status,COALESCE(error,'') FROM downloads WHERE id=?", job.ID).Scan(&status, &failure)
					if status == "failed" {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if status != "failed" || failure == "" {
					t.Fatalf("disk failure lost: %s %s", status, failure)
				}
				if _, err := os.Stat(request.OutputPath); !os.IsNotExist(err) {
					t.Fatal("failed transfer kept partial data")
				}
				return
			}
			stopped := make(chan struct{})
			go func() {
				if interrupt {
					manager.Shutdown()
				} else {
					if err := manager.Cancel(context.Background(), 1); err != nil {
						t.Error(err)
					}
				}
				close(stopped)
			}()
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown/cancel did not drain worker")
			}
			if _, err := os.Stat(request.OutputPath); !os.IsNotExist(err) {
				t.Fatal("partial file survived")
			}
			var status string
			err = conn.QueryRow("SELECT status FROM downloads WHERE id=?", job.ID).Scan(&status)
			if !interrupt {
				if err != sql.ErrNoRows {
					t.Fatalf("cancel kept job: %s, %v", status, err)
				}
				return
			}
			if err != nil || status != "queued" {
				t.Fatalf("interruption was not retryable: %s %v", status, err)
			}
			resumed := New(q, sc, root, root, "loose")
			defer resumed.Shutdown()
			resumed.SetVideoEngine(engineFunc(func(ctx context.Context, r VideoRequest, p func(VideoProgress)) error {
				if r.URL == request.URL {
					return fmt.Errorf("expired URL reused")
				}
				p(VideoProgress{Fraction: 1, Bytes: 8})
				var progress float64
				if err := conn.QueryRow("SELECT progress FROM downloads WHERE id=?", job.ID).Scan(&progress); err != nil {
					return err
				}
				if progress >= 1 {
					return fmt.Errorf("job completed before finalization")
				}
				return os.WriteFile(r.OutputPath, []byte("validated fixture media"), 0600)
			}))
			resumed.Start()
			resumed.Wake()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if err := conn.QueryRow("SELECT status FROM downloads WHERE id=?", job.ID).Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status == "done" {
					break
				}
				if status == "failed" {
					t.Fatal("retry failed")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if status != "done" || source.resolutions.Load() != 2 {
				t.Fatalf("retry: %s, resolutions=%d", status, source.resolutions.Load())
			}
			local := filepath.Join(filepath.Dir(request.OutputPath), "episode.mp4")
			if data, err := os.ReadFile(local); err != nil || string(data) != "validated fixture media" {
				t.Fatalf("not finalized: %v", err)
			}
		})
	}
}
