package deepcw

import (
	"runtime"
	"sync"
	"sync/atomic"
)

// Threads is how many goroutines a matrix multiply may use (set by the speed setting).
var threads atomic.Int32

func init() { threads.Store(1) }

// SetThreads sets the worker count for matrix math (clamped to 1..NumCPU).
func SetThreads(n int) {
	if n < 1 {
		n = 1
	}
	if c := runtime.NumCPU(); n > c {
		n = c
	}
	threads.Store(int32(n))
}

// Threads returns the current worker count.
func Threads() int { return int(threads.Load()) }

const (
	tileM = 64
	tileN = 256
)

// gemm: C[m×n] (row stride ldc) += A[m×k] (row stride lda) × B[k×n] (row stride ldb).
// Work is split into tiles that keep a panel of B hot in cache, shared out to workers.
func gemm(m, n, k int, a []float32, lda int, b []float32, ldb int, c []float32, ldc int) {
	if m == 0 || n == 0 || k == 0 {
		return
	}
	tm := (m + tileM - 1) / tileM
	tn := (n + tileN - 1) / tileN
	tiles := tm * tn
	w := Threads()
	if w > tiles {
		w = tiles
	}
	// Small products: not worth the goroutines.
	if w <= 1 || m*n*k < 1<<16 {
		for t := 0; t < tiles; t++ {
			gemmTile(t, tn, m, n, k, a, lda, b, ldb, c, ldc)
		}
		return
	}
	var next atomic.Int32
	var wg sync.WaitGroup
	wg.Add(w)
	for i := 0; i < w; i++ {
		go func() {
			defer wg.Done()
			for {
				t := int(next.Add(1)) - 1
				if t >= tiles {
					return
				}
				gemmTile(t, tn, m, n, k, a, lda, b, ldb, c, ldc)
			}
		}()
	}
	wg.Wait()
}

func gemmTile(t, tn, m, n, k int, a []float32, lda int, b []float32, ldb int, c []float32, ldc int) {
	i0, j0 := (t/tn)*tileM, (t%tn)*tileN
	i1, j1 := min(i0+tileM, m), min(j0+tileN, n)
	i := i0
	for ; i+4 <= i1; i += 4 {
		j := j0
		if useAVX2 {
			for ; j+16 <= j1; j += 16 {
				sgemmKernel4x16(&a[i*lda], lda, &b[j], ldb, &c[i*ldc+j], ldc, k)
			}
		}
		if j < j1 {
			gemm4rows(i, j, j1, k, a, lda, b, ldb, c, ldc)
		}
	}
	for ; i < i1; i++ {
		ci := c[i*ldc+j0 : i*ldc+j1]
		for p := 0; p < k; p++ {
			av := a[i*lda+p]
			bp := b[p*ldb+j0 : p*ldb+j1]
			for j, bv := range bp {
				ci[j] += av * bv
			}
		}
	}
}

// gemm4rows is the portable path: 4 rows of C over columns [j0,j1), unrolled by 4.
func gemm4rows(i, j0, j1, k int, a []float32, lda int, b []float32, ldb int, c []float32, ldc int) {
	w := j1 - j0
	c0 := c[i*ldc+j0 : i*ldc+j1]
	c1 := c[(i+1)*ldc+j0 : (i+1)*ldc+j1]
	c2 := c[(i+2)*ldc+j0 : (i+2)*ldc+j1]
	c3 := c[(i+3)*ldc+j0 : (i+3)*ldc+j1]
	for p := 0; p < k; p++ {
		a0, a1, a2, a3 := a[i*lda+p], a[(i+1)*lda+p], a[(i+2)*lda+p], a[(i+3)*lda+p]
		bp := b[p*ldb+j0 : p*ldb+j0+w]
		j := 0
		for ; j+4 <= w; j += 4 {
			b0, b1, b2, b3 := bp[j], bp[j+1], bp[j+2], bp[j+3]
			c0[j] += a0 * b0
			c0[j+1] += a0 * b1
			c0[j+2] += a0 * b2
			c0[j+3] += a0 * b3
			c1[j] += a1 * b0
			c1[j+1] += a1 * b1
			c1[j+2] += a1 * b2
			c1[j+3] += a1 * b3
			c2[j] += a2 * b0
			c2[j+1] += a2 * b1
			c2[j+2] += a2 * b2
			c2[j+3] += a2 * b3
			c3[j] += a3 * b0
			c3[j+1] += a3 * b1
			c3[j+2] += a3 * b2
			c3[j+3] += a3 * b3
		}
		for ; j < w; j++ {
			bv := bp[j]
			c0[j] += a0 * bv
			c1[j] += a1 * bv
			c2[j] += a2 * bv
			c3[j] += a3 * bv
		}
	}
}
