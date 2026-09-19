package rest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tsunagu/backend/internal/backup"
	"tsunagu/backend/internal/config"
	"tsunagu/backend/internal/db/sqlcgen"
)

// BackupImportHandler accepts a single uploaded .tachibk file and imports it
// directly, instead of requiring the user to drop it into the server's backup
// folder by hand first.
type BackupImportHandler struct {
	Q   *sqlcgen.Queries
	Cfg *config.Store
}

func (h *BackupImportHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, "invalid upload", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if !strings.HasSuffix(strings.ToLower(header.Filename), ".tachibk") {
		http.Error(w, "expected a .tachibk file", http.StatusBadRequest)
		return
	}

	dir := filepath.Join(h.Cfg.Config().DataDir, "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	name := "import-" + time.Now().Format("20060102-150405") + ".tachibk"
	dest := filepath.Join(dir, name)

	out, err := os.Create(dest)
	if err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		os.Remove(dest)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	out.Close()

	b, err := backup.ReadMihonFile(dest)
	if err != nil {
		os.Remove(dest)
		http.Error(w, fmt.Sprintf("invalid backup file: %v", err), http.StatusBadRequest)
		return
	}
	res, err := backup.Import(r.Context(), h.Q, b)
	if err != nil {
		os.Remove(dest)
		http.Error(w, fmt.Sprintf("import failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"mangaImported":      res.MangaImported,
		"mangaSkipped":       res.MangaSkipped,
		"categoriesImported": res.CategoriesImported,
		"trackingImported":   res.TrackingImported,
		"warnings":           res.Warnings,
	})
}
