package mobile

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDeferredSandboxPreservesCatalogBeforeAndAfterAttach(t *testing.T) {
	b := NewBackend()
	dir := t.TempDir()
	if err := b.Start(dir); err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	// Existing SQLite data, as on a subsequent app launch; no repository network fetch.
	if _, err := b.conn.Exec(`INSERT INTO repositories(id,index_url,name) VALUES(1,'https://example.com/index.pb','Saved repository');
        INSERT INTO extensions(repository_id,package_name,name,version,content_type,lang,apk_url,installed)
        VALUES(1,'source','Saved source','1','manga','en','fixture',1)`); err != nil {
		t.Fatal(err)
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := b.StartDeferredSandbox(dir); err != nil {
		t.Fatal(err)
	}
	url, token := b.BaseURL(), b.AccessToken()
	call := func(query string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"query": query})
		req, _ := http.NewRequest("POST", url+"/api/graphql", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := (&http.Client{Timeout: time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return string(data)
	}
	check := func() {
		result := call(`{repositories{name} installedExtensions{displayName} extensions(limit:40){total}}`)
		if strings.Contains(result, `"errors"`) || !strings.Contains(result, "Saved repository") || !strings.Contains(result, "Saved source") || !strings.Contains(result, `"total":1`) {
			t.Fatal(result)
		}
	}
	check()
	_, status := request(t, b, "/api/mobile/status", token)
	if !bytes.Contains(status, []byte(`"extensionsAvailable":false`)) {
		t.Fatal(string(status))
	}
	if result := call(`{sourcePreferences(extensionId:"source"){key}}`); !strings.Contains(result, "not yet available") {
		t.Fatal(result)
	}
	if err := b.AttachSandbox("example.com:1", strings.Repeat("a", 32)); err == nil {
		t.Fatal("invalid attachment accepted")
	}
	check() // A failed JVM attachment must not hide durable SQLite data.
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := b.AttachSandbox(ln.Addr().String(), strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	if b.BaseURL() != url || b.AccessToken() != token {
		t.Fatal("attachment restarted HTTP/SQLite")
	}
	check()
	_, status = request(t, b, "/api/mobile/status", token)
	if !bytes.Contains(status, []byte(`"extensionsAvailable":true`)) {
		t.Fatal(string(status))
	}
	if err := b.AttachSandbox(ln.Addr().String(), strings.Repeat("a", 32)); err == nil {
		t.Fatal("duplicate attachment accepted")
	}
}
