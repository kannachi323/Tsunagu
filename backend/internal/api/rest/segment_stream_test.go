package rest

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSegmentDeliversBytesBeforeUpstreamCompletes(t *testing.T) {
	h := &ContentHandler{Segments: NewSegmentCache(32 << 20)}
	release := make(chan struct{})
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		reader, writer := io.Pipe()
		go func() {
			defer writer.Close()
			_, _ = writer.Write([]byte("first"))
			<-release
			_, _ = writer.Write([]byte("last"))
		}()
		defer reader.Close()
		h.writeSegment(w, r, "segment", &http.Response{
			StatusCode: 200, Header: http.Header{"Content-Type": {"video/mp2t"}},
			Body: reader, ContentLength: -1,
		})
	}))
	defer server.Close()
	first := make(chan error, 1)
	complete := make(chan []byte, 1)
	go func() {
		response, err := server.Client().Get(server.URL)
		if err != nil {
			first <- err
			complete <- nil
			return
		}
		defer response.Body.Close()
		prefix := make([]byte, 5)
		_, err = io.ReadFull(response.Body, prefix)
		if err == nil && string(prefix) != "first" {
			err = errors.New("wrong first bytes")
		}
		first <- err
		rest, _ := io.ReadAll(response.Body)
		complete <- append(prefix, rest...)
	}()
	select {
	case err := <-first:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("player received no bytes while the upstream segment was still downloading")
	}
	close(release)
	if got := <-complete; string(got) != "firstlast" {
		t.Errorf("body = %q", got)
	}
	<-finished
	if got, _, ok := h.segments().get("segment"); !ok || string(got) != "firstlast" {
		t.Error("complete small segment was not cached")
	}
}

func TestLargeSegmentIsDeliveredInFullWithoutCaching(t *testing.T) {
	h := &ContentHandler{Segments: NewSegmentCache(32 << 20)}
	body := bytes.Repeat([]byte{0xA5}, segCacheMaxObject+65536)
	response := httptest.NewRecorder()
	h.writeSegment(response, httptest.NewRequest("GET", "/", nil), "large", &http.Response{
		StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: -1,
	})
	if !bytes.Equal(response.Body.Bytes(), body) {
		t.Fatalf("segment truncated: got %d, want %d", response.Body.Len(), len(body))
	}
	if _, _, ok := h.segments().get("large"); ok {
		t.Fatal("oversized segment cached")
	}
}

func TestRangeSegmentPreservesHeadersAndBypassesCache(t *testing.T) {
	h := &ContentHandler{Segments: NewSegmentCache(32 << 20)}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("Range", "bytes=2-4")
	response := httptest.NewRecorder()
	h.writeSegment(response, request, "range", &http.Response{
		StatusCode: 206, Header: http.Header{"Content-Range": {"bytes 2-4/9"}, "Content-Length": {"3"}},
		Body: io.NopCloser(bytes.NewBufferString("abc")), ContentLength: 3,
	})
	if response.Code != 206 || response.Header().Get("Content-Range") != "bytes 2-4/9" || response.Body.String() != "abc" {
		t.Fatal("range response changed")
	}
	if _, _, ok := h.segments().get("range"); ok {
		t.Fatal("partial segment cached")
	}
}

type failedSegmentReader struct{ sent bool }

type nonFlushingWriter struct{ http.ResponseWriter }

func TestSegmentSupportsWritersWithoutFlush(t *testing.T) {
	h := &ContentHandler{Segments: NewSegmentCache(32 << 20)}
	response := httptest.NewRecorder()
	h.writeSegment(nonFlushingWriter{response}, httptest.NewRequest("GET", "/", nil), "plain", &http.Response{
		StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewBufferString("complete")), ContentLength: 8,
	})
	if response.Body.String() != "complete" {
		t.Fatal("body missing")
	}
}

func (r *failedSegmentReader) Read(p []byte) (int, error) {
	if r.sent {
		return 0, errors.New("upstream disconnected")
	}
	r.sent = true
	return copy(p, "partial"), nil
}

func TestIncompleteSegmentIsAbortedAndNeverCached(t *testing.T) {
	for _, body := range []io.Reader{&failedSegmentReader{}, bytes.NewBufferString("short")} {
		h := &ContentHandler{Segments: NewSegmentCache(32 << 20)}
		aborted := false
		func() {
			defer func() {
				if reason := recover(); reason != nil {
					if reason != http.ErrAbortHandler {
						panic(reason)
					}
					aborted = true
				}
			}()
			h.writeSegment(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), "failed", &http.Response{
				StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(body), ContentLength: 100,
			})
		}()
		if !aborted {
			t.Error("incomplete response was reported as complete")
		}
		if _, _, ok := h.segments().get("failed"); ok {
			t.Error("incomplete response cached")
		}
	}
}
