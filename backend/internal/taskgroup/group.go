// Package taskgroup owns cancellable asynchronous service work.
package taskgroup

import (
	"context"
	"sync"
)

type Group struct {
	Context context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	wg      sync.WaitGroup
}

func New() *Group {
	ctx, cancel := context.WithCancel(context.Background())
	return &Group{Context: ctx, cancel: cancel}
}
func (g *Group) Go(fn func()) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.wg.Add(1)
	go func() { defer g.wg.Done(); fn() }()
	return true
}
func (g *Group) Cancel() { g.mu.Lock(); g.closed = true; g.cancel(); g.mu.Unlock() }
func (g *Group) Wait()   { g.wg.Wait() }
func (g *Group) Close()  { g.Cancel(); g.Wait() }

// Run registers synchronous work so Close also drains active request handlers.
func (g *Group) Run(fn func()) bool {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return false
	}
	g.wg.Add(1)
	g.mu.Unlock()
	defer g.wg.Done()
	fn()
	return true
}
