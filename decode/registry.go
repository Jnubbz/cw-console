// Package decode is the registry of receive modes.
//
// The engines stay in their packages. This registry is how the UI and the
// logbook find one by name:
//
//	cw.classic   cw.StreamDecoder, the Nexus port in cw/decoder.go
//	cw.deep      deepcw.Live
//	ft8.wsjtx    the jt9 pipeline in cmd/cwmac/ft8.go
//	sstv         sstv.Decoder, once that package is copied in
//
// Register is called by the program at startup. This package does not import
// those engines, so a test of the registry does not load the ONNX model.
// No engine is registered by init.
package decode

import (
	"fmt"
	"sync"
	"time"
)

// Mode is the operating mode, which is also the ADIF mode the log stores
// (SSTV included; FT8 is stored as FT8, not as MFSK).
type Mode string

const (
	ModeCW   Mode = "CW"
	ModeFT8  Mode = "FT8"
	ModeSSTV Mode = "SSTV"
)

// Event is text a decoder emitted. Call is set when the engine already
// picked a callsign out of Text; otherwise the logbook side runs call
// detection. At is when the decoder produced it.
type Event struct {
	Text string
	Call string
	At   time.Time
}

// Decoder consumes receive audio and emits events. sampleRate is the rate
// the capture was opened at; each implementation resamples for itself, as
// deepcw and the SSTV decoder already do.
type Decoder interface {
	ID() string
	Mode() Mode
	Write(samples []float32) []Event
	Close() error
}

// Factory builds a decoder. The registry does not keep the instance.
type Factory func() (Decoder, error)

// Registry is the set of modes the running program can turn on.
type Registry struct {
	mu    sync.Mutex
	order []string
	f     map[string]entry
}

type entry struct {
	mode Mode
	new  Factory
}

// Register adds a decoder. A second registration of the same name is an error,
// so two packages cannot silently replace each other.
func (r *Registry) Register(name string, mode Mode, new Factory) error {
	if name == "" || new == nil {
		return fmt.Errorf("decode: name and factory are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		r.f = map[string]entry{}
	}
	if _, ok := r.f[name]; ok {
		return fmt.Errorf("decode: %s is already registered", name)
	}
	r.f[name] = entry{mode: mode, new: new}
	r.order = append(r.order, name)
	return nil
}

// Names returns the registration order.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.order...)
}

// New builds the named decoder.
func (r *Registry) New(name string) (Decoder, error) {
	r.mu.Lock()
	e, ok := r.f[name]
	r.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("decode: %s is not registered", name)
	}
	d, err := e.new()
	if err != nil {
		return nil, err
	}
	if d.Mode() != e.mode {
		_ = d.Close()
		return nil, fmt.Errorf("decode: %s registered as %s but built %s", name, e.mode, d.Mode())
	}
	return d, nil
}
