package anilistrl

import (
	"context"
	"errors"
	"sync"
	"time"
)

const minGap = 3 * time.Second

// ErrRateLimited is returned by callers once AniList has rate-limited a
// request and a single retry still failed, so the caller can stop working
// through a whole batch instead of hitting the same wall on every item.
var ErrRateLimited = errors.New("anilist: rate limited")

var (
	mu       sync.Mutex
	nextSlot time.Time
)

func Wait(ctx context.Context) error {
	mu.Lock()
	now := time.Now()
	if nextSlot.Before(now) {
		nextSlot = now
	}
	wait := nextSlot.Sub(now)
	nextSlot = nextSlot.Add(minGap)
	mu.Unlock()

	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func Backoff(retryAfterSeconds int) {
	if retryAfterSeconds <= 0 {
		retryAfterSeconds = 60
	}
	extend := time.Now().Add(time.Duration(retryAfterSeconds) * time.Second)
	mu.Lock()
	if extend.After(nextSlot) {
		nextSlot = extend
	}
	mu.Unlock()
}
