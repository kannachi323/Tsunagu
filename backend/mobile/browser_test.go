package mobile

import (
	"context"
	"encoding/json"
	"google.golang.org/grpc"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	pb "tsunagu/backend/internal/sandbox/gen/sandbox/v1"
)

type browserFixture struct {
	pb.UnimplementedExtensionServiceServer
	transfers atomic.Int32
}

func (f *browserFixture) BrowserCookies(_ context.Context, r *pb.BrowserCookiesRequest) (*pb.BrowserCookiesResponse, error) {
	if r.Apply {
		f.transfers.Add(1)
	}
	return &pb.BrowserCookiesResponse{StateJson: `{"cookies":[],"userAgent":"Fixture"}`}, nil
}
func TestVerificationSessionOwnershipCancellationAndExpiry(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	defer server.Stop()
	fixture := &browserFixture{}
	pb.RegisterExtensionServiceServer(server, fixture)
	go server.Serve(listener)
	backend := NewBackend()
	directory := t.TempDir()
	token := strings.Repeat("a", 32)
	if err := backend.StartWithSandbox(directory, listener.Addr().String(), token); err != nil {
		t.Fatal(err)
	}
	defer backend.Stop()
	if _, err := backend.conn.Exec(`INSERT INTO repositories(id,index_url) VALUES(1,'fixture'); INSERT INTO extensions(repository_id,package_name,name,version,content_type,lang,apk_url) VALUES(1,'source','Source','1','manga','en','fixture')`); err != nil {
		t.Fatal(err)
	}
	begin := func() string {
		t.Helper()
		raw, err := backend.BeginBrowserVerification("source", "https://source.example/challenge")
		if err != nil {
			t.Fatal(err)
		}
		var session browserSession
		if err := json.Unmarshal([]byte(raw), &session); err != nil {
			t.Fatal(err)
		}
		if session.State != "pending" || session.ExtensionID != "source" || strings.Contains(raw, "cookies") {
			t.Fatal("invalid public session metadata")
		}
		return session.ID
	}
	first := begin()
	second := begin()
	if first == second {
		t.Fatal("session identity reused")
	}
	backend.CancelBrowserVerification(first)
	if _, err := backend.BrowserCookies(first); err == nil {
		t.Fatal("cancelled session remained usable")
	}
	if _, err := backend.BrowserCookies(second); err != nil {
		t.Fatal(err)
	}
	if err := backend.CompleteBrowserVerification(second, `{"cookies":[],"userAgent":"Fixture"}`); err != nil {
		t.Fatal(err)
	}
	if err := backend.CompleteBrowserVerification(second, `{}`); err == nil {
		t.Fatal("verification replay accepted")
	}
	if fixture.transfers.Load() != 1 {
		t.Fatal("duplicate transfer")
	}
	expired := begin()
	backend.browserSessions[expired].expires = time.Now().Add(-time.Second)
	if _, err := backend.BrowserCookies(expired); err == nil {
		t.Fatal("expired session accepted")
	}
	pending := begin()
	if err := backend.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := backend.StartWithSandbox(directory, listener.Addr().String(), token); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.BrowserCookies(pending); err == nil {
		t.Fatal("pending session survived lifecycle restart")
	}
	for _, u := range []string{"file:///secret", "http://127.0.0.1/private", "https://localhost./private", "https://user:password@example.org/"} {
		if _, err := backend.BeginBrowserVerification("source", u); err == nil {
			t.Fatalf("accepted unsafe URL %s", u)
		}
	}
}
