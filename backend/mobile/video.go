package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"tsunagu/backend/internal/download"
)

// VideoObserver is safe to call from the native engine's processing thread.
// Cancellation must also be checked from native network interrupt callbacks.
type VideoObserver interface {
	Cancelled() bool
	Progress(fraction float64, bytes int64, bytesPerSecond float64)
}

// VideoEngine is registered before Start; headersJSON is a string-to-string map.
type VideoEngine interface {
	Process(url, headersJSON, outputPath string, observer VideoObserver) error
}
type videoAdapter struct{ engine VideoEngine }
type videoObserver struct {
	ctx      context.Context
	progress func(download.VideoProgress)
}

func (o *videoObserver) Cancelled() bool { return o.ctx.Err() != nil }
func (o *videoObserver) Progress(fraction float64, bytes int64, speed float64) {
	o.progress(download.VideoProgress{Fraction: fraction, Bytes: bytes, BytesPerSecond: speed})
}
func (v videoAdapter) Download(ctx context.Context, request download.VideoRequest, progress func(download.VideoProgress)) error {
	if v.engine == nil {
		return errors.New("native video engine is not registered")
	}
	if request.Headers == nil {
		request.Headers = map[string]string{}
	}
	headers, err := json.Marshal(request.Headers)
	if err != nil {
		return err
	}
	return v.engine.Process(request.URL, string(headers), request.OutputPath, &videoObserver{ctx, progress})
}
func (b *Backend) SetVideoEngine(engine VideoEngine) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.server != nil {
		return errors.New("register the video engine before starting services")
	}
	b.videoEngine = engine
	return nil
}
