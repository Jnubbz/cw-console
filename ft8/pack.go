package ft8

import (
	"fmt"
	"strings"
)

// A standard FT8 message is 77 bits: two 29-bit call tokens, a 16-bit grid or
// report (the top bit is the roger flag), and a 3-bit type. Free text is 71
// bits of a 13-character string and a zero type code.

const (
	nTokens = 2063592
	max22   = 4194304
	maxGrid = 32400
)

// Pack turns an operator message into the 77-bit payload (10 bytes, the low
// bits of the last byte unused). It accepts CQ, standard call/call/grid,
// signal reports, RRR, RR73, 73, and free text of up to 13 characters.
func Pack(message string) ([]byte, error) {
	text := strings.ToUpper(strings.TrimSpace(message))
	if text == "" {
		return nil, fmt.Errorf("empty FT8 message")
	}
	to, de, extra, rest, err := splitMessage(text)
	if err != nil {
		return nil, err
	}
	var payload []byte
	if rest == "" {
		if p, ok := packStd(to, de, extra); ok {
			payload = p
		} else if p, ok = packNonstd(to, de, extra); ok {
			payload = p
		}
	}
	if payload == nil {
		payload, err = packFree(text)
		if err != nil {
			return nil, fmt.Errorf("can't encode FT8 message %q", message)
		}
	}
	return payload, nil
}

func splitMessage(text string) (to, de, extra, rest string, err error) {
	pos := text
	if strings.HasPrefix(text, "CQ ") {
		pos = text[3:]
		if cqMod(text) >= 0 {
			var tok string
			tok, pos = nextToken(pos)
			to = "CQ " + tok
		} else {
			to = "CQ"
		}
	} else {
		to, pos = nextToken(pos)
	}
	de, pos = nextToken(pos)
	extra, rest = nextToken(pos)
	if len(to) > 11 {
		return "", "", "", "", fmt.Errorf("callsign %q is too long", to)
	}
	if len(de) > 11 {
		return "", "", "", "", fmt.Errorf("callsign %q is too long", de)
	}
	if len(extra) > 19 {
		return "", "", "", "", fmt.Errorf("grid or report %q is too long", extra)
	}
	return to, de, extra, strings.TrimSpace(rest), nil
}

