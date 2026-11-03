package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jnubbz/cw-console/internal/selfupdate"
)

func TestRefusesAWrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	t.Setenv("UPDATE_SIGNING_KEY", string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	dir := t.TempDir()
	zip := filepath.Join(dir, "CW-Decoder-Mac.zip")
	os.WriteFile(zip, []byte("zip"), 0o644)
	err := run("0.2.1", "https://example.invalid/d", "", dir, []string{"darwin-arm64=" + zip})
	if err == nil || !strings.Contains(err.Error(), "doesn't match") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "latest.json")); err == nil {
		t.Fatal("wrote a manifest the apps would refuse")
	}
}

func TestNeedsTheKey(t *testing.T) {
	t.Setenv("UPDATE_SIGNING_KEY", "")
	if err := run("0.2.1", "https://example.invalid/d", "", t.TempDir(), []string{"x=y"}); err == nil ||
		!strings.Contains(err.Error(), "isn't set") {
		t.Fatalf("got %v", err)
	}
	_ = selfupdate.PublicKey
}
