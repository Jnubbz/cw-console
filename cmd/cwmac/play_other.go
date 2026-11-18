//go:build !windows

package main

// Desktops other than Windows play a WAV through the system player. The Pi
// uses aplay -D and never gets here when that device name is an ALSA one.

func outputDevices() []outputDev { return nil }

func startPlatformAudio(pcm []byte, rate, ch int, dev string) (ft8Playback, bool, error) {
	return nil, false, nil
}
