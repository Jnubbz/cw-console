//go:build !windows

package main

import "os/exec"

// Desktops other than Windows: the Mac app plays through its own cw-play (play_mac.go)
// to the device picked in Setup > Radio > Playback device; without it, the system
// player and the default output. The Pi uses aplay -D and never gets here when that
// device name is an ALSA one.

func outputDevices() []outputDev { return macOutputs() }

func startPlatformAudio(pcm []byte, rate, ch int, dev string) (ft8Playback, bool, error) {
	p := macPlayer()
	if p == "" {
		return nil, false, nil
	}
	cmd := exec.Command(p, macPlayArgs(dev, rate, ch)...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, true, err
	}
	return &cmdPlayback{cmd: cmd, pcm: pcm, in: stdin, ready: true}, true, nil
}
