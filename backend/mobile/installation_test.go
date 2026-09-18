package mobile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	sandboxv1 "tsunagu/backend/internal/sandbox/gen/sandbox/v1"
)

type failedLoader struct {
	sandboxv1.UnimplementedExtensionServiceServer
}

func (failedLoader) LoadExtensions(context.Context, *sandboxv1.LoadExtensionsRequest) (*sandboxv1.ExtensionList, error) {
	return &sandboxv1.ExtensionList{}, nil
}
func TestFailedEmbeddedInstallIsNotMarkedInstalled(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	defer server.Stop()
	sandboxv1.RegisterExtensionServiceServer(server, failedLoader{})
	go server.Serve(ln)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.json" {
			io.WriteString(w, `[{"name":"Broken","pkg":"org.moku.broken","apk":"broken.apk","lang":"en","version":"1.0"}]`)
		} else {
			io.WriteString(w, "not an APK")
		}
	}))
	defer fixture.Close()
	b := NewBackend()
	if err := b.StartWithSandbox(t.TempDir(), ln.Addr().String(), strings.Repeat("a", 32)); err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	call := func(query string) string {
		data, _ := json.Marshal(map[string]string{"query": query})
		req, _ := http.NewRequest("POST", b.BaseURL()+"/api/graphql", bytes.NewReader(data))
		req.Header.Set("Authorization", "Bearer "+b.AccessToken())
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		return string(body)
	}
	if result := call(fmt.Sprintf(`mutation{addRepository(indexUrl:%q){id}}`, fixture.URL+"/index.json")); strings.Contains(result, `"errors"`) {
		t.Fatal(result)
	}
	result := call(`mutation{installExtension(packageName:"org.moku.broken"){installed}}`)
	if !strings.Contains(result, "sandbox did not load extension") {
		t.Fatal(result)
	}
	result = call(`{extensions(query:"Broken"){items{installed jarPath}}}`)
	if !strings.Contains(result, `"installed":false`) || !strings.Contains(result, `"jarPath":null`) {
		t.Fatal(result)
	}
}
