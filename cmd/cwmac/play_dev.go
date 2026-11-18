package main

import (
	"strconv"
	"strings"
)

// outputDev is one playback device the operator can pick for FT8 transmit.
type outputDev struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// pickOutput chooses a device from names. -1 means the system default.
// want is "wave:N", a device number, or part of a device name (the radio's
// USB playback device, often the same words as its recording device).
func pickOutput(names []string, want string) int {
	want = strings.TrimSpace(want)
	if want == "" || strings.EqualFold(want, "default") {
		return -1
	}
	low := strings.ToLower(want)
	if strings.HasPrefix(low, "wave:") {
		if n, err := strconv.Atoi(want[5:]); err == nil && n >= 0 && n < len(names) {
			return n
		}
	}
	if n, err := strconv.Atoi(want); err == nil && n >= 0 && n < len(names) {
		return n
	}
	best, score := -1, 0
	wantWords := outputWords(low)
	for i, name := range names {
		n := strings.ToLower(name)
		if n == low || strings.Contains(n, low) || strings.Contains(low, n) {
			return i
		}
		s := 0
		for _, w := range wantWords {
			if len(w) >= 4 && strings.Contains(n, w) {
				s += len(w)
			}
		}
		if s > score {
			best, score = i, s
		}
	}
	return best
}

func outputWords(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
}
