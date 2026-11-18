package ft8

import "math"

// Costas is the 7-tone FT8 synchronization pattern, placed at symbols
// 0–6, 36–42 and 72–78.
var Costas = [7]byte{3, 1, 4, 0, 6, 5, 2}

// Gray maps a 3-bit group onto an 8-FSK tone.
var Gray = [8]byte{0, 1, 3, 2, 5, 6, 4, 7}

const (
	symbolPeriod = 0.16 // seconds
	toneSpacing  = 6.25 // Hz; modulation index 1 at this symbol rate
	crcPoly      = 0x2757
	crcWidth     = 14
)

// Symbols is the 79-tone sequence for msg, as digits.
func Symbols(msg string) (string, error) {
	p, err := Pack(msg)
	if err != nil {
		return "", err
	}
	t := Tones(p)
	b := make([]byte, len(t))
	for i, s := range t {
		b[i] = '0' + s
	}
	return string(b), nil
}

// Wave is the GFSK audio for msg. freqHz is the audio frequency of tone 0.
// sampleRate is the device rate; 48000 is the usual one. The result is one
// 12.64 s transmission (79 symbols of 0.16 s) at unit amplitude.
func Wave(msg string, freqHz, sampleRate int) ([]float32, error) {
	p, err := Pack(msg)
	if err != nil {
		return nil, err
	}
	if sampleRate <= 0 {
		sampleRate = 48000
	}
	if freqHz < 0 {
		freqHz = 0
	}
	return gfsk(Tones(p), float64(freqHz), sampleRate), nil
}

// Tones is the 79 channel symbols for a 77-bit payload: CRC-14, the (174,91)
// LDPC code, Gray-mapped 8-FSK, and the Costas arrays.
func Tones(payload []byte) [79]byte {
	a91 := addCRC(payload)
	code := encode174(a91)
	var tones [79]byte
	mask := byte(0x80)
	iByte := 0
	for i := 0; i < 79; i++ {
		switch {
		case i < 7:
			tones[i] = Costas[i]
		case i >= 36 && i < 43:
			tones[i] = Costas[i-36]
		case i >= 72:
			tones[i] = Costas[i-72]
		default:
			bits := byte(0)
			if code[iByte]&mask != 0 {
				bits |= 4
			}
			mask >>= 1
			if mask == 0 {
				mask = 0x80
				iByte++
			}
			if code[iByte]&mask != 0 {
				bits |= 2
			}
			mask >>= 1
			if mask == 0 {
				mask = 0x80
				iByte++
			}
			if code[iByte]&mask != 0 {
				bits |= 1
			}
			mask >>= 1
			if mask == 0 {
				mask = 0x80
				iByte++
			}
			tones[i] = Gray[bits]
		}
	}
	return tones
}

func addCRC(payload []byte) []byte {
	a := make([]byte, 12)
	n := len(payload)
	if n > 10 {
		n = 10
	}
	copy(a, payload[:n])
	a[9] &= 0xF8
	a[10] = 0
	sum := crc14(a, 82)
	a[9] |= byte(sum >> 11)
	a[10] = byte(sum >> 3)
	a[11] = byte(sum << 5)
	return a
}

func crc14(message []byte, numBits int) uint16 {
	const top = uint16(1) << (crcWidth - 1)
	var rem uint16
	idx := 0
	for bit := 0; bit < numBits; bit++ {
		if bit%8 == 0 {
			rem ^= uint16(message[idx]) << (crcWidth - 8)
			idx++
		}
		if rem&top != 0 {
			rem = (rem << 1) ^ crcPoly
		} else {
			rem <<= 1
		}
	}
	return rem & ((top << 1) - 1)
}

func parity8(x byte) byte {
	x ^= x >> 4
	x ^= x >> 2
	x ^= x >> 1
	return x & 1
}

func encode174(message []byte) []byte {
	const kBytes = 12
	code := make([]byte, 22)
	copy(code, message[:kBytes])
	colMask := byte(0x80 >> (91 % 8))
	col := kBytes - 1
	for i := 0; i < 83; i++ {
		var sum byte
		for j := 0; j < kBytes; j++ {
			sum ^= parity8(message[j] & ldpcGen[i][j])
		}
		if sum&1 != 0 {
			code[col] |= colMask
		}
		colMask >>= 1
		if colMask == 0 {
			colMask = 0x80
			col++
		}
	}
	return code
}

// gfsk is the FT8 Gaussian-shaped 8-FSK waveform: BT = 2, tone spacing
// 6.25 Hz, symbol 0 at f0. The first and last symbols are extended so the
// pulse has somewhere to land, then a raised-cosine ramp covers the first
// and last eighth of a symbol.
func gfsk(tones [79]byte, f0 float64, sampleRate int) []float32 {
	nSym := len(tones)
	nPer := int(float32(0.5) + float32(sampleRate)*float32(symbolPeriod))
	nWave := nSym * nPer
	dphiPeak := float32(2*math.Pi) / float32(nPer)
	base := float32(2*math.Pi*f0) / float32(sampleRate)
	dphi := make([]float32, nWave+2*nPer)
	for i := range dphi {
		dphi[i] = base
	}
	pulse := gfskPulse(nPer)
	for i, tone := range tones {
		ib := i * nPer
		scale := dphiPeak * float32(tone)
		for j, p := range pulse {
			dphi[ib+j] += scale * p
		}
	}
	for j := 0; j < 2*nPer; j++ {
		dphi[j] += dphiPeak * pulse[j+nPer] * float32(tones[0])
		dphi[j+nWave] += dphiPeak * pulse[j] * float32(tones[nSym-1])
	}
	out := make([]float32, nWave)
	var phi float32
	twoPi := float32(2 * math.Pi)
	for k := 0; k < nWave; k++ {
		out[k] = sin32(phi)
		phi += dphi[k+nPer]
		if phi >= twoPi || phi < 0 {
			phi -= twoPi * float32(math.Floor(float64(phi)/float64(twoPi)))
		}
	}
	nRamp := nPer / 8
	for i := 0; i < nRamp; i++ {
		env := (1 - cos32(twoPi*float32(i)/float32(2*nRamp))) / 2
		out[i] *= env
		out[nWave-1-i] *= env
	}
	return out
}

func gfskPulse(nPer int) []float32 {
	// pi * sqrt(2 / ln 2), the constant in the FT8 Gaussian pulse (BT = 2).
	gk := float32(math.Pi * math.Sqrt(2/math.Ln2))
	pulse := make([]float32, 3*nPer)
	nf := float32(nPer)
	for i := range pulse {
		t := float32(i)/nf - 1.5
		pulse[i] = (erf32(gk*2*(t+0.5)) - erf32(gk*2*(t-0.5))) / 2
	}
	return pulse
}

func sin32(x float32) float32 { return float32(math.Sin(float64(x))) }
func cos32(x float32) float32 { return float32(math.Cos(float64(x))) }
func erf32(x float32) float32 { return float32(math.Erf(float64(x))) }
