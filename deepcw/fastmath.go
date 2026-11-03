package deepcw

import (
	"math"
	"sync"
)

// expf is a float32 exp (Cephes expf: range reduction by ln2 + degree-6 polynomial),
// accurate to ~1-2 ulp and several times faster than math.Exp in float64.
func expf(x float32) float32 {
	if x > 88.72 {
		return float32(math.Inf(1))
	}
	if x < -87.33 {
		return 0
	}
	const log2e = 1.44269504088896341
	fx := x*log2e + 0.5
	n := float32(int32(fx))
	if fx < 0 && n != fx { // floor for negatives
		n--
	}
	x = x - n*0.693359375 - n*(-2.12194440e-4)
	z := x * x
	y := float32(1.9875691500e-4)
	y = y*x + 1.3981999507e-3
	y = y*x + 8.3334519073e-3
	y = y*x + 4.1665795894e-2
	y = y*x + 1.6666665459e-1
	y = y*x + 5.0000001201e-1
	y = y*z + x + 1
	return y * math.Float32frombits(uint32(int32(n)+127)<<23)
}

// parallelRows runs f over [0,rows) split across the configured threads.
func parallelRows(rows, minPerTask int, f func(lo, hi int)) {
	w := Threads()
	if w <= 1 || rows < 2*minPerTask {
		f(0, rows)
		return
	}
	if max := rows / minPerTask; w > max {
		w = max
	}
	var wg sync.WaitGroup
	chunk := (rows + w - 1) / w
	for lo := 0; lo < rows; lo += chunk {
		hi := min(lo+chunk, rows)
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			f(lo, hi)
		}(lo, hi)
	}
	wg.Wait()
}
