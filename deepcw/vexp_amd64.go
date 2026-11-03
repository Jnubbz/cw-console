//go:build amd64

package deepcw

//go:noescape
func expSubSumAVX(dst, src *float32, n int, sub float32) float32

//go:noescape
func sigmoidAVX(dst, src *float32, n int)

// expSubSum sets dst[i] = exp(src[i]-sub) and returns the sum.
func expSubSum(dst, src []float32, sub float32) float32 {
	n8 := 0
	var s float32
	if useAVX2 && len(src) >= 8 {
		n8 = len(src) &^ 7
		s = expSubSumAVX(&dst[0], &src[0], n8, sub)
	}
	for i := n8; i < len(src); i++ {
		e := expf(src[i] - sub)
		dst[i] = e
		s += e
	}
	return s
}

// sigmoidSlice sets dst[i] = 1/(1+exp(-src[i])).
func sigmoidSlice(dst, src []float32) {
	n8 := 0
	if useAVX2 && len(src) >= 8 {
		n8 = len(src) &^ 7
		sigmoidAVX(&dst[0], &src[0], n8)
	}
	for i := n8; i < len(src); i++ {
		dst[i] = 1 / (1 + expf(-src[i]))
	}
}
