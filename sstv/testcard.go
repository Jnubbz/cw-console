package sstv

import "image"

// TestCard draws colour bars over smooth gradients: a picture for trying a mode out.
func TestCard(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	bars := [][3]uint8{{255, 255, 255}, {255, 255, 0}, {0, 255, 255}, {0, 255, 0}, {255, 0, 255}, {255, 0, 0}, {0, 0, 255}, {0, 0, 0}}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := img.Pix[(y*w+x)*4:]
			p[3] = 255
			if y < h/2 {
				c := bars[x*len(bars)/w]
				p[0], p[1], p[2] = c[0], c[1], c[2]
			} else {
				p[0] = uint8(x * 255 / w)
				p[1] = uint8((y - h/2) * 255 / (h / 2))
				p[2] = uint8(255 - x*255/w)
			}
		}
	}
	return img
}
