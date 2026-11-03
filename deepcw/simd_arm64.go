//go:build arm64

package deepcw

//go:noescape
func sgemmKernel4x16(a *float32, lda int, b *float32, ldb int, c *float32, ldc int, k int)

// Every arm64 CPU has NEON (Advanced SIMD); it's part of the base architecture.
// (useAVX2 is the historical name of "use the vector kernel".)
var useAVX2 = true

// SIMDName describes the active matrix kernel.
func SIMDName() string {
	if useAVX2 {
		return "NEON"
	}
	return "portable (NEON off)"
}

// SetSIMD enables or disables the NEON kernel.
func SetSIMD(on bool) { useAVX2 = on }

// HasAVX2 reports whether a vector kernel is available (NEON here).
func HasAVX2() bool { return true }

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
