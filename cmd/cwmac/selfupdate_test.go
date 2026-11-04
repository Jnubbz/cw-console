package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Jnubbz/cw-console/internal/selfupdate"
)

func macZip(t *testing.T, withApp bool) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	add := func(name, body string, mode os.FileMode) {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(mode)
		w, _ := z.CreateHeader(h)
		w.Write([]byte(body))
	}
	add("CW-Decoder-Mac/README-Mac.txt", "readme", 0o644)
	if withApp {
		add("CW-Decoder-Mac/CW Decoder.app/Contents/MacOS/cw-decoder", "NEW", 0o755)
		add("CW-Decoder-Mac/CW Decoder.app/Contents/Info.plist", "<plist/>", 0o644)
	}
	z.Close()
	return b.Bytes()
}

func fakeApp(t *testing.T) string {
	app := filepath.Join(t.TempDir(), "Applications", "CW Decoder.app")
	os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755)
	os.WriteFile(filepath.Join(app, "Contents", "MacOS", "cw-decoder"), []byte("OLD"), 0o755)
	return app
}

func TestSwapApp(t *testing.T) {
	app := fakeApp(t)
	stage, _ := os.MkdirTemp(filepath.Dir(app), ".cw-update-")
	zp := filepath.Join(stage, "u.zip")
	os.WriteFile(zp, macZip(t, true), 0o644)
	if err := swapApp(app, zp, stage); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "Contents", "MacOS", "cw-decoder")); string(b) != "NEW" {
		t.Fatalf("app has %q", b)
	}
	os.RemoveAll(stage)
	if ents, _ := os.ReadDir(filepath.Dir(app)); len(ents) != 1 {
		t.Fatalf("left over: %v", ents)
	}
}

func TestSwapAppKeepsTheOldOneIfTheUpdateIsBroken(t *testing.T) {
	app := fakeApp(t)
	stage, _ := os.MkdirTemp(filepath.Dir(app), ".cw-update-")
	zp := filepath.Join(stage, "u.zip")
	os.WriteFile(zp, macZip(t, false), 0o644)
	if err := swapApp(app, zp, stage); err == nil {
		t.Fatal("a zip with no app was installed")
	}
	if b, _ := os.ReadFile(filepath.Join(app, "Contents", "MacOS", "cw-decoder")); string(b) != "OLD" {
		t.Fatalf("app has %q", b)
	}
}

func TestDesktopUpdateEndToEnd(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	z := macZip(t, true)
	var manifest []byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			w.Write(manifest)
		case "/latest.json.sig":
			w.Write([]byte(selfupdate.Sign(manifest, priv)))
		case "/mac.zip":
			w.Write(z)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	sum := sha256.Sum256(z)
	plat := runtime.GOOS + "-" + runtime.GOARCH
	manifest, _ = json.Marshal(selfupdate.Manifest{Version: "0.2.9", Notes: "new stuff", Assets: map[string]selfupdate.Asset{
		plat: {Name: "mac.zip", URL: srv.URL + "/mac.zip", Size: int64(len(z)), SHA256: hex.EncodeToString(sum[:])}}})

	old := appVersion
	appVersion = "0.2.3"
	defer func() { appVersion = old }()

	app := fakeApp(t)
	restarted := make(chan bool, 1)
	sending := false
	u := &desktopUpdater{
		client:  &selfupdate.Client{HTTP: srv.Client(), ManifestURL: srv.URL + "/latest.json", SignatureURL: srv.URL + "/latest.json.sig", Key: base64.StdEncoding.EncodeToString(pub)},
		platKey: plat,
		restart: func() { restarted <- true },
		busy: func() error {
			if sending {
				return errors.New("wait until sending has finished")
			}
			return nil
		},
		stage: func() (string, error) { return os.MkdirTemp(filepath.Dir(app), ".cw-update-") },
	}
	u.apply = func(zp, stage string) error { defer os.RemoveAll(stage); return swapApp(app, zp, stage) }

	s := u.check(context.Background())
	if !s.Available || s.Latest != "0.2.9" || s.Current != "0.2.3" || !s.CanInstall || s.Notes != "new stuff" {
		t.Fatalf("check: %+v", s)
	}
	sending = true
	if err := u.startInstall(); err == nil {
		t.Fatal("installed while sending")
	}
	sending = false
	if err := u.startInstall(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-restarted:
	case <-time.After(10 * time.Second):
		t.Fatalf("no restart: %+v", u.status())
	}
	if s := u.status(); s.State != "restarting" || s.Done != int64(len(z)) {
		t.Fatalf("status: %+v", s)
	}
	if b, _ := os.ReadFile(filepath.Join(app, "Contents", "MacOS", "cw-decoder")); string(b) != "NEW" {
		t.Fatalf("app has %q", b)
	}

	// same version: nothing to install; a broken signature: an error, not an update
	appVersion = "0.2.9"
	u2 := &desktopUpdater{client: u.client, platKey: plat}
	if s := u2.check(context.Background()); s.Available || s.Error != "" {
		t.Fatalf("up to date: %+v", s)
	}
	u2.client = &selfupdate.Client{HTTP: srv.Client(), ManifestURL: srv.URL + "/latest.json", SignatureURL: srv.URL + "/latest.json.sig", Key: selfupdate.PublicKey}
	appVersion = "0.2.3"
	if s := u2.check(context.Background()); s.Available || s.Error == "" {
		t.Fatalf("wrong key accepted: %+v", s)
	}
}
