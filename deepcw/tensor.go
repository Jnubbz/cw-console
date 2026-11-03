package deepcw

import "fmt"

// Tensor is a dense row-major tensor of float32 or int64. Ops never modify their
// inputs, so reshapes may share the backing slice.
type Tensor struct {
	Shape []int
	F     []float32
	I     []int64
	IsInt bool
}

func size(shape []int) int {
	n := 1
	for _, d := range shape {
		n *= d
	}
	return n
}

func (t *Tensor) Size() int { return size(t.Shape) }

func newF(shape []int) *Tensor {
	return &Tensor{Shape: append([]int(nil), shape...), F: make([]float32, size(shape))}
}

func newI(shape []int) *Tensor {
	return &Tensor{Shape: append([]int(nil), shape...), I: make([]int64, size(shape)), IsInt: true}
}

func (t *Tensor) reshaped(shape []int) *Tensor {
	return &Tensor{Shape: append([]int(nil), shape...), F: t.F, I: t.I, IsInt: t.IsInt}
}

func (t *Tensor) String() string {
	kind := "f32"
	if t.IsInt {
		kind = "i64"
	}
	return fmt.Sprintf("%s%v", kind, t.Shape)
}

func stridesOf(shape []int) []int {
	s := make([]int, len(shape))
	acc := 1
	for i := len(shape) - 1; i >= 0; i-- {
		s[i] = acc
		acc *= shape[i]
	}
	return s
}

// intsOf reads a 1-D/scalar int tensor (shape vectors, axes, indices...).
func intsOf(t *Tensor) ([]int64, error) {
	if t == nil {
		return nil, nil
	}
	if t.IsInt {
		return t.I, nil
	}
	out := make([]int64, len(t.F))
	for i, v := range t.F {
		out[i] = int64(v)
	}
	return out, nil
}

func normAxis(axis int64, rank int) int {
	a := int(axis)
	if a < 0 {
		a += rank
	}
	return a
}

// broadcastShape returns the numpy-style broadcast of a and b.
func broadcastShape(a, b []int) ([]int, error) {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	out := make([]int, n)
	for i := 0; i < n; i++ {
		da, db := 1, 1
		if j := len(a) - n + i; j >= 0 {
			da = a[j]
		}
		if j := len(b) - n + i; j >= 0 {
			db = b[j]
		}
		switch {
		case da == db, db == 1:
			out[i] = da
		case da == 1:
			out[i] = db
		default:
			return nil, fmt.Errorf("cannot broadcast %v and %v", a, b)
		}
	}
	return out, nil
}

// bstrides gives operand strides aligned to the output rank, 0 on broadcast dims.
func bstrides(shape, out []int) []int {
	s := make([]int, len(out))
	src := stridesOf(shape)
	off := len(out) - len(shape)
	for i := range out {
		j := i - off
		if j >= 0 && shape[j] != 1 {
			s[i] = src[j]
		}
	}
	return s
}

// coalesce merges adjacent dims that every operand walks contiguously (or not at all),
// so broadcasting loops run long inner loops.
func coalesce(shape []int, strides ...[]int) ([]int, [][]int) {
	sh := []int{}
	st := make([][]int, len(strides))
	for i := 0; i < len(shape); i++ {
		if shape[i] == 1 {
			continue
		}
		if len(sh) > 0 {
			last := len(sh) - 1
			ok := true
			for k := range strides {
				if st[k][last] != strides[k][i]*shape[i] {
					ok = false
					break
				}
			}
			if ok {
				sh[last] *= shape[i]
				for k := range strides {
					st[k][last] = strides[k][i]
				}
				continue
			}
		}
		sh = append(sh, shape[i])
		for k := range strides {
			st[k] = append(st[k], strides[k][i])
		}
	}
	if len(sh) == 0 {
		sh = []int{1}
		for k := range st {
			st[k] = []int{0}
		}
	}
	return sh, st
}

