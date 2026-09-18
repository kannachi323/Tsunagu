package download

import "context"

// VideoDownloadEngine writes a complete MP4 to OutputPath or returns an error.
// Implementations must honour cancellation and never report success for partial output.
type VideoDownloadEngine interface {
	Download(context.Context, VideoRequest, func(VideoProgress)) error
}
type VideoRequest struct {
	URL        string
	Headers    map[string]string
	OutputPath string
}
type VideoProgress struct {
	Fraction       float64
	Bytes          int64
	BytesPerSecond float64
}

// SetVideoEngine must be called before Start. Mobile always supplies an engine,
// including an explicit unavailable engine when native registration is missing.
func (m *Manager) SetVideoEngine(engine VideoDownloadEngine) { m.videoEngine = engine }
