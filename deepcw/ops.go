package deepcw

import (
	"fmt"
	"math"
	"sync"
)

type opFunc func(n *Node, in []*Tensor) ([]*Tensor, error)

var ops = map[string]opFunc{
	"Add":                   opAdd,
	"Mul":                   opMul,
	"Sigmoid":               opSigmoid,
	"Sin":                   unaryF(func(x float32) float32 { return float32(math.Sin(float64(x))) }),
	"Cos":                   unaryF(func(x float32) float32 { return float32(math.Cos(float64(x))) }),
	"Shape":                 opShape,
	"Reshape":               opReshape,
	"Squeeze":               opSqueeze,
	"Unsqueeze":             opUnsqueeze,
	"Transpose":             opTranspose,
	"Concat":                opConcat,
	"Gather":                opGather,
	"Slice":                 opSlice,
	"Range":                 opRange,
	"Cast":                  opCast,
	"Expand":                opExpand,
	"ScatterND":             opScatterND,
	"Split":                 opSplit,
	"Softmax":               opSoftmax(false),
	"LogSoftmax":            opSoftmax(true),
	"LayerNormalization":    opLayerNorm,
	"InstanceNormalization": opInstanceNorm,
	"MatMul":                opMatMul,
	"Gemm":                  opGemm,
	"Conv":                  opConv,
}

func one(t *Tensor) []*Tensor { return []*Tensor{t} }

func unaryF(f func(float32) float32) opFunc {
	return func(n *Node, in []*Tensor) ([]*Tensor, error) {
		r := newF(in[0].Shape)
		for i, v := range in[0].F {
			r.F[i] = f(v)
		}
		return one(r), nil
	}
}

func opAdd(n *Node, in []*Tensor) ([]*Tensor, error) {
	a, b := in[0], in[1]
	if a.IsInt || b.IsInt {
		r, err := binaryI(toInt(a), toInt(b), func(x, y int64) int64 { return x + y })
		return one(r), err
	}
	r, err := binaryF(a, b, func(x, y float32) float32 { return x + y })
	return one(r), err
}

func opMul(n *Node, in []*Tensor) ([]*Tensor, error) {
	a, b := in[0], in[1]
	if a.IsInt || b.IsInt {
		r, err := binaryI(toInt(a), toInt(b), func(x, y int64) int64 { return x * y })
		return one(r), err
	}
	r, err := binaryF(a, b, func(x, y float32) float32 { return x * y })
	return one(r), err
}

func toInt(t *Tensor) *Tensor {
	if t.IsInt {
		return t
	}
	r := newI(t.Shape)
	for i, v := range t.F {
		r.I[i] = int64(v)
	}
	return r
}

func opShape(n *Node, in []*Tensor) ([]*Tensor, error) {
	sh := in[0].Shape
	r := len(sh)
	s := normAxis(n.intAttr("start", 0), r)
	e := r
	if _, ok := n.Attrs["end"]; ok {
		e = normAxis(n.intAttr("end", int64(r)), r)
	}
	s = max(0, min(s, r))
	e = max(s, min(e, r))
	out := newI([]int{e - s})
	for i := s; i < e; i++ {
		out.I[i-s] = int64(sh[i])
	}
	return one(out), nil
}

func opReshape(n *Node, in []*Tensor) ([]*Tensor, error) {
	want, _ := intsOf(in[1])
	allowZero := n.intAttr("allowzero", 0) == 1
	shape := make([]int, len(want))
	infer := -1
	known := 1
	for i, d := range want {
		switch {
		case d == 0 && !allowZero:
			shape[i] = in[0].Shape[i]
		case d == -1:
			infer = i
			continue
		default:
			shape[i] = int(d)
		}
		known *= shape[i]
	}
	total := in[0].Size()
	if infer >= 0 {
		if known == 0 {
			return nil, fmt.Errorf("reshape: cannot infer")
		}
		shape[infer] = total / known
	}
	if size(shape) != total {
		return nil, fmt.Errorf("reshape %v -> %v", in[0].Shape, want)
	}
	return one(in[0].reshaped(shape)), nil
}

func opSqueeze(n *Node, in []*Tensor) ([]*Tensor, error) {
	sh := in[0].Shape
	drop := map[int]bool{}
	if len(in) > 1 && in[1] != nil {
		axes, _ := intsOf(in[1])
		for _, a := range axes {
			drop[normAxis(a, len(sh))] = true
		}
	} else {
		for i, d := range sh {
			if d == 1 {
				drop[i] = true
			}
		}
	}
	var out []int
	for i, d := range sh {
		if !drop[i] {
			out = append(out, d)
		} else if d != 1 {
			return nil, fmt.Errorf("squeeze of non-1 dim")
		}
	}
	return one(in[0].reshaped(out)), nil
}

