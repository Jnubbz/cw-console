//go:build windows

package main

// Operating aids: heard callsigns, Auto-CQ + auto-reply, match speed, logging.
// The logic is in package ops (shared with the Mac app); this is the Win32 glue.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"cwdecoder/deepcw"
	"cwdecoder/ops"
)

const (
	idAutoCQ = 401 + iota
	idCQMacro
	idListen
	idAutoReply
	idReplyMacro
	idMatch
	idMatchMin
	idMatchMax
	idFreq
	idLogQSO
	idLogUndo
	idLogDir
	idOpsShow
	idChip0 // ..idChip0+nChips-1
)

const (
	nChips     = 6
	esNumber   = 0x2000
	enChange   = 0x0300
	opsRowH    = 34
	colorBtnFc = 15
)

var (
	pGetSysColorBrush = user32.NewProc("GetSysColorBrush")
	pSetFocus         = user32.NewProc("SetFocus")
)

type opsState struct {
	lblHeard, lblNone, chkShow                         uintptr
	chips                                              [nChips]uintptr
	chkAutoCQ, cmbCQ, lblListen, edListen, lblSecs     uintptr
	chkReply, cmbReply, chkMatch, edMin, lblTo, edMax  uintptr
	lblThem, lblFreq, edFreq, lblBand, btnLog, btnUndo uintptr
	btnDir, lblDupe                                    uintptr
	chipCalls                                          []string
	chipCap                                            int  // chips that fit the window
	visible                                            bool // the two operating rows are shown
	status                                             string
	shown                                              [nChips]bool

	assist      *ops.Assist
	logbook     *ops.Logbook
	freq        string
	lastClassic time.Time
	lastText    map[uintptr]string
	loading     bool // filling controls from settings: ignore change notifications
	ticks       int
}

var OPS = opsState{assist: ops.NewAssist(), lastText: map[uintptr]string{}, visible: true, chipCap: 4}

// opsHeight: the operating rows' height (0 when hidden or the sender is hidden).
func opsHeight() int32 {
	if !OPS.visible {
		return 0
	}
	return px(2 * opsRowH)
}

func logDir() string {
	p := settingsPath()
	if p == "" {
		return "Logs"
	}
	return filepath.Join(filepath.Dir(p), "Logs")
}

func createOpsControls() {
	OPS.logbook = ops.OpenLog(logDir())
	OPS.loading = true
	defer func() { OPS.loading = false }()
	OPS.lblHeard = child("STATIC", "Heard:", SS_LEFT, 0, 0)
	OPS.lblNone = child("STATIC", "(calls you copy appear here: click one to use it)", SS_LEFT, 0, 0)
	send(OPS.lblNone, WM_SETFONT, A.fontSmall, 1)
	for i := range OPS.chips {
		OPS.chips[i] = child("BUTTON", "", BS_PUSHBUTTON, idChip0+i, 0)
		pShowWindowCtl.Call(OPS.chips[i], swHide)
	}
	OPS.chkShow = child("BUTTON", "Ops", BS_AUTOCHECKBOX|WS_TABSTOP, idOpsShow, 0)
	if OPS.visible {
		send(OPS.chkShow, BM_SETCHECK, 1, 0)
	}

	OPS.chkAutoCQ = child("BUTTON", "Auto-CQ", BS_AUTOCHECKBOX|WS_TABSTOP, idAutoCQ, 0)
	OPS.cmbCQ = child("COMBOBOX", "", CBS_DROPDOWNLIST|CBS_HASSTRINGS|WS_TABSTOP, idCQMacro, 0)
	OPS.lblListen = child("STATIC", "listen", SS_LEFT, 0, 0)
	OPS.edListen = child("EDIT", "", esNumber|WS_TABSTOP, idListen, WS_EX_CLIENTEDGE)
	send(OPS.edListen, EM_SETLIMITTEXT, 2, 0)
	OPS.lblSecs = child("STATIC", "s", SS_LEFT, 0, 0)
	OPS.chkReply = child("BUTTON", "Auto-reply", BS_AUTOCHECKBOX|WS_TABSTOP, idAutoReply, 0)
	OPS.cmbReply = child("COMBOBOX", "", CBS_DROPDOWNLIST|CBS_HASSTRINGS|WS_TABSTOP, idReplyMacro, 0)
	OPS.chkMatch = child("BUTTON", "Match speed", BS_AUTOCHECKBOX|WS_TABSTOP, idMatch, 0)
	OPS.edMin = child("EDIT", "", esNumber|WS_TABSTOP, idMatchMin, WS_EX_CLIENTEDGE)
	send(OPS.edMin, EM_SETLIMITTEXT, 2, 0)
	OPS.lblTo = child("STATIC", "to", SS_LEFT, 0, 0)
	OPS.edMax = child("EDIT", "", esNumber|WS_TABSTOP, idMatchMax, WS_EX_CLIENTEDGE)
	send(OPS.edMax, EM_SETLIMITTEXT, 2, 0)
	OPS.lblThem = child("STATIC", "", SS_LEFT, 0, 0)

	OPS.lblFreq = child("STATIC", "Freq:", SS_LEFT, 0, 0)
	OPS.edFreq = child("EDIT", "", esAutoHScroll|WS_TABSTOP, idFreq, WS_EX_CLIENTEDGE)
	send(OPS.edFreq, EM_SETLIMITTEXT, 12, 0)
	OPS.lblBand = child("STATIC", "", SS_LEFT, 0, 0)
	OPS.btnLog = child("BUTTON", "Log QSO", BS_PUSHBUTTON|WS_TABSTOP, idLogQSO, 0)
	OPS.btnUndo = child("BUTTON", "Undo", BS_PUSHBUTTON|WS_TABSTOP, idLogUndo, 0)
	OPS.btnDir = child("BUTTON", "Log folder", BS_PUSHBUTTON|WS_TABSTOP, idLogDir, 0)
	OPS.lblDupe = child("STATIC", "", SS_LEFT, 0, 0)

	c := OPS.assist.Cfg
	setText(OPS.edListen, strconv.Itoa(int(c.ListenSecs)))
	setText(OPS.edMin, strconv.Itoa(c.MatchMin))
	setText(OPS.edMax, strconv.Itoa(c.MatchMax))
	setText(OPS.edFreq, OPS.freq)
	if c.AutoReply {
		send(OPS.chkReply, BM_SETCHECK, 1, 0)
	}
	if c.MatchSpeed {
		send(OPS.chkMatch, BM_SETCHECK, 1, 0)
	}
	fillMacroCombos()
	opsRefreshLog()
}

