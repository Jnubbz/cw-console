package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func winZip(t *testing.T, files map[string]string) string {
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for name, body := range files {
		w, _ := z.Create(name)
		w.Write([]byte(body))
	}
	z.Close()
	p := filepath.Join(t.TempDir(), "u.zip")
	os.WriteFile(p, b.Bytes(), 0o644)
	return p
}

func installed(t *testing.T, files map[string]string) string {
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	return dir
}

func read(dir, name string) string {
	b, _ := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	return string(b)
}

func TestApplyUpdateReplacesTheFiles(t *testing.T) {
	dir := installed(t, map[string]string{"CW-Decoder.exe": "old exe", "deepcw/model.onnx": "old model", "my-notes.txt": "keep"})
	zp := winZip(t, map[string]string{
		"CW-Decoder-Windows/CW-Decoder.exe":         "new exe",
		"CW-Decoder-Windows/deepcw/model.onnx":      "new model",
		"CW-Decoder-Windows/cwdecode-file.exe":      "new tool",
		"CW-Decoder-Windows/deepcw/model.onnx.json": "{}",
	})
	stage, _ := os.MkdirTemp(dir, ".cw-update-")
	if err := applyUpdate(zp, stage, dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"CW-Decoder.exe": "new exe", "deepcw/model.onnx": "new model",
		"cwdecode-file.exe": "new tool", "my-notes.txt": "keep", "CW-Decoder.exe.old": "old exe"} {
		if got := read(dir, name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
}

func TestApplyUpdateRefusesAZipWithoutTheApp(t *testing.T) {
	dir := installed(t, map[string]string{"CW-Decoder.exe": "old exe"})
	zp := winZip(t, map[string]string{"CW-Decoder-Windows/README.txt": "hi"})
	stage, _ := os.MkdirTemp(dir, ".cw-update-")
	if err := applyUpdate(zp, stage, dir); err == nil {
		t.Fatal("installed a zip without CW-Decoder.exe")
	}
	if read(dir, "CW-Decoder.exe") != "old exe" {
		t.Fatal("old app touched")
	}
}

func TestApplyUpdateUndoesOnFailure(t *testing.T) {
	dir := installed(t, map[string]string{"CW-Decoder.exe": "old exe", "deepcw/model.onnx": "old model",
		"deepcw/model.onnx.old/blocker": "x"}) // a non-empty folder where model.onnx would be set aside
	zp := winZip(t, map[string]string{
		"CW-Decoder-Windows/CW-Decoder.exe":    "new exe",
		"CW-Decoder-Windows/deepcw/model.onnx": "new model",
	})
	stage, _ := os.MkdirTemp(dir, ".cw-update-")
	if err := applyUpdate(zp, stage, dir); err == nil {
		t.Fatal("expected a failure")
	}
	if read(dir, "CW-Decoder.exe") != "old exe" || read(dir, "deepcw/model.onnx") != "old model" {
		t.Fatalf("not undone: exe %q, model %q", read(dir, "CW-Decoder.exe"), read(dir, "deepcw/model.onnx"))
	}
}
