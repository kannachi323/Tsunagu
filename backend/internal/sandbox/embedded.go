package sandbox

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"time"

	"google.golang.org/grpc"
)

type embeddedCredential string

func (t embeddedCredential) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"authorization": "Bearer " + string(t)}, nil
}
func (embeddedCredential) RequireTransportSecurity() bool { return false } // Validated numeric loopback only.

// NewEmbedded connects to the JVM hosted in this app. Never spawns, reaps, or
// kills a process, even if the JVM is unavailable. Caller owns JVM lifetime.
func NewEmbedded(addr, token string) (*SupervisedClient, error) {
	host, port, err := net.SplitHostPort(addr)
	n, parseErr := strconv.Atoi(port)
	if err != nil || host != "127.0.0.1" || parseErr != nil || n < 1 || n > 65535 || len(token) < 32 {
		return nil, fmt.Errorf("invalid embedded sandbox endpoint or credential")
	}
	c, err := newClient(addr, grpc.WithPerRPCCredentials(embeddedCredential(token)))
	if err != nil {
		return nil, err
	}
	c.callTimeout = 120 * time.Second // Interpreter APK conversion is slower than desktop JIT.
	return &SupervisedClient{embedded: true, client: c, addr: addr, stopReaper: make(chan struct{})}, nil
}

func (sc *SupervisedClient) IsEmbedded() bool { return sc.embedded }

// NewPendingEmbedded lets SQLite and local HTTP services start before the JVM.
// Source operations fail promptly until the app attaches its authenticated JVM.
func NewPendingEmbedded() *SupervisedClient {
	return &SupervisedClient{embedded: true, stopReaper: make(chan struct{})}
}

func (sc *SupervisedClient) AttachEmbedded(addr, token string) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if !sc.embedded || sc.closed || sc.client != nil {
		return fmt.Errorf("embedded sandbox cannot be attached in this state")
	}
	connected, err := NewEmbedded(addr, token)
	if err != nil {
		return err
	}
	sc.client, sc.addr = connected.client, connected.addr
	return nil
}

// Ready does not start a process or wait on network I/O.
func (sc *SupervisedClient) Ready() bool {
	if sc == nil {
		return false
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return !sc.closed && (!sc.embedded || sc.client != nil)
}
