package deepcw

// Minimal ONNX (protobuf) reader: just the parts of ModelProto/GraphProto this
// interpreter needs. No external protobuf library.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
)

type pbField struct {
	num  int
	wire int
	v    uint64 // varint / fixed value
	b    []byte // length-delimited payload
}

func pbFields(b []byte) ([]pbField, error) {
	var out []pbField
	i := 0
	varint := func() (uint64, error) {
		var r uint64
		for s := uint(0); ; s += 7 {
			if i >= len(b) || s > 63 {
				return 0, errors.New("bad varint")
			}
			c := b[i]
			i++
			r |= uint64(c&0x7f) << s
			if c < 0x80 {
				return r, nil
			}
		}
	}
	for i < len(b) {
		k, err := varint()
		if err != nil {
			return nil, err
		}
		f := pbField{num: int(k >> 3), wire: int(k & 7)}
		switch f.wire {
		case 0:
			if f.v, err = varint(); err != nil {
				return nil, err
			}
		case 1:
			if i+8 > len(b) {
				return nil, errors.New("truncated")
			}
			f.v = binary.LittleEndian.Uint64(b[i:])
			i += 8
		case 5:
			if i+4 > len(b) {
				return nil, errors.New("truncated")
			}
			f.v = uint64(binary.LittleEndian.Uint32(b[i:]))
			i += 4
		case 2:
			n, err := varint()
			if err != nil {
				return nil, err
			}
			if i+int(n) > len(b) || int(n) < 0 {
				return nil, errors.New("truncated")
			}
			f.b = b[i : i+int(n)]
			i += int(n)
		default:
			return nil, fmt.Errorf("unsupported wire type %d", f.wire)
		}
		out = append(out, f)
	}
	return out, nil
}

// packedVarints decodes a packed repeated varint field.
func packedVarints(b []byte) []int64 {
	var out []int64
	i := 0
	for i < len(b) {
		var r uint64
		for s := uint(0); i < len(b); s += 7 {
			c := b[i]
			i++
			r |= uint64(c&0x7f) << s
			if c < 0x80 {
				break
			}
		}
		out = append(out, int64(r))
	}
	return out
}

// Attr is a node attribute (only the kinds this model uses).
type Attr struct {
	I      int64
	F      float32
	S      string
	Ints   []int64
	Floats []float32
	hasI   bool
	hasF   bool
}

type Node struct {
	Op      string
	Name    string
	Inputs  []string
	Outputs []string
	Attrs   map[string]Attr
}

func (n *Node) intAttr(name string, def int64) int64 {
	if a, ok := n.Attrs[name]; ok && a.hasI {
		return a.I
	}
	return def
}

func (n *Node) floatAttr(name string, def float32) float32 {
	if a, ok := n.Attrs[name]; ok && a.hasF {
		return a.F
	}
	return def
}

func (n *Node) intsAttr(name string) []int64 { return n.Attrs[name].Ints }

type Graph struct {
	Nodes   []Node
	Inits   map[string]*Tensor
	Inputs  []string
	Outputs []string
}

func LoadONNX(path string) (*Graph, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	top, err := pbFields(b)
	if err != nil {
		return nil, err
	}
	var gb []byte
	for _, f := range top {
		if f.num == 7 {
			gb = f.b
		}
	}
	if gb == nil {
		return nil, errors.New("no graph in model")
	}
	gf, err := pbFields(gb)
	if err != nil {
		return nil, err
	}
	g := &Graph{Inits: map[string]*Tensor{}}
	for _, f := range gf {
		switch f.num {
		case 1:
			n, err := parseNode(f.b)
			if err != nil {
				return nil, err
			}
			g.Nodes = append(g.Nodes, n)
		case 5:
			name, t, err := parseTensor(f.b)
			if err != nil {
				return nil, err
			}
			g.Inits[name] = t
		case 11, 12:
			vf, _ := pbFields(f.b)
			for _, x := range vf {
				if x.num == 1 {
					if f.num == 11 {
						g.Inputs = append(g.Inputs, string(x.b))
					} else {
						g.Outputs = append(g.Outputs, string(x.b))
					}
				}
			}
		}
	}
	return g, nil
}

