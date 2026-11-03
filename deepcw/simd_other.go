//go:build !amd64 && !arm64

package deepcw

var useAVX2 = false

func sgemmKernel4x16(a *float32, lda int, b *float32, ldb int, c *float32, ldc int, k int) {
	panic("no AVX2 kernel on this architecture")
}

// SIMDName describes the active matrix kernel.
func SIMDName() string { return "portable" }

// SetSIMD is a no-op without AVX2.
func SetSIMD(on bool) {}

// HasAVX2 reports CPU support.
func HasAVX2() bool { return false }

func expSubSum(dst, src []float32, sub float32) float32 {
	var s float32
	for i, v := range src {
		e := expf(v - sub)
		dst[i] = e
		s += e
	}
	return s
}

func sigmoidSlice(dst, src []float32) {
	for i, v := range src {
		dst[i] = 1 / (1 + expf(-v))
	}
}
