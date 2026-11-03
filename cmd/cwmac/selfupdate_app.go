package main

// The Mac app's in-place swap (here, not in the darwin file, so the tests run everywhere).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jnubbz/cw-console/internal/selfupdate"
)

// appBundle: the .app the running program is in ("" if it isn't, e.g. go run).
func appBundle() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	macos := filepath.Dir(exe)
	contents := filepath.Dir(macos)
	app := filepath.Dir(contents)
	if filepath.Base(macos) != "MacOS" || filepath.Base(contents) != "Contents" || !strings.HasSuffix(app, ".app") {
		return ""
	}
	return app
}

// swapApp unpacks zip in stage and replaces app with the CW Decoder.app inside it.
func swapApp(app, zip, stage string) error {
	x := filepath.Join(stage, "x")
	if err := os.Mkdir(x, 0o755); err != nil {
		return err
	}
	if err := selfupdate.Unzip(zip, x); err != nil {
		return err
	}
	found, _ := filepath.Glob(filepath.Join(x, "*", "*.app"))
	if len(found) != 1 {
		return errors.New("the update doesn't contain the app")
	}
	newApp := found[0]
	bin := filepath.Join(newApp, "Contents", "MacOS", "cw-decoder")
	if st, err := os.Stat(bin); err != nil || st.Mode()&0o100 == 0 {
		return errors.New("the update's app has no program in it")
	}
	old := filepath.Join(stage, "old.app")
	if err := os.Rename(app, old); err != nil {
		return fmt.Errorf("can't replace %s: %v", app, err)
	}
	if err := os.Rename(newApp, app); err != nil {
		os.Rename(old, app) // put it back
		return fmt.Errorf("can't put the new version in place: %v", err)
	}
	return nil
}
