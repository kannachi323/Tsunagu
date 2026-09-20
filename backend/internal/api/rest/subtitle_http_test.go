package rest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"tsunagu/backend/internal/db"
	"tsunagu/backend/internal/db/sqlcgen"
)

type subtitleFixtureTransport struct{}

func (subtitleFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	status, body := 200, ""
	if r.Header.Get("Cookie") != "source-session=fixture" {
		status = 403
	} else {
		switch r.URL.Path {
		case "/track.srt":
			body = "1\r\n00:00:01,500 --> 00:00:03,000\r\nFixture subtitle.\r\n"
		case "/track.ass":
			body = "[Script Info]\nTitle: Fixture\n"
		default:
			body = "WEBVTT\n\n00:01.500 --> 00:03.000\nFixture subtitle.\n"
		}
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
}
func TestPublicSubtitleFormatsPreserveSourceHeaders(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = conn.Exec(`INSERT INTO media(id,external_id,content_type,title) VALUES(1,'series','anime','Series'); INSERT INTO chapters(id,media_id,external_id) VALUES(1,1,'episode')`); err != nil {
		t.Fatal(err)
	}
	original := proxyClient
	proxyClient = &http.Client{Transport: subtitleFixtureTransport{}}
	defer func() { proxyClient = original }()
	handler := &ContentHandler{Q: sqlcgen.New(conn)}
	server := httptest.NewServer(handler)
	defer server.Close()
	headers, _ := json.Marshal(map[string]string{"Cookie": "source-session=fixture"})
	for _, format := range []string{"vtt", "srt", "ass"} {
		target := base64.RawURLEncoding.EncodeToString([]byte("https://subtitles.example/track." + format))
		req, _ := http.NewRequestWithContext(context.Background(), "GET", server.URL+"/content/1/1/subtitle?u="+target+"&h="+base64.RawURLEncoding.EncodeToString(headers), nil)
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || response.Header.Get("X-Subtitle-Format") != format {
			t.Fatalf("%s: status %d %s", format, response.StatusCode, body)
		}
		if format == "srt" && !strings.Contains(string(body), "00:00:01.500 --> 00:00:03.000") {
			t.Fatalf("SRT not converted: %s", body)
		}
		if format != "ass" && !strings.HasPrefix(string(body), "WEBVTT") {
			t.Fatalf("missing VTT header: %s", body)
		}
	}
}