// fillMacroCombos (re)labels the CQ / reply macro pickers from the macros file.
func fillMacroCombos() {
	if OPS.cmbCQ == 0 {
		return
	}
	for _, cb := range []uintptr{OPS.cmbCQ, OPS.cmbReply} {
		send(cb, 0x014B /*CB_RESETCONTENT*/, 0, 0)
		for i, m := range TX.macros {
			l := fmt.Sprintf("F%d", i+1)
			if m.label != "" {
				l += " " + m.label
			}
			sendStr(cb, CB_ADDSTRING, 0, l)
		}
	}
	send(OPS.cmbCQ, CB_SETCURSEL, uintptr(OPS.assist.Cfg.CQMacro), 0)
	send(OPS.cmbReply, CB_SETCURSEL, uintptr(OPS.assist.Cfg.ReplyMacro), 0)
}

func opsControls() []uintptr {
	return []uintptr{OPS.lblHeard, OPS.lblNone, OPS.chkAutoCQ, OPS.cmbCQ, OPS.lblListen, OPS.edListen,
		OPS.lblSecs, OPS.chkReply, OPS.cmbReply, OPS.chkMatch, OPS.edMin, OPS.lblTo, OPS.edMax, OPS.lblThem,
		OPS.lblFreq, OPS.edFreq, OPS.lblBand, OPS.btnLog, OPS.btnUndo, OPS.btnDir}
}

func showOps(senderOn bool) {
	on := senderOn && OPS.visible
	cmd := uintptr(swHide)
	if on {
		cmd = SW_SHOW
	}
	for _, h := range opsControls() {
		pShowWindowCtl.Call(h, cmd)
	}
	OPS.chipCalls = nil // relaid out on the next tick
	for i, h := range OPS.chips {
		pShowWindowCtl.Call(h, swHide)
		OPS.shown[i] = false
	}
	dupe := uintptr(swHide)
	if senderOn {
		dupe = SW_SHOW
	}
	pShowWindowCtl.Call(OPS.lblDupe, dupe)
	if on {
		opsShowChips()
	}
}

