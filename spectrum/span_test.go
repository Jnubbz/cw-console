package spectrum

import "testing"

func TestFT8SpanMatchesTheWideGraph(t *testing.T) {
	if HzAt(SpanFT8, 0) != 0 {
		t.Fatalf("left edge %v", HzAt(SpanFT8, 0))
	}
	if HzAt(SpanFT8, SpanFT8.Cols-1) != 3000 {
		t.Fatalf("right edge %v", HzAt(SpanFT8, SpanFT8.Cols-1))
	}
	// 1500 Hz is the middle of 0-3000, which is the middle column.
	if col := ColAt(SpanFT8, 1500); col != 128 && col != 127 {
		t.Fatalf("1500 Hz landed on column %d", col)
	}
}

func TestSSTVGuidesFallInsideTheSpan(t *testing.T) {
	for _, hz := range []float64{1200, 1500, 2300} {
		c := ColAt(SpanSSTV, hz)
		if c <= 0 || c >= SpanSSTV.Cols-1 {
			t.Fatalf("%v Hz mapped to column %d, outside the SSTV span", hz, c)
		}
	}
}

func TestClickRoundTrip(t *testing.T) {
	for _, s := range []Span{SpanCW, SpanFT8, SpanSSTV} {
		for col := 0; col < s.Cols; col += 17 {
			back := ColAt(s, HzAt(s, col))
			if back != col && back != col-1 && back != col+1 {
				t.Fatalf("%s col %d -> %v Hz -> col %d", s.Name, col, HzAt(s, col), back)
			}
		}
	}
}
