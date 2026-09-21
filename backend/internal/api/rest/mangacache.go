package rest

import (
	"context"
	"golang.org/x/sync/singleflight"
	"time"
	"tsunagu/backend/internal/image"
	"tsunagu/backend/internal/sandbox"
	"tsunagu/backend/internal/taskgroup"
)

type MangaCache struct {
	images *segCache
	group  singleflight.Group
	slots  chan struct{}
	Work   *taskgroup.Group
}

func NewMangaCache(bytes int64, prefetch int) *MangaCache {
	return &MangaCache{images: newSegCache(bytes), slots: make(chan struct{}, prefetch), Work: taskgroup.New()}
}

var desktopMangaCache = NewMangaCache(384<<20, 6)

func (h *ContentHandler) images() *MangaCache {
	if h.Images != nil {
		return h.Images
	}
	return desktopMangaCache
}
func mangaImageKey(extensionID, url string) string { return extensionID + "\x00" + url }
func (m *MangaCache) fetch(ctx context.Context, client *sandbox.Client, extensionID, url string) ([]byte, string, error) {
	key := mangaImageKey(extensionID, url)
	if data, ct, ok := m.images.get(key); ok {
		return data, ct, nil
	}
	result := m.group.DoChan(key, func() (any, error) {
		var value any
		var fetchErr error
		if !m.Work.Run(func() { value, fetchErr = m.fetchShared(client, extensionID, url, key) }) {
			return nil, context.Canceled
		}
		return value, fetchErr
	})
	select {
	case <-ctx.Done():
		return nil, "", ctx.Err()
	case value := <-result:
		if value.Err != nil {
			return nil, "", value.Err
		}
		pair := value.Val.([2]any)
		return pair[0].([]byte), pair[1].(string), nil
	}
}
func (m *MangaCache) fetchShared(client *sandbox.Client, extensionID, url, key string) (any, error) {
	if data, ct, ok := m.images.get(key); ok {
		return [2]any{data, ct}, nil
	}
	work, cancel := context.WithTimeout(m.Work.Context, 25*time.Second)
	defer cancel()
	img, err := client.GetImageBytes(work, extensionID, url)
	if err != nil {
		return nil, err
	}
	data, ct := img.GetData(), img.GetContentType()
	if err := image.Validate(data); err != nil {
		return nil, err
	}
	m.images.put(key, data, ct)
	return [2]any{data, ct}, nil
}
func (m *MangaCache) prefetch(client *sandbox.Client, extensionID string, urls []string) {
	for _, url := range urls {
		if url == "" {
			continue
		}
		if _, _, ok := m.images.get(mangaImageKey(extensionID, url)); ok {
			continue
		}
		select {
		case m.slots <- struct{}{}:
		default:
			return
		}
		if !m.Work.Go(func() { defer func() { <-m.slots }(); _, _, _ = m.fetch(m.Work.Context, client, extensionID, url) }) {
			<-m.slots
			return
		}
	}
}

func (m *MangaCache) Clear() { m.images.clear() }
