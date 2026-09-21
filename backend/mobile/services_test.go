package mobile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"tsunagu/backend/internal/metadata"
)

type fixtureMetadata struct{ fail bool }

func (f fixtureMetadata) Key() string { return "anilist" }
func (f fixtureMetadata) Search(context.Context, string, metadata.ContentType) ([]metadata.Candidate, error) {
	if f.fail {
		return nil, errors.New("provider offline")
	}
	value, _ := f.Fetch(context.Background(), "42")
	return []metadata.Candidate{*value}, nil
}
func (f fixtureMetadata) Fetch(context.Context, string) (*metadata.Candidate, error) {
	return &metadata.Candidate{ProviderID: "42", URL: "https://anilist.co/manga/42", PrimaryTitle: "Fixture", Titles: []string{"Fixture"}, Description: "Provider description", Authors: []string{"Provider author"}, Genres: []string{"Adventure"}}, nil
}

type accountFixtureTransport struct{ fallback http.RoundTripper }

func (f accountFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body string
	switch req.URL.Hostname() {
	case "graphql.anilist.co":
		if req.Header.Get("Authorization") != "Bearer fixture-token" {
			return nil, fmt.Errorf("unexpected tracker credential")
		}
		var input struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			return nil, err
		}
		body = `{"data":{"Viewer":{"name":"Fixture Reader","mediaListOptions":{"scoreFormat":"POINT_10"}},"Media":{"id":42,"type":"MANGA","chapters":10,"title":{"romaji":"Fixture"},"mediaListEntry":{"id":8,"status":"CURRENT","progress":1,"score":7}}}}`
		if strings.Contains(input.Query, "SaveMediaListEntry") {
			out, _ := json.Marshal(map[string]any{"data": map[string]any{"SaveMediaListEntry": map[string]any{"id": 8, "status": input.Variables["status"], "progress": input.Variables["progress"], "score": input.Variables["score"]}}})
			body = string(out)
		}
	case "myanimelist.net":
		if err := req.ParseForm(); err != nil {
			return nil, err
		}
		if req.Form.Get("code") != "fixture-code" || req.Form.Get("code_verifier") == "" {
			return nil, fmt.Errorf("OAuth code/PKCE verifier missing")
		}
		body = `{"access_token":"fixture-mal-token","refresh_token":"fixture-refresh","expires_in":3600}`
	case "api.myanimelist.net":
		if req.Header.Get("Authorization") != "Bearer fixture-mal-token" {
			return nil, fmt.Errorf("wrong MAL credential")
		}
		body = `{"name":"Fixture MAL Reader"}`
	default:
		return f.fallback.RoundTrip(req)
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestPublicMetadataLibraryAndAdmin(t *testing.T) {
	originalTransport := http.DefaultTransport
	http.DefaultTransport = accountFixtureTransport{originalTransport}
	defer func() { http.DefaultTransport = originalTransport }()
	b := NewBackend()
	if err := b.Start(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	b.services.Md.SetProvider(fixtureMetadata{})
	call := func(query string) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"query": query})
		req, _ := http.NewRequest("POST", b.BaseURL()+"/api/graphql", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+b.AccessToken())
		req.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result["errors"] != nil {
			t.Fatal(string(data))
		}
		return result["data"].(map[string]any)
	}
	_, err := b.conn.Exec(`INSERT INTO media(id,external_id,content_type,title,description,author,details_fetched_at,chapters_synced_at) VALUES(1,'fixture','manga','Fixture','Source description','Source author',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	call(`{searchMetadata(query:"Fixture",contentType:MANGA){providerId title}}`)
	call(`mutation{applyMetadataMatch(mediaId:"1",providerId:"42"){id metadata{providerId}}}`)
	result := call(`{media(id:"1"){description author metadata{providerId}}}`)["media"].(map[string]any)
	if result["description"] != "Source description" || result["author"] != "Source author" {
		t.Fatalf("provider overwrote source fields: %+v", result)
	}
	call(`mutation{refreshMetadataMatch(mediaId:"1"){id metadata{providerId}}}`)
	call(`mutation{unlinkMetadata(mediaId:"1")}`)
	b.services.Md.SetProvider(fixtureMetadata{fail: true})
	// A provider outage must not erase source metadata.
	_ = b.services.Md.AutoEnrich(context.Background(), 1)
	source := call(`{media(id:"1"){description author}}`)["media"].(map[string]any)
	if source["description"] != "Source description" || source["author"] != "Source author" {
		t.Fatal(source)
	}
	b.services.Md.SetProvider(fixtureMetadata{})
	call(`mutation{setInLibrary(mediaId:"1",inLibrary:true){id}}`)
	call(`mutation{createFolder(name:"Saved"){id}}`)
	call(`{library{total items{id}} storageInfo{__typename} trackers{key isLoggedIn} serverSettings{key}}`)
	call(`mutation{unlinkMetadata(mediaId:"1")}`)
	login := call(`mutation{trackerLogin(trackerKey:"anilist",token:"fixture-token"){username isLoggedIn}}`)["trackerLogin"].(map[string]any)
	if login["username"] != "Fixture Reader" || login["isLoggedIn"] != true {
		t.Fatal(login)
	}
	link := call(`mutation{bindTrack(mediaId:"1",trackerKey:"anilist",remoteId:"42"){id lastChapterRead}}`)["bindTrack"].(map[string]any)
	updated := call(fmt.Sprintf(`mutation{updateTrack(linkId:%q,lastChapterRead:3,score:9){lastChapterRead score}}`, link["id"]))["updateTrack"].(map[string]any)
	if updated["lastChapterRead"] != float64(3) || updated["score"] != float64(9) {
		t.Fatal(updated)
	}
	call(fmt.Sprintf(`mutation{unbindTrack(linkId:%q)}`, link["id"]))
	call(`mutation{trackerLogout(trackerKey:"anilist")}`)
	trackers := call(`{trackers{key authUrl}}`)["trackers"].([]any)
	var state string
	for _, item := range trackers {
		row := item.(map[string]any)
		if row["key"] == "mal" {
			u, _ := url.Parse(row["authUrl"].(string))
			state = u.Query().Get("state")
		}
	}
	if state == "" {
		t.Fatal("MAL authorization state missing")
	}
	callback, _ := json.Marshal(map[string]string{"tracker": "mal", "callbackURL": "moku://tracker?code=fixture-code&state=" + url.QueryEscape(state)})
	for attempt := 0; attempt < 2; attempt++ {
		req, _ := http.NewRequest("POST", b.BaseURL()+"/api/mobile/tracker/callback", bytes.NewReader(callback))
		req.Header.Set("Authorization", "Bearer "+b.AccessToken())
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		expected := 200
		if attempt == 1 {
			expected = 400
		}
		if response.StatusCode != expected {
			t.Fatalf("OAuth attempt %d: %s", attempt, body)
		}
	}
	call(`mutation{trackerLogout(trackerKey:"mal")}`)

	call(`mutation{createDatabaseBackup{name}}`)
	call(`{databaseBackups{name}}`)
	req, _ := http.NewRequest("PUT", b.BaseURL()+"/api/mobile/automation?mediaId=0", bytes.NewBufferString(`{"downloadAhead":2}`))
	req.Header.Set("Authorization", "Bearer "+b.AccessToken())
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 204 {
		data, _ := io.ReadAll(response.Body)
		t.Fatal(string(data))
	}
}
