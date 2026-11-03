package main

// Updates on Windows: a few seconds after starting (and every 6 hours) look for a new
// release; if there is one, ask. On Yes: download it (progress in the title bar), check it
// against the signed manifest (internal/selfupdate), then replace the files next to
// CW-Decoder.exe. Windows lets a running .exe be renamed but not overwritten, so each
// file is renamed to *.old first and the new one put in its place. The new app is started
// with -after-update <pid>: it waits for this one to close before opening its window, and
// clears the *.old files.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/Jnubbz/cw-console/internal/selfupdate"
)

var appVersion = "dev" // set at build time (-ldflags -X main.appVersion=...)

const (
	MB_YESNO        = 0x4
	MB_ICONQUESTION = 0x20
	MB_ICONINFO     = 0x40
	IDYES           = 6
)

var upd struct {
	mu       sync.Mutex
	found    *selfupdate.Manifest // newer release, not yet asked about
	asked    bool                 // asked once this run
	busy     bool                 // downloading/installing
	progress string               // title bar text while busy
	fail     string               // error to show
	ready    bool                 // installed: restart now
}

var updClient = &selfupdate.Client{}

const updPlat = "windows-amd64"

// updateStartup: called at the very start of main. Waits for the old copy to close after
// an update, clears its *.old files, and starts the periodic check.
func updateStartup() {
	fs := flag.NewFlagSet("cw", flag.ContinueOnError)
	after := fs.Int("after-update", 0, "")
	fs.Parse(os.Args[1:])
	if *after > 0 {
		waitForExit(uint32(*after), 15*time.Second)
	}
	if dir, err := exeDir(); err == nil {
		filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() && strings.HasSuffix(p, ".old") {
				os.Remove(p)
			}
			return nil
		})
	}
	go func() {
		time.Sleep(6 * time.Second)
		for {
			if m, err := updClient.Check(context.Background()); err == nil &&
				selfupdate.Newer(m.Version, appVersion) && m.Assets[updPlat].URL != "" {
				upd.mu.Lock()
				upd.found = m
				upd.mu.Unlock()
			}
			time.Sleep(6 * time.Hour)
		}
	}()
}

// updateTick: from the UI timer. Asks, shows progress and errors, restarts when ready.
func updateTick() {
	upd.mu.Lock()
	m, asked, busy, prog, fail, ready := upd.found, upd.asked, upd.busy, upd.progress, upd.fail, upd.ready
	if m != nil && !asked && !busy {
		upd.asked = true // set before the box: the timer keeps running while it's open
	}
	upd.fail = ""
	upd.mu.Unlock()

	if busy {
		pSetWindowTextW.Call(A.hwnd, uintptr(unsafe.Pointer(u16(appTitle+" - "+prog))))
	}
	if fail != "" {
		pSetWindowTextW.Call(A.hwnd, uintptr(unsafe.Pointer(u16(appTitle))))
		messageBox(A.hwnd, fail, appTitle+" update", MB_ICONWARNING)
	}
	if ready {
		restartIntoUpdate()
		return
	}
	if m == nil || asked || busy {
		return
	}
	msg := fmt.Sprintf("CW Console %s is ready (you have %s).\n\n", m.Version, appVersion)
	if m.Notes != "" {
		msg += m.Notes + "\n\n"
	}
	msg += "Install it and restart now? Sending stops for a moment; your settings, macros and log are kept."
	r, _, _ := pMessageBoxW.Call(A.hwnd, uintptr(unsafe.Pointer(u16(msg))), uintptr(unsafe.Pointer(u16(appTitle+" update"))),
		MB_YESNO|MB_ICONQUESTION)
	if r != IDYES {
		return
	}
	if TX.keyer != nil && (len(TX.queue) > 0 || TX.keyer.Busy()) {
		messageBox(A.hwnd, "Wait until sending has finished, then restart CW Decoder to install the update.", appTitle+" update", MB_ICONINFO)
		upd.mu.Lock()
		upd.asked = false
		upd.mu.Unlock()
		return
	}
	upd.mu.Lock()
	upd.busy, upd.progress = true, "downloading the update..."
	upd.mu.Unlock()
	go installUpdate(m)
}

