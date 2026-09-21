package sandbox

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	sandboxv1 "tsunagu/backend/internal/sandbox/gen/sandbox/v1"
)

type embeddedFixture struct {
	sandboxv1.UnimplementedExtensionServiceServer
	token chan string
}

func (f *embeddedFixture) ListLoadedExtensions(ctx context.Context, _ *sandboxv1.Empty) (*sandboxv1.ExtensionList, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	f.token <- md.Get("authorization")[0]
	return &sandboxv1.ExtensionList{}, nil
}
func TestEmbeddedClientLifecycle(t *testing.T) {
	for _, addr := range []string{"example.com:1", "localhost:1", "127.0.0.1:0", "127.0.0.1:65536"} {
		if _, err := NewEmbedded(addr, "01234567890123456789012345678901"); err == nil {
			t.Fatalf("accepted %s", addr)
		}
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	defer server.Stop()
	fixture := &embeddedFixture{token: make(chan string, 1)}
	sandboxv1.RegisterExtensionServiceServer(server, fixture)
	go server.Serve(ln)
	token := "01234567890123456789012345678901"
	sc, err := NewEmbedded(ln.Addr().String(), token)
	if err != nil {
		t.Fatal(err)
	}
	c, err := sc.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.ListLoadedExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := <-fixture.token; got != "Bearer "+token {
		t.Fatalf("credential missing")
	}
	sc.Shutdown()
	sc.Shutdown()
	if _, err = sc.Ensure(context.Background()); err == nil {
		t.Fatal("closed sandbox accepted")
	}
	if sc.cmd != nil {
		t.Fatal("embedded client launched a process")
	}
}

func TestPendingEmbeddedDoesNotSpawnAndCanAttachOnce(t *testing.T) {
	sc := NewPendingEmbedded()
	defer sc.Shutdown()
	if sc.Ready() {
		t.Fatal("pending runtime reported ready")
	}
	if c, err := sc.Ensure(context.Background()); err == nil || c != nil {
		t.Fatal("pending runtime must return an error, not a nil client")
	}
	if sc.cmd != nil {
		t.Fatal("pending runtime spawned a process")
	}
	if err := sc.AttachEmbedded("example.com:1", "01234567890123456789012345678901"); err == nil {
		t.Fatal("accepted non-loopback endpoint")
	}
	if sc.Ready() {
		t.Fatal("invalid attachment changed readiness")
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	defer server.Stop()
	fixture := &embeddedFixture{token: make(chan string, 1)}
	sandboxv1.RegisterExtensionServiceServer(server, fixture)
	go server.Serve(ln)
	token := "01234567890123456789012345678901"
	if err := sc.AttachEmbedded(ln.Addr().String(), token); err != nil {
		t.Fatal(err)
	}
	if !sc.Ready() {
		t.Fatal("attachment did not make runtime ready")
	}
	c, err := sc.Ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListLoadedExtensions(context.Background()); err != nil {
		t.Fatal(err)
	}
	if <-fixture.token != "Bearer "+token {
		t.Fatal("missing credential")
	}
	if err := sc.AttachEmbedded(ln.Addr().String(), token); err == nil {
		t.Fatal("duplicate attachment accepted")
	}
	sc.Shutdown()
	if sc.Ready() {
		t.Fatal("closed runtime reported ready")
	}
	if err := sc.AttachEmbedded(ln.Addr().String(), token); err == nil {
		t.Fatal("closed runtime accepted attachment")
	}
}
