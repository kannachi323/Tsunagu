package mobile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCatalogPersistenceAndCapabilityGate(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"name":"MangaDex fixture","pkg":"org.moku.fixture","apk":"fixture.apk","lang":"en","version":"1.0"}]`)
	}))
	defer fixture.Close()
	b := NewBackend()
	dir := t.TempDir()
	if err := b.Start(dir); err != nil {
		t.Fatal(err)
	}
	defer b.Stop()
	call := func(query string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"query": query})
		req, _ := http.NewRequest("POST", b.BaseURL()+"/api/graphql", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+b.AccessToken())
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, _ := io.ReadAll(res.Body)
		return string(data)
	}
	add := fmt.Sprintf(`addRepository(indexUrl:%q){id}`, fixture.URL)
	// A later unsupported aliased fragment must prevent the earlier mutation too.
	result := call(`mutation { ` + add + ` ...Blocked } fragment Blocked on Mutation { nope: uninstallExtension(packageName:"test") { id } }`)
	if !strings.Contains(result, "not yet available") {
		t.Fatal(result)
	}
	if result = call(`{repositories{id}}`); !strings.Contains(result, `"repositories":[]`) {
		t.Fatal(result)
	}
	if result = call(`mutation {` + add + `}`); strings.Contains(result, `"errors"`) {
		t.Fatal(result)
	}
	if err := b.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := b.Start(dir); err != nil {
		t.Fatal(err)
	}
	result = call(`{ extensions(query:"mangadex",limit:10){total items{packageName installed}} }`)
	if !strings.Contains(result, `"total":1`) || !strings.Contains(result, `"installed":false`) || !strings.Contains(result, "org.moku.fixture") {
		t.Fatal(result)
	}
	if result = call(`mutation {renameRepository(repositoryId:"1",name:"My sources"){name}}`); !strings.Contains(result, `"name":"My sources"`) {
		t.Fatal(result)
	}
	if result = call(`{sourcePreferences(extensionId:"org.moku.fixture"){key}}`); !strings.Contains(result, "not yet available") {
		t.Fatal(result)
	}
	if result = call(`mutation {deleteRepository(repositoryId:"1")}`); !strings.Contains(result, `"deleteRepository":true`) {
		t.Fatal(result)
	}
	if result = call(`{repositories{id}}`); !strings.Contains(result, `"repositories":[]`) {
		t.Fatal(result)
	}

}