func installUpdate(m *selfupdate.Manifest) {
	fail := func(err error) {
		upd.mu.Lock()
		upd.busy, upd.fail = false, "The update didn't install: "+err.Error()+"\n\nCW Decoder keeps working as it was."
		upd.mu.Unlock()
	}
	dir, err := exeDir()
	if err != nil {
		fail(err)
		return
	}
	stage, err := os.MkdirTemp(dir, ".cw-update-")
	if err != nil {
		fail(fmt.Errorf("can't write in %s (%v): unzip the new version by hand this time", dir, err))
		return
	}
	defer os.RemoveAll(stage)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	zip, err := updClient.Download(ctx, m.Assets[updPlat], stage, func(done, total int64) {
		upd.mu.Lock()
		upd.progress = fmt.Sprintf("downloading the update... %d%%", done*100/max(total, 1))
		upd.mu.Unlock()
	})
	if err != nil {
		fail(err)
		return
	}
	upd.mu.Lock()
	upd.progress = "installing the update..."
	upd.mu.Unlock()
	if err := applyUpdate(zip, stage, dir); err != nil {
		fail(err)
		return
	}
	upd.mu.Lock()
	upd.busy, upd.ready = false, true
	upd.mu.Unlock()
}

// applyUpdate unpacks the zip and puts its files in dir (the folder with CW-Decoder.exe).
// Existing files are renamed to *.old first; if anything fails, what was done is undone.
func applyUpdate(zip, stage, dir string) error {
	x := filepath.Join(stage, "x")
	if err := os.Mkdir(x, 0o755); err != nil {
		return err
	}
	if err := selfupdate.Unzip(zip, x); err != nil {
		return err
	}
	roots, _ := filepath.Glob(filepath.Join(x, "*"))
	if len(roots) != 1 {
		return errors.New("the update has an unexpected layout")
	}
	root := roots[0]
	if _, err := os.Stat(filepath.Join(root, "CW-Decoder.exe")); err != nil {
		return errors.New("the update doesn't contain CW-Decoder.exe")
	}
	var files []string
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			rel, _ := filepath.Rel(root, p)
			files = append(files, rel)
		}
		return nil
	})
	type move struct{ target, old string }
	var done []move
	undo := func() {
		for i := len(done) - 1; i >= 0; i-- {
			os.Remove(done[i].target)
			if done[i].old != "" {
				os.Rename(done[i].old, done[i].target)
			}
		}
	}
	for _, rel := range files {
		target := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			undo()
			return err
		}
		mv := move{target: target}
		if _, err := os.Stat(target); err == nil {
			mv.old = target + ".old"
			os.Remove(mv.old)
			if err := os.Rename(target, mv.old); err != nil {
				undo()
				return fmt.Errorf("can't replace %s: %v", rel, err)
			}
		}
		if err := os.Rename(filepath.Join(root, rel), target); err != nil {
			if mv.old != "" {
				os.Rename(mv.old, target)
			}
			undo()
			return fmt.Errorf("can't put %s in place: %v", rel, err)
		}
		done = append(done, mv)
	}
	return nil
}

// restartIntoUpdate starts the new CW-Decoder.exe (it waits for this one) and closes.
func restartIntoUpdate() {
	upd.mu.Lock()
	upd.ready = false
	upd.mu.Unlock()
	exe, err := os.Executable()
	if err == nil {
		cmd := exec.Command(exe, "-after-update", strconv.Itoa(os.Getpid()))
		err = cmd.Start()
	}
	if err != nil {
		messageBox(A.hwnd, "The update is installed. Start CW Decoder again to use it.", appTitle+" update", MB_ICONINFO)
	}
	pSendMessageW.Call(A.hwnd, WM_CLOSE, 0, 0)
}

func exeDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return filepath.Dir(exe), nil
}

var (
	pOpenProcess         = kernel32.NewProc("OpenProcess")
	pWaitForSingleObject = kernel32.NewProc("WaitForSingleObject")
)

// waitForExit waits (up to d) for process pid to end.
func waitForExit(pid uint32, d time.Duration) {
	const SYNCHRONIZE = 0x00100000
	h, _, _ := pOpenProcess.Call(SYNCHRONIZE, 0, uintptr(pid))
	if h == 0 {
		return // already gone
	}
	pWaitForSingleObject.Call(h, uintptr(d.Milliseconds()))
	pCloseHandle.Call(h)
}