func opUnsqueeze(n *Node, in []*Tensor) ([]*Tensor, error) {
	axes, _ := intsOf(in[1])
	rank := len(in[0].Shape) + len(axes)
	ins := map[int]bool{}
	for _, a := range axes {
		ins[normAxis(a, rank)] = true
	}
	out := make([]int, 0, rank)
	j := 0
	for i := 0; i < rank; i++ {
		if ins[i] {
			out = append(out, 1)
		} else {
			out = append(out, in[0].Shape[j])
			j++
		}
	}
	return one(in[0].reshaped(out)), nil
}

func opTranspose(n *Node, in []*Tensor) ([]*Tensor, error) {
	p := n.intsAttr("perm")
	perm := make([]int, len(in[0].Shape))
	if len(p) == 0 {
		for i := range perm {
			perm[i] = len(perm) - 1 - i
		}
	} else {
		for i, v := range p {
			perm[i] = int(v)
		}
	}
	return one(transpose(in[0], perm)), nil
}

func opConcat(n *Node, in []*Tensor) ([]*Tensor, error) {
	var ts []*Tensor
	isInt := false
	for _, t := range in {
		if t != nil && t.Size() >= 0 {
			ts = append(ts, t)
			isInt = isInt || t.IsInt
		}
	}
	rank := len(ts[0].Shape)
	ax := normAxis(n.intAttr("axis", 0), rank)
	out := append([]int(nil), ts[0].Shape...)
	out[ax] = 0
	for _, t := range ts {
		if len(t.Shape) != rank {
			return nil, fmt.Errorf("concat rank mismatch")
		}
		out[ax] += t.Shape[ax]
	}
	var r *Tensor
	if isInt {
		r = newI(out)
	} else {
		r = newF(out)
	}
	outer := size(out[:ax])
	rowOut := size(out[ax:])
	off := 0
	for _, t := range ts {
		if isInt {
			t = toInt(t)
		}
		row := size(t.Shape[ax:])
		for o := 0; o < outer; o++ {
			if isInt {
				copy(r.I[o*rowOut+off:], t.I[o*row:(o+1)*row])
			} else {
				copy(r.F[o*rowOut+off:], t.F[o*row:(o+1)*row])
			}
		}
		off += row
	}
	return one(r), nil
}

func opGather(n *Node, in []*Tensor) ([]*Tensor, error) {
	data, idxT := in[0], in[1]
	idx, _ := intsOf(idxT)
	ax := normAxis(n.intAttr("axis", 0), len(data.Shape))
	dim := data.Shape[ax]
	outer := size(data.Shape[:ax])
	inner := size(data.Shape[ax+1:])
	out := append(append(append([]int(nil), data.Shape[:ax]...), idxT.Shape...), data.Shape[ax+1:]...)
	var r *Tensor
	if data.IsInt {
		r = newI(out)
	} else {
		r = newF(out)
	}
	p := 0
	for o := 0; o < outer; o++ {
		for _, ix := range idx {
			i := int(ix)
			if i < 0 {
				i += dim
			}
			if i < 0 || i >= dim {
				return nil, fmt.Errorf("gather index %d out of range %d", ix, dim)
			}
			src := (o*dim + i) * inner
			if data.IsInt {
				copy(r.I[p:p+inner], data.I[src:src+inner])
			} else {
				copy(r.F[p:p+inner], data.F[src:src+inner])
			}
			p += inner
		}
	}
	return one(r), nil
}

