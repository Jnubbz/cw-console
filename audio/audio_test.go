package audio

import (
	"context"
	"io"
	"testing"
)

// fake is a stand-in capture. It exists so the interface stays compilable and
// the lease rules of a later ALSA or browser backend have a type to satisfy.
type fake struct {
	paused bool
	left   []float32
}

func (f *fake) List(context.Context) ([]Device, error) {
	return []Device{{ID: "rig", Name: "USB audio", Input: true, Output: true}}, nil
}

func (f *fake) Capture(context.Context, string, int) (Stream, error) { return f, nil }

func (f *fake) Play(context.Context, string, int, []float32) error { return nil }

func (f *fake) Read(dst []float32) (int, error) {
	if f.paused {
		return 0, nil
	}
	if len(f.left) == 0 {
		return 0, io.EOF
	}
	n := copy(dst, f.left)
	f.left = f.left[n:]
	return n, nil
}

func (f *fake) Pause(on bool) error { f.paused = on; return nil }
func (f *fake) Close() error        { return nil }

func TestFakeSatisfiesInterfaces(t *testing.T) {
	var in Input = &fake{left: []float32{0.25, -0.25}}
	var out Output = &fake{}
	devs, err := in.List(context.Background())
	if err != nil || len(devs) != 1 || !devs[0].Input {
		t.Fatalf("list: %+v %v", devs, err)
	}
	s, err := in.Capture(context.Background(), "rig", 12000)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]float32, 4)
	n, err := s.Read(buf)
	if n != 2 || err != nil {
		t.Fatalf("read %d %v", n, err)
	}
	if err := s.Pause(true); err != nil {
		t.Fatal(err)
	}
	n, err = s.Read(buf)
	if n != 0 || err != nil {
		t.Fatalf("paused read %d %v", n, err)
	}
	if err := out.Play(context.Background(), "rig", 48000, []float32{0}); err != nil {
		t.Fatal(err)
	}
}
