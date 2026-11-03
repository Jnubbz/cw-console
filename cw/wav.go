package cw

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
)

// ReadWAV loads a WAV file as mono float32 (channels averaged).
// Supports PCM 8/16/24/32-bit and IEEE float 32/64-bit, incl. WAVE_FORMAT_EXTENSIBLE.
func ReadWAV(path string) (samples []float32, sampleRate uint32, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	return ParseWAV(b)
}

// ParseWAV decodes WAV bytes. See ReadWAV.
func ParseWAV(b []byte) ([]float32, uint32, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a WAV file")
	}
	var (
		format, channels, bits uint16
		rate                   uint32
		haveFmt                bool
		data                   []byte
	)
	p := 12
	for p+8 <= len(b) {
		id := string(b[p : p+4])
		size := int(binary.LittleEndian.Uint32(b[p+4 : p+8]))
		body := p + 8
		end := body + size
		if end > len(b) || size < 0 {
			end = len(b) // tolerate truncated files
		}
		switch id {
		case "fmt ":
			if end-body < 16 {
				return nil, 0, errors.New("bad fmt chunk")
			}
			format = binary.LittleEndian.Uint16(b[body:])
			channels = binary.LittleEndian.Uint16(b[body+2:])
			rate = binary.LittleEndian.Uint32(b[body+4:])
			bits = binary.LittleEndian.Uint16(b[body+14:])
			if format == 0xFFFE && end-body >= 26 {
				format = binary.LittleEndian.Uint16(b[body+24:]) // sub-format GUID's first 2 bytes
			}
			haveFmt = true
		case "data":
			data = b[body:end]
		}
		p = body + size + (size & 1)
	}
	if !haveFmt || data == nil {
		return nil, 0, errors.New("WAV missing fmt or data chunk")
	}
	if channels == 0 || rate == 0 {
		return nil, 0, errors.New("bad WAV header")
	}
	bps := int(bits) / 8
	if bps == 0 {
		return nil, 0, errors.New("bad bit depth")
	}
	frame := bps * int(channels)
	n := len(data) / frame
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		var sum float64
		for c := 0; c < int(channels); c++ {
			s := data[i*frame+c*bps:]
			var v float64
			switch {
			case format == 3 && bits == 32:
				v = float64(math.Float32frombits(binary.LittleEndian.Uint32(s)))
			case format == 3 && bits == 64:
				v = math.Float64frombits(binary.LittleEndian.Uint64(s))
			case format == 1 && bits == 8:
				v = (float64(s[0]) - 128) / 128
			case format == 1 && bits == 16:
				v = float64(int16(binary.LittleEndian.Uint16(s))) / 32768
			case format == 1 && bits == 24:
				x := int32(uint32(s[0])<<8|uint32(s[1])<<16|uint32(s[2])<<24) >> 8
				v = float64(x) / 8388608
			case format == 1 && bits == 32:
				v = float64(int32(binary.LittleEndian.Uint32(s))) / 2147483648
			default:
				return nil, 0, errors.New("unsupported WAV format (use PCM or float)")
			}
			sum += v
		}
		out[i] = float32(sum / float64(channels))
	}
	return out, rate, nil
}

// WriteWAV16 writes mono 16-bit PCM (used to make test files).
func WriteWAV16(w io.Writer, samples []float32, rate uint32) error {
	n := uint32(len(samples))
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], 36+n*2)
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1)
	binary.LittleEndian.PutUint16(hdr[22:], 1)
	binary.LittleEndian.PutUint32(hdr[24:], rate)
	binary.LittleEndian.PutUint32(hdr[28:], rate*2)
	binary.LittleEndian.PutUint16(hdr[32:], 2)
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], n*2)
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	buf := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(int16(clampF(s, -1, 1)*32767)))
	}
	_, err := w.Write(buf)
	return err
}

// Spectrum returns tone power at each frequency, summed over ~50 ms segments so each
// channel is ~20 Hz wide (a whole-buffer Goertzel would be far too narrow to land on a
// tone between steps).
func Spectrum(samples []float32, sr float32, freqs []float32) []float32 {
	seg := int(sr * 0.05)
	if seg < 16 {
		seg = 16
	}
	out := make([]float32, len(freqs))
	for i := 0; i+seg <= len(samples); i += seg {
		for k, f := range freqs {
			out[k] += TonePower(samples[i:i+seg], sr, f)
		}
	}
	return out
}

// FindPitch returns the strongest tone between lo and hi Hz (step Hz) in samples,
// and its power relative to the median channel. Used for auto-tune.
func FindPitch(samples []float32, sr float32, lo, hi, step float32) (pitch float32, ratio float32) {
	var fs []float32
	for f := lo; f <= hi; f += step {
		fs = append(fs, f)
	}
	ps := Spectrum(samples, sr, fs)
	if len(ps) == 0 {
		return 0, 0
	}
	best := 0
	for i := range ps {
		if ps[i] > ps[best] {
			best = i
		}
	}
	sorted := append([]float32(nil), ps...)
	for i := 1; i < len(sorted); i++ { // tiny insertion sort; few dozen entries
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	med := max32(sorted[len(sorted)/2], 1e-12)
	return fs[best], ps[best] / med
}