func opSlice(n *Node, in []*Tensor) ([]*Tensor, error) {
	data := in[0]
	rank := len(data.Shape)
	starts, _ := intsOf(in[1])
	ends, _ := intsOf(in[2])
	var axes, steps []int64
	if len(in) > 3 && in[3] != nil {
		axes, _ = intsOf(in[3])
	}
	if len(in) > 4 && in[4] != nil {
		steps, _ = intsOf(in[4])
	}
	st := make([]int, rank)
	sp := make([]int, rank)
	cnt := append([]int(nil), data.Shape...)
	for i := range sp {
		sp[i] = 1
	}
	for i := range starts {
		ax := i
		if axes != nil {
			ax = normAxis(axes[i], rank)
		}
		dim := int64(data.Shape[ax])
		step := int64(1)
		if steps != nil {
			step = steps[i]
		}
		if step == 0 {
			return nil, fmt.Errorf("slice step 0")
		}
		s, e := starts[i], ends[i]
		if s < 0 {
			s += dim
		}
		if e < 0 {
			e += dim
		}
		if step > 0 {
			s = max(0, min(s, dim))
			e = max(0, min(e, dim))
		} else {
			s = max(0, min(s, dim-1))
			e = max(-1, min(e, dim-1))
		}
		c := int64(0)
		if step > 0 && e > s {
			c = (e - s + step - 1) / step
		} else if step < 0 && s > e {
			c = (s - e + (-step) - 1) / (-step)
		}
		st[ax], sp[ax], cnt[ax] = int(s), int(step), int(c)
	}
	var r *Tensor
	if data.IsInt {
		r = newI(cnt)
	} else {
		r = newF(cnt)
	}
	if r.Size() == 0 {
		return one(r), nil
	}
	src := stridesOf(data.Shape)
	idx := make([]int, rank)
	for o := 0; o < r.Size(); o++ {
		p := 0
		for d := 0; d < rank; d++ {
			p += (st[d] + idx[d]*sp[d]) * src[d]
		}
		if data.IsInt {
			r.I[o] = data.I[p]
		} else {
			r.F[o] = data.F[p]
		}
		for d := rank - 1; d >= 0; d-- {
			idx[d]++
			if idx[d] < cnt[d] {
				break
			}
			idx[d] = 0
		}
	}
	return one(r), nil
}

func opRange(n *Node, in []*Tensor) ([]*Tensor, error) {
	if in[0].IsInt {
		s, l, d := in[0].I[0], in[1].I[0], in[2].I[0]
		if in[1].IsInt == false {
			l = int64(in[1].F[0])
		}
		cnt := int64(0)
		if d != 0 {
			cnt = max(0, int64(math.Ceil(float64(l-s)/float64(d))))
		}
		r := newI([]int{int(cnt)})
		for i := range r.I {
			r.I[i] = s + int64(i)*d
		}
		return one(r), nil
	}
	get := func(t *Tensor) float32 {
		if t.IsInt {
			return float32(t.I[0])
		}
		return t.F[0]
	}
	s, l, d := get(in[0]), get(in[1]), get(in[2])
	cnt := 0
	if d != 0 {
		cnt = max(0, int(math.Ceil(float64((l-s)/d))))
	}
	r := newF([]int{cnt})
	for i := range r.F {
		r.F[i] = s + float32(i)*d
	}
	return one(r), nil
}

func opCast(n *Node, in []*Tensor) ([]*Tensor, error) {
	to := n.intAttr("to", 1)
	t := in[0]
	switch to {
	case 1:
		if !t.IsInt {
			return one(t), nil
		}
		r := newF(t.Shape)
		for i, v := range t.I {
			r.F[i] = float32(v)
		}
		return one(r), nil
	case 7:
		return one(toInt(t)), nil
	}
	return nil, fmt.Errorf("cast to type %d not supported", to)
}

func opExpand(n *Node, in []*Tensor) ([]*Tensor, error) {
	want, _ := intsOf(in[1])
	ws := make([]int, len(want))
	for i, v := range want {
		ws[i] = int(v)
	}
	out, err := broadcastShape(in[0].Shape, ws)
	if err != nil {
		return nil, err
	}
	if in[0].IsInt {
		r, err := binaryI(in[0], newI(out), func(x, _ int64) int64 { return x })
		return one(r), err
	}
	r, err := binaryF(in[0], newF(out), func(x, _ float32) float32 { return x })
	return one(r), err
}

func opScatterND(n *Node, in []*Tensor) ([]*Tensor, error) {
	if s := n.Attrs["reduction"].S; s != "" && s != "none" {
		return nil, fmt.Errorf("ScatterND reduction %q not supported", s)
	}
	data, idxT, upd := in[0], in[1], in[2]
	idx, _ := intsOf(idxT)
	k := idxT.Shape[len(idxT.Shape)-1]
	nIdx := len(idx) / max(k, 1)
	slice := size(data.Shape[k:])
	ds := stridesOf(data.Shape)
	var r *Tensor
	if data.IsInt {
		r = &Tensor{Shape: append([]int(nil), data.Shape...), I: append([]int64(nil), data.I...), IsInt: true}
	} else {
		r = &Tensor{Shape: append([]int(nil), data.Shape...), F: append([]float32(nil), data.F...)}
	}
	for q := 0; q < nIdx; q++ {
		off := 0
		for j := 0; j < k; j++ {
			v := int(idx[q*k+j])
			if v < 0 {
				v += data.Shape[j]
			}
			off += v * ds[j]
		}
		if data.IsInt {
			copy(r.I[off:off+slice], upd.I[q*slice:(q+1)*slice])
		} else {
			copy(r.F[off:off+slice], upd.F[q*slice:(q+1)*slice])
		}
	}
	return one(r), nil
}

