package deepcw

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jnubbz/cw-console/cw"
)

const engineDir = "../third_party/deepcw-engine"

func loadT(t *testing.T) *Model {
	if _, err := os.Stat(engineDir + "/model.onnx"); err != nil {
		t.Skip("model not present")
	}
	m, err := Load(engineDir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDecodesCleanCW(t *testing.T) {
	m := loadT(t)
	for _, c := range []struct {
		text  string
		wpm   uint32
		pitch float32
	}{{"CQ CQ DE KN0BLE KN0BLE K", 20, 600}, {"TEST DE W1AW 599 TU", 28, 750}} {
		sig := cw.MorseSamples(c.text, c.wpm, c.pitch, 3200)
		a := append(make([]float32, 1600), sig...)
		a = append(a, make([]float32, 1600)...)
		for i := range a {
			a[i] *= 0.5
		}
		t0 := time.Now()
		got, err := m.Decode(a)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%.1f s clip decoded in %v: %q", float64(len(a))/3200, time.Since(t0), got)
		if strings.TrimSpace(got) != c.text {
			t.Errorf("want %q got %q", c.text, got)
		}
	}
}
