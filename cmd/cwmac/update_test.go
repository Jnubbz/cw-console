package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type upFile struct {
	name string
	data []byte
}

func makeUpdate(t *testing.T, version string, files []upFile, corrupt string) []byte {
	var man strings.Builder
	man.WriteString("version " + version + "\n")
	for _, f := range files {
		h := sha256.Sum256(f.data)
		man.WriteString("file " + f.name + " " + hex.EncodeToString(h[:]) + "\n")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name string, b []byte) {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b)), Typeflag: tar.TypeReg})
		tw.Write(b)
	}
	add("MANIFEST", []byte(man.String()))
	for _, f := range files {
		d := f.data
		if f.name == corrupt {
			d = append([]byte{}, d...)
			d[len(d)/2] ^= 0xff
		}
		add(f.name, d)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func setupSlots(t *testing.T, slot string, trial bool) (boot, parts string) {
	dir := t.TempDir()
	boot, parts = filepath.Join(dir, "boot"), filepath.Join(dir, "parts")
	os.MkdirAll(filepath.Join(boot, "a"), 0o755)
	os.MkdirAll(parts, 0o755)
	os.WriteFile(filepath.Join(boot, "a", "Image"), []byte("old kernel"), 0o644)
	os.WriteFile(filepath.Join(boot, "config.txt"), []byte("# x\r\nkernel=Image\r\n[all]\r\ndevice_tree=bcm2712d0-rpi-5-b.dtb\r\nos_prefix="+slot+"/\r\n[tryboot]\r\nos_prefix="+otherSlot(slot)+"/\r\n[all]\r\n"), 0o644)
	for _, n := range []string{"02", "03"} {
		os.WriteFile(filepath.Join(parts, diskID+"-"+n), make([]byte, 0), 0o644)
	}
	cl := filepath.Join(dir, "cmdline")
	part := "02"
	if slot == "b" {
		part = "03"
	}
	// what the Pi firmware really shows: its own settings in front of cmdline.txt
	os.WriteFile(cl, []byte("reboot=w coherent_pool=1M 8250.nr_uarts=1 pci=pcie_bus_skip_ss smsc95xx.macaddr=2C:CF:67:F4:10:85 vc_mem.mem_base=0x3fc00000 vc_mem.mem_size=0x40000000 root=PARTUUID="+diskID+"-"+part+" rootwait ro console=tty1 quiet panic=10 cwslot="+slot+"\n"), 0o644)
	tb := filepath.Join(dir, "tryboot")
	v := []byte{0, 0, 0, 0}
	if trial {
		v[3] = 1
	}
	os.WriteFile(tb, v, 0o644)
	cmdlinePath, trybootPath, partByUUIDDir, bootMountDir = cl, tb, parts, boot
	committedPath = filepath.Join(dir, "committed")
	t.Setenv("CWBOX_BOOTDIR", boot)
	return
}

func TestUpdateWritesOtherSlot(t *testing.T) {
	boot, parts := setupSlots(t, "a", false)
	root := make([]byte, 3<<20)
	rand.Read(root)
	files := []upFile{{"Image", []byte("new kernel")}, {"bcm2712-rpi-5-b.dtb", []byte("dtb")},
		{"overlays/bcm2712d0.dtbo", []byte("d0")}, {"rootfs.ext4", root}}
	ver, err := applyUpdate(bytes.NewReader(makeUpdate(t, "2026.10.20", files, "")), func(string) {})
	if err != nil || ver != "2026.10.20" {
		t.Fatal(ver, err)
	}
	got, _ := os.ReadFile(filepath.Join(parts, diskID+"-03"))
	if !bytes.Equal(got, root) {
		t.Fatal("root B not written")
	}
	if k, _ := os.ReadFile(filepath.Join(boot, "b", "Image")); string(k) != "new kernel" {
		t.Fatal("kernel B not written")
	}
	if o, _ := os.ReadFile(filepath.Join(boot, "b", "overlays", "bcm2712d0.dtbo")); string(o) != "d0" {
		t.Fatal("overlay B not written")
	}
	cl, _ := os.ReadFile(filepath.Join(boot, "b", "cmdline.txt"))
	if string(cl) != "root=PARTUUID="+diskID+"-03 rootwait ro console=ttyAMA10,115200 console=tty1 quiet loglevel=3 panic=10 usbcore.autosuspend=-1 cwslot=b\n" {
		t.Fatalf("cmdline B (must not copy the firmware's settings): %q", cl)
	}
	tb, _ := os.ReadFile(filepath.Join(boot, "tryboot.txt"))
	if want := "# Trial boot of system b after an update. Written by the box: don't edit.\r\nkernel=Image\r\ndevice_tree=bcm2712d0-rpi-5-b.dtb\r\nos_prefix=b/\r\n"; string(tb) != want {
		t.Fatalf("tryboot.txt: %q", tb)
	}
	if k, _ := os.ReadFile(filepath.Join(boot, "a", "Image")); string(k) != "old kernel" {
		t.Fatal("running slot touched")
	}
	if a, _ := os.ReadFile(filepath.Join(parts, diskID+"-02")); len(a) != 0 {
		t.Fatal("running root touched")
	}
	// from slot b the next update goes to a
	setupSlots(t, "b", false)
	if _, err := applyUpdate(bytes.NewReader(makeUpdate(t, "2026.10.21", files, "")), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(partByUUIDDir, diskID+"-02")); !bytes.Equal(got, root) {
		t.Fatal("from b: root A not written")
	}
}

func TestUpdateRefusesBadFiles(t *testing.T) {
	boot, _ := setupSlots(t, "a", false)
	files := []upFile{{"Image", []byte("new kernel")}, {"rootfs.ext4", []byte("root")}}
	if _, err := applyUpdate(bytes.NewReader(makeUpdate(t, "x", files, "rootfs.ext4")), func(string) {}); err == nil || !strings.Contains(err.Error(), "doesn't match") {
		t.Fatalf("corrupt rootfs accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(boot, "b")); err == nil {
		t.Fatal("a failed update must not leave a bootable slot b")
	}
	if _, err := applyUpdate(bytes.NewReader([]byte("hello")), func(string) {}); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := applyUpdate(bytes.NewReader(makeUpdate(t, "x", files[:1], "")), func(string) {}); err == nil {
		t.Fatal("update without rootfs accepted")
	}
	setupSlots(t, "b", true)
	if _, err := applyUpdate(bytes.NewReader(makeUpdate(t, "x", files, "")), func(string) {}); err == nil || !strings.Contains(err.Error(), "trying") {
		t.Fatalf("update during a trial boot accepted: %v", err)
	}
}

func TestUpdateAfterCommit(t *testing.T) {
	// a trial boot that has been accepted: the tryboot flag is still set, but updates
	// must work again (they used to need a reboot)
	setupSlots(t, "b", true)
	files := []upFile{{"Image", []byte("k")}, {"rootfs.ext4", []byte("root")}}
	if _, err := applyUpdate(bytes.NewReader(makeUpdate(t, "x", files, "")), func(string) {}); err == nil {
		t.Fatal("not committed yet: must refuse")
	}
	os.WriteFile(committedPath, nil, 0o644)
	if _, err := applyUpdate(bytes.NewReader(makeUpdate(t, "y", files, "")), func(string) {}); err != nil {
		t.Fatalf("after commit: %v", err)
	}
}
