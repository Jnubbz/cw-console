//go:build windows

package main

// The sender: free-text type-ahead, F1-F8 macros, speed, Esc to stop, keyed through a
// COM port's DTR/RTS line. Everything here runs on the UI thread; the keyer has its own
// timing goroutine and is fed one character at a time when it asks (true type-ahead:
// text not yet taken can still be edited).

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/Jnubbz/cw-console/cw"
	"github.com/Jnubbz/cw-console/ops"
)

const (
	idCall = 301 + iota
	idTxInput
	idWPM
	idTxPort
	idTxLine
	idTxEnable
	idTxMode
	idTxMacros
	idTxStop
	idTxShow
	idMacro0 // ..idMacro0+7
)

const (
	vkReturn      = 0x0D
	vkEscape      = 0x1B
	vkF1          = 0x70
	wmKeyDown     = 0x0100
	esUppercase   = 0x0008
	esAutoHScroll = 0x0080
	swHide        = 0
	swShowNormal  = 1
	txMuteHang    = 400 * time.Millisecond // decoders stay muted this long after key-up
)

var (
	pGetFocus             = user32.NewProc("GetFocus")
	pGetTextExtentPoint32 = gdi32.NewProc("GetTextExtentPoint32W")
	pShowWindowCtl        = user32.NewProc("ShowWindow")
	pShellExecuteW        = shell32.NewProc("ShellExecuteW")
	pGetSystemMetrics     = user32.NewProc("GetSystemMetrics")
)

type macro struct {
	label, text string
}

type txState struct {
	strip                                            RECT
	lblCall, edCall, edInput, lblWPM, trkWPM, valWPM uintptr
	btnMacro                                         [8]uintptr
	lblPort, cmbPort, cmbLine, chkEnable, cmbMode    uintptr
	btnMacros, btnStop, chkShow                      uintptr
	fontStrip                                        uintptr
	brStrip, brTX                                    uintptr
	charW                                            int32

	visible     bool
	ports       []comPort
	portName    string
	useRTS      bool
	wpm         int
	sendOnEnter bool

	line     *serialLine
	keyer    *cw.Keyer
	queue    []rune // committed text not yet fed (Send on Enter mode, macros)
	sentTail []rune
	inTXLine bool

	macros     [8]macro
	ctx        cw.MacroContext
	macroMod   time.Time
	macroCheck time.Time
	muteBuf    []float32

	settingInput bool // setInput is changing the box (not the operator typing)
}

var TX = txState{visible: true, wpm: 20}

func macrosPath() string {
	p := settingsPath()
	if p == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "macros.txt")
}

// loadMacros reads macros.txt (creating it with defaults the first time; adding {LOG}
// to the TU / 73 macros of an older file).
func loadMacros() {
	p := macrosPath()
	if p == "" {
		return
	}
	if _, err := os.Stat(p); err != nil {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(strings.ReplaceAll(cw.DefaultMacrosText, "\n", "\r\n")), 0o644)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	if m, changed := ops.MigrateMacros(string(b)); changed {
		if os.WriteFile(p, []byte(m), 0o644) == nil {
			b = []byte(m)
		}
	}
	st, err := os.Stat(p)
	if err != nil {
		return
	}
	ctx, parsed := cw.ParseMacros(string(b))
	var ms [8]macro
	for i, m := range parsed {
		ms[i] = macro{m.Label, m.Text}
	}
	TX.macros, TX.ctx, TX.macroMod = ms, ctx, st.ModTime()
	for i, b := range TX.btnMacro {
		if b != 0 {
			label := fmt.Sprintf("F%d", i+1)
			if ms[i].label != "" {
				label += " " + ms[i].label
			}
			setText(b, label)
		}
	}
	fillMacroCombos()
	if OPS.btnLog != 0 {
		opsRefreshLog() // PARK may have changed
	}
}

