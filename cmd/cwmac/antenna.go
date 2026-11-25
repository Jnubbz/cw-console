package main

// The FT-991A has two antenna jacks: HF/50 MHz and 144/430 MHz. Transmitting into the
// one with nothing on it is a bad idea, so Setup > Radio says which are connected, and
// nothing keys the radio (CW, FT8, Tune, the PTT test) while its transmit frequency is
// on a band whose jack is empty. The band keys for that jack are left off too.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Jnubbz/cw-console/rig/ft710"
)

// rigFreq: the radio's VFOs as the state polls last read them.
type rigFreq struct {
	a, b  int
	split bool // transmitting on VFO-B
	vhf   bool // a radio with 144/430 MHz (the FT-991A)
	at    time.Time
}

// note takes one CAT answer from the state polls (FA, FB, FT; others are ignored).
func (f *rigFreq) note(ans string, vhf bool, now time.Time) {
	ans = strings.TrimSuffix(ans, ";")
	switch {
	case len(ans) > 2 && (strings.HasPrefix(ans, "FA") || strings.HasPrefix(ans, "FB")):
		hz, err := strconv.Atoi(ans[2:])
		if err != nil {
			return
		}
		if ans[1] == 'A' {
			f.a = hz
		} else {
			f.b = hz
		}
	case len(ans) == 3 && strings.HasPrefix(ans, "FT"):
		f.split = ans[2] == '1'
	default:
		return
	}
	f.vhf, f.at = vhf, now
}

// noteState takes a whole state read (the Rig sheet's).
func (f *rigFreq) noteState(st *ft710.State, vhf bool, now time.Time) {
	f.a, f.b, f.split, f.vhf, f.at = st.FreqA, st.FreqB, st.Split, vhf, now
}

// rigFreshFor: how long a polled frequency is trusted.
const rigFreshFor = 10 * time.Second

// txHz: where the radio would transmit, 0 if not known lately.
func (f *rigFreq) txHz(now time.Time) int {
	if f.at.IsZero() || now.Sub(f.at) > rigFreshFor {
		return 0
	}
	if f.split && f.b > 0 {
		return f.b
	}
	return f.a
}

// vhfHz: on the 144/430 MHz jack.
func vhfHz(hz int) bool { return hz >= 100_000_000 }

// antennaBlockLocked: why the radio mustn't transmit now, or nil. Only for a radio with
// both jacks (the FT-991A), and only when its frequency has been read lately.
func (e *Engine) antennaBlockLocked() error {
	f := e.rigFreq
	hz := f.txHz(time.Now())
	if !f.vhf || hz == 0 {
		return nil
	}
	if vhfHz(hz) && !e.st.VHFAnt {
		return fmt.Errorf("not transmitting on %s MHz: no 144/430 MHz antenna is ticked in Setup > Radio > Antennas", mhzStr(hz))
	}
	if !vhfHz(hz) && e.st.HFAntOff {
		return fmt.Errorf("not transmitting on %s MHz: no HF/50 MHz antenna is ticked in Setup > Radio > Antennas", mhzStr(hz))
	}
	return nil
}

func (e *Engine) antennaBlock() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.antennaBlockLocked()
}

// bandsWithAntennaLocked: the radio's band keys, less the ones with no antenna.
func (e *Engine) bandsWithAntennaLocked(m *ft710.Model) []string {
	all := m.Bands()
	if !m.HasVHF() {
		return all
	}
	var out []string
	for _, b := range all {
		vhf := b == "2m" || b == "70cm"
		if (vhf && e.st.VHFAnt) || (!vhf && !e.st.HFAntOff) {
			out = append(out, b)
		}
	}
	return out
}
