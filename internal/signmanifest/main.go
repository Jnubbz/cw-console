// signmanifest writes a release's latest.json and latest.json.sig (run by the release
// workflow). The signing key comes from the UPDATE_SIGNING_KEY environment variable: an
// Ed25519 private key in PEM (openssl genpkey -algorithm ed25519). It must match
// selfupdate.PublicKey.
//
//	signmanifest -version 0.2.17 -base https://github.com/OWNER/REPO/releases/download/v0.2.17 \
//	    -out dist darwin-arm64=dist/CW-Decoder-Mac.zip windows-amd64=dist/CW-Decoder-Windows.zip
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jnubbz/cw-console/internal/selfupdate"
)

func main() {
	version := flag.String("version", "", "release version, e.g. 0.2.17")
	base := flag.String("base", "", "download URL the assets are published under")
	notes := flag.String("notes", "", "release notes shown in the app")
	out := flag.String("out", ".", "folder for latest.json and latest.json.sig")
	flag.Parse()
	if err := run(*version, *base, *notes, *out, flag.Args()); err != nil {
		fmt.Fprintln(os.Stderr, "signmanifest:", err)
		os.Exit(1)
	}
}

func run(version, base, notes, out string, assets []string) error {
	if version == "" || !strings.HasPrefix(base, "https://") || len(assets) == 0 {
		return fmt.Errorf("need -version, an https -base and platform=zip arguments")
	}
	priv, err := loadKey(os.Getenv("UPDATE_SIGNING_KEY"))
	if err != nil {
		return err
	}
	pub, _ := base64.StdEncoding.DecodeString(selfupdate.PublicKey)
	if !priv.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(pub)) {
		return fmt.Errorf("UPDATE_SIGNING_KEY doesn't match selfupdate.PublicKey: apps would refuse this release")
	}
	m := selfupdate.Manifest{Version: version, Date: time.Now().UTC().Format(time.RFC3339), Notes: notes,
		Assets: map[string]selfupdate.Asset{}}
	for _, a := range assets {
		plat, path, ok := strings.Cut(a, "=")
		if !ok {
			return fmt.Errorf("asset %q: want platform=path", a)
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		name := filepath.Base(path)
		m.Assets[plat] = selfupdate.Asset{Name: name, URL: strings.TrimSuffix(base, "/") + "/" + name, Size: n,
			SHA256: hex.EncodeToString(h.Sum(nil))}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	sig := selfupdate.Sign(b, priv)
	if _, err := selfupdate.Verify(b, []byte(sig), selfupdate.PublicKey); err != nil {
		return fmt.Errorf("self-check failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(out, "latest.json"), b, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "latest.json.sig"), []byte(sig+"\n"), 0o644)
}

func loadKey(s string) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("UPDATE_SIGNING_KEY isn't set (repository Settings > Secrets and variables > Actions)")
	}
	blk, _ := pem.Decode([]byte(s))
	if blk == nil {
		return nil, fmt.Errorf("UPDATE_SIGNING_KEY isn't a PEM key")
	}
	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("UPDATE_SIGNING_KEY: %v", err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("UPDATE_SIGNING_KEY isn't an Ed25519 key")
	}
	return priv, nil
}