func opSplit(n *Node, in []*Tensor) ([]*Tensor, error) {
	t := in[0]
	ax := normAxis(n.intAttr("axis", 0), len(t.Shape))
	dim := t.Shape[ax]
	var parts []int
	if len(in) > 1 && in[1] != nil {
		s, _ := intsOf(in[1])
		for _, v := range s {
			parts = append(parts, int(v))
		}
	} else {
		num := len(n.Outputs)
		if v := n.intAttr("num_outputs", 0); v > 0 {
			num = int(v)
		}
		each := (dim + num - 1) / num
		for left := dim; left > 0; left -= each {
			parts = append(parts, min(each, left))
		}
	}
	outer := size(t.Shape[:ax])
	inner := size(t.Shape[ax+1:])
	var res []*Tensor
	off := 0
	for _, p := range parts {
		sh := append([]int(nil), t.Shape...)
		sh[ax] = p
		r := newF(sh)
		for o := 0; o < outer; o++ {
			copy(r.F[o*p*inner:(o+1)*p*inner], t.F[(o*dim+off)*inner:(o*dim+off+p)*inner])
		}
		off += p
		res = append(res, r)
	}
	return res, nil
}

func opSoftmax(logMode bool) opFunc {
	return func(n *Node, in []*Tensor) ([]*Tensor, error) {
		t := in[0]
		ax := normAxis(n.intAttr("axis", -1), len(t.Shape))
		if ax != len(t.Shape)-1 {
			return nil, fmt.Errorf("softmax only on last axis")
		}
		d := t.Shape[ax]
		r := newF(t.Shape)
		rows := len(t.F) / d
		parallelRows(rows, 64, func(lo, hi int) {
			for o := lo * d; o < hi*d; o += d {
				row, dst := t.F[o:o+d], r.F[o:o+d]
				mx := row[0]
				for _, v := range row {
					if v > mx {
						mx = v
					}
				}
				sum := expSubSum(dst, row, mx)
				if logMode {
					ls := float32(math.Log(float64(sum)))
					for j, v := range row {
						dst[j] = v - mx - ls
					}
				} else {
					inv := 1 / sum
					for j := range dst {
						dst[j] *= inv
					}
				}
			}
		})
		return one(r), nil
	}
}

func opLayerNorm(n *Node, in []*Tensor) ([]*Tensor, error) {
	t, scale := in[0], in[1]
	var bias *Tensor
	if len(in) > 2 {
		bias = in[2]
	}
	ax := normAxis(n.intAttr("axis", -1), len(t.Shape))
	eps := float64(n.floatAttr("epsilon", 1e-5))
	d := size(t.Shape[ax:])
	r := newF(t.Shape)
	parallelRows(len(t.F)/d, 64, func(lo, hi int) { layerNormRows(t, scale, bias, r, d, eps, lo, hi) })
	return one(r), nil
}

func layerNormRows(t, scale, bias, r *Tensor, d int, eps float64, lo, hi int) {
	for o := lo * d; o < hi*d; o += d {
		row, dst := t.F[o:o+d], r.F[o:o+d]
		var mean, v float64
		for _, x := range row {
			mean += float64(x)
		}
		mean /= float64(d)
		for _, x := range row {
			dx := float64(x) - mean
			v += dx * dx
		}
		inv := 1 / math.Sqrt(v/float64(d)+eps)
		for j, x := range row {
			y := float32((float64(x) - mean) * inv)
			y *= scale.F[j]
			if bias != nil {
				y += bias.F[j]
			}
			dst[j] = y
		}
	}
}

