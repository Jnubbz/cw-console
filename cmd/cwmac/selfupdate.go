package main

// Updates for the desktop app (the Pi box has its own A/B update, update.go): look for a
// new release a few seconds after starting and every 6 hours, show it on the page, and on
// "Install and restart" download it, check it against the signed manifest, swap the app
// in place and start the new one. The page reloads itself when the new version answers.
// See internal/selfupdate for what's checked.

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"time"

	"github.com/Jnubbz/cw-console/internal/selfupdate"
)

type updateStatus struct {
	Current    string `json:"current"`
	Latest     string `json:"latest,omitempty"`
	Available  bool   `json:"available"`
	Notes      string `json:"notes,omitempty"`
	State      string `json:"state"` // "" | "checking" | "downloading" | "installing" | "restarting"
	Error      string `json:"error,omitempty"`
	Done       int64  `json:"done,omitempty"`
	Total      int64  `json:"total,omitempty"`
	CanInstall bool   `json:"canInstall"`
	Checked    string `json:"checked,omitempty"` // when it last checked (RFC 3339)
}

type desktopUpdater struct {
	mu      sync.Mutex
	st      updateStatus
	found   *selfupdate.Manifest
	client  *selfupdate.Client
	platKey string                 // "darwin-arm64"
	restart func()                 // start the new app and quit this one
	busy    func() error           // refuses while sending
	stage   func() (string, error) // folder on the app's volume for the download
	apply   func(zip, stage string) error
}

var updater = &desktopUpdater{client: &selfupdate.Client{}}

func (u *desktopUpdater) status() updateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	s := u.st
	s.Current = appVersion
	s.CanInstall = u.apply != nil && s.Available
	return s
}

// check asks for the latest release. A failed or stale check never hides an update
// already found.
func (u *desktopUpdater) check(ctx context.Context) updateStatus {
	u.mu.Lock()
	if u.st.State != "" && u.st.State != "checking" {
		u.mu.Unlock()
		return u.status()
	}
	u.st.State, u.st.Error = "checking", ""
	u.mu.Unlock()
	m, err := u.client.Check(ctx)
	u.mu.Lock()
	u.st.State = ""
	u.st.Checked = time.Now().UTC().Format(time.RFC3339)
	switch {
	case selfupdate.NoRelease(err):
	case err != nil:
		u.st.Error = err.Error()
	case selfupdate.Newer(m.Version, appVersion) && m.Assets[u.platKey].URL != "":
		u.found = m
		u.st.Available, u.st.Latest, u.st.Notes = true, m.Version, m.Notes
	case u.found == nil:
		u.st.Latest = m.Version
	}
	u.mu.Unlock()
	return u.status()
}

// startInstall downloads, verifies and installs in the background, then restarts.
func (u *desktopUpdater) startInstall() error {
	if u.apply == nil {
		return errors.New("updating itself works in the installed Mac app; download the new version from GitHub instead")
	}
	if err := u.busy(); err != nil {
		return err
	}
	u.mu.Lock()
	if u.found == nil {
		u.mu.Unlock()
		return errors.New("you're up to date")
	}
	if u.st.State != "" && u.st.State != "checking" {
		u.mu.Unlock()
		return errors.New("already updating")
	}
	m := u.found
	u.st.State, u.st.Error, u.st.Done, u.st.Total = "downloading", "", 0, m.Assets[u.platKey].Size
	u.mu.Unlock()
	go func() {
		fail := func(err error) {
			u.mu.Lock()
			u.st.State, u.st.Error = "", "Update failed: "+err.Error()
			u.mu.Unlock()
		}
		stage, err := u.stage()
		if err != nil {
			fail(err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		zip, err := u.client.Download(ctx, m.Assets[u.platKey], stage, func(done, total int64) {
			u.mu.Lock()
			u.st.Done, u.st.Total = done, total
			u.mu.Unlock()
		})
		if err != nil {
			fail(err)
			return
		}
		u.mu.Lock()
		u.st.State = "installing"
		u.mu.Unlock()
		if err := u.apply(zip, stage); err != nil {
			fail(err)
			return
		}
		u.mu.Lock()
		u.st.State = "restarting"
		u.mu.Unlock()
		time.Sleep(700 * time.Millisecond) // let the page see "restarting"
		u.restart()
	}()
	return nil
}

// startUpdater: the periodic check (desktop app only).
func (u *desktopUpdater) start(e *Engine, restart func()) {
	u.platKey = runtime.GOOS + "-" + runtime.GOARCH
	u.restart = restart
	u.busy = func() error {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.tx.busyLocked() {
			return errors.New("wait until sending has finished")
		}
		return nil
	}
	setupInstaller(u) // per OS: sets stage and apply where updating in place works
	go func() {
		time.Sleep(4 * time.Second)
		for {
			u.check(context.Background())
			time.Sleep(6 * time.Hour)
		}
	}()
}
