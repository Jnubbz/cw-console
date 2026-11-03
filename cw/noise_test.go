package cw

import "testing"

// Empty band: 60 s of broadband noise (plus crashes) must not produce more junk in
// improved mode than in exact-Nexus mode, at any sensitivity.
func TestImprovedNoWorseOnEmptyBand(t *testing.T) {
	for _, amp := range []float32{0.02, 0.1, 0.3} {
		for _, sens := range []float32{0, 0.5, 1} {
			noise := whiteNoise(int(tSR*60), amp)
			storm := crashStorm(12)
			for i := range storm {
				noise[i] += 0.5 * amp * storm[i]
			}
			rs := 0
			count := func(imp bool) int {
				d := NewStreamDecoder(tSR, tPitch)
				d.SetSensitivity(sens)
				d.SetImproved(imp)
				d.Push(noise)
				if imp {
					rs = d.Resyncs
				}
				return glyphs(d.Transcript())
			}
			nx, im := count(false), count(true)
			t.Logf("noise %.2f sens %.1f: nexus %d junk chars, improved %d (resyncs %d)", amp, sens, nx, im, rs)
			if im > nx+3 {
				t.Errorf("improved mode adds junk: nexus=%d improved=%d", nx, im)
			}
		}
	}
}