// layoutOps places the two operating rows starting at y.
func layoutOps(m, y, w int32) {
	bh := px(28)
	// Row 1: heard calls (as many as fit) ... Freq, Log QSO, Undo, Log folder on the right
	x := w - m - px(424)
	move(OPS.lblFreq, x, y+px(5), px(36), px(20))
	x += px(38)
	move(OPS.edFreq, x, y+px(1), px(74), px(26))
	x += px(78)
	move(OPS.lblBand, x, y+px(5), px(40), px(20))
	x += px(42)
	move(OPS.btnLog, x, y, px(110), bh)
	x += px(114)
	move(OPS.btnUndo, x, y, px(56), bh)
	x += px(60)
	move(OPS.btnDir, x, y, px(82), bh)

	move(OPS.lblHeard, m, y+px(5), px(46), px(20))
	cx := m + px(48)
	cw := px(86)
	avail := w - m - px(424) - px(10) - cx
	OPS.chipCap = max(1, min(nChips, int(avail/(cw+px(4)))))
	for i, h := range OPS.chips {
		move(h, cx+int32(i)*(cw+px(4)), y, cw, bh)
	}
	move(OPS.lblNone, cx, y+px(7), avail, px(18))
	OPS.chipCalls = nil // re-show for the new capacity
	y += px(opsRowH)
	// Row 2: Auto-CQ, auto-reply, match speed
	x = m
	move(OPS.chkAutoCQ, x, y+px(4), px(78), px(22))
	x += px(80)
	move(OPS.cmbCQ, x, y+px(1), px(108), px(220))
	x += px(114)
	move(OPS.lblListen, x, y+px(5), px(36), px(20))
	x += px(38)
	move(OPS.edListen, x, y+px(1), px(34), px(26))
	x += px(36)
	move(OPS.lblSecs, x, y+px(5), px(12), px(20))
	x += px(24)
	move(OPS.chkReply, x, y+px(4), px(90), px(22))
	x += px(92)
	move(OPS.cmbReply, x, y+px(1), px(108), px(220))
	x += px(122)
	move(OPS.chkMatch, x, y+px(4), px(104), px(22))
	x += px(106)
	move(OPS.edMin, x, y+px(1), px(34), px(26))
	x += px(38)
	move(OPS.lblTo, x, y+px(5), px(16), px(20))
	x += px(20)
	move(OPS.edMax, x, y+px(1), px(34), px(26))
	x += px(44)
	move(OPS.lblThem, x, y+px(5), w-m-x, px(20))
}

// opsCommand handles the operating controls.
func opsCommand(id, code uint32) bool {
	if OPS.loading {
		return id >= idAutoCQ && id < idChip0+nChips
	}
	c := &OPS.assist.Cfg
	num := func(h uintptr, def int) int {
		v, err := strconv.Atoi(strings.TrimSpace(windowText(h)))
		if err != nil {
			return def
		}
		return v
	}
	switch {
	case id == idOpsShow:
		OPS.visible = send(OPS.chkShow, BM_GETCHECK, 0, 0) == 1
		showTX(TX.visible) // re-layout
	case id >= idChip0 && id < idChip0+nChips:
		if i := int(id - idChip0); i < len(OPS.chipCalls) {
			setText(TX.edCall, OPS.chipCalls[i])
			pSetFocus.Call(TX.edInput)
		}
	case id == idAutoCQ:
		on := send(OPS.chkAutoCQ, BM_GETCHECK, 0, 0) == 1
		if on && TX.keyer == nil {
			send(OPS.chkAutoCQ, BM_SETCHECK, 0, 0)
			setStatusNote("Auto-CQ: tick 'Transmit enabled' first.")
			return true
		}
		if on && TX.macros[c.CQMacro].text == "" {
			send(OPS.chkAutoCQ, BM_SETCHECK, 0, 0)
			setStatusNote(fmt.Sprintf("Auto-CQ: F%d is empty. Use 'Edit macros...' to set it.", c.CQMacro+1))
			return true
		}
		OPS.assist.SetAutoCQ(on, time.Now())
	case id == idCQMacro && code == CBN_SELCHANGE:
		c.CQMacro = int(send(OPS.cmbCQ, CB_GETCURSEL, 0, 0))
	case id == idReplyMacro && code == CBN_SELCHANGE:
		c.ReplyMacro = int(send(OPS.cmbReply, CB_GETCURSEL, 0, 0))
	case id == idAutoReply:
		c.AutoReply = send(OPS.chkReply, BM_GETCHECK, 0, 0) == 1
	case id == idMatch:
		c.MatchSpeed = send(OPS.chkMatch, BM_GETCHECK, 0, 0) == 1
	case id == idListen && code == enChange:
		c.ListenSecs = float64(num(OPS.edListen, 6))
	case id == idMatchMin && code == enChange:
		c.MatchMin = num(OPS.edMin, 12)
	case id == idMatchMax && code == enChange:
		c.MatchMax = num(OPS.edMax, 30)
	case id == idFreq && code == enChange:
		OPS.freq = strings.TrimSpace(windowText(OPS.edFreq))
	case id == idLogQSO:
		logQSO(windowText(TX.edCall))
	case id == idLogUndo:
		if q, err := OPS.logbook.Undo(); err != nil {
			setStatusNote("Undo: " + err.Error())
		} else {
			setStatusNote("Removed " + q.Call + " from the log.")
		}
		opsRefreshLog()
	case id == idLogDir:
		os.MkdirAll(logDir(), 0o755)
		pShellExecuteW.Call(A.hwnd, uintptr(unsafe.Pointer(u16("open"))), uintptr(unsafe.Pointer(u16(logDir()))), 0, 0, swShowNormal)
	default:
		return false
	}
	c.Clamp() // (the edit boxes keep what was typed; the clamped value is what's used)
	return true
}