func opInstanceNorm(n *Node, in []*Tensor) ([]*Tensor, error) {
	t, scale, bias := in[0], in[1], in[2]
	eps := float64(n.floatAttr("epsilon", 1e-5))
	N, C := t.Shape[0], t.Shape[1]
	sp := size(t.Shape[2:])
	r := newF(t.Shape)
	for b := 0; b < N; b++ {
		for c := 0; c < C; c++ {
			o := (b*C + c) * sp
			row, dst := t.F[o:o+sp], r.F[o:o+sp]
			var mean, v float64
			for _, x := range row {
				mean += float64(x)
			}
			mean /= float64(sp)
			for _, x := range row {
				dx := float64(x) - mean
				v += dx * dx
			}
			inv := 1 / math.Sqrt(v/float64(sp)+eps)
			s, bb := float64(scale.F[c]), float64(bias.F[c])
			for j, x := range row {
				dst[j] = float32((float64(x)-mean)*inv*s + bb)
			}
		}
	}
	return one(r), nil
}

func opMatMul(n *Node, in []*Tensor) ([]*Tensor, error) {
	a, b := in[0], in[1]
	if len(a.Shape) < 2 || len(b.Shape) < 2 {
		return nil, fmt.Errorf("matmul needs rank>=2 (%v x %v)", a.Shape, b.Shape)
	}
	M, K := a.Shape[len(a.Shape)-2], a.Shape[len(a.Shape)-1]
	K2, N := b.Shape[len(b.Shape)-2], b.Shape[len(b.Shape)-1]
	if K != K2 {
		return nil, fmt.Errorf("matmul %v x %v", a.Shape, b.Shape)
	}
	if len(b.Shape) == 2 { // weights: fold all of A's batch into rows
		out := append(append([]int(nil), a.Shape[:len(a.Shape)-1]...), N)
		r := newF(out)
		rows := a.Size() / K
		gemm(rows, N, K, a.F, K, b.F, N, r.F, N)
		return one(r), nil
	}
	ba, bb := a.Shape[:len(a.Shape)-2], b.Shape[:len(b.Shape)-2]
	bs, err := broadcastShape(ba, bb)
	if err != nil {
		return nil, err
	}
	out := append(append(append([]int(nil), bs...), M), N)
	r := newF(out)
	nb := size(bs)
	sa, sb := bstrides(ba, bs), bstrides(bb, bs)
	idx := make([]int, len(bs))
	for i := 0; i < nb; i++ {
		rem, pa, pb := i, 0, 0
		for d := len(bs) - 1; d >= 0; d-- {
			idx[d] = rem % bs[d]
			rem /= bs[d]
			pa += idx[d] * sa[d]
			pb += idx[d] * sb[d]
		}
		gemm(M, N, K, a.F[pa*M*K:], K, b.F[pb*K*N:], N, r.F[i*M*N:], N)
	}
	return one(r), nil
}

// transposed-weight cache for Gemm(transB=1): weights never change.
var (
	gemmT   = map[*Tensor]*Tensor{}
	gemmTMu sync.Mutex
)

func opGemm(n *Node, in []*Tensor) ([]*Tensor, error) {
	a, b := in[0], in[1]
	if n.intAttr("transA", 0) != 0 || n.floatAttr("alpha", 1) != 1 || n.floatAttr("beta", 1) != 1 {
		return nil, fmt.Errorf("Gemm variant not supported")
	}
	if n.intAttr("transB", 0) != 0 {
		gemmTMu.Lock()
		bt, ok := gemmT[b]
		if !ok {
			bt = transpose(b, []int{1, 0})
			gemmT[b] = bt
		}
		gemmTMu.Unlock()
		b = bt
	}
	M, K, N := a.Shape[0], a.Shape[1], b.Shape[1]
	r := newF([]int{M, N})
	if len(in) > 2 && in[2] != nil {
		c := in[2]
		switch len(c.F) {
		case M * N:
			copy(r.F, c.F)
		case N:
			for i := 0; i < M; i++ {
				copy(r.F[i*N:(i+1)*N], c.F)
			}
		case 1:
			for i := range r.F {
				r.F[i] = c.F[0]
			}
		default:
			return nil, fmt.Errorf("Gemm bias shape %v", c.Shape)
		}
	}
	gemm(M, N, K, a.F, K, b.F, N, r.F, N)
	return one(r), nil
}

