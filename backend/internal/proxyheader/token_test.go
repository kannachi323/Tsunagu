package proxyheader

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestHeadersOpaqueAndTamperRejected(t *testing.T) {
	token := Encode(map[string]string{"Cookie": "secret-clearance"})
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "v1."))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-clearance") {
		t.Fatal("cookie exposed")
	}
	var headers map[string]string
	if err := Decode(token, &headers); err != nil || headers["Cookie"] != "secret-clearance" {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	if Decode("v1."+base64.RawURLEncoding.EncodeToString(raw), &headers) == nil {
		t.Fatal("tampered token accepted")
	}
	if Decode(base64.RawURLEncoding.EncodeToString([]byte(`{"Referer":"https://source.example/"}`)), &headers) != nil {
		t.Fatal("legacy contract broken")
	}
}
