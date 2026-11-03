package cw

import (
	"bufio"
	"strings"
)

// Macro is one F-key: a short button label and the template to send.
type Macro struct {
	Label, Text string
}

// DefaultMacrosText is the starter macros.txt (shared by the Windows and Mac apps).
const DefaultMacrosText = `# CW Decoder macros. Edit, save, and the buttons update right away.
#
#   F1 = Label | text to send
#
# Tokens: {MYCALL} {NAME} {MYSTATE} {MYGRID}, {RST} (sent as cut numbers: 599 -> 5NN),
# {CALL} or ! = the call in the Call box. Any other KEY = value line below
# (e.g. RIG = FT-710) can be used as {RIG}. {PARK} is the park set on the main screen
# (Park, next to the contact count); PARK here is only used when that is empty.
# {LOG} logs the contact (the call in the Call box) when the macro is sent.
# Tip: end with "E E" (two separate dits), not "EE" (that's an I).

MYCALL  = KN0BLE
NAME    = JOSH
MYSTATE = FL
MYGRID  =
RST     = 599
PARK    =

F1 = CQ      | CQ CQ DE {MYCALL} {MYCALL} K
F2 = CQ POTA | CQ POTA DE {MYCALL} {MYCALL} K
F3 = Exch    | {CALL} {RST} {RST} {MYSTATE} {MYSTATE} BK
F4 = TU      | TU 73 E E {LOG}
F5 = QRZ?    | QRZ?
F6 = AGN?    | AGN?
F7 = My call | {MYCALL}
F8 = 73      | {CALL} TU 73 DE {MYCALL} E E {LOG}
`

// ParseMacros reads the macros.txt format.
func ParseMacros(text string) (MacroContext, [8]Macro) {
	ctx := MacroContext{Extra: map[string]string{}}
	var ms [8]Macro
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.ToUpper(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if len(k) == 2 && k[0] == 'F' && k[1] >= '1' && k[1] <= '8' {
			label, body, has := strings.Cut(v, "|")
			if !has {
				body, label = v, v
			}
			label, body = strings.TrimSpace(label), strings.ToUpper(strings.TrimSpace(body))
			if len(label) > 10 {
				label = label[:10]
			}
			ms[k[1]-'1'] = Macro{label, body}
			continue
		}
		v = strings.ToUpper(v)
		switch k {
		case "MYCALL":
			ctx.MyCall = v
		case "NAME":
			ctx.MyName = v
		case "MYSTATE":
			ctx.MyState = v
		case "MYGRID":
			ctx.MyGrid = v
		case "RST":
			ctx.RST = v
		default:
			ctx.Extra[k] = v
		}
	}
	return ctx, ms
}