func createTXControls() {
	TX.fontStrip = makeFont(px(17), 400, "Consolas")
	TX.brStrip = brush(rgb(20, 24, 30))
	TX.brTX = brush(rgb(230, 50, 40))
	TX.lblCall = child("STATIC", "Call:", SS_LEFT, 0, 0)
	TX.edCall = child("EDIT", "", esUppercase|esAutoHScroll|WS_TABSTOP, idCall, WS_EX_CLIENTEDGE)
	send(TX.edCall, EM_SETLIMITTEXT, 12, 0)
	TX.edInput = child("EDIT", "", esUppercase|esAutoHScroll|WS_TABSTOP, idTxInput, WS_EX_CLIENTEDGE)
	send(TX.edInput, WM_SETFONT, TX.fontStrip, 1)
	TX.lblWPM = child("STATIC", "WPM", SS_LEFT, 0, 0)
	TX.trkWPM = child("msctls_trackbar32", "", TBS_HORZ|TBS_NOTICKS|WS_TABSTOP, idWPM, 0)
	send(TX.trkWPM, TBM_SETRANGE, 1, 5|50<<16)
	send(TX.trkWPM, TBM_SETPAGESIZE, 0, 5)
	TX.valWPM = child("STATIC", "", SS_LEFT, 0, 0)
	for i := range TX.btnMacro {
		TX.btnMacro[i] = child("BUTTON", fmt.Sprintf("F%d", i+1), BS_PUSHBUTTON, idMacro0+i, 0)
	}
	TX.lblPort = child("STATIC", "Key port:", SS_LEFT, 0, 0)
	TX.cmbPort = child("COMBOBOX", "", CBS_DROPDOWNLIST|CBS_HASSTRINGS|WS_VSCROLL|WS_TABSTOP, idTxPort, 0)
	TX.cmbLine = child("COMBOBOX", "", CBS_DROPDOWNLIST|CBS_HASSTRINGS|WS_TABSTOP, idTxLine, 0)
	sendStr(TX.cmbLine, CB_ADDSTRING, 0, "DTR")
	sendStr(TX.cmbLine, CB_ADDSTRING, 0, "RTS")
	TX.chkEnable = child("BUTTON", "Transmit enabled", BS_AUTOCHECKBOX|WS_TABSTOP, idTxEnable, 0)
	TX.cmbMode = child("COMBOBOX", "", CBS_DROPDOWNLIST|CBS_HASSTRINGS|WS_TABSTOP, idTxMode, 0)
	sendStr(TX.cmbMode, CB_ADDSTRING, 0, "Send as I type")
	sendStr(TX.cmbMode, CB_ADDSTRING, 0, "Send on Enter")
	TX.btnMacros = child("BUTTON", "Edit macros...", BS_PUSHBUTTON|WS_TABSTOP, idTxMacros, 0)
	TX.btnStop = child("BUTTON", "Stop (Esc)", BS_PUSHBUTTON|WS_TABSTOP, idTxStop, 0)
	TX.chkShow = child("BUTTON", "Sender", BS_AUTOCHECKBOX|WS_TABSTOP, idTxShow, 0)

	refreshPorts()
	line := uintptr(0)
	if TX.useRTS {
		line = 1
	}
	send(TX.cmbLine, CB_SETCURSEL, line, 0)
	mode := uintptr(0)
	if TX.sendOnEnter {
		mode = 1
	}
	send(TX.cmbMode, CB_SETCURSEL, mode, 0)
	send(TX.trkWPM, TBM_SETPOS, 1, uintptr(TX.wpm))
	setText(TX.valWPM, strconv.Itoa(TX.wpm))
	if TX.visible {
		send(TX.chkShow, BM_SETCHECK, 1, 0)
	}
	createOpsControls()
	loadMacros()
	showTX(TX.visible)
	// measure the strip font
	hdc, _, _ := pGetDC.Call(A.hwnd)
	old, _, _ := pSelectObject.Call(hdc, TX.fontStrip)
	var sz struct{ cx, cy int32 }
	m := u16("M")
	pGetTextExtentPoint32.Call(hdc, uintptr(unsafe.Pointer(m)), 1, uintptr(unsafe.Pointer(&sz)))
	pSelectObject.Call(hdc, old)
	pReleaseDC.Call(A.hwnd, hdc)
	TX.charW = max(sz.cx, 1)
}

