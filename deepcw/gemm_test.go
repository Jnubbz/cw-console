package deepcw

import (
	"math"
	"math/rand"
	"testing"
)

// The AVX2 kernel must agree with the portable path on every shape class:
// edges (m%4, n%16), tiny k, strided operands, and multiple threads.
func TestAVX2MatchesPortable(t *testing.T) {
	if !HasAVX2() {
		t.Skip("no AVX2 here")
	}
	defer SetSIMD(true)
	defer SetThreads(1)
	r := rand.New(rand.NewSource(1))
	shapes := [][3]int{{1, 1, 1}, {4, 16, 1}, {4, 16, 7}, {5, 17, 3}, {7, 33, 64}, {64, 256, 192},
		{65, 257, 100}, {3, 15, 9}, {128, 42, 192}, {96, 1000, 64}, {33, 600, 288}}
	for _, th := range []int{1, 2, 3} {
		SetThreads(th)
		for _, s := range shapes {
			m, n, k := s[0], s[1], s[2]
			lda, ldb, ldc := k+3, n+5, n+2 // non-contiguous strides on purpose
			a := make([]float32, m*lda)
			b := make([]float32, k*ldb)
			c0 := make([]float32, m*ldc)
			for i := range a {
				a[i] = r.Float32()*2 - 1
			}
			for i := range b {
				b[i] = r.Float32()*2 - 1
			}
			for i := range c0 {
				c0[i] = r.Float32()
			}
			c1 := append([]float32(nil), c0...)
			c2 := append([]float32(nil), c0...)
			SetSIMD(false)
			gemm(m, n, k, a, lda, b, ldb, c1, ldc)
			SetSIMD(true)
			gemm(m, n, k, a, lda, b, ldb, c2, ldc)
			for i := 0; i < m; i++ {
				for j := 0; j < ldc; j++ {
					x, y := c1[i*ldc+j], c2[i*ldc+j]
					if j >= n && x != c0[i*ldc+j] || y != x && j >= n {
						t.Fatalf("%v: wrote outside C at %d,%d", s, i, j)
					}
					if math.Abs(float64(x-y)) > 1e-4*(1+math.Abs(float64(x))) {
						t.Fatalf("%v threads %d: mismatch at %d,%d: %v vs %v", s, th, i, j, x, y)
					}
				}
			}
		}
	}
}
