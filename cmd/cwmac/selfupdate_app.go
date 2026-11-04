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

// swapApp unpacks zip in stage and puts the app inside it in place of app, under the new
// app's own name (an update from a build still called "CW Decoder.app" lands as "CW
// Console.app"). Returns where it went. The old app is moved into stage (thrown away with
// it); if the new one can't be put in place, the old one is put back.
func swapApp(app, zip, stage string) (string, error) {
	x := filepath.Join(stage, "x")
	if err := os.Mkdir(x, 0o755); err != nil {
		return "", err
	}
	if err := selfupdate.Unzip(zip, x); err != nil {
		return "", err
	}
	found, _ := filepath.Glob(filepath.Join(x, "*", "*.app"))
	if len(found) != 1 {
		return "", errors.New("the update doesn't contain the app")
	}
	newApp := found[0]
	if !hasProgram(newApp) {
		return "", errors.New("the update's app has no program in it")
	}
	target := filepath.Join(filepath.Dir(app), filepath.Base(newApp))
	old := filepath.Join(stage, "old.app")
	if err := os.Rename(app, old); err != nil {
		return "", fmt.Errorf("can't replace %s: %v", app, err)
	}
	var older string // a copy already at the new name (renamed): set it aside too
	if target != app {
		if _, err := os.Stat(target); err == nil {
			older = filepath.Join(stage, "older.app")
			if err := os.Rename(target, older); err != nil {
				os.Rename(old, app)
				return "", fmt.Errorf("can't replace %s: %v", target, err)
			}
		}
	}
	if err := os.Rename(newApp, target); err != nil {
		if older != "" {
			os.Rename(older, target)
		}
		os.Rename(old, app) // put it back
		return "", fmt.Errorf("can't put the new version in place: %v", err)
	}
	return target, nil
}

// hasProgram: the bundle's main program (Info.plist CFBundleExecutable) is there and runnable.
func hasProgram(app string) bool {
	exe := "cw-decoder"
	if b, err := os.ReadFile(filepath.Join(app, "Contents", "Info.plist")); err == nil {
		s := string(b)
		if i := strings.Index(s, "<key>CFBundleExecutable</key>"); i >= 0 {
			rest := s[i:]
			if a := strings.Index(rest, "<string>"); a >= 0 {
				if z := strings.Index(rest[a:], "</string>"); z > 0 {
					exe = rest[a+len("<string>") : a+z]
				}
			}
		}
	}
	st, err := os.Stat(filepath.Join(app, "Contents", "MacOS", exe))
	return err == nil && st.Mode()&0o100 != 0
}
