// Package cw is a CW (Morse) decoder ported line-for-line from Nexus by KD9TAW
// (https://github.com/kd9taw/nexus, GPL-3.0-only): crates/tempo-core/src/cw.rs,
// cw_decode.rs and spectrum.rs (goertzel). Same algorithm, same constants.
package cw

import (
	"math"
	"strings"
	"unicode"
)

var morseTable = map[rune]string{
	'a': ".-", 'b': "-...", 'c': "-.-.", 'd': "-..", 'e': ".", 'f': "..-.",
	'g': "--.", 'h': "....", 'i': "..", 'j': ".---", 'k': "-.-", 'l': ".-..",
	'm': "--", 'n': "-.", 'o': "---", 'p': ".--.", 'q': "--.-", 'r': ".-.",
	's': "...", 't': "-", 'u': "..-", 'v': "...-", 'w': ".--", 'x': "-..-",
	'y': "-.--", 'z': "--..",
	'0': "-----", '1': ".----", '2': "..---", '3': "...--", '4': "....-",
	'5': ".....", '6': "-....", '7': "--...", '8': "---..", '9': "----.",
	'.': ".-.-.-", ',': "--..--", '?': "..--..", '/': "-..-.", '=': "-...-",
	'+': ".-.-.", '-': "-....-", '(': "-.--.", ')': "-.--.-", ':': "---...",
	';': "-.-.-.", '@': ".--.-.",
}

// MorseCode returns the dit/dah string for ch (case-insensitive), or "" and false.
func MorseCode(ch rune) (string, bool) {
	c, ok := morseTable[unicode.ToLower(ch)]
	return c, ok
}

var reverseTable = func() map[string]rune {
	m := map[string]rune{}
	// Same construction as Nexus: walk printable ASCII uppercased, first glyph wins.
	for u := 0x20; u < 0x7f; u++ {
		c := unicode.ToUpper(rune(u))
		if code, ok := MorseCode(c); ok {
			if _, exists := m[code]; !exists {
				m[code] = c
			}
		}
	}
	return m
}()

func morseToChar(code string) (rune, bool) {
	c, ok := reverseTable[code]
	return c, ok
}

// ToneMorse returns the Morse for text, characters separated by spaces and words by " / ".
func ToneMorse(text string) string {
	var words []string
	for _, w := range strings.Fields(text) {
		var cs []string
		for _, ch := range w {
			if c, ok := MorseCode(ch); ok {
				cs = append(cs, c)
			}
		}
		words = append(words, strings.Join(cs, " "))
	}
	return strings.Join(words, " / ")
}

func goertzel(samples []float32, sr, f float32) float32 {
	if len(samples) == 0 {
		return 0
	}
	w := 2 * float32(math.Pi) * f / sr
	coeff := 2 * float32(math.Cos(float64(w)))
	var s1, s2 float32
	for _, x := range samples {
		s0 := x + coeff*s1 - s2
		s2 = s1
		s1 = s0
	}
	p := s1*s1 + s2*s2 - coeff*s1*s2
	if p < 0 {
		return 0
	}
	return p
}

// TonePower is the raw Goertzel power at frequency f (Hz).
func TonePower(samples []float32, sr, f float32) float32 { return goertzel(samples, sr, f) }

// MorseSamples generates keyed-tone PCM for text (PARIS timing, ~5 ms raised-cosine edges).
func MorseSamples(text string, wpm uint32, pitchHz float32, sampleRate uint32) []float32 {
	if wpm < 5 {
		wpm = 5
	} else if wpm > 60 {
		wpm = 60
	}
	if sampleRate < 1 {
		sampleRate = 1
	}
	sr := float32(sampleRate)
	ditN := int(max32((1.2/float32(wpm))*sr, 1))
	rampN := int(0.005 * sr)
	if rampN < 1 {
		rampN = 1
	}
	if rampN > ditN/2+1 {
		rampN = ditN/2 + 1
	}
	step := 2 * float32(math.Pi) * pitchHz / sr
	var out []float32
	var phase float32
	for wi, word := range strings.Fields(text) {
		if wi > 0 {
			out = appendGap(out, ditN*7)
		}
		ci := 0
		for _, ch := range word {
			code, ok := MorseCode(ch)
			if !ok {
				ci++
				continue
			}
			if ci > 0 {
				out = appendGap(out, ditN*3)
			}
			ci++
			for ei, el := range code {
				if ei > 0 {
					out = appendGap(out, ditN)
				}
				d := 1
				if el == '-' {
					d = 3
				}
				out = appendTone(out, ditN*d, rampN, step, &phase)
			}
		}
	}
	return out
}

func appendGap(out []float32, n int) []float32 {
	for i := 0; i < n; i++ {
		out = append(out, 0)
	}
	return out
}

func appendTone(out []float32, n, rampN int, step float32, phase *float32) []float32 {
	for i := 0; i < n; i++ {
		env := float32(1)
		if i < rampN {
			env = 0.5 - 0.5*float32(math.Cos(math.Pi*float64(i)/float64(rampN)))
		} else if i+rampN >= n {
			env = 0.5 - 0.5*float32(math.Cos(math.Pi*float64(n-1-i)/float64(rampN)))
		}
		out = append(out, env*float32(math.Sin(float64(*phase))))
		*phase += step
	}
	return out
}

func max32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b float32) float32 {
	if a < b {
		return a
	}
	return b
}