// opConv handles 1-D and 2-D convolution with groups, padding and strides.
func opConv(n *Node, in []*Tensor) ([]*Tensor, error) {
	x, w := in[0], in[1]
	var bias *Tensor
	if len(in) > 2 {
		bias = in[2]
	}
	nd := len(x.Shape) - 2
	if nd != 1 && nd != 2 {
		return nil, fmt.Errorf("conv%dd not supported", nd)
	}
	if s := n.Attrs["auto_pad"].S; s != "" && s != "NOTSET" {
		return nil, fmt.Errorf("auto_pad %s not supported", s)
	}
	for _, d := range n.intsAttr("dilations") {
		if d != 1 {
			return nil, fmt.Errorf("dilation not supported")
		}
	}
	// Treat 1-D as 2-D with height 1.
	N, C := x.Shape[0], x.Shape[1]
	H, W := 1, x.Shape[2]
	KH, KW := 1, w.Shape[2]
	sh, sw := 1, 1
	ph0, pw0, ph1, pw1 := 0, 0, 0, 0
	pads := n.intsAttr("pads")
	strides := n.intsAttr("strides")
	if nd == 2 {
		H, W = x.Shape[2], x.Shape[3]
		KH, KW = w.Shape[2], w.Shape[3]
		if len(strides) == 2 {
			sh, sw = int(strides[0]), int(strides[1])
		}
		if len(pads) == 4 {
			ph0, pw0, ph1, pw1 = int(pads[0]), int(pads[1]), int(pads[2]), int(pads[3])
		}
	} else {
		if len(strides) == 1 {
			sw = int(strides[0])
		}
		if len(pads) == 2 {
			pw0, pw1 = int(pads[0]), int(pads[1])
		}
	}
	M := w.Shape[0]
	G := int(n.intAttr("group", 1))
	CG, MG := C/G, M/G
	OH := (H+ph0+ph1-KH)/sh + 1
	OW := (W+pw0+pw1-KW)/sw + 1
	outShape := []int{N, M, OW}
	if nd == 2 {
		outShape = []int{N, M, OH, OW}
	}
	r := newF(outShape)
	S := OH * OW
	K := CG * KH * KW
	for b := 0; b < N; b++ {
		xb := x.F[b*C*H*W : (b+1)*C*H*W]
		ob := r.F[b*M*S : (b+1)*M*S]
		if bias != nil {
			for m := 0; m < M; m++ {
				v := bias.F[m]
				row := ob[m*S : (m+1)*S]
				for i := range row {
					row[i] = v
				}
			}
		}
		if CG == 1 && MG == 1 { // depthwise
			parallelRows(C, 8, func(clo, chi int) {
				for c := clo; c < chi; c++ {
					wk := w.F[c*KH*KW : (c+1)*KH*KW]
					src := xb[c*H*W : (c+1)*H*W]
					dst := ob[c*S : (c+1)*S]
					for oh := 0; oh < OH; oh++ {
						for kh := 0; kh < KH; kh++ {
							ih := oh*sh - ph0 + kh
							if ih < 0 || ih >= H {
								continue
							}
							for kw := 0; kw < KW; kw++ {
								wv := wk[kh*KW+kw]
								srow := src[ih*W : (ih+1)*W]
								drow := dst[oh*OW : (oh+1)*OW]
								for ow := range drow {
									iw := ow*sw - pw0 + kw
									if iw >= 0 && iw < W {
										drow[ow] += wv * srow[iw]
									}
								}
							}
						}
					}
				}
			})
			continue
		}
		for g := 0; g < G; g++ {
			xg := xb[g*CG*H*W : (g+1)*CG*H*W]
			var cols []float32
			if KH == 1 && KW == 1 && sh == 1 && sw == 1 && ph0+ph1+pw0+pw1 == 0 {
				cols = xg // pointwise: the input already is the column matrix
			} else {
				cols = make([]float32, K*S)
				for c := 0; c < CG; c++ {
					for kh := 0; kh < KH; kh++ {
						for kw := 0; kw < KW; kw++ {
							row := cols[((c*KH+kh)*KW+kw)*S:]
							for oh := 0; oh < OH; oh++ {
								ih := oh*sh - ph0 + kh
								if ih < 0 || ih >= H {
									continue
								}
								srow := xg[(c*H+ih)*W : (c*H+ih+1)*W]
								drow := row[oh*OW : (oh+1)*OW]
								for ow := range drow {
									iw := ow*sw - pw0 + kw
									if iw >= 0 && iw < W {
										drow[ow] = srow[iw]
									}
								}
							}
						}
					}
				}
			}
			gemm(MG, S, K, w.F[g*MG*K:], K, cols, S, ob[g*MG*S:], S)
		}
	}
	return one(r), nil
}

func opSigmoid(n *Node, in []*Tensor) ([]*Tensor, error) {
	r := newF(in[0].Shape)
	sigmoidSlice(r.F, in[0].F)
	return one(r), nil
}