func refreshPorts() {
	TX.ports = listCOMPorts()
	send(TX.cmbPort, 0x014B /*CB_RESETCONTENT*/, 0, 0)
	sel := -1
	for i, p := range TX.ports {
		sendStr(TX.cmbPort, CB_ADDSTRING, 0, p.Label())
		if p.Name == TX.portName {
			sel = i
		}
	}
	if len(TX.ports) == 0 {
		sendStr(TX.cmbPort, CB_ADDSTRING, 0, "(no COM ports found)")
		sel = 0
	}
	if sel < 0 {
		sel = 0
	}
	send(TX.cmbPort, CB_SETCURSEL, uintptr(sel), 0)
}

func txHeight() int32 {
	if !TX.visible {
		return 0
	}
	return px(140) + opsHeight()
}

func layoutTX(m, y, w int32) {
	bh := px(28)
	TX.strip = RECT{m, y, w - m, y + px(26)}
	y += px(32)
	move(TX.lblCall, m, y+px(5), px(34), px(20))
	move(TX.edCall, m+px(36), y, px(110), bh)
	move(OPS.lblDupe, m+px(150), y+px(5), px(40), px(20))
	wpmW := px(190)
	move(TX.edInput, m+px(194), y, w-2*m-px(194)-wpmW-px(8), bh)
	x := w - m - wpmW
	move(TX.lblWPM, x, y+px(5), px(36), px(20))
	move(TX.trkWPM, x+px(36), y, px(118), px(30))
	move(TX.valWPM, x+px(158), y+px(5), px(32), px(20))
	y += px(36)
	bw := (w - 2*m - 7*px(6)) / 8
	for i, b := range TX.btnMacro {
		move(b, m+int32(i)*(bw+px(6)), y, bw, bh)
	}
	y += px(36)
	if OPS.visible {
		layoutOps(m, y, w)
	}
	y += opsHeight()
	x = m
	move(TX.lblPort, x, y+px(5), px(62), px(20))
	x += px(64)
	move(TX.cmbPort, x, y+px(1), px(180), px(220))
	x += px(186)
	move(TX.cmbLine, x, y+px(1), px(64), px(120))
	x += px(72)
	move(TX.chkEnable, x, y+px(4), px(140), px(22))
	x += px(146)
	move(TX.cmbMode, x, y+px(1), px(140), px(120))
	x += px(148)
	move(TX.btnMacros, x, y, px(104), bh)
	x += px(110)
	move(TX.btnStop, x, y, px(92), bh)
}

func showTX(on bool) {
	TX.visible = on
	cmd := uintptr(swHide)
	if on {
		cmd = SW_SHOW
	}
	for _, h := range []uintptr{TX.lblCall, TX.edCall, TX.edInput, TX.lblWPM, TX.trkWPM, TX.valWPM,
		TX.lblPort, TX.cmbPort, TX.cmbLine, TX.chkEnable, TX.cmbMode, TX.btnMacros, TX.btnStop} {
		pShowWindowCtl.Call(h, cmd)
	}
	for _, b := range TX.btnMacro {
		pShowWindowCtl.Call(b, cmd)
	}
	showOps(on)
	var rc RECT
	pGetClientRect.Call(A.hwnd, ptr(&rc))
	layout(rc.Right, rc.Bottom)
}

// txCommand handles the sender's controls.
func txCommand(id, code uint32) bool {
	switch {
	case id >= idMacro0 && id < idMacro0+8:
		txMacro(int(id - idMacro0))
	case id == idTxStop:
		txStop()
	case id == idTxShow:
		showTX(send(TX.chkShow, BM_GETCHECK, 0, 0) == 1)
	case id == idTxMacros:
		loadMacros() // make sure the file exists
		if p := macrosPath(); p != "" {
			pShellExecuteW.Call(A.hwnd, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16("notepad.exe"))),
				uintptr(unsafe.Pointer(u16(`"`+p+`"`))), 0, swShowNormal)
		}
	case id == idCall && code == enChange:
		if strings.TrimSpace(windowText(TX.edCall)) != "" && OPS.assist.Armed() {
			OPS.assist.Manual(false, time.Now()) // the operator is entering a call: their turn
		}
	case id == idTxInput && code == enChange:
		if !TX.settingInput && strings.TrimSpace(windowText(TX.edInput)) != "" {
			OPS.assist.Manual(false, time.Now()) // the operator is typing: their turn
		}
	case id == idTxMode && code == CBN_SELCHANGE:
		TX.sendOnEnter = send(TX.cmbMode, CB_GETCURSEL, 0, 0) == 1
	case (id == idTxPort || id == idTxLine) && code == CBN_SELCHANGE:
		readPortChoice()
		if TX.keyer != nil { // reopen on the new port/line
			closeTX()
			openTX()
		}
	case id == idTxEnable:
		if send(TX.chkEnable, BM_GETCHECK, 0, 0) == 1 {
			openTX()
		} else {
			closeTX()
		}
	default:
		return false
	}
	return true
}

