package decode

import (
	"testing"
	"time"
)

type fake struct {
	id   string
	mode Mode
}

func (f *fake) ID() string { return f.id }
func (f *fake) Mode() Mode { return f.mode }
func (f *fake) Write([]float32) []Event {
	return []Event{{Text: "W1AW", Call: "W1AW", At: time.Unix(0, 0).UTC()}}
}
func (f *fake) Close() error { return nil }

func TestRegisterAndBuild(t *testing.T) {
	var r Registry
	err := r.Register("cw.classic", ModeCW, func() (Decoder, error) {
		return &fake{id: "cw.classic", mode: ModeCW}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register("cw.classic", ModeCW, func() (Decoder, error) { return &fake{}, nil }); err == nil {
		t.Fatal("duplicate registration was accepted")
	}
	if err := r.Register("sstv", ModeSSTV, func() (Decoder, error) {
		return &fake{id: "sstv", mode: ModeSSTV}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if names := r.Names(); len(names) != 2 || names[0] != "cw.classic" || names[1] != "sstv" {
		t.Fatalf("names %v", names)
	}
	d, err := r.New("sstv")
	if err != nil {
		t.Fatal(err)
	}
	ev := d.Write(nil)
	if len(ev) != 1 || ev[0].Call != "W1AW" || d.Mode() != ModeSSTV {
		t.Fatalf("event %+v mode %s", ev, d.Mode())
	}
	if _, err := r.New("ft8.wsjtx"); err == nil {
		t.Fatal("unregistered decoder was built")
	}
}

func TestFactoryModeMustMatch(t *testing.T) {
	var r Registry
	if err := r.Register("ft8.wsjtx", ModeFT8, func() (Decoder, error) {
		return &fake{id: "nope", mode: ModeCW}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.New("ft8.wsjtx"); err == nil {
		t.Fatal("mode mismatch was accepted")
	}
}