func parseNode(b []byte) (Node, error) {
	n := Node{Attrs: map[string]Attr{}}
	fs, err := pbFields(b)
	if err != nil {
		return n, err
	}
	for _, f := range fs {
		switch f.num {
		case 1:
			n.Inputs = append(n.Inputs, string(f.b))
		case 2:
			n.Outputs = append(n.Outputs, string(f.b))
		case 3:
			n.Name = string(f.b)
		case 4:
			n.Op = string(f.b)
		case 7:
			if len(f.b) > 0 && string(f.b) != "ai.onnx" {
				return n, fmt.Errorf("unsupported op domain %q", f.b)
			}
		case 5:
			af, err := pbFields(f.b)
			if err != nil {
				return n, err
			}
			var name string
			var a Attr
			for _, x := range af {
				switch x.num {
				case 1:
					name = string(x.b)
				case 2:
					a.F, a.hasF = math.Float32frombits(uint32(x.v)), true
				case 3:
					a.I, a.hasI = int64(x.v), true
				case 4:
					a.S = string(x.b)
				case 7:
					if x.wire == 2 {
						for j := 0; j+4 <= len(x.b); j += 4 {
							a.Floats = append(a.Floats, math.Float32frombits(binary.LittleEndian.Uint32(x.b[j:])))
						}
					} else {
						a.Floats = append(a.Floats, math.Float32frombits(uint32(x.v)))
					}
				case 8:
					if x.wire == 2 {
						a.Ints = append(a.Ints, packedVarints(x.b)...)
					} else {
						a.Ints = append(a.Ints, int64(x.v))
					}
				case 5:
					return n, fmt.Errorf("tensor attributes not supported (%s)", name)
				}
			}
			n.Attrs[name] = a
		}
	}
	return n, nil
}

func parseTensor(b []byte) (string, *Tensor, error) {
	fs, err := pbFields(b)
	if err != nil {
		return "", nil, err
	}
	var (
		name   string
		dims   []int
		dt     int
		raw    []byte
		floats []float32
		ints   []int64
	)
	for _, f := range fs {
		switch f.num {
		case 1:
			if f.wire == 2 {
				for _, d := range packedVarints(f.b) {
					dims = append(dims, int(d))
				}
			} else {
				dims = append(dims, int(f.v))
			}
		case 2:
			dt = int(f.v)
		case 4:
			if f.wire == 2 {
				for j := 0; j+4 <= len(f.b); j += 4 {
					floats = append(floats, math.Float32frombits(binary.LittleEndian.Uint32(f.b[j:])))
				}
			} else {
				floats = append(floats, math.Float32frombits(uint32(f.v)))
			}
		case 7:
			if f.wire == 2 {
				ints = append(ints, packedVarints(f.b)...)
			} else {
				ints = append(ints, int64(f.v))
			}
		case 8:
			name = string(f.b)
		case 9:
			raw = f.b
		case 13, 14:
			return "", nil, fmt.Errorf("external tensor data not supported")
		}
	}
	size := 1
	for _, d := range dims {
		size *= d
	}
	t := &Tensor{Shape: dims}
	switch dt {
	case 1: // float
		if raw != nil {
			if len(raw) != 4*size {
				return "", nil, fmt.Errorf("tensor %s size mismatch", name)
			}
			t.F = make([]float32, size)
			for i := range t.F {
				t.F[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
			}
		} else {
			t.F = floats
		}
		if len(t.F) != size {
			return "", nil, fmt.Errorf("tensor %s size mismatch", name)
		}
	case 7: // int64
		t.IsInt = true
		if raw != nil {
			t.I = make([]int64, size)
			for i := range t.I {
				t.I[i] = int64(binary.LittleEndian.Uint64(raw[8*i:]))
			}
		} else {
			t.I = ints
		}
		if len(t.I) != size {
			return "", nil, fmt.Errorf("tensor %s size mismatch", name)
		}
	default:
		return "", nil, fmt.Errorf("tensor %s: unsupported data type %d", name, dt)
	}
	return name, t, nil
}
