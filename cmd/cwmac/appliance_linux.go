//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

func syncDisk() { syscall.Sync() }

// setClock sets the system clock (the Pi has no clock battery unless one is fitted).
func setClock(t time.Time) error {
	tv := syscall.NsecToTimeval(t.UnixNano())
	if err := syscall.Settimeofday(&tv); err != nil {
		return err
	}
	exec.Command("hwclock", "-w", "-u").Run() // keep it in the clock chip (if it has a battery)
	saveLastClock()
	return nil
}

func powerOff() { syscall.Sync(); exec.Command("poweroff").Run() }

func restartHotspot() error { return exec.Command("/etc/init.d/S45hotspot", "restart").Run() }

// saveLastClock remembers the time on the data drive, so a reboot without a clock
// battery starts from roughly the right date instead of 1970.
func saveLastClock() {
	os.WriteFile(filepath.Join(dataDir, ".lastclock"), []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o644)
}

func mountBoot() error {
	if os.Getenv("CWBOX_BOOTDIR") != "" {
		return nil // tests: a plain folder
	}
	os.MkdirAll(bootMountDir, 0o755)
	dev := filepath.Join(partByUUIDDir, diskID+"-01")
	return syscall.Mount(dev, bootMountDir, "vfat", syscall.MS_NOATIME, "flush")
}

func unmountBoot() {
	if os.Getenv("CWBOX_BOOTDIR") != "" {
		return
	}
	syscall.Sync()
	syscall.Unmount(bootMountDir, 0)
}

// trialReboot restarts into the newly written system for one try ("0 tryboot" tells
// the Pi firmware to use the [tryboot] part of config.txt).
func trialReboot() error {
	if os.Getenv("CWBOX_BOOTDIR") != "" {
		return nil
	}
	syscall.Sync()
	syscall.Mount("", "/data", "", syscall.MS_REMOUNT|syscall.MS_RDONLY, "")
	syscall.Sync()
	arg, _ := syscall.BytePtrFromString("0 tryboot")
	_, _, e := syscall.Syscall6(syscall.SYS_REBOOT, syscall.LINUX_REBOOT_MAGIC1, syscall.LINUX_REBOOT_MAGIC2,
		syscall.LINUX_REBOOT_CMD_RESTART2, uintptr(unsafe.Pointer(arg)), 0, 0)
	if e != 0 {
		return e
	}
	return nil
}
