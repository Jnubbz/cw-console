// Package selfupdate finds, checks and unpacks new releases of CW Console.
//
// Every merge to main publishes a GitHub release (.github/workflows/release.yml) with the
// Windows and Mac zips and a manifest, latest.json, that names each zip's SHA-256. The
// manifest is signed with an Ed25519 key that exists only as a GitHub secret
// (UPDATE_SIGNING_KEY); the app carries the public key. An update is installed only if
// the manifest's signature checks out AND the zip's size and SHA-256 match it, so neither a
// tampered download nor someone who can upload release files can put code on the radio
// computer without the key.
package selfupdate

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PublicKey verifies latest.json (raw Ed25519 public key, base64). The private half is the
// repository secret UPDATE_SIGNING_KEY. To rotate: new key pair, new secret, new PublicKey,
// and that one release installed by hand.
const PublicKey = "GEYNu9hrLkF2aphaJp1TCQ6efsPqKyvX8Im2VpUSCp4="

// Where releases are published.
const (
	ManifestURL  = "https://github.com/Jnubbz/cw-console/releases/latest/download/latest.json"
	SignatureURL = ManifestURL + ".sig"
)

const (
	maxManifest = 64 << 10
	maxAsset    = 300 << 20
)

// Asset: one platform's download.
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest is latest.json.
type Manifest struct {
	Version string           `json:"version"` // "0.2.17"
	Date    string           `json:"date"`
	Notes   string           `json:"notes"`
	Assets  map[string]Asset `json:"assets"` // "darwin-arm64", "windows-amd64"
}

// Verify checks sig (base64) over the exact manifest bytes with key (base64), then parses.
func Verify(manifest, sig []byte, key string) (*Manifest, error) {
	pub, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("update key is broken")
	}
	s, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(s) != ed25519.SignatureSize {
		return nil, errors.New("the update's signature is missing or malformed")
	}
	if !ed25519.Verify(pub, manifest, s) {
		return nil, errors.New("the update's signature doesn't match: not installing it")
	}
	var m Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, fmt.Errorf("the update's manifest is unreadable: %v", err)
	}
	if _, ok := parse(m.Version); !ok {
		return nil, fmt.Errorf("the update's version %q is unreadable", m.Version)
	}
	return &m, nil
}

// Sign: the release workflow's half (cmd-less, used by internal/signmanifest).
func Sign(manifest []byte, priv ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest))
}

// parse "0.2.17", "v0.2.17", "0.2.0-test" -> [0 2 17]; "dev" isn't a version.
func parse(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Newer: whether latest is newer than current. A build with no version ("dev") never
// updates itself (it's someone's own build).
func Newer(latest, current string) bool {
	l, ok1 := parse(latest)
	c, ok2 := parse(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	// same numbers: a pre-release ("0.2.0-test") is older than the release
	return strings.ContainsAny(strings.TrimPrefix(current, "v"), "-+") && !strings.ContainsAny(strings.TrimPrefix(latest, "v"), "-+")
}

// Client fetches with time limits; Base overrides the URLs (tests).
type Client struct {
	HTTP         *http.Client
	ManifestURL  string
	SignatureURL string
	Key          string
}

func (c *Client) defaults() *Client {
	d := *c
	if d.HTTP == nil {
		d.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	if d.ManifestURL == "" {
		d.ManifestURL, d.SignatureURL = ManifestURL, SignatureURL
	}
	if d.Key == "" {
		d.Key = PublicKey
	}
	return &d
}

func (c *Client) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "cw-console-updater")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoRelease
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s is too large", url)
	}
	return b, nil
}

var errNoRelease = errors.New("no release published yet")

// Check fetches and verifies the latest manifest. It returns it whether or not it's newer.
func (c *Client) Check(ctx context.Context) (*Manifest, error) {
	d := c.defaults()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m, err := d.get(ctx, d.ManifestURL, maxManifest)
	if err != nil {
		if errors.Is(err, errNoRelease) {
			return nil, errNoRelease
		}
		return nil, fmt.Errorf("couldn't check for updates: %v", err)
	}
	s, err := d.get(ctx, d.SignatureURL, 1024)
	if err != nil {
		return nil, fmt.Errorf("couldn't check for updates (signature): %v", err)
	}
	return Verify(m, s, d.Key)
}

// NoRelease: whether err means nothing has been published yet.
func NoRelease(err error) bool { return errors.Is(err, errNoRelease) }

// Download fetches a into dir, checking its size and SHA-256 as it goes; progress gets the
// bytes so far. On any mismatch the file is removed.
func (c *Client) Download(ctx context.Context, a Asset, dir string, progress func(done, total int64)) (string, error) {
	d := c.defaults()
	if a.Size <= 0 || a.Size > maxAsset || len(a.SHA256) != 64 || !strings.HasPrefix(a.URL, "https://") {
		return "", errors.New("the update's manifest has a bad download entry")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "cw-console-updater")
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("download failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: %s", resp.Status)
	}
	f, err := os.CreateTemp(dir, "cw-update-*.zip")
	if err != nil {
		return "", err
	}
	path := f.Name()
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	h := sha256.New()
	var done int64
	buf := make([]byte, 64<<10)
	body := io.LimitReader(resp.Body, a.Size+1)
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return "", err
			}
			h.Write(buf[:n])
			done += int64(n)
			if done > a.Size {
				return "", errors.New("the download is larger than the manifest says: not installing it")
			}
			if progress != nil {
				progress(done, a.Size)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", fmt.Errorf("download failed: %v", rerr)
		}
	}
	if done != a.Size {
		return "", fmt.Errorf("the download stopped early (%d of %d bytes)", done, a.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, a.SHA256) {
		return "", errors.New("the download doesn't match the signed manifest: not installing it")
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}

// Unzip extracts zipPath into dir (which must exist), keeping executable bits and refusing
// entries that would land outside dir or are links.
func Unzip(zipPath, dir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	root, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	for _, f := range r.File {
		name := filepath.FromSlash(f.Name)
		target := filepath.Join(root, name)
		if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
			return fmt.Errorf("bad path in update: %q", f.Name)
		}
		mode := f.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			return fmt.Errorf("unexpected link in update: %q", f.Name)
		case f.FileInfo().IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		perm := os.FileMode(0o644)
		if mode&0o111 != 0 {
			perm = 0o755
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(out, io.LimitReader(rc, maxAsset))
		rc.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}
