package deepcw

import (
	"math"
	"testing"
)

func TestExpfAccuracy(t *testing.T) {
	worst := 0.0
	for x := -87.0; x <= 88.0; x += 0.0137 {
		got := float64(expf(float32(x)))
		want := math.Exp(float64(float32(x)))
		if rel := math.Abs(got-want) / want; rel > worst {
			worst = rel
		}
	}
	if worst > 4e-7 {
		t.Fatalf("worst relative error %g", worst)
	}
	t.Logf("worst relative error %.2g", worst)
	if expf(-100) != 0 || !math.IsInf(float64(expf(100)), 1) {
		t.Fatal("bad limits")
	}
}

func TestVectorExpMatchesScalar(t *testing.T) {
	src := make([]float32, 1003)
	for i := range src {
		src[i] = float32(i)*0.19 - 100 // spans the clamp region too
	}
	for _, simd := range []bool{false, true} {
		SetSIMD(simd)
		d1 := make([]float32, len(src))
		sub := src[len(src)-1] // softmax subtracts the row max
		s := expSubSum(d1, src, sub)
		var want float64
		for i, v := range src {
			w := math.Exp(float64(v - sub))
			want += w
			if math.Abs(float64(d1[i])-w) > 4e-7*w+1e-37 {
				t.Fatalf("simd=%v exp[%d] %v vs %v", simd, i, d1[i], w)
			}
		}
		if math.Abs(float64(s)-want) > 1e-5*want {
			t.Fatalf("simd=%v sum %v vs %v", simd, s, want)
		}
		sg := make([]float32, len(src))
		sigmoidSlice(sg, src)
		for i, v := range src {
			w := 1 / (1 + math.Exp(-float64(v)))
			if math.Abs(float64(sg[i])-w) > 1e-6 {
				t.Fatalf("simd=%v sigmoid[%d] %v vs %v", simd, i, sg[i], w)
			}
		}
	}
	SetSIMD(true)
}
