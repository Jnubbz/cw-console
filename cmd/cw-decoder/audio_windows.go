//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

// waveIn capture, polled from the GUI timer (CALLBACK_NULL): no callbacks, no threads.

const (
	numBufs  = 10
	bufMilli = 40 // ms per buffer
)

type inputDevice struct {
	ID   uint32
	Name string
}

func listInputs() []inputDevice {
	devs := []inputDevice{{WAVE_MAPPER, "Default input (Windows setting)"}}
	n, _, _ := pWaveInGetNumDevs.Call()
	for i := uint32(0); i < uint32(n); i++ {
		var caps WAVEINCAPSW
		r, _, _ := pWaveInGetDevCapsW.Call(uintptr(i), ptr(&caps), unsafe.Sizeof(caps))
		if r == 0 {
			devs = append(devs, inputDevice{i, syscall.UTF16ToString(caps.SzPname[:])})
		}
	}
	return devs
}

type capture struct {
	h       uintptr
	rate    uint32
	hdrs    [numBufs]WAVEHDR
	bufs    [numBufs][]int16
	next    int
	running bool
}

// open starts capture on device id, trying 12 kHz first (plenty for CW, light on CPU),
// then common rates if the driver refuses.
func (c *capture) open(id uint32) error {
	var lastErr uintptr
	for _, rate := range []uint32{12000, 48000, 44100, 16000, 8000} {
		wfx := WAVEFORMATEX{
			WFormatTag: WAVE_FORMAT_PCM, NChannels: 1, NSamplesPerSec: rate,
			NAvgBytesPerSec: rate * 2, NBlockAlign: 2, WBitsPerSample: 16,
		}
		var h uintptr
		r, _, _ := pWaveInOpen.Call(ptr(&h), uintptr(id), ptr(&wfx), 0, 0, 0)
		if r != 0 {
			lastErr = r
			continue
		}
		c.h, c.rate = h, rate
		n := int(rate) * bufMilli / 1000
		for i := range c.hdrs {
			c.bufs[i] = make([]int16, n)
			c.hdrs[i] = WAVEHDR{
				LpData:         uintptr(unsafe.Pointer(&c.bufs[i][0])),
				DwBufferLength: uint32(n * 2),
			}
			pWaveInPrepareHeader.Call(h, ptr(&c.hdrs[i]), unsafe.Sizeof(c.hdrs[i]))
			pWaveInAddBuffer.Call(h, ptr(&c.hdrs[i]), unsafe.Sizeof(c.hdrs[i]))
		}
		c.next = 0
		if r, _, _ := pWaveInStart.Call(h); r != 0 {
			c.close()
			return fmt.Errorf("could not start recording (error %d)", r)
		}
		c.running = true
		return nil
	}
	return fmt.Errorf("could not open the sound card input (error %d). Check it isn't disabled in Windows Sound settings, and that microphone access is allowed for desktop apps", lastErr)
}

// poll returns any newly-recorded audio as float32 and re-queues finished buffers.
func (c *capture) poll(out []float32) []float32 {
	if !c.running {
		return out
	}
	for k := 0; k < numBufs; k++ {
		h := &c.hdrs[c.next]
		if h.DwFlags&WHDR_DONE == 0 {
			break
		}
		n := int(h.DwBytesRecorded / 2)
		for _, s := range c.bufs[c.next][:n] {
			out = append(out, float32(s)/32768)
		}
		h.DwBytesRecorded = 0
		h.DwFlags &^= WHDR_DONE // not queued right now, safe to clear before re-adding
		pWaveInAddBuffer.Call(c.h, ptr(h), unsafe.Sizeof(*h))
		c.next = (c.next + 1) % numBufs
	}
	return out
}

func (c *capture) close() {
	if c.h == 0 {
		return
	}
	pWaveInReset.Call(c.h)
	for i := range c.hdrs {
		pWaveInUnprepareHeader.Call(c.h, ptr(&c.hdrs[i]), unsafe.Sizeof(c.hdrs[i]))
	}
	pWaveInClose.Call(c.h)
	c.h = 0
	c.running = false
}
