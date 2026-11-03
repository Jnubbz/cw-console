//go:build linux

package main

// On a Linux desktop (a development build, or the Pi box, which has its own updates) the
// app only says when there's a new version; it doesn't replace itself.
func setupInstaller(u *desktopUpdater) {}
