// Package mobile exposes Tsunagu's embedded core, catalog and source-search API.
// Extension execution uses an explicitly supplied app-hosted JVM; no process is spawned.
package mobile

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"tsunagu/backend/internal/api/graph"
	"tsunagu/backend/internal/api/rest"
	"tsunagu/backend/internal/db"
	"tsunagu/backend/internal/db/sqlcgen"
	"tsunagu/backend/internal/sandbox"
	"tsunagu/backend/internal/taskgroup"
)

// Backend owns one loopback listener and a migrated Tsunagu database. Methods
// serialize lifecycle transitions and are safe to call from native threads.
// Keep one Backend instance per database directory.
type Backend struct {
	requests        *taskgroup.Group
	browserSessions map[string]*browserSession
	videoEngine     VideoEngine
	services        *graph.Resolver
	images          *rest.MangaCache
	mu              sync.Mutex
	sc              *sandbox.SupervisedClient
	conn            *sql.DB
	server          *http.Server
	done            chan struct{}
	url             string
	token           string
	serveErr        error
}

func NewBackend() *Backend { return &Backend{} }

// Start requires an absolute app-owned directory. Success means migrations have
// completed and the listener is bound. A running instance rejects another start.
func (b *Backend) Start(dataDirectory string) error { return b.start(dataDirectory, nil) }

func (b *Backend) StartWithSandbox(dataDirectory, addr, token string) error {
	sc, err := sandbox.NewEmbedded(addr, token)
	if err != nil {
		return err
	}
	if err = b.start(dataDirectory, sc); err != nil {
		sc.Shutdown()
	}
	return err
}

// StartDeferredSandbox exposes persisted data before the app starts its JVM.
func (b *Backend) StartDeferredSandbox(dataDirectory string) error {
	sc := sandbox.NewPendingEmbedded()
	if err := b.start(dataDirectory, sc); err != nil {
		sc.Shutdown()
		return err
	}
	return nil
}

func (b *Backend) AttachSandbox(addr, token string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server == nil || b.sc == nil {
		return errors.New("backend has no pending sandbox")
	}
	if err := b.sc.AttachEmbedded(addr, token); err != nil {
		return err
	}
	b.services.Dm.Start()
	return nil
}

func (b *Backend) start(dataDirectory string, sc *sandbox.SupervisedClient) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server != nil {
		return errors.New("backend already started; stop before restarting")
	}
	if !filepath.IsAbs(dataDirectory) {
		return errors.New("data directory must be absolute")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return fmt.Errorf("session credential: %w", err)
	}
	conn, err := db.Open(filepath.Join(dataDirectory, "tsunagu.db"))
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("listen: %w", err)
	}
	token := hex.EncodeToString(secret)
	mux := http.NewServeMux()
	services, err := newServices(conn, dataDirectory, "http://"+ln.Addr().String(), sc, b.videoEngine)
	if err != nil {
		ln.Close()
		conn.Close()
		return err
	}
	b.services = services
	mux.Handle("/api/graphql", catalogHandler(services))
	b.images = rest.NewMangaCache(64<<20, 2)
	services.ClearMemoryImages = b.images.Clear
	mountServices(mux, services, b.images)
	b.mountAdmin(mux)
	coverDirectory := filepath.Join(dataDirectory, "media", "covers")
	remoteCovers := &rest.RemoteCoverProxyHandler{CoverCacheDir: coverDirectory}
	mux.Handle("GET /proxy/img/", remoteCovers)
	mux.Handle("GET /proxy/cover/remote/", remoteCovers)
	mux.Handle("GET /proxy/cover/", &rest.CoverProxyHandler{Sc: sc, Q: sqlcgen.New(conn), CoverCacheDir: coverDirectory})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := conn.PingContext(ctx); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/mobile/status", func(w http.ResponseWriter, r *http.Request) {
		var migrations int
		if err := conn.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sourceReady := sc.Ready()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name": "Tsunagu", "mode": "embedded-core", "migrations": migrations,
			"graphqlAvailable": true, "extensionCatalogAvailable": true, "extensionsAvailable": sourceReady,
			"contentAvailable": sourceReady, "nativeVideoAvailable": b.videoEngine != nil, "backgroundTransfers": false, "browserVerificationRequired": true,
			"capabilities": map[string]bool{"metadata": true, "images": true, "library": true, "administration": true, "automation": true, "localContent": true, "sourceContent": sourceReady, "browserVerification": sourceReady, "videoDownloads": sourceReady && b.videoEngine != nil},
			"limitations":  []string{"foreground scheduling only", "manual browser challenge completion", "no managed subprocess services", "filesystem relocation stays in app storage", "JVM settings require app relaunch", "DRM and unbounded live downloads are unsupported"},
		})
	})
	b.requests = taskgroup.New()
	requests := b.requests
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if !requests.Run(func() { mux.ServeHTTP(w, r) }) {
			http.Error(w, "backend stopping", http.StatusServiceUnavailable)
		}
	})
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	b.sc = sc
	b.conn, b.server, b.done = conn, srv, make(chan struct{})
	b.url, b.token, b.serveErr = "http://"+ln.Addr().String(), token, nil
	done := b.done
	go func() {
		err := srv.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			b.serveErr = err
		}
		close(done)
	}()
	return nil
}

// BaseURL returns an empty string when stopped.
func (b *Backend) BaseURL() string { b.mu.Lock(); defer b.mu.Unlock(); return b.url }

// AccessToken is a per-start credential. Never persist or log it.
func (b *Backend) AccessToken() string { b.mu.Lock(); defer b.mu.Unlock(); return b.token }

func (b *Backend) Status() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server == nil {
		return "stopped"
	}
	select {
	case <-b.done:
		return "failed"
	default:
	}
	return "running"
}

// Stop is idempotent. Call off the main thread: it drains HTTP requests for up to
// five seconds, closes the listener, and closes SQLite before permitting restart.
func (b *Backend) Stop() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownErr := b.server.Shutdown(ctx)
	if shutdownErr != nil {
		_ = b.server.Close()
	}
	<-b.done
	b.requests.Close()
	b.images.Work.Close()
	b.services.Sy.Work.Cancel()
	b.services.Dm.Shutdown()
	b.services.Sy.Work.Wait()
	if b.sc != nil {
		b.sc.Shutdown()
		b.sc = nil
	}
	dbErr := b.conn.Close()
	b.server, b.conn = nil, nil
	b.url, b.token = "", ""
	b.browserSessions = nil
	b.services = nil
	return errors.Join(shutdownErr, dbErr, b.serveErr)
}
