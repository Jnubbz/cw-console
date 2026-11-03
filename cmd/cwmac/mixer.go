package main

// The rig's USB sound chip has its own playback volume (and mute). Windows remembers
// one; on the box it is whatever the chip powers up with, possibly muted or near zero,
// so the rig would get no transmit audio. Before transmitting, set it to full and
// unmuted; the page's Level sets the actual drive.

import (
	"os/exec"
	"regexp"
	"strings"
	"sync"
)

var amixerPath = envOr("CWBOX_AMIXER", "amixer")

var mixerDone struct {
	sync.Mutex
	card string
	log  string
}

// cardOf: "plughw:CARD=Device,DEV=0" -> "Device".
func cardOf(dev string) string {
	m := regexp.MustCompile(`CARD=([^,]+)`).FindStringSubmatch(dev)
	if m == nil {
		return ""
	}
	return m[1]
}

// playbackControls: the simple mixer controls with a playback volume, from `amixer
// scontents`, except Mic (its playback side is a monitor loop). Switch-only controls
// (e.g. the C-Media "Auto Gain Control", which is the mic's AGC) are left alone.
func playbackControls(scontents string) []string {
	var out []string
	cur := ""
	for _, l := range strings.Split(scontents, "\n") {
		if m := regexp.MustCompile(`^Simple mixer control '([^']+)',0`).FindStringSubmatch(l); m != nil {
			cur = m[1]
			continue
		}
		if cur != "" && strings.Contains(l, "Capabilities:") && strings.Contains(l, " pvolume") {
			if !strings.EqualFold(cur, "Mic") {
				out = append(out, cur)
			}
			cur = ""
		}
	}
	return out
}

func setPlaybackVolume(dev string) {
	card := cardOf(dev)
	if card == "" || !appliance {
		return
	}
	mixerDone.Lock()
	defer mixerDone.Unlock()
	b, _ := exec.Command(amixerPath, "-c", card, "scontents").CombinedOutput()
	log := ""
	for _, c := range playbackControls(string(b)) {
		o, err := exec.Command(amixerPath, "-q", "-c", card, "sset", c, "100%", "unmute").CombinedOutput()
		log += "set " + c + " 100% unmute"
		if err != nil {
			log += ": " + strings.TrimSpace(string(o))
		}
		log += "\n"
	}
	mixerDone.card, mixerDone.log = card, log
}

// mixerStatus: for the System log.
func mixerStatus(dev string) string {
	card := cardOf(dev)
	if card == "" {
		return "no rig sound card\n"
	}
	b, _ := exec.Command(amixerPath, "-c", card, "scontents").CombinedOutput()
	mixerDone.Lock()
	defer mixerDone.Unlock()
	return mixerDone.log + string(b)
}