// binaryF applies f elementwise with broadcasting.
func binaryF(a, b *Tensor, f func(x, y float32) float32) (*Tensor, error) {
	out, err := broadcastShape(a.Shape, b.Shape)
	if err != nil {
		return nil, err
	}
	r := newF(out)
	switch {
	case sameShape(a.Shape, b.Shape):
		parallelRows(len(r.F), 1<<15, func(lo, hi int) {
			x, y, d := a.F[lo:hi], b.F[lo:hi], r.F[lo:hi]
			for i := range d {
				d[i] = f(x[i], y[i])
			}
		})
		return r, nil
	case len(b.F) == 1:
		y := b.F[0]
		parallelRows(len(r.F), 1<<15, func(lo, hi int) {
			for i := lo; i < hi; i++ {
				r.F[i] = f(a.F[i%len(a.F)], y)
			}
		})
		return r, nil
	}
	sh, st := coalesce(out, bstrides(a.Shape, out), bstrides(b.Shape, out))
	sa, sb := st[0], st[1]
	last := len(sh) - 1
	inner := sh[last]
	ia, ib := sa[last], sb[last]
	blocks := len(r.F) / inner
	parallelRows(blocks, max(1, (1<<15)/inner), func(lo, hi int) {
		for blk := lo; blk < hi; blk++ {
			pa, pb, rem := 0, 0, blk
			for d := last - 1; d >= 0; d-- {
				i := rem % sh[d]
				rem /= sh[d]
				pa += i * sa[d]
				pb += i * sb[d]
			}
			dst := r.F[blk*inner : (blk+1)*inner]
			switch {
			case ia == 1 && ib == 1:
				src1, src2 := a.F[pa:pa+inner], b.F[pb:pb+inner]
				for j := range dst {
					dst[j] = f(src1[j], src2[j])
				}
			case ia == 1 && ib == 0:
				y := b.F[pb]
				src1 := a.F[pa : pa+inner]
				for j := range dst {
					dst[j] = f(src1[j], y)
				}
			case ia == 0 && ib == 1:
				x := a.F[pa]
				src2 := b.F[pb : pb+inner]
				for j := range dst {
					dst[j] = f(x, src2[j])
				}
			default:
				for j := range dst {
					dst[j] = f(a.F[pa+j*ia], b.F[pb+j*ib])
				}
			}
		}
	})
	return r, nil
}

// binaryI is the (small, shape-arithmetic) int64 version.
func binaryI(a, b *Tensor, f func(x, y int64) int64) (*Tensor, error) {
	out, err := broadcastShape(a.Shape, b.Shape)
	if err != nil {
		return nil, err
	}
	r := newI(out)
	sa, sb := bstrides(a.Shape, out), bstrides(b.Shape, out)
	idx := make([]int, len(out))
	for o := range r.I {
		rem, pa, pb := o, 0, 0
		for d := len(out) - 1; d >= 0; d-- {
			idx[d] = rem % out[d]
			rem /= out[d]
			pa += idx[d] * sa[d]
			pb += idx[d] * sb[d]
		}
		r.I[o] = f(a.I[pa], b.I[pb])
	}
	return r, nil
}

func sameShape(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// transpose permutes dims (general N-D).
func transpose(t *Tensor, perm []int) *Tensor {
	in := t.Shape
	out := make([]int, len(perm))
	for i, p := range perm {
		out[i] = in[p]
	}
	src := stridesOf(in)
	st := make([]int, len(perm))
	for i, p := range perm {
		st[i] = src[p]
	}
	sh, sts := coalesce(out, st, stridesOf(out))
	st = sts[0]
	var r *Tensor
	if t.IsInt {
		r = newI(out)
	} else {
		r = newF(out)
	}
	last := len(sh) - 1
	inner, is := sh[last], st[last]
	idx := make([]int, len(sh))
	off, o := 0, 0
	for {
		if t.IsInt {
			for j := 0; j < inner; j++ {
				r.I[o+j] = t.I[off+j*is]
			}
		} else {
			dst := r.F[o : o+inner]
			if is == 1 {
				copy(dst, t.F[off:off+inner])
			} else {
				for j := range dst {
					dst[j] = t.F[off+j*is]
				}
			}
		}
		o += inner
		d := last - 1
		for ; d >= 0; d-- {
			idx[d]++
			off += st[d]
			if idx[d] < sh[d] {
				break
			}
			off -= st[d] * sh[d]
			idx[d] = 0
		}
		if d < 0 {
			return r
		}
	}
}
