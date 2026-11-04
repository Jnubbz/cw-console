package appdata

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstRunCopiesCWDecodersFolder(t *testing.T) {
	base := t.TempDir()
	old := filepath.Join(base, OldName)
	os.MkdirAll(filepath.Join(old, "Logs"), 0o755)
	os.WriteFile(filepath.Join(old, "settings.json"), []byte(`{"wpm":22}`), 0o644)
	os.WriteFile(filepath.Join(old, "macros.txt"), []byte("F1 CQ"), 0o644)
	os.WriteFile(filepath.Join(old, "Logs", "cw-decoder-log.adi"), []byte("<EOH>"), 0o644)

	dir := Dir(base)
	if dir != filepath.Join(base, Name) {
		t.Fatal(dir)
	}
	for _, f := range []string{"settings.json", "macros.txt", "Logs/cw-decoder-log.adi"} {
		a, _ := os.ReadFile(filepath.Join(old, f))
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil || string(a) != string(b) {
			t.Errorf("%s: %q vs %q (%v)", f, a, b, err)
		}
	}
	// the old folder is untouched; later changes stay separate
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"wpm":30}`), 0o644)
	if b, _ := os.ReadFile(filepath.Join(old, "settings.json")); string(b) != `{"wpm":22}` {
		t.Fatalf("old settings changed: %s", b)
	}
	if Dir(base) != dir { // second run: no copy again
		t.Fatal("second run")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "settings.json")); string(b) != `{"wpm":30}` {
		t.Fatalf("copied again over new settings: %s", b)
	}
}

func TestFreshInstall(t *testing.T) {
	base := t.TempDir()
	dir := Dir(base)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatal(err)
	}
}
