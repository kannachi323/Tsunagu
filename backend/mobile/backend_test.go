package mobile

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func request(t *testing.T, b *Backend, path, token string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest("GET", b.BaseURL()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, body
}

func TestLifecycleAndPersistence(t *testing.T) {
	b := NewBackend()
	dir := t.TempDir()
	if err := b.Start(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = b.Stop() })
	if err := b.Start(dir); err == nil {
		t.Fatal("duplicate start accepted")
	}
	if code, _ := request(t, b, "/healthz", ""); code != 401 {
		t.Fatalf("unauthenticated response: %d", code)
	}
	if code, _ := request(t, b, "/healthz", "wrong"); code != 401 {
		t.Fatalf("wrong credential response: %d", code)
	}
	oldToken := b.AccessToken()
	if code, body := request(t, b, "/healthz", oldToken); code != 200 || string(body) != "ok" {
		t.Fatalf("health: %d %s", code, body)
	}
	code, body := request(t, b, "/api/mobile/status", oldToken)
	var status struct {
		Migrations          int
		GraphqlAvailable    bool
		ExtensionsAvailable bool
	}
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatal(err)
	}
	if code != 200 || status.Migrations != 19 || !status.GraphqlAvailable || status.ExtensionsAvailable {
		t.Fatalf("unexpected status: %s", body)
	}
	if _, err := b.conn.Exec("INSERT INTO app_settings(key,value) VALUES ('mobile-test','persisted')"); err != nil {
		t.Fatal(err)
	}
	url := b.BaseURL()
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	if b.Status() != "stopped" || b.BaseURL() != "" || b.AccessToken() != "" {
		t.Fatal("stale stopped state")
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: time.Second}
	if res, err := client.Get(url + "/healthz"); err == nil {
		res.Body.Close()
		t.Fatal("listener remains open")
	}
	if err := b.Start(dir); err != nil {
		t.Fatal(err)
	}
	if b.AccessToken() == oldToken {
		t.Fatal("credential was reused")
	}
	if code, _ := request(t, b, "/healthz", oldToken); code != 401 {
		t.Fatal("old credential accepted")
	}
	var value string
	if err := b.conn.QueryRow("SELECT value FROM app_settings WHERE key='mobile-test'").Scan(&value); err != nil || value != "persisted" {
		t.Fatalf("persistence: %q %v", value, err)
	}
}

func TestStartupFailureCanRecover(t *testing.T) {
	b := NewBackend()
	if err := b.Start("relative"); err == nil {
		t.Fatal("relative path accepted")
	}
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(path); err == nil {
		t.Fatal("invalid directory accepted")
	}
	if b.Status() != "stopped" {
		t.Fatal("failed startup leaked state")
	}
	if err := b.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentLifecycle(t *testing.T) {
	b := NewBackend()
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 4; j++ {
				_ = b.Start(dir)
				_ = b.Status()
				_ = b.BaseURL()
				_ = b.AccessToken()
				_ = b.Stop()
			}
		}()
	}
	wg.Wait()
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
}

// Route image fetches to an in-memory public-host fixture; no external network.
type coverFixtureTransport struct {
	fallback http.RoundTripper
	data     []byte
	t        *testing.T
}

func (f coverFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Hostname() != "covers.example" {
		return f.fallback.RoundTrip(r)
	}
	if r.Header.Get("Authorization") != "" {
		f.t.Error("backend credential sent upstream")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(bytes.NewReader(f.data)), Request: r}, nil
}

func TestAuthenticatedCoverProxy(t *testing.T) {
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYPgPAAEDAQAIicLsAAAAAElFTkSuQmCC")
	if err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	http.DefaultTransport = coverFixtureTransport{fallback: original, data: data, t: t}
	defer func() { http.DefaultTransport = original }()
	b := NewBackend()
	if err := b.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	encoded := base64.URLEncoding.EncodeToString([]byte("https://covers.example/cover.png"))
	for _, prefix := range []string{"/proxy/img/", "/proxy/cover/remote/"} {
		if code, _ := request(t, b, prefix+encoded, ""); code != http.StatusUnauthorized {
			t.Fatalf("missing auth: %d", code)
		}
		if code, body := request(t, b, prefix+encoded, b.AccessToken()); code != http.StatusOK || !bytes.Equal(body, data) {
			t.Fatalf("cover: %d %q", code, body)
		}
	}
	local := base64.URLEncoding.EncodeToString([]byte("http://127.0.0.1/private"))
	if code, _ := request(t, b, "/proxy/img/"+local, b.AccessToken()); code != http.StatusBadRequest {
		t.Fatalf("local image accepted: %d", code)
	}
}
