package deepcw

import (
	"os"
	"sort"
	"testing"
	"time"

	"github.com/Jnubbz/cw-console/cw"
)

// CW_PROFILE=1 go test -run Profile -v ./deepcw
func TestProfileOps(t *testing.T) {
	if os.Getenv("CW_PROFILE") == "" {
		t.Skip()
	}
	m := loadT(t)
	a := cw.MorseSamples("CQ CQ DE KN0BLE KN0BLE K", 20, 600, 3200)
	if n := 15*3200 - len(a); n > 0 {
		a = append(a, make([]float32, n)...)
	}
	a = a[:15*3200]
	if os.Getenv("CW_THREADS") == "2" {
		SetThreads(2)
		defer SetThreads(1)
	}
	agg := map[string]time.Duration{}
	m.Trace = func(op string, _ []*Tensor, d time.Duration) { agg[op] += d }
	t0 := time.Now()
	if _, err := m.Decode(a); err != nil {
		t.Fatal(err)
	}
	total := time.Since(t0)
	type kv struct {
		k string
		v time.Duration
	}
	var l []kv
	for k, v := range agg {
		l = append(l, kv{k, v})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].v > l[j].v })
	t.Logf("threads=%d simd=%s total %v", Threads(), SIMDName(), total)
	for _, x := range l[:10] {
		t.Logf("  %-22s %6.0f ms  %4.1f%%", x.k, x.v.Seconds()*1000, 100*x.v.Seconds()/total.Seconds())
	}
}
