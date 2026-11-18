package sstv

import (
	"image"
	"math"
)

// Encode turns img into audio for mode m at sampleRate, header (leader + VIS) included.
// The picture is scaled to the mode's size. Samples are -1..1 at 0.8 of full scale.
func Encode(img image.Image, m *Mode, sampleRate int) []float32 {
	return encode(img, m, float64(sampleRate), 0)
}

type toneGen struct {
	fs, off float64
	t       float64 // end of everything asked for so far, seconds
	n       int     // samples written
	phase   float64
	out     []float32
}

func (g *toneGen) emit(f float64) {
	g.phase += 2 * math.Pi * (f + g.off) / g.fs
	if g.phase > 2*math.Pi {
		g.phase -= 2 * math.Pi
	}
	g.out = append(g.out, float32(0.8*math.Sin(g.phase)))
	g.n++
}

func (g *toneGen) tone(f, dur float64) {
	g.t += dur
	for float64(g.n)/g.fs < g.t {
		g.emit(f)
	}
}

// scan sends px (0..255 values) over dur.
func (g *toneGen) scan(px []float64, dur float64) {
	start := g.t
	g.t += dur
	per := dur / float64(len(px))
	for {
		t := float64(g.n) / g.fs
		if t >= g.t {
			return
		}
		i := int((t - start) / per)
		if i < 0 {
			i = 0
		}
		if i >= len(px) {
			i = len(px) - 1
		}
		g.emit(FreqBlack + px[i]*(FreqWhite-FreqBlack)/255)
	}
}

func encode(img image.Image, m *Mode, fs, off float64) []float32 {
	pic := Resize(img, m.Width, m.Height)
	g := &toneGen{fs: fs, off: off}
	// calibration header and VIS
	g.tone(freqLead, 0.300)
	g.tone(FreqSync, 0.010)
	g.tone(freqLead, 0.300)
	g.tone(FreqSync, 0.030)
	ones := 0
	for i := 0; i < 7; i++ {
		if m.VIS>>i&1 == 1 {
			g.tone(1100, 0.030)
			ones++
		} else {
			g.tone(1300, 0.030)
		}
	}
	if ones%2 == 1 {
		g.tone(1100, 0.030)
	} else {
		g.tone(1300, 0.030)
	}
	g.tone(FreqSync, 0.030)

	W := m.Width
	row := func(y, c int) []float64 { // c: 0,1,2 = R,G,B
		out := make([]float64, W)
		for x := 0; x < W; x++ {
			out[x] = float64(pic.Pix[(y*W+x)*4+c])
		}
		return out
	}
	yc := func(y int) (Y, Cr, Cb []float64) {
		Y, Cr, Cb = make([]float64, W), make([]float64, W), make([]float64, W)
		for x := 0; x < W; x++ {
			p := pic.Pix[(y*W+x)*4:]
			Y[x], Cr[x], Cb[x] = toYC(float64(p[0]), float64(p[1]), float64(p[2]))
		}
		return
	}
	avg := func(a, b []float64) []float64 {
		out := make([]float64, len(a))
		for i := range a {
			out[i] = (a[i] + b[i]) / 2
		}
		return out
	}
	half := func(a []float64) []float64 {
		out := make([]float64, len(a)/2)
		for i := range out {
			out[i] = (a[2*i] + a[2*i+1]) / 2
		}
		return out
	}

	switch m.Family {
	case Martin:
		for y := 0; y < m.Height; y++ {
			g.tone(FreqSync, m.Sync)
			g.tone(FreqBlack, m.Porch)
			for _, c := range []int{1, 2, 0} {
				g.scan(row(y, c), m.Scan)
				g.tone(FreqBlack, m.Sep)
			}
		}
	case Scottie:
		g.tone(FreqSync, m.Sync) // the starting sync, first line only
		for y := 0; y < m.Height; y++ {
			g.tone(FreqBlack, m.Sep)
			g.scan(row(y, 1), m.Scan)
			g.tone(FreqBlack, m.Sep)
			g.scan(row(y, 2), m.Scan)
			g.tone(FreqSync, m.Sync)
			g.tone(FreqBlack, m.Porch)
			g.scan(row(y, 0), m.Scan)
		}
	case Robot36:
		for y := 0; y < m.Height; y++ {
			Y, Cr, Cb := yc(y)
			o := y ^ 1 // the other line of the pair
			if o >= m.Height {
				o = y
			}
			_, Cr2, Cb2 := yc(o)
			g.tone(FreqSync, m.Sync)
			g.tone(FreqBlack, m.Porch)
			g.scan(Y, m.Scan)
			if y%2 == 0 {
				g.tone(FreqBlack, 4.5e-3)
				g.tone(freqLead, 1.5e-3)
				g.scan(half(avg(Cr, Cr2)), m.Scan/2)
			} else {
				g.tone(FreqWhite, 4.5e-3)
				g.tone(freqLead, 1.5e-3)
				g.scan(half(avg(Cb, Cb2)), m.Scan/2)
			}
		}
	case Robot72:
		for y := 0; y < m.Height; y++ {
			Y, Cr, Cb := yc(y)
			g.tone(FreqSync, m.Sync)
			g.tone(FreqBlack, m.Porch)
			g.scan(Y, m.Scan)
			g.tone(FreqBlack, 4.5e-3)
			g.tone(freqLead, 1.5e-3)
			g.scan(half(Cr), m.Scan/2)
			g.tone(FreqWhite, 4.5e-3)
			g.tone(FreqBlack, 1.5e-3)
			g.scan(half(Cb), m.Scan/2)
		}
	case PD:
		for y := 0; y+1 < m.Height; y += 2 {
			Y0, Cr0, Cb0 := yc(y)
			Y1, Cr1, Cb1 := yc(y + 1)
			g.tone(FreqSync, m.Sync)
			g.tone(FreqBlack, m.Porch)
			g.scan(Y0, m.Scan)
			g.scan(avg(Cr0, Cr1), m.Scan)
			g.scan(avg(Cb0, Cb1), m.Scan)
			g.scan(Y1, m.Scan)
		}
	}
	g.tone(FreqBlack, 0.050) // a clean end
	return g.out
}