// aiLag: how far decoded text trails the audio with the current decoder.
func aiLag() time.Duration {
	if AI.mode != decAI || AI.live == nil {
		return 300 * time.Millisecond
	}
	_, pass := AI.live.Status()
	if pass <= 0 {
		return 3 * time.Second
	}
	return time.Duration(deepcw.TailGuardSecs*float64(time.Second)) + currentPlan().Interval(pass) + pass
}

func txBusy() bool {
	k := TX.keyer
	return k != nil && (len(TX.queue) > 0 || pendingInput() != "" || k.Busy() || k.Active(txMuteHang))
}

// opsRx gets every piece of decoded receive text.
func opsRx(t string, lineStart bool) {
	now := time.Now()
	if lineStart {
		OPS.assist.Rx(" ", now)
	}
	OPS.assist.Rx(t, now)
}

// opsTick runs on every UI timer tick (after the sender).
func opsTick() {
	now := time.Now()
	in := ops.Inputs{Now: now, TxBusy: txBusy(), TxOn: TX.keyer != nil, Lag: aiLag(), MyCall: TX.ctx.MyCall}
	if A.cap.running && A.dec != nil {
		// Key down only counts while the classic decoder is copying characters, so noise
		// flicker doesn't look like someone keying.
		in.Signal = A.dec.KeyDown() && now.Sub(OPS.lastClassic) < 3*time.Second
		in.RxWPM = float64(A.dec.WPM())
	}
	for _, a := range OPS.assist.Tick(in) {
		switch a.Kind {
		case ops.SendMacro:
			call := a.Call
			if call == "" {
				call = windowText(TX.edCall)
			}
			if !sendMacro(a.Macro, call, false) {
				OPS.assist.Stop()
			}
		case ops.SetCall:
			setText(TX.edCall, a.Call)
		case ops.Note:
			setStatusNote(a.Text)
		}
	}
	OPS.ticks++
	if TX.visible && OPS.ticks%4 == 0 {
		opsRefresh(now)
	}
}

func setIfChanged(h uintptr, s string) {
	if OPS.lastText[h] != s {
		OPS.lastText[h] = s
		setText(h, s)
	}
}

func opsRefresh(now time.Time) {
	armed := OPS.assist.Armed()
	if (send(OPS.chkAutoCQ, BM_GETCHECK, 0, 0) == 1) != armed {
		v := uintptr(0)
		if armed {
			v = 1
		}
		send(OPS.chkAutoCQ, BM_SETCHECK, v, 0)
	}
	OPS.status = OPS.assist.Status(now)
	them := ""
	if w := OPS.assist.TheirWPM(now); w > 0 {
		them = fmt.Sprintf("Them: %d WPM", w)
		if s := OPS.assist.SendWPM(TX.wpm, now); s != TX.wpm {
			them += fmt.Sprintf(", sending %d", s)
		}
	}
	setIfChanged(OPS.lblThem, them)
	_, band := ops.ParseFreq(OPS.freq)
	if band == "" && OPS.freq != "" {
		band = "?"
	}
	setIfChanged(OPS.lblBand, band)
	call := strings.ToUpper(strings.TrimSpace(windowText(TX.edCall)))
	dupe := ""
	if call != "" && OPS.logbook.IsDupe(call, band, TX.ctx.Extra["PARK"], now) {
		dupe = "DUPE"
	}
	setIfChanged(OPS.lblDupe, dupe)
	opsShowChips()
}

