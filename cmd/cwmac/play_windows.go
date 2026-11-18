//go:build windows

package main

import (
	"fmt"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

// Windows playback is waveOut to the device the operator picked (Setup >
// Playback device). SoundPlayer cannot choose a device, so it is not used.

var (
	winmm                   = syscall.NewLazyDLL("winmm.dll")
	pWaveOutGetNumDevs      = winmm.NewProc("waveOutGetNumDevs")
	pWaveOutGetDevCapsW     = winmm.NewProc("waveOutGetDevCapsW")
	pWaveOutOpen            = winmm.NewProc("waveOutOpen")
	pWaveOutPrepareHeader   = winmm.NewProc("waveOutPrepareHeader")
	pWaveOutUnprepareHeader = winmm.NewProc("waveOutUnprepareHeader")
	pWaveOutWrite           = winmm.NewProc("waveOutWrite")
	pWaveOutReset           = winmm.NewProc("waveOutReset")
	pWaveOutClose           = winmm.NewProc("waveOutClose")
)

const (
	waveMapper    = 0xFFFFFFFF
	waveFormatPCM = 1
	whdrDone      = 0x00000001
)

type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	Size           uint16
}

type waveHdr struct {
	LpData          uintptr
	DwBufferLength  uint32
	DwBytesRecorded uint32
	DwUser          uintptr
	DwFlags         uint32
	DwLoops         uint32
	LpNext          uintptr
	Reserved        uintptr
}

type waveOutCaps struct {
	Mid           uint16
	Pid           uint16
	DriverVersion uint32
	Name          [32]uint16
	Formats       uint32
	Channels      uint16
	Reserved      uint16
	Support       uint32
}

func outputDevices() []outputDev {
	n, _, _ := pWaveOutGetNumDevs.Call()
	out := make([]outputDev, 0, n)
	for i := uint32(0); i < uint32(n); i++ {
		var caps waveOutCaps
		r, _, _ := pWaveOutGetDevCapsW.Call(uintptr(i), uintptr(unsafe.Pointer(&caps)), unsafe.Sizeof(caps))
		if r != 0 {
			continue
		}
		out = append(out, outputDev{
			ID:   fmt.Sprintf("wave:%d", i),
			Name: syscall.UTF16ToString(caps.Name[:]),
		})
	}
	return out
}

func waveOutNames() []string {
	devs := outputDevices()
	names := make([]string, len(devs))
	for i, d := range devs {
		names[i] = d.Name
	}
	return names
}

type wavePlayback struct {
	h    uintptr
	pcm  []byte
	hdr  waveHdr
	err  string
	name string
	done chan struct{}
	quit chan struct{}
}

func startPlatformAudio(pcm []byte, rate, ch int, dev string) (ft8Playback, bool, error) {
	p, err := openWavePlayback(pcm, rate, ch, dev)
	return p, true, err
}

func openWavePlayback(pcm []byte, rate, ch int, dev string) (*wavePlayback, error) {
	if rate <= 0 || ch <= 0 || len(pcm) == 0 {
		return nil, fmt.Errorf("nothing to play")
	}
	names := waveOutNames()
	id := uintptr(waveMapper)
	label := "Windows default playback"
	if n := pickOutput(names, dev); n >= 0 {
		id = uintptr(n)
		label = names[n]
	}
	wfx := waveFormatEx{
		FormatTag: waveFormatPCM, Channels: uint16(ch), SamplesPerSec: uint32(rate),
		AvgBytesPerSec: uint32(rate * ch * 2), BlockAlign: uint16(ch * 2), BitsPerSample: 16,
	}
	var h uintptr
	r, _, e := pWaveOutOpen.Call(uintptr(unsafe.Pointer(&h)), id, uintptr(unsafe.Pointer(&wfx)), 0, 0, 0)
	if r != 0 {
		return nil, fmt.Errorf("could not open playback device %s (waveOut error %d: %v)", label, r, e)
	}
	return &wavePlayback{h: h, pcm: pcm, name: label, done: make(chan struct{}), quit: make(chan struct{})}, nil
}

// start queues the whole transmission. The leading samples are silence, so
// the caller can key the rig once this returns and the card is already playing.
func (p *wavePlayback) start() error {
	p.hdr = waveHdr{LpData: uintptr(unsafe.Pointer(&p.pcm[0])), DwBufferLength: uint32(len(p.pcm))}
	if r, _, e := pWaveOutPrepareHeader.Call(p.h, uintptr(unsafe.Pointer(&p.hdr)), unsafe.Sizeof(p.hdr)); r != 0 {
		p.close()
		return fmt.Errorf("could not prepare playback (%d: %v)", r, e)
	}
	if r, _, e := pWaveOutWrite.Call(p.h, uintptr(unsafe.Pointer(&p.hdr)), unsafe.Sizeof(p.hdr)); r != 0 {
		p.err = e.Error()
		p.close()
		return fmt.Errorf("could not start playback (%d: %v)", r, e)
	}
	go p.waitDone()
	return nil
}

func (p *wavePlayback) waitDone() {
	defer close(p.done)
	defer p.close()
	for atomic.LoadUint32(&p.hdr.DwFlags)&whdrDone == 0 {
		select {
		case <-p.quit:
			return
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func (p *wavePlayback) wait() error {
	<-p.done
	if p.err != "" {
		return fmt.Errorf("%s", p.err)
	}
	return nil
}

func (p *wavePlayback) stop() {
	select {
	case <-p.quit:
	default:
		close(p.quit)
	}
	if p.h != 0 {
		pWaveOutReset.Call(p.h)
	}
}

func (p *wavePlayback) close() {
	if p.h == 0 {
		return
	}
	pWaveOutReset.Call(p.h)
	pWaveOutUnprepareHeader.Call(p.h, uintptr(unsafe.Pointer(&p.hdr)), unsafe.Sizeof(p.hdr))
	pWaveOutClose.Call(p.h)
	p.h = 0
}

func (p *wavePlayback) label() string   { return "waveOut " + p.name }
func (p *wavePlayback) errText() string { return p.err }
