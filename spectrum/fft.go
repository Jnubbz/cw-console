package spectrum

import (
	"math"
	"math/cmplx"
)

// Hann is the window both of the old displays used.
func Hann(n int) []float64 {
	w := make([]float64, n)
	if n <= 1 {
		return w
	}
	for i := range w {
		w[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n))
	}
	return w
}

// FFT is an in-place radix-2 transform. len(x) must be a power of two.
func FFT(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		step := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u, v := x[start+k], x[start+k+size/2]*w
				x[start+k], x[start+k+size/2] = u+v, u-v
				w *= step
			}
		}
	}
}

// Magnitudes returns |X[k]| for bins 0 .. n/2-1 of a Hann-windowed FFT.
// samples must already be a power-of-two length. win may be nil.
func Magnitudes(samples []float32, win []float64) []float64 {
	n := len(samples)
	if n < 2 || n&(n-1) != 0 {
		return nil
	}
	buf := make([]complex128, n)
	for i, s := range samples {
		w := 1.0
		if i < len(win) {
			w = win[i]
		}
		buf[i] = complex(float64(s)*w, 0)
	}
	FFT(buf)
	mag := make([]float64, n/2)
	for i := range mag {
		mag[i] = cmplx.Abs(buf[i])
	}
	return mag
}
