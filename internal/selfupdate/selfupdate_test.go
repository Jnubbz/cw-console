package selfupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicKeyIsValid(t *testing.T) {
	b, err := base64.StdEncoding.DecodeString(PublicKey)
	if err != nil || len(b) != ed25519.PublicKeySize {
		t.Fatalf("PublicKey: %v, %d bytes", err, len(b))
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		latest, current string
		want            bool
	}{
		{"0.2.17", "0.2.16", true},
		{"0.3.0", "0.2.99", true},
		{"1.0.0", "0.9.9", true},
		{"0.2.16", "0.2.16", false},
		{"0.2.15", "0.2.16", false},
		{"v0.2.17", "0.2.9", true},    // numeric, not string, order
		{"0.2.0", "0.2.0-test", true}, // the release beats its test build
		{"0.2.1", "0.2.0-test", true},
		{"0.2.0-test", "0.2.0", false},
		{"0.2.17", "dev", false}, // your own builds don't update themselves
		{"garbage", "0.2.0", false},
	} {
		if got := Newer(c.latest, c.current); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.latest, c.current, got)
		}
	}
}

type release struct {
	pub        string
	manifest   []byte
	sig        string
	zip        []byte
	srv        *httptest.Server
	manifestOK bool
}

func makeZip(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if strings.HasSuffix(name, "/") {
			h.SetMode(os.ModeDir | 0o755)
		} else if strings.Contains(name, "MacOS/") || strings.HasSuffix(name, ".exe") {
			h.SetMode(0o755)
		} else {
			h.SetMode(0o644)
		}
		w, err := z.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	z.Close()
	return b.Bytes()
}

// newRelease: a test server publishing a signed release, like GitHub's.
func newRelease(t *testing.T) *release {
	pub, priv, _ := ed25519.GenerateKey(nil)
	r := &release{pub: base64.StdEncoding.EncodeToString(pub)}
	r.zip = makeZip(t, map[string]string{
		"CW-Decoder-Mac/CW Decoder.app/Contents/MacOS/cw-decoder": "new binary",
		"CW-Decoder-Mac/CW Decoder.app/Contents/Info.plist":       "<plist/>",
	})
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		switch q.URL.Path {
		case "/latest.json":
			w.Write(r.manifest)
		case "/latest.json.sig":
			w.Write([]byte(r.sig))
		case "/CW-Decoder-Mac.zip":
			w.Write(r.zip)
		default:
			http.NotFound(w, q)
		}
	}))
	t.Cleanup(r.srv.Close)
	sum := sha256.Sum256(r.zip)
	m := Manifest{Version: "0.2.17", Notes: "test", Assets: map[string]Asset{"darwin-arm64": {
		Name: "CW-Decoder-Mac.zip", URL: r.srv.URL + "/CW-Decoder-Mac.zip", Size: int64(len(r.zip)), SHA256: hex.EncodeToString(sum[:])}}}
	r.manifest, _ = json.Marshal(m)
	r.sig = Sign(r.manifest, priv)
	return r
}

func (r *release) client() *Client {
	return &Client{HTTP: r.srv.Client(), ManifestURL: r.srv.URL + "/latest.json", SignatureURL: r.srv.URL + "/latest.json.sig", Key: r.pub}
}

func TestCheckDownloadUnzip(t *testing.T) {
	r := newRelease(t)
	// The test server is http; Download insists on https, so serve the zip over TLS too.
	tls := httptest.NewTLSServer(r.srv.Config.Handler)
	defer tls.Close()
	c := r.client()
	m, err := c.Check(context.Background())
	if err != nil || m.Version != "0.2.17" {
		t.Fatal(m, err)
	}
	a := m.Assets["darwin-arm64"]
	a.URL = tls.URL + "/CW-Decoder-Mac.zip"
	c.HTTP = tls.Client()
	dir := t.TempDir()
	var last int64
	path, err := c.Download(context.Background(), a, dir, func(done, total int64) { last = done })
	if err != nil || last != a.Size {
		t.Fatal(err, last)
	}
	out := filepath.Join(dir, "x")
	os.Mkdir(out, 0o755)
	if err := Unzip(path, out); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(out, "CW-Decoder-Mac", "CW Decoder.app", "Contents", "MacOS", "cw-decoder")
	b, _ := os.ReadFile(bin)
	st, _ := os.Stat(bin)
	if string(b) != "new binary" || st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("binary %q mode %v", b, st.Mode())
	}
}

func TestRefusesTampering(t *testing.T) {
	r := newRelease(t)
	c := r.client()

	// manifest changed after signing
	good := r.manifest
	r.manifest = bytes.Replace(good, []byte("0.2.17"), []byte("9.9.99"), 1)
	if _, err := c.Check(context.Background()); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("tampered manifest: %v", err)
	}
	r.manifest = good

	// signed by someone else
	_, other, _ := ed25519.GenerateKey(nil)
	goodSig := r.sig
	r.sig = Sign(r.manifest, other)
	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("wrong key accepted")
	}
	r.sig = "not base64!"
	if _, err := c.Check(context.Background()); err == nil {
		t.Fatal("garbage signature accepted")
	}
	r.sig = goodSig

	// zip swapped after signing
	m, err := c.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tls := httptest.NewTLSServer(r.srv.Config.Handler)
	defer tls.Close()
	c.HTTP = tls.Client()
	a := m.Assets["darwin-arm64"]
	a.URL = tls.URL + "/CW-Decoder-Mac.zip"
	dir := t.TempDir()
	orig := r.zip
	r.zip = append([]byte(nil), orig...)
	r.zip[len(r.zip)/2] ^= 0xff
	if _, err := c.Download(context.Background(), a, dir, nil); err == nil || !strings.Contains(err.Error(), "doesn't match") {
		t.Fatalf("swapped zip: %v", err)
	}
	r.zip = append(orig, 'x') // longer than signed
	if _, err := c.Download(context.Background(), a, dir, nil); err == nil {
		t.Fatal("oversized zip accepted")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatalf("bad downloads left behind: %v", ents)
	}
	// http (not https) refused
	a.URL = r.srv.URL + "/CW-Decoder-Mac.zip"
	r.zip = orig
	if _, err := c.Download(context.Background(), a, dir, nil); err == nil {
		t.Fatal("plain http accepted")
	}
}

func TestNoReleaseYet(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), ManifestURL: srv.URL + "/latest.json", SignatureURL: srv.URL + "/latest.json.sig", Key: PublicKey}
	if _, err := c.Check(context.Background()); !NoRelease(err) {
		t.Fatalf("got %v", err)
	}
}

func TestUnzipRefusesEscapes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../evil", "a/../../evil"} {
		p := filepath.Join(dir, "bad.zip")
		os.WriteFile(p, makeZip(t, map[string]string{name: "x"}), 0o644)
		out := filepath.Join(dir, "out")
		os.MkdirAll(out, 0o755)
		if err := Unzip(p, out); err == nil {
			t.Errorf("%q extracted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "evil")); err == nil {
		t.Fatal("file written outside")
	}
}
