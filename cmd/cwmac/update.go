package main

// Over-the-air updates for the Pi box (A/B). The card holds two systems, a and b; the one
// not running is overwritten from an update file uploaded on the page, then the box
// reboots into it once on trial (the Pi firmware's "tryboot"). If the new system comes
// up properly it becomes the default (S50cwdecoder does that); if not, the firmware goes
// back to the old one on the next boot. Settings and logs (CWDATA) are never touched.
//
// Update file: a gzipped tar whose first entry is MANIFEST:
//   version 2026.10.19
//   file Image <sha256>
//   file bcm2712-rpi-5-b.dtb <sha256>
//   file overlays/bcm2712d0.dtbo <sha256>   (device-tree overlays)
//   file rootfs.ext4 <sha256>

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var appVersion = "dev" // set at build time (-ldflags -X main.appVersion=...)

const diskID = "43574445" // the image's MBR disk signature ("CWDE")

var updateMu sync.Mutex

// test hooks
var (
	cmdlinePath   = envOr("CWBOX_CMDLINE", "/proc/cmdline")
	trybootPath   = envOr("CWBOX_TRYBOOT", "/proc/device-tree/chosen/bootloader/tryboot")
	partByUUIDDir = envOr("CWBOX_PARTDIR", "/dev/disk/by-partuuid")
	bootMountDir  = envOr("CWBOX_BOOTDIR", "/boot")
	committedPath = envOr("CWBOX_COMMITTED", "/tmp/cw-update-committed")
)

func currentSlot() string {
	b, _ := os.ReadFile(cmdlinePath)
	m := regexp.MustCompile(`\bcwslot=([ab])\b`).FindStringSubmatch(string(b))
	if m == nil {
		return ""
	}
	return m[1]
}

func otherSlot(s string) string {
	if s == "a" {
		return "b"
	}
	return "a"
}

// inTrial: running a just-installed system that hasn't been accepted yet.
// inTrial: running a just-installed system that hasn't been accepted yet. The firmware's
// tryboot flag stays set for the whole boot, so the boot script also leaves a marker once
// it has made this system the default (after that, updates are fine again).
func inTrial() bool {
	if _, err := os.Stat(committedPath); err == nil {
		return false
	}
	b, err := os.ReadFile(trybootPath)
	return err == nil && len(b) == 4 && b[3] == 1 && b[0]|b[1]|b[2] == 0
}

// updateBlocked: why an update can't be installed right now ("" = it can).
func updateBlocked() string {
	if currentSlot() == "" {
		return "this system can't be updated this way (no A/B slots)"
	}
	if inTrial() {
		return "the box is still trying the last update (it decides within about a minute of starting): wait a moment and try again"
	}
	return ""
}

func slotPart(slot string) string {
	n := "02"
	if slot == "b" {
		n = "03"
	}
	return filepath.Join(partByUUIDDir, diskID+"-"+n)
}

type manifest struct {
	version string
	files   map[string]string // name -> sha256 hex
}

var safeName = regexp.MustCompile(`^(overlays/)?[A-Za-z0-9._,+-]{1,64}$`)

func parseManifest(r io.Reader) (manifest, error) {
	m := manifest{files: map[string]string{}}
	sc := bufio.NewScanner(io.LimitReader(r, 1<<20))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		switch {
		case len(f) == 2 && f[0] == "version":
			m.version = f[1]
		case len(f) == 3 && f[0] == "file" && safeName.MatchString(f[1]) && len(f[2]) == 64:
			m.files[f[1]] = strings.ToLower(f[2])
		}
	}
	if m.version == "" || m.files["rootfs.ext4"] == "" || m.files["Image"] == "" {
		return m, errors.New("not a CW Decoder update file (bad manifest)")
	}
	return m, nil
}

