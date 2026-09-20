package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"tsunagu/backend/internal/automation"
	"tsunagu/backend/internal/contentfilter"
)

func (b *Backend) mountAdmin(mux *http.ServeMux) {
	r := b.services
	mux.HandleFunc("/api/mobile/source-overrides", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case "GET":
			value, err := contentfilter.LoadSourceOverrides(req.Context(), r.DB)
			if err != nil {
				http.Error(w, "Could not read overrides", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(value)
		case "PUT":
			var value contentfilter.SourceOverrides
			decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 131072))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&value); err != nil {
				http.Error(w, "Invalid overrides", 400)
				return
			}
			if err := contentfilter.SaveSourceOverrides(req.Context(), r.DB, value); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	})
	policies := r.Policies
	mux.HandleFunc("/api/mobile/automation-controls", func(w http.ResponseWriter, req *http.Request) {
		switch req.Method {
		case "GET":
			value, err := policies.Controls(req.Context())
			if err != nil {
				http.Error(w, "Could not read automation controls", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(value)
		case "PUT":
			var value automation.Controls
			decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&value); err != nil {
				http.Error(w, "Invalid controls", 400)
				return
			}
			if err := policies.SetControls(req.Context(), value); err != nil {
				http.Error(w, "Could not save controls", 500)
				return
			}
			w.WriteHeader(204)
		case "DELETE":
			if err := policies.ResetOverrides(req.Context()); err != nil {
				http.Error(w, "Could not reset overrides", 500)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	})
	mux.HandleFunc("/api/mobile/automation", func(w http.ResponseWriter, req *http.Request) {
		media, err := strconv.ParseInt(req.URL.Query().Get("mediaId"), 10, 64)
		if err != nil || media < 0 {
			http.Error(w, "invalid mediaId", 400)
			return
		}
		switch req.Method {
		case "GET":
			p, err := policies.SavedPolicy(req.Context(), media)
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(p)
		case "PUT":
			raw, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 16384))
			if err == nil {
				err = policies.SetPolicy(req.Context(), media, raw)
			}
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.WriteHeader(204)
		case "DELETE":
			if media == 0 {
				http.Error(w, "choose a series", 400)
				return
			}
			if err := policies.SetPolicy(req.Context(), media, []byte(`{}`)); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.WriteHeader(204)
		default:
			w.WriteHeader(405)
		}
	})
	mux.HandleFunc("POST /api/mobile/chapter-open", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			MediaID   int64 `json:"mediaId"`
			ChapterID int64 `json:"chapterId"`
		}
		err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024)).Decode(&body)
		if err == nil {
			err = policies.ChapterEvent(req.Context(), body.MediaID, body.ChapterID, false)
		}
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /api/mobile/tracker/callback", func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Tracker     string `json:"tracker"`
			CallbackURL string `json:"callbackURL"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 16384)).Decode(&body); err != nil {
			http.Error(w, "invalid callback", 400)
			return
		}
		callback, err := url.Parse(body.CallbackURL)
		if err != nil {
			http.Error(w, "invalid callback", 400)
			return
		}
		info, err := r.Tk.OAuthCallback(req.Context(), body.Tracker, callback.Query())
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	})
	// This route supports desktop-compatible redirect handling, but remains behind
	// the backend bearer boundary. Native hosts forward callbacks via POST above.
	mux.HandleFunc("GET /api/tracker/mal/callback", func(w http.ResponseWriter, req *http.Request) {
		info, err := r.Tk.OAuthCallback(req.Context(), "mal", req.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	})
}

// ImportLocalFile receives a file already opened under a user-granted native scope.
// Call while the security-scoped resource remains accessible. No arbitrary target path is accepted.
func (b *Backend) ImportLocalFile(source, kind, series, chapter, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server == nil {
		return fmt.Errorf("backend is stopped")
	}
	if kind != "manga" && kind != "anime" && kind != "novels" {
		return fmt.Errorf("invalid local content type")
	}
	for _, part := range []string{series, chapter, name} {
		if part == "" || part == "." || part == ".." || filepath.Base(part) != part || strings.Contains(part, "\\") {
			return fmt.Errorf("invalid import name")
		}
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("only regular files can be imported")
	}
	root := b.services.Ls.LocalDir()
	directory := filepath.Join(root, kind, series)
	if kind != "manga" || (strings.ToLower(filepath.Ext(name)) != ".cbz" && strings.ToLower(filepath.Ext(name)) != ".zip") {
		directory = filepath.Join(directory, chapter)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	owned, err := filepath.EvalSymlinks(b.services.Cfg.Config().DataDir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(owned, canonical)
	if err != nil || relative == ".." || strings.HasPrefix(relative, "../") {
		return fmt.Errorf("import destination escapes app storage")
	}
	destination := filepath.Join(directory, name)
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return fmt.Errorf("import destination already exists")
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(directory, ".import-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(out.Name(), destination); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	_, err = b.services.Ls.Scan(ctx)
	return err
}
