package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// received.json is the app's own file, but a damaged or edited one must not be able to make
// the app touch files outside its inbox.
func TestAnIndexCannotPointOutsideTheInbox(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.png") // what "../outside" would reach from inbox/
	os.WriteFile(outside, []byte("not yours to delete"), 0o644)
	old := time.Now().Add(-30 * 24 * time.Hour).UTC().Format(time.RFC3339) // old enough to expire
	idx, _ := json.Marshal([]Picture{
		{ID: "../outside", Time: old, Mode: "Scottie 1"},
		{ID: `..\outside`, Time: old, Mode: "Scottie 1"},
		{ID: "", Time: old, Mode: "Scottie 1"},
	})
	os.WriteFile(filepath.Join(dir, "received.json"), idx, 0o600)

	r := newReceiver(dir, t.TempDir(), func() int { return 0 })

	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("a file outside the inbox was removed: %v", err)
	}
	if len(r.pics) != 0 {
		t.Errorf("listed %d pictures with unsafe names", len(r.pics))
	}
}

func TestAPictureIdIsAFileName(t *testing.T) {
	for id, want := range map[string]bool{
		"2026-11-14 193012Z Scottie 1 14230 kHz":    true,
		"2026-11-14 193012Z Sent Robot 36 7171 kHz": true,
		"2026-11-14 193012Z PD120":                  true,
		"":                                          false,
		"../x":                                      false,
		`a\b`:                                       false,
		"C:x":                                       false,
		"a\x00b":                                    false,
		"a\nb":                                      false,
		string(make([]byte, 300)):                   false,
		"2026-11-14 193012Z Scottie 1 14230 kHz.json": true, // a dot is fine: ".png" goes on the end
	} {
		if got := validID(id); got != want {
			t.Errorf("validID(%q) = %v, want %v", id, got, want)
		}
	}
}

// A damaged index is set aside, not overwritten by the next save.
func TestADamagedIndexIsKept(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "received.json"), []byte(`[{"id": "half a fi`), 0o600)
	r := newReceiver(dir, t.TempDir(), func() int { return 0 })
	if len(r.pics) != 0 {
		t.Fatalf("%d pictures from a damaged index", len(r.pics))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "received.json.bad")); err != nil || string(b) != `[{"id": "half a fi` {
		t.Fatalf("the damaged index wasn't kept: %q, %v", b, err)
	}
}

// The app's own folder is for this user only; the Pictures folder is the user's to share from.
func TestAppFilesArePrivate(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Windows doesn't use these permission bits")
	}
	dir, pics := t.TempDir(), filepath.Join(t.TempDir(), "Pictures")
	r := newReceiver(dir, pics, func() int { return 0 })
	r.mu.Lock()
	r.writeIndex()
	r.mu.Unlock()
	for _, p := range []string{filepath.Join(dir, "inbox"), filepath.Join(dir, "received.json")} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is open to other users: %v", p, st.Mode().Perm())
		}
	}
}
