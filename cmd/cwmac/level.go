package main

import (
	"math"
	"time"
)

// audioLevel: how loud the receive audio is, over the last levelWindow, for the input
// meter and the "too quiet" / "clipping" warnings. Blocks heard while we transmit are
// left out (the decoders get silence then, and the sidetone isn't the band).
type audioLevel struct {
	blocks []levelBlock
}

type levelBlock struct {
	t    time.Time
	peak float32
	ss   float64 // sum of squares
	n    int
}

const levelWindow = 1500 * time.Millisecond

// levelMsg: dBFS (0 = full scale).
type levelMsg struct {
	Peak float32 `json:"peak"`
	RMS  float32 `json:"rms"`
}

func (l *audioLevel) add(samples []float32, now time.Time) {
	if len(samples) == 0 {
		return
	}
	b := levelBlock{t: now, n: len(samples)}
	for _, v := range samples {
		if v < 0 {
			v = -v
		}
		if v > b.peak {
			b.peak = v
		}
		b.ss += float64(v) * float64(v)
	}
	l.blocks = append(l.blocks, b)
	l.trim(now)
}

func (l *audioLevel) trim(now time.Time) {
	i := 0
	for i < len(l.blocks) && now.Sub(l.blocks[i].t) > levelWindow {
		i++
	}
	if i > 0 {
		l.blocks = append(l.blocks[:0], l.blocks[i:]...)
	}
}

// read: the level over the window, or nil when nothing was heard in it.
func (l *audioLevel) read(now time.Time) *levelMsg {
	l.trim(now)
	var peak float32
	var ss float64
	n := 0
	for _, b := range l.blocks {
		if b.peak > peak {
			peak = b.peak
		}
		ss += b.ss
		n += b.n
	}
	if n == 0 {
		return nil
	}
	return &levelMsg{Peak: dbfs(float64(peak)), RMS: dbfs(math.Sqrt(ss / float64(n)))}
}

// dbfs: 20·log10, floored at -120 dB (digital silence).
func dbfs(v float64) float32 {
	if v <= 1e-6 {
		return -120
	}
	return float32(math.Round(20*math.Log10(v)*10) / 10)
}
