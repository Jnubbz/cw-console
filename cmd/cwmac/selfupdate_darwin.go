//go:build darwin

package main

// Installing an update on the Mac: the new CW Console.app is unpacked next to the running
// one (same disk, so the swap is two renames), checked, then swapped in. The old one is
// thrown away; if the second rename fails the old one is put back. Then `open` starts the
// new app (without opening another browser tab: the page reloads by itself).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func setupInstaller(u *desktopUpdater) {
	app := appBundle()
	if app == "" {
		return
	}
	u.stage = func() (string, error) {
		d, err := os.MkdirTemp(filepath.Dir(app), ".cw-update-")
		if err != nil {
			return "", fmt.Errorf("can't write next to %s (%v): drag the new version to Applications by hand this time", app, err)
		}
		return d, nil
	}
	installed := app
	u.apply = func(zip, stage string) error {
		defer os.RemoveAll(stage)
		to, err := swapApp(app, zip, stage)
		if err == nil {
			installed = to
		}
		return err
	}
	u.restart = func(quit func()) func() {
		return func() {
			// The new app: its own window (CW Console.app), or this page reloading (-nobrowser).
			cmd := exec.Command("/bin/sh", "-c", `sleep 1.5; exec /usr/bin/open -n "$1" --args -nobrowser`, "sh", installed)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			cmd.Start()
			quit()
		}
	}(u.restart)
}
