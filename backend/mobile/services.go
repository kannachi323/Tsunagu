package mobile

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"tsunagu/backend/internal/api/graph"
	"tsunagu/backend/internal/api/rest"
	"tsunagu/backend/internal/auth"
	"tsunagu/backend/internal/automation"
	"tsunagu/backend/internal/backup"
	"tsunagu/backend/internal/config"
	"tsunagu/backend/internal/contentfilter"
	"tsunagu/backend/internal/db/sqlcgen"
	"tsunagu/backend/internal/download"
	"tsunagu/backend/internal/localsource"
	"tsunagu/backend/internal/metadata"
	"tsunagu/backend/internal/sandbox"
	"tsunagu/backend/internal/streamresolve"
	syncpkg "tsunagu/backend/internal/sync"
)

func newServices(conn *sql.DB, directory, baseURL string, sc *sandbox.SupervisedClient, engine VideoEngine) (*graph.Resolver, error) {
	q := sqlcgen.New(conn)
	cfg := config.Defaults()
	cfg.DataDir, cfg.DBPath = directory, filepath.Join(directory, "tsunagu.db")
	cfg.MediaDir, cfg.JarCacheDir = filepath.Join(directory, "media"), filepath.Join(directory, "jar-cache")
	cfg.LocalSourceDir, cfg.DownloadsDir = filepath.Join(cfg.MediaDir, "local"), cfg.MediaDir
	cfg.PublicURL, cfg.CloudflareSolverMode, cfg.IdleTimeoutMin = baseURL, "disabled", 0
	cfg.SandboxExtDir, cfg.SandboxStorageDir = filepath.Join(directory, "extensions"), filepath.Join(directory, "plugin-storage")
	if err := os.MkdirAll(cfg.MediaDir, 0700); err != nil {
		return nil, err
	}
	store := config.NewStore(&cfg, q, filepath.Join(directory, "tsunagu.toml"), map[string]bool{"media_dir": true, "public_url": true, "jar_cache_dir": true, "cloudflare_solver_mode": true, "idle_timeout_minutes": true})
	if err := store.Sync(context.Background()); err != nil {
		return nil, err
	}
	// Desktop backups can contain paths outside the app sandbox. Keep valid
	// mobile relocations; normalize foreign paths before any service uses them.
	for _, setting := range []struct{ key, value, fallback string }{{"downloads_dir", cfg.DownloadsDir, cfg.MediaDir}, {"local_source_dir", cfg.LocalSourceDir, filepath.Join(cfg.MediaDir, "local")}} {
		path := setting.value
		if path == "" {
			path = setting.fallback
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil || !filepath.IsAbs(path) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			path = setting.fallback
		}
		if err := os.MkdirAll(path, 0700); err != nil {
			return nil, err
		}
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		owned, err := filepath.EvalSymlinks(directory)
		if err != nil {
			return nil, err
		}
		relative, err = filepath.Rel(owned, canonical)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s escapes app storage", setting.key)
		}
		if path != setting.value {
			if _, err := store.Set(context.Background(), setting.key, path); err != nil {
				return nil, err
			}
		}
	}
	cf, err := contentfilter.New(q)
	if err != nil {
		return nil, err
	}
	cf.SetLevel(contentfilter.ParseLevel(store.Config().ContentFilterLevel))
	store.OnChange("content_filter_level", func(context.Context) { cf.SetLevel(contentfilter.ParseLevel(store.Config().ContentFilterLevel)) })
	sy := syncpkg.New(conn, q, cfg.JarCacheDir, cfg.MediaDir)
	md := metadata.NewManager(conn, q)
	md.Work = sy.Work
	md.SetRecomputer(cf)
	sy.SetRecomputer(cf)
	sy.SetEnricher(md)
	tk := newTrackers(q, store.Config(), baseURL)
	dm := download.New(q, sc, cfg.MediaDir, cfg.DownloadsDir, store.Config().MangaDownloadFormat)
	dm.SetVideoEngine(videoAdapter{engine})
	store.OnChange("manga_download_format", func(context.Context) { dm.SetImageFormat(store.Config().MangaDownloadFormat) })
	ls := localsource.New(q, cfg.MediaDir)
	ls.Work = sy.Work
	ls.SetLocalDir(cfg.LocalSourceDir)
	ls.SetEnricher(md)
	store.OnChange("downloads_dir", func(context.Context) { dm.SetDownloadsDir(store.Config().DownloadsDir) })
	store.OnChange("local_source_dir", func(context.Context) { ls.SetLocalDir(store.Config().LocalSourceDir) })
	r := &graph.Resolver{Q: q, DB: conn, Sc: sc, Sy: sy, Md: md, Tk: tk, Dm: dm, Ls: ls, Sr: streamresolve.New(sc), Cf: cf, Cfg: store, Am: auth.New(q), MediaDir: cfg.MediaDir, Name: "Tsunagu", Version: "embedded", BuildTime: "unknown"}
	policies := automation.New(conn, q, dm)
	r.Policies = policies
	policies.Refresh = func(ctx context.Context, id int64) error {
		client, err := sc.Ensure(ctx)
		if err != nil {
			return err
		}
		_, err = sy.RefreshMetadata(ctx, client, id, true)
		return err
	}
	sy.OnProgress = policies.ChapterEvent
	sy.OnNewChapters = policies.NewChapters
	dm.OnComplete = func(id int64) {
		sy.Work.Go(func() {
			if err := policies.DownloadCompleted(sy.Work.Context, id); err != nil {
				log.Printf("automation retention: %v", err)
			}
		})
	}
	if sc.Ready() {
		dm.Start()
	}
	sy.Work.Go(func() {
		ctx := sy.Work.Context
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastRun := func(name string) time.Time {
			now := time.Now().Unix()
			_, _ = conn.ExecContext(ctx, "INSERT INTO service_schedule(name,last_run) VALUES(?,?) ON CONFLICT(name) DO NOTHING", name, now)
			_ = conn.QueryRowContext(ctx, "SELECT last_run FROM service_schedule WHERE name=?", name).Scan(&now)
			return time.Unix(now, 0)
		}
		saveRun := func(name string, now time.Time) {
			_, _ = conn.ExecContext(ctx, "UPDATE service_schedule SET last_run=? WHERE name=?", now.Unix(), name)
		}
		lastTracker, lastBackup := lastRun("trackers"), lastRun("backups")
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := policies.Tick(ctx); err != nil && ctx.Err() == nil {
					log.Printf("automation: %v", err)
				}
				if sc.Ready() {
					if err := policies.RefreshDue(ctx); err != nil && ctx.Err() == nil {
						log.Printf("scheduled library refresh: %v", err)
					}
				}
				c := store.Config()
				if c.TrackerPollHours > 0 && now.Sub(lastTracker) >= time.Duration(c.TrackerPollHours)*time.Hour {
					tk.PollAll(ctx)
					lastTracker = now
					saveRun("trackers", now)
				}
				if c.BackupIntervalHours > 0 && now.Sub(lastBackup) >= time.Duration(c.BackupIntervalHours)*time.Hour {
					if _, err := backup.CreateSnapshot(ctx, conn, filepath.Join(directory, "backups")); err == nil {
						_ = backup.PruneSnapshots(filepath.Join(directory, "backups"), c.BackupRetentionCount)
						lastBackup = now
						saveRun("backups", now)
					}
				}
			}
		}
	})
	if store.Config().MetadataBackfill {
		sy.Work.Go(func() { md.EnrichLibrary(sy.Work.Context) })
	}
	sy.Work.Go(func() { _ = cf.RecomputeAll(sy.Work.Context) })
	return r, nil
}
func mountServices(mux *http.ServeMux, r *graph.Resolver, images *rest.MangaCache) {
	mux.Handle("/content/", &rest.ContentHandler{Images: images, Segments: rest.NewSegmentCache(32 << 20), Q: r.Q, Sc: r.Sc, Sr: r.Sr})
	mux.Handle("GET /proxy/icon/", &rest.IconProxyHandler{Q: r.Q, IconCacheDir: filepath.Join(r.MediaDir, "icons")})
	mux.Handle("POST /api/backups/import-file", &rest.BackupImportHandler{Q: r.Q, Cfg: r.Cfg})
}