func nextToken(s string) (string, string) {
	s = strings.TrimLeft(s, " ")
	if s == "" {
		return "", ""
	}
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func packStd(to, de, extra string) ([]byte, bool) {
	ipa, ipb := byte(0), byte(0)
	n28a, ok := pack28(to, &ipa)
	if !ok {
		return nil, false
	}
	n28b, ok := pack28(de, &ipb)
	if !ok {
		return nil, false
	}
	i3 := byte(1)
	if strings.HasSuffix(to, "/P") || strings.HasSuffix(de, "/P") {
		i3 = 2
		if strings.HasSuffix(to, "/R") || strings.HasSuffix(de, "/R") {
			return nil, false
		}
	}
	icq := to == "CQ" || strings.HasPrefix(to, "CQ ")
	if i := strings.IndexByte(de, '/'); i >= 2 && icq && de[i:] != "/P" && de[i:] != "/R" {
		return nil, false
	}
	grid, ok := packGrid(extra)
	if !ok {
		return nil, false
	}
	n29a := (uint32(n28a) << 1) | uint32(ipa)
	n29b := (uint32(n28b) << 1) | uint32(ipb)
	if strings.HasSuffix(to, "/R") || strings.HasSuffix(to, "/P") {
		n29a |= 1
		if strings.HasSuffix(to, "/P") {
			i3 = 2
		}
	}
	var p [10]byte
	p[0] = byte(n29a >> 21)
	p[1] = byte(n29a >> 13)
	p[2] = byte(n29a >> 5)
	p[3] = byte(n29a<<3) | byte(n29b>>26)
	p[4] = byte(n29b >> 18)
	p[5] = byte(n29b >> 10)
	p[6] = byte(n29b >> 2)
	p[7] = byte(n29b<<6) | byte(grid>>10)
	p[8] = byte(grid >> 2)
	p[9] = byte(grid<<6) | byte(i3<<3)
	return p[:], true
}

func pack28(call string, ip *byte) (uint32, bool) {
	*ip = 0
	switch call {
	case "DE":
		return 0, true
	case "QRZ":
		return 1, true
	case "CQ":
		return 2, true
	}
	if strings.HasPrefix(call, "CQ ") && len(call) < 8 {
		v := cqMod(call)
		if v < 0 {
			return 0, false
		}
		return uint32(3 + v), true
	}
	base := call
	if strings.HasSuffix(call, "/P") || strings.HasSuffix(call, "/R") {
		*ip = 1
		base = call[:len(call)-2]
	}
	if n, ok := packBase(base); ok {
		if _, ok := hash22(call); !ok {
			return 0, false
		}
		return nTokens + max22 + uint32(n), true
	}
	if len(call) >= 3 && len(call) <= 11 {
		n22, ok := hash22(call)
		if !ok {
			return 0, false
		}
		*ip = 0
		return nTokens + n22, true
	}
	return 0, false
}

func packBase(call string) (int32, bool) {
	if len(call) <= 2 {
		return 0, false
	}
	var c6 [6]byte
	for i := range c6 {
		c6[i] = ' '
	}
	switch {
	case strings.HasPrefix(call, "3DA0") && len(call) > 4 && len(call) <= 7:
		copy(c6[:], "3D0")
		copy(c6[3:], call[4:])
	case strings.HasPrefix(call, "3X") && len(call) >= 3 && isLetter(call[2]) && len(call) <= 7:
		c6[0] = 'Q'
		copy(c6[1:], call[2:])
	case len(call) >= 3 && isDigit(call[2]) && len(call) <= 6:
		copy(c6[:], call)
	case len(call) >= 2 && isDigit(call[1]) && len(call) <= 5:
		copy(c6[1:], call)
	default:
		return 0, false
	}
	i0 := nchar(c6[0], tabSpace)
	i1 := nchar(c6[1], tabAlnum)
	i2 := nchar(c6[2], tabDigit)
	i3 := nchar(c6[3], tabLetter)
	i4 := nchar(c6[4], tabLetter)
	i5 := nchar(c6[5], tabLetter)
	if i0 < 0 || i1 < 0 || i2 < 0 || i3 < 0 || i4 < 0 || i5 < 0 {
		return 0, false
	}
	n := int32(i0)
	n = n*36 + int32(i1)
	n = n*10 + int32(i2)
	n = n*27 + int32(i3)
	n = n*27 + int32(i4)
	n = n*27 + int32(i5)
	return n, true
}

// cqMod is the number for CQ nnn or CQ abcd, or -1.
func cqMod(s string) int {
	nnum, nlet, m := 0, 0, 0
	for i := 3; i < 8 && i < len(s); i++ {
		switch {
		case s[i] == ' ':
			i = 8
		case isDigit(s[i]):
			nnum++
		case isLetter(s[i]):
			nlet++
			m = 27*m + int(s[i]-'A'+1)
		default:
			return -1
		}
	}
	if nnum == 3 && nlet == 0 {
		return int(s[3]-'0')*100 + int(s[4]-'0')*10 + int(s[5]-'0')
	}
	if nnum == 0 && nlet > 0 && nlet <= 4 {
		return 1000 + m
	}
	return -1
}

func packGrid(extra string) (uint16, bool) {
	if extra == "" {
		return maxGrid + 1, true
	}
	switch extra {
	case "RRR":
		return maxGrid + 2, true
	case "RR73":
		return maxGrid + 3, true
	case "73":
		return maxGrid + 4, true
	}
	if grid, ok := maiden4(extra); ok {
		return grid, true
	}
	roger := false
	rpt := extra
	if strings.HasPrefix(rpt, "R") {
		roger = true
		rpt = rpt[1:]
	}
	dd, ok := report(rpt)
	if !ok {
		return 0, false
	}
	g := uint16(maxGrid + 35 + dd)
	if roger {
		g |= 0x8000
	}
	return g, true
}

func maiden4(s string) (uint16, bool) {
	if len(s) != 4 || s[0] < 'A' || s[0] > 'R' || s[1] < 'A' || s[1] > 'R' || !isDigit(s[2]) || !isDigit(s[3]) {
		return 0, false
	}
	g := uint16(s[0] - 'A')
	g = g*18 + uint16(s[1]-'A')
	g = g*10 + uint16(s[2]-'0')
	g = g*10 + uint16(s[3]-'0')
	return g, true
}

func report(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	neg := false
	i := 0
	switch s[0] {
	case '-':
		neg = true
		i++
	case '+':
		i++
	}
	if i >= len(s) || len(s)-i > 2 {
		return 0, false
	}
	n := 0
	for ; i < len(s); i++ {
		if !isDigit(s[i]) {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	if neg {
		n = -n
	}
	if n < -50 || n > 49 {
		return 0, false
	}
	return n, true
}

func packNonstd(to, de, extra string) ([]byte, bool) {
	icq := byte(0)
	if to == "CQ" || strings.HasPrefix(to, "CQ ") {
		icq = 1
	}
	if icq == 0 && len(to) < 3 {
		return nil, false
	}
	if len(de) < 3 {
		return nil, false
	}
	iflip := byte(0)
	var n12 uint32
	call58 := de
	if icq == 0 {
		if len(de) > 0 && de[0] == '<' && to != "" && to[len(to)-1] == '>' {
			iflip = 1
		}
		call12 := to
		if iflip == 1 {
			call12, call58 = de, to
		}
		h, ok := hash22(strings.Trim(call12, "<>"))
		if !ok {
			return nil, false
		}
		n12 = h >> 10
	}
	n58, ok := pack58(call58)
	if !ok {
		return nil, false
	}
	nrpt := byte(0)
	if icq == 0 {
		switch extra {
		case "RRR":
			nrpt = 1
		case "RR73":
			nrpt = 2
		case "73":
			nrpt = 3
		}
	}
	const i3 = byte(4)
	var p [10]byte
	p[0] = byte(n12 >> 4)
	p[1] = byte(n12<<4) | byte(n58>>54)
	p[2] = byte(n58 >> 46)
	p[3] = byte(n58 >> 38)
	p[4] = byte(n58 >> 30)
	p[5] = byte(n58 >> 22)
	p[6] = byte(n58 >> 14)
	p[7] = byte(n58 >> 6)
	p[8] = byte(n58<<2) | byte(iflip<<1) | byte(nrpt>>1)
	p[9] = byte(nrpt<<7) | byte(icq<<6) | byte(i3<<3)
	return p[:], true
}

func pack58(call string) (uint64, bool) {
	src := strings.TrimPrefix(call, "<")
	var n uint64
	plain := make([]byte, 0, 11)
	for i := 0; i < len(src) && src[i] != '<' && len(plain) < 11; i++ {
		j := nchar(src[i], tabSlash)
		if j < 0 {
			return 0, false
		}
		n = n*38 + uint64(j)
		plain = append(plain, src[i])
	}
	if _, ok := hash22(string(plain)); !ok {
		return 0, false
	}
	return n, true
}

func hash22(call string) (uint32, bool) {
	var n58 uint64
	i := 0
	for ; i < len(call) && i < 11; i++ {
		j := nchar(call[i], tabSlash)
		if j < 0 {
			return 0, false
		}
		n58 = 38*n58 + uint64(j)
	}
	for ; i < 11; i++ {
		n58 *= 38
	}
	return uint32((47055833459*n58)>>42) & 0x3FFFFF, true
}

func packFree(text string) ([]byte, error) {
	if len(text) > 13 {
		return nil, fmt.Errorf("free text longer than 13 characters")
	}
	var b71 [9]byte
	for idx := 0; idx < 13; idx++ {
		c := byte(' ')
		if idx < len(text) {
			c = text[idx]
		}
		cid := nchar(c, tabFull)
		if cid < 0 {
			return nil, fmt.Errorf("character %q is not in the FT8 free-text set", c)
		}
		rem := uint16(cid)
		for i := 8; i >= 0; i-- {
			rem += uint16(b71[i]) * 42
			b71[i] = byte(rem)
			rem >>= 8
		}
	}
	var p [10]byte
	carry := byte(0)
	for i := 8; i >= 0; i-- {
		p[i] = (b71[i] << 1) | (carry >> 7)
		carry = b71[i] & 0x80
	}
	return p[:], nil
}

const (
	tabSpace = iota
	tabAlnum
	tabDigit
	tabLetter
	tabFull
	tabSlash
)

func nchar(c byte, table int) int {
	n := 0
	if table != tabAlnum && table != tabDigit {
		if c == ' ' {
			return 0
		}
		n++
	}
	if table != tabLetter {
		if c >= '0' && c <= '9' {
			return n + int(c-'0')
		}
		n += 10
	}
	if table != tabDigit {
		if c >= 'A' && c <= 'Z' {
			return n + int(c-'A')
		}
		n += 26
	}
	switch table {
	case tabFull:
		switch c {
		case '+':
			return n
		case '-':
			return n + 1
		case '.':
			return n + 2
		case '/':
			return n + 3
		case '?':
			return n + 4
		}
	case tabSlash:
		if c == '/' {
			return n
		}
	}
	return -1
}

func isDigit(c byte) bool  { return c >= '0' && c <= '9' }
func isLetter(c byte) bool { return c >= 'A' && c <= 'Z' }