func opsShowChips() {
	calls := OPS.assist.Calls()
	if len(calls) > OPS.chipCap {
		calls = calls[:OPS.chipCap]
	}
	if strings.Join(calls, ",") == strings.Join(OPS.chipCalls, ",") && OPS.chipCalls != nil {
		return
	}
	OPS.chipCalls = append([]string{}, calls...)
	for i, h := range OPS.chips {
		show := i < len(calls) && TX.visible && OPS.visible
		if show {
			setText(h, calls[i])
		}
		if show != OPS.shown[i] {
			cmd := uintptr(swHide)
			if show {
				cmd = SW_SHOW
			}
			pShowWindowCtl.Call(h, cmd)
			OPS.shown[i] = show
		}
	}
	cmd := uintptr(swHide)
	if len(calls) == 0 && TX.visible && OPS.visible {
		cmd = SW_SHOW
	}
	pShowWindowCtl.Call(OPS.lblNone, cmd)
}

// logQSO logs call (Log button or a {LOG} macro), then clears the Call box.
func logQSO(call string) bool {
	call = strings.ToUpper(strings.TrimSpace(call))
	if call == "" {
		setStatusNote("Nothing to log: the Call box is empty.")
		return false
	}
	now := time.Now()
	freq, band := ops.ParseFreq(OPS.freq)
	ctx := TX.ctx
	rstSent, rstRcvd := ctx.RST, OPS.assist.RSTRcvd()
	if rstSent == "" {
		rstSent = "599"
	}
	if rstRcvd == "" {
		rstRcvd = "599"
	}
	q := ops.QSO{Call: call, Start: OPS.assist.StartTime(call, now), Freq: freq, Band: band,
		RSTSent: rstSent, RSTRcvd: rstRcvd, MyCall: ctx.MyCall, Park: ctx.Extra["PARK"],
		MyState: ctx.MyState, MyGrid: ctx.MyGrid}
	dupe, err := OPS.logbook.Add(q)
	if err != nil {
		messageBox(A.hwnd, "Couldn't write the log: "+err.Error(), appTitle, MB_ICONWARNING)
		return false
	}
	msg := fmt.Sprintf("Logged %s %sz", call, now.UTC().Format("1504"))
	if dupe {
		msg += " (dupe)"
	}
	if band == "" {
		msg += ". No band: type the frequency in Freq so the log has one."
	}
	setStatusNote(msg)
	OPS.assist.Logged(now)
	setText(TX.edCall, "")
	opsRefreshLog()
	return true
}

func opsRefreshLog() {
	now := time.Now()
	setIfChanged(OPS.btnLog, fmt.Sprintf("Log QSO (%d)", OPS.logbook.CountToday(TX.ctx.Extra["PARK"], now)))
}

// opsCtlColor colours the status (amber) and DUPE (red) labels.
func opsCtlColor(hdc, h uintptr) (uintptr, bool) {
	var c uintptr
	switch h {
	case OPS.lblDupe:
		c = rgb(200, 30, 30)
	default:
		return 0, false
	}
	pSetTextColor.Call(hdc, c)
	pSetBkMode.Call(hdc, TRANSPARENT)
	b, _, _ := pGetSysColorBrush.Call(colorBtnFc)
	return b, true
}

// ---- settings ----

func opsLoadSetting(k, v string) {
	c := &OPS.assist.Cfg
	n, _ := strconv.Atoi(v)
	switch k {
	case "cqmacro":
		c.CQMacro = n
	case "replymacro":
		c.ReplyMacro = n
	case "listen":
		c.ListenSecs = float64(n)
	case "autoreply":
		c.AutoReply = v == "1"
	case "match":
		c.MatchSpeed = v == "1"
	case "matchmin":
		c.MatchMin = n
	case "matchmax":
		c.MatchMax = n
	case "freq":
		OPS.freq = v
	case "opsrows":
		OPS.visible = v != "0"
	}
	c.Clamp()
}

func opsSettings() string {
	c := OPS.assist.Cfg
	b := func(x bool) int {
		if x {
			return 1
		}
		return 0
	}
	return fmt.Sprintf("cqmacro=%d\nreplymacro=%d\nlisten=%d\nautoreply=%d\nmatch=%d\nmatchmin=%d\nmatchmax=%d\nopsrows=%d\nfreq=%s\n",
		c.CQMacro, c.ReplyMacro, int(c.ListenSecs), b(c.AutoReply), b(c.MatchSpeed), c.MatchMin, c.MatchMax, b(OPS.visible), OPS.freq)
}