// Resize scales img to w x h (bilinear) into RGBA.
func Resize(img image.Image, w, h int) *image.RGBA {
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	sw, sh := b.Dx(), b.Dy()
	if sw == 0 || sh == 0 {
		return out
	}
	at := func(x, y int) (r, g, bl float64) {
		if x >= sw {
			x = sw - 1
		}
		if y >= sh {
			y = sh - 1
		}
		R, G, B, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
		return float64(R >> 8), float64(G >> 8), float64(B >> 8)
	}
	for y := 0; y < h; y++ {
		fy := (float64(y)+0.5)*float64(sh)/float64(h) - 0.5
		if fy < 0 {
			fy = 0
		}
		y0 := int(fy)
		wy := fy - float64(y0)
		for x := 0; x < w; x++ {
			fx := (float64(x)+0.5)*float64(sw)/float64(w) - 0.5
			if fx < 0 {
				fx = 0
			}
			x0 := int(fx)
			wx := fx - float64(x0)
			r00, g00, b00 := at(x0, y0)
			r10, g10, b10 := at(x0+1, y0)
			r01, g01, b01 := at(x0, y0+1)
			r11, g11, b11 := at(x0+1, y0+1)
			mix := func(a, b, c, d float64) uint8 {
				return clamp8((a*(1-wx)+b*wx)*(1-wy) + (c*(1-wx)+d*wx)*wy)
			}
			p := out.Pix[(y*w+x)*4:]
			p[0], p[1], p[2], p[3] = mix(r00, r10, r01, r11), mix(g00, g10, g01, g11), mix(b00, b10, b01, b11), 255
		}
	}
	return out
}