func readPortChoice() {
	i := int(send(TX.cmbPort, CB_GETCURSEL, 0, 0))
	if i >= 0 && i < len(TX.ports) {
		TX.portName = TX.ports[i].Name
	}
	TX.useRTS = send(TX.cmbLine, CB_GETCURSEL, 0, 0) == 1
}

func openTX() {
	refreshPorts()
	readPortChoice()
	if TX.portName == "" || len(TX.ports) == 0 {
		messageBox(A.hwnd, "No COM port selected. Connect the radio's USB cable, then pick its port in 'Key port'.", appTitle, MB_ICONWARNING)
		send(TX.chkEnable, BM_SETCHECK, 0, 0)
		return
	}
	l, err := openSerialLine(TX.portName, TX.useRTS)
	if err != nil {
		messageBox(A.hwnd, err.Error(), appTitle, MB_ICONWARNING)
		send(TX.chkEnable, BM_SETCHECK, 0, 0)
		return
	}
	TX.line = l
	TX.queue = nil
	setInput("") // never transmit text that was typed while transmit was off
	TX.keyer = cw.NewKeyer(l, TX.wpm, keyerThreadSetup)
	lineName := "DTR"
	if TX.useRTS {
		lineName = "RTS"
	}
	setStatusNote(fmt.Sprintf("Transmit ready: keying %s on %s. The rig must be in CW with break-in on.", lineName, TX.portName))
}

func closeTX() {
	OPS.assist.Stop()
	if TX.keyer != nil {
		TX.keyer.Abort()
		TX.keyer.Close()
		keyerTimersDone()
		TX.keyer = nil
	}
	if TX.line != nil {
		TX.line.Close()
		TX.line = nil
	}
	TX.queue = nil
	endTXLine("")
	send(TX.chkEnable, BM_SETCHECK, 0, 0)
}

func txReady() bool {
	if TX.keyer == nil {
		setStatusNote("Tick 'Transmit enabled' first (pick the radio's key port and line).")
		return false
	}
	return true
}

func txMacro(i int) {
	if !TX.visible || !txReady() {
		return
	}
	sendMacro(i, windowText(TX.edCall), true)
}

// sendMacro queues macro i for call. manual = pressed by the operator (rather than sent
// by Auto-CQ / auto-reply). A {LOG} in the macro logs the contact.
func sendMacro(i int, call string, manual bool) bool {
	if TX.keyer == nil {
		setStatusNote("Tick 'Transmit enabled' first (pick the radio's key port and line).")
		return false
	}
	m := TX.macros[i]
	if m.text == "" {
		setStatusNote(fmt.Sprintf("F%d is empty. Use 'Edit macros...' to set it.", i+1))
		return false
	}
	body, doLog := ops.StripLog(m.text)
	ctx := TX.ctx
	ctx.HisCall = strings.ToUpper(strings.TrimSpace(call))
	text := cw.Expand(body, ctx)
	now := time.Now()
	if text != "" {
		text += " "
		if TX.sendOnEnter {
			TX.queue = append(TX.queue, []rune(text)...)
		} else { // keep order with anything typed but not yet sent
			cur := windowText(TX.edInput)
			if cur != "" && !strings.HasSuffix(cur, " ") {
				cur += " "
			}
			setInput(cur + text)
		}
		if manual {
			OPS.assist.Manual(strings.HasPrefix(text, "CQ"), now)
		}
		if ctx.HisCall != "" && (strings.Contains(body, "{CALL}") || strings.Contains(body, "!")) {
			OPS.assist.UsedCall(ctx.HisCall, now)
		}
	}
	if doLog {
		logQSO(ctx.HisCall)
	}
	return true
}