// applyUpdate installs an update into the slot that isn't running. It returns the new
// version. Nothing about the running system changes; the caller then trial-reboots.
func applyUpdate(r io.Reader, progress func(string)) (string, error) {
	locked := false
	for i := 0; i < 100 && !locked; i++ { // a replaced upload needs a moment to let go
		if locked = updateMu.TryLock(); !locked {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if !locked {
		return "", errors.New("another upload is still being installed; wait a minute and try again")
	}
	defer updateMu.Unlock()
	cur := currentSlot()
	if why := updateBlocked(); why != "" {
		return "", errors.New(why)
	}
	target := otherSlot(cur)
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", errors.New("not an update file (not gzip)")
	}
	tr := tar.NewReader(gz)
	hdr, err := tr.Next()
	if err != nil || hdr.Name != "MANIFEST" {
		return "", errors.New("not a CW Decoder update file (no manifest)")
	}
	man, err := parseManifest(tr)
	if err != nil {
		return "", err
	}
	if err := mountBoot(); err != nil {
		return "", fmt.Errorf("can't open the boot partition: %v", err)
	}
	defer unmountBoot()
	staging := filepath.Join(bootMountDir, target+".new")
	os.RemoveAll(staging)
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return "", err
	}
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("update file is damaged: %v", err)
		}
		name := hdr.Name
		want, ok := man.files[name]
		if !ok || hdr.Typeflag != tar.TypeReg {
			continue // ignore anything not in the manifest
		}
		var dst string
		if name == "rootfs.ext4" {
			dst = slotPart(target)
			progress("Writing the new system (" + mb(hdr.Size) + ")...")
			if err := checkPartSize(dst, hdr.Size); err != nil {
				return "", err
			}
		} else {
			dst = filepath.Join(staging, filepath.FromSlash(name))
			os.MkdirAll(filepath.Dir(dst), 0o755)
		}
		got, err := writeVerified(dst, tr, name != "rootfs.ext4")
		if err != nil {
			return "", fmt.Errorf("writing %s: %v", name, err)
		}
		if got != want {
			return "", fmt.Errorf("%s doesn't match the manifest (damaged download?)", name)
		}
		seen[name] = true
	}
	for name := range man.files {
		if !seen[name] {
			return "", fmt.Errorf("update file is missing %s", name)
		}
	}
	// Kernel command line for the target slot, written fresh. (Not copied from
	// /proc/cmdline: the Pi firmware adds its own settings there at every boot, so a
	// copy grows with each update until the kernel cuts off root= and can't boot.)
	if err := os.WriteFile(filepath.Join(staging, "cmdline.txt"), []byte(slotCmdline(target)+"\n"), 0o644); err != nil {
		return "", err
	}
	final := filepath.Join(bootMountDir, target)
	os.RemoveAll(final)
	if err := os.Rename(staging, final); err != nil {
		return "", err
	}
	// On a "tryboot" the Pi bootloader reads tryboot.txt instead of config.txt.
	cfg, err := os.ReadFile(filepath.Join(bootMountDir, "config.txt"))
	if err != nil {
		return "", fmt.Errorf("can't read config.txt: %v", err)
	}
	tmp := filepath.Join(bootMountDir, "tryboot.new")
	if err := os.WriteFile(tmp, []byte(trybootConfig(string(cfg), target)), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(bootMountDir, "tryboot.txt")); err != nil {
		return "", err
	}
	syncDisk()
	return man.version, nil
}

// trybootConfig: the box's config.txt (keeping board settings such as device_tree=),
// pointed at the slot being tried, with no conditional sections.
func trybootConfig(cfg, slot string) string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(cfg, "\r", ""), "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "[") || strings.HasPrefix(t, "os_prefix=") {
			continue
		}
		if strings.HasPrefix(t, "#") {
			t = "# Trial boot of system " + slot + " after an update. Written by the box: don't edit."
		}
		out = append(out, t)
	}
	out = append(out, "os_prefix="+slot+"/")
	return strings.Join(out, "\r\n") + "\r\n"
}

// slotCmdline: the kernel command line for a slot (same as board/post-image.sh writes).
func slotCmdline(slot string) string {
	return "root=PARTUUID=" + filepath.Base(slotPart(slot)) +
		" rootwait ro console=ttyAMA10,115200 console=tty1 quiet loglevel=3 panic=10 usbcore.autosuspend=-1 cwslot=" + slot
}

func writeVerified(path string, r io.Reader, create bool) (string, error) {
	flags := os.O_WRONLY
	if create {
		flags |= os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(f, io.TeeReader(r, h)); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func checkPartSize(dev string, need int64) error {
	real, err := filepath.EvalSymlinks(dev)
	if err != nil {
		return fmt.Errorf("can't find the spare system partition: %v", err)
	}
	b, err := os.ReadFile(filepath.Join("/sys/class/block", filepath.Base(real), "size"))
	if err != nil {
		return nil // can't tell (tests); the write will fail if it doesn't fit
	}
	var sectors int64
	fmt.Sscan(strings.TrimSpace(string(b)), &sectors)
	if sectors > 0 && need > sectors*512 {
		return errors.New("this update is too big for this card's layout")
	}
	return nil
}

func mb(n int64) string { return fmt.Sprintf("%.0f MB", float64(n)/1e6) }
