// Package audio is the shared capture and playback surface for CW, FT8 and SSTV.
//
// Today the desktop apps capture in the browser (an AudioWorklet posting float32
// to the Go process) and the Pi box captures with ALSA inside cmd/cwmac. Playback
// for FT8 and SSTV is the reverse path. Those implementations stay where they
// are. This package is the interface they will share, so one device is opened
// once and every mode reads the same stream.
//
// Nothing here opens a sound card.
package audio

import "context"

// Device is one input or output the operator can pick.
type Device struct {
	ID     string
	Name   string
	Input  bool
	Output bool
}

// Stream is a float32 capture in the range -1..1, one or more channels
// interleaved. Pause drops samples without closing the device, which is what
// the Pi box does around a transmission so the receiver does not decode itself.
type Stream interface {
	Read(dst []float32) (int, error)
	Pause(on bool) error
	Close() error
}

// Input lists capture devices and opens one.
type Input interface {
	List(ctx context.Context) ([]Device, error)
	Capture(ctx context.Context, deviceID string, sampleRate int) (Stream, error)
}

// Output plays a finished buffer (an SSTV picture, an FT8 wave, a tune tone).
// CW sidetone that is generated as it is keyed stays with the keyer; it is not
// a buffered clip.
type Output interface {
	List(ctx context.Context) ([]Device, error)
	Play(ctx context.Context, deviceID string, sampleRate int, samples []float32) error
}