func txEnter() {
	if !TX.visible {
		return
	}
	if TX.sendOnEnter {
		if !txReady() {
			return
		}
		t := strings.TrimSpace(windowText(TX.edInput))
		if t != "" {
			TX.queue = append(TX.queue, []rune(t+" ")...)
			OPS.assist.Manual(false, time.Now())
		}
		setInput("")
	} else if t := windowText(TX.edInput); t != "" && !strings.HasSuffix(t, " ") {
		setInput(t + " ")
	}
}

func txStop() {
	OPS.assist.Stop()
	if TX.keyer != nil {
		TX.keyer.Abort()
	}
	TX.queue = nil
	setInput("")
	endTXLine(" [stopped]")
}

func setInput(s string) {
	TX.settingInput = true
	setText(TX.edInput, s)
	TX.settingInput = false
	n := uintptr(len([]rune(s)))
	send(TX.edInput, EM_SETSEL, n, n)
}

func endTXLine(suffix string) {
	if TX.inTXLine {
		appendRaw(suffix + "\r\n")
		A.atBreak, A.pending = true, false
		TX.inTXLine = false
	}
}

// txKey handles keys from the message loop; true = consumed.
func txKey(msg *MSG) bool {
	if msg.Message != wmKeyDown {
		return false
	}
	switch k := msg.WParam; {
	case k == vkEscape:
		txStop()
		return true
	case k >= vkF1 && k < vkF1+8:
		txMacro(int(k - vkF1))
		return true
	case k == vkReturn:
		f, _, _ := pGetFocus.Call()
		if f == TX.edInput {
			txEnter()
			return true
		}
		if f == TX.edCall {
			pSetFocus.Call(TX.edInput) // call entered: on to the text box
			return true
		}
		if f == OPS.edFreq || f == OPS.edListen || f == OPS.edMin || f == OPS.edMax {
			return true // swallow it (single-line edits beep on Enter)
		}
	}
	return false
}

// txTick runs on every UI timer tick.
func txTick() {
	if time.Since(TX.macroCheck) > 2*time.Second {
		TX.macroCheck = time.Now()
		if st, err := os.Stat(macrosPath()); err == nil && !st.ModTime().Equal(TX.macroMod) {
			loadMacros()
		}
	}
	k := TX.keyer
	if k == nil {
		if TX.visible {
			pInvalidateRect.Call(A.hwnd, ptr(&TX.strip), 0)
		}
		return
	}
	select {
	case err := <-k.Errors():
		closeTX()
		messageBox(A.hwnd, "Keying stopped: "+err.Error(), appTitle, MB_ICONWARNING)
		return
	default:
	}
	for drained := false; !drained; {
		select {
		case ch := <-k.Sent():
			if !TX.inTXLine {
				markBreak()
				appendRaw("TX> ")
				A.atBreak, TX.inTXLine = false, true
			}
			appendRaw(string(ch))
			TX.sentTail = append(TX.sentTail, ch)
			if len(TX.sentTail) > 200 {
				TX.sentTail = TX.sentTail[len(TX.sentTail)-200:]
			}
		default:
			drained = true
		}
	}
	if k.Wants() {
		if ch, ok := nextTXChar(); ok {
			k.SetWPM(OPS.assist.SendWPM(TX.wpm, time.Now())) // Match speed (or the slider)
			k.Feed(ch)
		}
	}
	if TX.inTXLine && !k.Busy() && len(TX.queue) == 0 && pendingInput() == "" && !k.Active(700*time.Millisecond) {
		endTXLine("")
	}
	pInvalidateRect.Call(A.hwnd, ptr(&TX.strip), 0)
}

func pendingInput() string {
	if TX.sendOnEnter {
		return ""
	}
	return windowText(TX.edInput)
}

func nextTXChar() (rune, bool) {
	if len(TX.queue) > 0 {
		ch := TX.queue[0]
		TX.queue = TX.queue[1:]
		return ch, true
	}
	if TX.sendOnEnter {
		return 0, false
	}
	r := []rune(windowText(TX.edInput))
	if len(r) == 0 {
		return 0, false
	}
	setInput(string(r[1:]))
	return r[0], true
}

