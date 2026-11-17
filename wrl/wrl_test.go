package wrl

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBodyUsesADIFNamesAndOneMode(t *testing.T) {
	buf, err := Body(Contact{
		Call: "w1aw", At: time.Date(2026, 8, 5, 14, 35, 0, 0, time.UTC),
		FreqMHz: 14.074, Band: "20m", Mode: "FT8", State: "ct",
		MyActivities: []Activity{{Type: "POTA", Ref: "US-0662", Primary: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatal(err)
	}
	if m["programId"] != ProgramID || m["call"] != "W1AW" || m["mode"] != "FT8" {
		t.Fatalf("%s", buf)
	}
	if _, ok := m["submode"]; ok {
		t.Fatalf("submode was sent: %s", buf)
	}
	if _, ok := m["callsign"]; ok || m["frequency"] != nil {
		t.Fatalf("wrong field names: %s", buf)
	}
	if m["freq"] != 14.074 || m["band"] != "20m" || m["state"] != "CT" {
		t.Fatalf("%s", buf)
	}
	if _, ok := m["logbookId"]; ok {
		t.Fatalf("empty logbook id was sent: %s", buf)
	}
	acts, _ := m["myActivities"].([]any)
	if len(acts) != 1 {
		t.Fatalf("activities %s", buf)
	}
}

func TestCreateAndImportDoNotLeakTheKeyOntoTheUpload(t *testing.T) {
	var uploadHit bool
	upload := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploadHit = true
		if r.Method != http.MethodPut {
			t.Errorf("method %s", r.Method)
		}
		if r.Header.Get("Authorization") != "" {
			t.Errorf("API key was sent to the upload URL")
		}
		buf, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(buf), "<CALL:4>W1AW") {
			t.Errorf("body %s", buf)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upload.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer wrl_live_test" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/contacts":
			_, _ = io.WriteString(w, `{"data":{"id":"c1"}}`)
		case "/v1/imports":
			_, _ = io.WriteString(w, `{"data":{"id":"i1","upload":{"url":"`+upload.URL+`"}}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer apiSrv.Close()

	c := &Client{Base: apiSrv.URL, Key: "wrl_live_test", HTTP: apiSrv.Client()}
	id, err := c.Create(context.Background(), Contact{
		Call: "W1AW", At: time.Date(2026, 8, 5, 14, 35, 0, 0, time.UTC), Mode: "CW",
	})
	if err != nil || id != "c1" {
		t.Fatalf("create %s %v", id, err)
	}
	imp, err := c.StartImport(context.Background(), "")
	if err != nil || imp.ID != "i1" {
		t.Fatalf("import %+v %v", imp, err)
	}
	if err := UploadADIF(context.Background(), upload.Client(), imp.UploadURL, []byte("<CALL:4>W1AW <EOR>\n")); err != nil {
		t.Fatal(err)
	}
	if !uploadHit {
		t.Fatal("upload URL was not called")
	}
}

func TestRateLimitSurfacesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":"RATE_LIMITED","message":"slow down"}}`)
	}))
	defer srv.Close()
	c := &Client{Base: srv.URL, Key: "k", HTTP: srv.Client()}
	_, err := c.Create(context.Background(), Contact{Call: "W1AW", At: time.Now()})
	if err == nil || !strings.Contains(err.Error(), "30") {
		t.Fatalf("%v", err)
	}
}

func TestEmptyKeyDoesNotDial(t *testing.T) {
	c := &Client{}
	if _, err := c.Create(context.Background(), Contact{Call: "W1AW", At: time.Now()}); err == nil {
		t.Fatal("missing key was accepted")
	}
}
