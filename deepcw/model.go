package deepcw

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Model is a loaded DeepCW engine (model.onnx + model.onnx.json).
type Model struct {
	g       *Graph
	Meta    *Metadata
	lastUse map[string]int
	Trace   func(op string, out []*Tensor, d time.Duration) // optional per-op hook
}

// Load reads model.onnx and model.onnx.json from dir.
func Load(dir string) (*Model, error) {
	mp, jp := filepath.Join(dir, "model.onnx"), filepath.Join(dir, "model.onnx.json")
	if _, err := os.Stat(mp); err != nil {
		return nil, fmt.Errorf("model weights missing: %s", mp)
	}
	if _, err := os.Stat(jp); err != nil {
		return nil, fmt.Errorf("model metadata missing: %s", jp)
	}
	meta, err := LoadMetadata(jp)
	if err != nil {
		return nil, err
	}
	g, err := LoadONNX(mp)
	if err != nil {
		return nil, fmt.Errorf("load model: %w", err)
	}
	for _, n := range g.Nodes {
		if ops[n.Op] == nil {
			return nil, fmt.Errorf("model uses unsupported operation %s", n.Op)
		}
	}
	m := &Model{g: g, Meta: meta, lastUse: map[string]int{}}
	for i, n := range g.Nodes {
		for _, in := range n.Inputs {
			m.lastUse[in] = i
		}
	}
	for _, o := range g.Outputs {
		m.lastUse[o] = len(g.Nodes)
	}
	return m, nil
}

// Run executes the graph on a [1,1,frames,bins] spectrogram and returns the
// [time × classes] log-probabilities.
func (m *Model) Run(spec []float32, frames int) ([]float32, int, int, error) {
	vals := map[string]*Tensor{}
	vals[m.Meta.InputName] = &Tensor{Shape: []int{1, 1, frames, m.Meta.FreqBins}, F: spec}
	get := func(name string) (*Tensor, error) {
		if name == "" {
			return nil, nil
		}
		if t, ok := vals[name]; ok {
			return t, nil
		}
		if t, ok := m.g.Inits[name]; ok {
			return t, nil
		}
		return nil, fmt.Errorf("missing value %q", name)
	}
	for i := range m.g.Nodes {
		n := &m.g.Nodes[i]
		in := make([]*Tensor, len(n.Inputs))
		for j, name := range n.Inputs {
			t, err := get(name)
			if err != nil {
				return nil, 0, 0, fmt.Errorf("%s (%s): %w", n.Op, n.Name, err)
			}
			in[j] = t
		}
		t0 := time.Now()
		out, err := ops[n.Op](n, in)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("%s (%s): %w", n.Op, n.Name, err)
		}
		if m.Trace != nil {
			m.Trace(n.Op, out, time.Since(t0))
		}
		for j, name := range n.Outputs {
			if j < len(out) && name != "" {
				vals[name] = out[j]
			}
		}
		for _, name := range n.Inputs { // free intermediates after their last use
			if m.lastUse[name] == i {
				delete(vals, name)
			}
		}
	}
	o := vals[m.Meta.OutputName]
	if o == nil {
		o = vals[m.g.Outputs[0]]
	}
	if o == nil || len(o.Shape) != 3 || o.Shape[0] != 1 {
		return nil, 0, 0, fmt.Errorf("unexpected model output")
	}
	return o.F, o.Shape[1], o.Shape[2], nil
}

// DecodeTimed decodes mono audio already at the model rate (3200 Hz).
func (m *Model) DecodeTimed(audio []float32) ([]TimedChar, error) {
	return m.DecodeTimedFocus(audio, nil)
}

// DecodeTimedFocus is DecodeTimed with an optional pitch focus.
func (m *Model) DecodeTimedFocus(audio []float32, focus *Focus) ([]TimedChar, error) {
	spec, frames, err := SpectrogramFocus(audio, m.Meta, focus)
	if err != nil {
		return nil, err
	}
	lp, t, c, err := m.Run(spec, frames)
	if err != nil {
		return nil, err
	}
	return GreedyCTCTimed(lp, t, c, float64(len(audio))/float64(m.Meta.SampleRate), m.Meta), nil
}

// Decode returns the plain text for a clip at the model rate.
func (m *Model) Decode(audio []float32) (string, error) {
	cs, err := m.DecodeTimed(audio)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	for _, c := range cs {
		sb.WriteString(c.Ch)
	}
	return sb.String(), nil
}
