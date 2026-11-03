// Package ops holds the operating aids shared by the Windows and Mac apps: callsign
// capture, RST capture, Auto-CQ with an optional auto-reply, matching the other
// station's speed, and an ADIF log that POTA accepts.
package ops

import (
	"regexp"
	"strings"
)

// A callsign: optional "PFX/" + prefix (1-2 letters, letter+digit or digit+letter),
// one digit, 1-4 letters, optional "/P", "/QRP", "/4" etc.
var callRe = regexp.MustCompile(`^(?:[A-Z0-9]{1,4}/)?(?:[A-Z]{1,2}|[A-Z][0-9]|[0-9][A-Z])[0-9][A-Z]{1,4}(?:/[A-Z0-9]{1,4})?$`)

// IsCall reports whether tok looks like an amateur callsign (4+ characters, so
// short decode fragments don't count).
func IsCall(tok string) bool {
	tok = strings.ToUpper(strings.TrimSpace(tok))
	if len(tok) < 4 || len(tok) > 14 || !callRe.MatchString(tok) {
		return false
	}
	// "TU5NN", "R599" and friends are an exchange with the spaces lost, not a call.
	core := baseCall(tok)
	for _, bad := range []string{"5NN", "599", "55N", "57N", "58N"} {
		if strings.Contains(core, bad) {
			return false
		}
	}
	return true
}

// baseCall strips a portable prefix or suffix ("VE3/W1AW/P" -> "W1AW").
func baseCall(c string) string {
	parts := strings.Split(c, "/")
	best := ""
	for _, p := range parts {
		if len(p) > len(best) {
			best = p
		}
	}
	return best
}

// SameStation: two calls are the same operator if their base calls match.
func SameStation(a, b string) bool {
	return a != "" && b != "" && baseCall(strings.ToUpper(a)) == baseCall(strings.ToUpper(b))
}

// FindCalls returns the distinct callsigns in text, in order of first appearance,
// leaving out the operator's own call.
func FindCalls(text, myCall string) []string {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.Fields(strings.ToUpper(text)) {
		tok = strings.Trim(tok, "?.,=+")
		if h := len(tok) / 2; len(tok)%2 == 0 && h >= 4 && tok[:h] == tok[h:] {
			tok = tok[:h] // "K4XYZK4XYZ": a call sent twice with the space lost
		}
		if !IsCall(tok) || SameStation(tok, myCall) || seen[tok] {
			continue
		}
		seen[tok] = true
		out = append(out, tok)
	}
	return out
}

// distinctStations groups calls that are the same operator (W1AW and W1AW/P).
func distinctStations(calls []string) []string {
	var out []string
	for _, c := range calls {
		dup := false
		for i, o := range out {
			if SameStation(c, o) {
				dup = true
				if len(c) > len(o) { // keep the fuller form (with /P etc.)
					out[i] = c
				}
				break
			}
		}
		if !dup {
			out = append(out, c)
		}
	}
	return out
}

// FindRST returns the last signal report in text ("5NN" -> "599"), or "".
func FindRST(text string) string {
	toks := strings.Fields(strings.ToUpper(text))
	for i := len(toks) - 1; i >= 0; i-- {
		t := strings.Trim(toks[i], "?.,=+")
		if len(t) != 3 || t[0] < '1' || t[0] > '5' {
			continue
		}
		ok := true
		b := []byte(t)
		for j := 1; j < 3; j++ {
			switch {
			case b[j] == 'N':
				b[j] = '9'
			case b[j] >= '1' && b[j] <= '9':
			default:
				ok = false
			}
		}
		if ok {
			return string(b)
		}
	}
	return ""
}

// StripLog removes the {LOG} token from a macro and reports whether it was there.
func StripLog(macro string) (string, bool) {
	up := strings.ToUpper(macro)
	if !strings.Contains(up, "{LOG}") {
		return macro, false
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(up, "{LOG}", " ")), " "), true
}

// MigrateMacros adds {LOG} to the TU and 73 macros if they are still the original
// defaults, so existing macros files pick up automatic logging. Returns the new text
// and whether anything changed.
func MigrateMacros(text string) (string, bool) {
	if strings.Contains(strings.ToUpper(text), "{LOG}") {
		return text, false
	}
	changed := false
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		trim := strings.TrimRight(l, "\r")
		cr := l[len(trim):]
		k, v, ok := strings.Cut(trim, "=")
		if !ok {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(k))
		label, body, has := strings.Cut(v, "|")
		if !has {
			continue
		}
		b := strings.Join(strings.Fields(body), " ")
		if (key == "F4" && b == "TU 73 E E") || (key == "F8" && b == "{CALL} TU 73 DE {MYCALL} E E") {
			lines[i] = k + "=" + label + "| " + b + " {LOG}" + cr
			changed = true
		}
	}
	if !changed {
		return text, false
	}
	cr := ""
	if strings.Contains(text, "\r\n") {
		cr = "\r"
	}
	for i, l := range lines { // explain the new token just above the settings
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(l)), "MYCALL") {
			note := []string{"# {LOG} in a macro logs the contact (the call in the Call box) when you send it." + cr, cr}
			lines = append(lines[:i], append(note, lines[i:]...)...)
			break
		}
	}
	return strings.Join(lines, "\n"), true
}