// muted returns the audio the decoders should see: silence while we transmit
// (so they don't copy our own signal), the real audio otherwise.
func decoderAudio(buf []float32) []float32 {
	if TX.keyer == nil || !TX.keyer.Active(txMuteHang) {
		return buf
	}
	if cap(TX.muteBuf) < len(buf) {
		TX.muteBuf = make([]float32, len(buf))
	}
	z := TX.muteBuf[:len(buf)]
	for i := range z {
		z[i] = 0
	}
	return z
}

func paintTXStrip(hdc uintptr) {
	if !TX.visible {
		return
	}
	r := TX.strip
	w, h := r.Right-r.Left, r.Bottom-r.Top
	if w <= 0 || h <= 0 {
		return
	}
	mdc, _, _ := pCreateCompatibleDC.Call(hdc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(h))
	oldBmp, _, _ := pSelectObject.Call(mdc, bmp)
	rc := RECT{0, 0, w, h}
	pFillRect.Call(mdc, ptr(&rc), TX.brStrip)
	oldFont, _, _ := pSelectObject.Call(mdc, TX.fontStrip)
	pSetBkMode.Call(mdc, TRANSPARENT)
	text := func(x int32, s string, c uintptr) {
		if s == "" {
			return
		}
		u := syscall.StringToUTF16(s)
		u = u[:len(u)-1] // drop the NUL
		pSetTextColor.Call(mdc, c)
		pTextOutW.Call(mdc, uintptr(x), uintptr(px(4)), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)))
	}
	// TX lamp
	lamp := px(52)
	if TX.keyer != nil && TX.keyer.Active(0) {
		lr := RECT{px(4), px(5), px(16), h - px(5)}
		pFillRect.Call(mdc, ptr(&lr), TX.brTX)
		text(px(20), "TX", rgb(255, 120, 110))
	} else if TX.keyer != nil {
		text(px(6), "ready", rgb(120, 200, 130))
	} else {
		text(px(6), "off", rgb(110, 116, 126))
	}
	mid := lamp + (w-lamp)*45/100
	pending := string(TX.queue) + pendingInput()
	if len(TX.sentTail) == 0 && pending == "" {
		text(lamp+px(6), "Type below. F1-F8 send macros, Esc stops. Sent text shows here in grey.", rgb(110, 116, 126))
	} else {
		n := int((mid - lamp - px(6)) / TX.charW)
		tail := TX.sentTail
		if len(tail) > n {
			tail = tail[len(tail)-n:]
		}
		text(mid-int32(len(tail))*TX.charW, string(tail), rgb(130, 136, 146))
		if TX.keyer != nil {
			text(mid, pending, rgb(255, 200, 80)) // queued to go out
		} else {
			text(mid, pending+"   (transmit is off)", rgb(150, 156, 166))
		}
	}
	if st := OPS.status; st != "" { // Auto-CQ state, right-aligned over a backing box
		pSelectObject.Call(mdc, A.fontUI)
		u := syscall.StringToUTF16(st)
		u = u[:len(u)-1]
		var sz struct{ cx, cy int32 }
		pGetTextExtentPoint32.Call(mdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)), uintptr(unsafe.Pointer(&sz)))
		x := w - sz.cx - px(10)
		box := RECT{x - px(8), 0, w, h}
		pFillRect.Call(mdc, ptr(&box), TX.brStrip)
		pSetTextColor.Call(mdc, rgb(255, 190, 70))
		pTextOutW.Call(mdc, uintptr(x), uintptr((h-sz.cy)/2), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)))
	}
	pSelectObject.Call(mdc, oldFont)
	pBitBlt.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(w), uintptr(h), mdc, 0, 0, SRCCOPY)
	pSelectObject.Call(mdc, oldBmp)
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(mdc)
}

// defaultWindowHeight fits the screen (the CF-33 at 200% scaling is ~720 px tall).
func defaultWindowHeight() int32 {
	want := px(800)
	if r, _, _ := pGetSystemMetrics.Call(62 /*SM_CYMAXIMIZED*/); r > 0 && int32(r)-px(20) < want {
		return int32(r) - px(20)
	}
	return want
}
