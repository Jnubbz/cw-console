//go:build windows

// CW Decoder — a standalone Windows window around the CW decoder from Nexus by KD9TAW
// (https://github.com/kd9taw/nexus). GPL-3.0-only.
package main

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/Jnubbz/cw-console/cw"
	"github.com/Jnubbz/cw-console/deepcw"
	"github.com/Jnubbz/cw-console/ops"
)

func init() { runtime.LockOSThread() } // all Win32 UI calls on one OS thread

const (
	idCombo = 101 + iota
	idStart
	idOpenWav
	idPitch
	idAuto
	idSens
	idClear
	idCopy
	idSave
	idImproved
)

const (
	timerID    = 1
	timerMs    = 25
	specLo     = 200
	specHi     = 1400
	specStep   = 10
	pitchMin   = 300
	pitchMax   = 1200
	breakAfter = 2500 * time.Millisecond // new line after this much silence
	appTitle   = "CW Decoder"
)

type app struct {
	hinst, hwnd                                              uintptr
	combo, btnStart, btnWav, lblInput                        uintptr
	lblPitch, trkPitch, valPitch, btnAuto                    uintptr
	lblSens, trkSens, valSens                                uintptr
	status, edit, btnClear, btnCopy, btnSave                 uintptr
	chkImproved                                              uintptr
	fontUI, fontSmall, fontMono                              uintptr
	brEdit, brBg, brPass, brBar, brLedOn, brLedSig, brLedOff uintptr
	penPitch, penTick                                        uintptr
	scale                                                    float64

	devs     []inputDevice
	cap      capture
	dec      *cw.StreamDecoder
	pitch    float32
	sens     float32
	improved bool
	devName  string

	hist      []float32 // last few seconds of audio (auto-tune + spectrum)
	buf       []float32
	freqs     []float32
	spec      []float32 // smoothed dB above median
	specRect  RECT
	tick      int
	lastChar  time.Time
	pending   bool // text since the last line break
	atBreak   bool // edit currently ends at a line start
	statusTxt string
}

var A app

func px(v int) int32 { return int32(math.Round(float64(v) * A.scale)) }

func main() {
	updateStartup() // after an update: wait for the old copy to close; then look for new ones
	enableVisualStyles()
	pSetProcessDPIAware.Call()
	icc := INITCOMMONCONTROLSEX{DwSize: 8, DwICC: 0x4 | 0x4000}
	pInitCommonControlsEx.Call(ptr(&icc))

	A.hinst, _, _ = pGetModuleHandleW.Call(0)
	hdc, _, _ := pGetDC.Call(0)
	dpi, _, _ := pGetDeviceCaps.Call(hdc, LOGPIXELSY)
	pReleaseDC.Call(0, hdc)
	A.scale = float64(dpi) / 96
	if A.scale < 1 {
		A.scale = 1
	}

	A.pitch, A.sens, A.improved = 600, 0.5, true
	loadSettings()
	for f := float32(specLo); f <= specHi; f += specStep {
		A.freqs = append(A.freqs, f)
	}
	A.spec = make([]float32, len(A.freqs))
	A.atBreak = true

	makeGDI()

	cls := u16("CWDecoderMain")
	cursor, _, _ := pLoadCursorW.Call(0, IDC_ARROW)
	icon, _, _ := pLoadIconW.Call(0, IDI_APPLICATION)
	wc := WNDCLASSEXW{
		LpfnWndProc: syscall.NewCallback(wndProc), HInstance: A.hinst,
		HCursor: cursor, HIcon: icon, HIconSm: icon,
		HbrBackground: COLOR_BTNFACE + 1, LpszClassName: cls,
	}
	wc.CbSize = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(ptr(&wc))

	A.hwnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(u16(appTitle))), WS_OVERLAPPEDWINDOW|WS_CLIPCHILDREN,
		CW_USEDEFAULT, CW_USEDEFAULT, uintptr(px(860)), uintptr(defaultWindowHeight()), 0, 0, A.hinst, 0)
	if A.hwnd == 0 {
		messageBox(0, "Could not create the main window.", appTitle, MB_ICONWARNING)
		return
	}
	createControls()
	var rc RECT
	pGetClientRect.Call(A.hwnd, ptr(&rc))
	layout(rc.Right, rc.Bottom)
	pDragAcceptFiles.Call(A.hwnd, 1)
	pShowWindow.Call(A.hwnd, SW_SHOW)
	pUpdateWindow.Call(A.hwnd)
	pSetTimer.Call(A.hwnd, timerID, timerMs, 0)

	// Start listening straight away on the remembered (or default) input.
	startCapture(false)

	var msg MSG
	for {
		r, _, _ := pGetMessageW.Call(ptr(&msg), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if txKey(&msg) {
			continue
		}
		pTranslateMessage.Call(ptr(&msg))
		pDispatchMessageW.Call(ptr(&msg))
	}
}

// enableVisualStyles activates Common Controls v6 (modern-looking buttons/sliders) via a
// temporary manifest, since this exe carries no resource section. Harmless if it fails.
func enableVisualStyles() {
	const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
<dependency><dependentAssembly><assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/></dependentAssembly></dependency>
</assembly>`
	path := filepath.Join(os.TempDir(), "cw-decoder-styles.manifest")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		return
	}
	ctx := ACTCTXW{LpSource: u16(path)}
	ctx.CbSize = uint32(unsafe.Sizeof(ctx))
	h, _, _ := pCreateActCtxW.Call(ptr(&ctx))
	if h == 0 || h == ^uintptr(0) {
		return
	}
	var cookie uintptr
	pActivateActCtx.Call(h, ptr(&cookie))
}

func makeFont(height int32, weight int, face string) uintptr {
	f, _, _ := pCreateFontW.Call(uintptr(-height), 0, 0, 0, uintptr(weight), 0, 0, 0,
		1 /*DEFAULT_CHARSET*/, 0, 0, 5 /*CLEARTYPE_QUALITY*/, 0, uintptr(unsafe.Pointer(u16(face))))
	return f
}

func brush(c uintptr) uintptr { b, _, _ := pCreateSolidBrush.Call(c); return b }

func makeGDI() {
	A.fontUI = makeFont(px(15), 400, "Segoe UI")
	A.fontSmall = makeFont(px(12), 400, "Segoe UI")
	A.fontMono = makeFont(px(20), 400, "Consolas")
	A.brEdit = brush(rgb(14, 18, 22))
	A.brBg = brush(rgb(16, 20, 26))
	A.brPass = brush(rgb(44, 52, 66))
	A.brBar = brush(rgb(0, 170, 140))
	A.brLedOn = brush(rgb(60, 230, 90))
	A.brLedSig = brush(rgb(30, 90, 45))
	A.brLedOff = brush(rgb(55, 60, 66))
	A.penPitch, _, _ = pCreatePen.Call(PS_SOLID, uintptr(px(2)), rgb(255, 170, 0))
	A.penTick, _, _ = pCreatePen.Call(PS_SOLID, 1, rgb(70, 78, 90))
}

func child(class, text string, style uintptr, id int, exStyle uintptr) uintptr {
	h, _, _ := pCreateWindowExW.Call(exStyle, uintptr(unsafe.Pointer(u16(class))),
		uintptr(unsafe.Pointer(u16(text))), WS_CHILD|WS_VISIBLE|style,
		0, 0, 10, 10, A.hwnd, uintptr(id), A.hinst, 0)
	send(h, WM_SETFONT, A.fontUI, 1)
	return h
}

func createControls() {
	A.lblInput = child("STATIC", "Input:", SS_LEFT, 0, 0)
	A.combo = child("COMBOBOX", "", CBS_DROPDOWNLIST|CBS_HASSTRINGS|WS_VSCROLL|WS_TABSTOP, idCombo, 0)
	A.btnStart = child("BUTTON", "Start", BS_PUSHBUTTON|WS_TABSTOP, idStart, 0)
	A.btnWav = child("BUTTON", "Decode WAV file...", BS_PUSHBUTTON|WS_TABSTOP, idOpenWav, 0)

	A.lblPitch = child("STATIC", "Pitch:", SS_LEFT, 0, 0)
	A.trkPitch = child("msctls_trackbar32", "", TBS_HORZ|TBS_NOTICKS|WS_TABSTOP, idPitch, 0)
	A.valPitch = child("STATIC", "", SS_LEFT, 0, 0)
	A.btnAuto = child("BUTTON", "Auto tune", BS_PUSHBUTTON|WS_TABSTOP, idAuto, 0)
	A.lblSens = child("STATIC", "Sensitivity:", SS_LEFT, 0, 0)
	A.trkSens = child("msctls_trackbar32", "", TBS_HORZ|TBS_NOTICKS|WS_TABSTOP, idSens, 0)
	A.valSens = child("STATIC", "", SS_LEFT, 0, 0)

	A.status = child("STATIC", "", SS_LEFT, 0, 0)
	A.edit = child("EDIT", "", ES_MULTILINE|ES_AUTOVSCROLL|ES_READONLY|ES_NOHIDESEL|WS_VSCROLL, 0, WS_EX_CLIENTEDGE)
	send(A.edit, WM_SETFONT, A.fontMono, 1)
	send(A.edit, EM_SETLIMITTEXT, 0, 0)
	A.btnClear = child("BUTTON", "Clear", BS_PUSHBUTTON|WS_TABSTOP, idClear, 0)
	A.btnCopy = child("BUTTON", "Copy all", BS_PUSHBUTTON|WS_TABSTOP, idCopy, 0)
	A.btnSave = child("BUTTON", "Save...", BS_PUSHBUTTON|WS_TABSTOP, idSave, 0)
	A.chkImproved = child("BUTTON", "Improved speed/fade tracking (uncheck for exact Nexus)", BS_AUTOCHECKBOX|WS_TABSTOP, idImproved, 0)
	if A.improved {
		send(A.chkImproved, BM_SETCHECK, 1, 0)
	}

	send(A.trkPitch, TBM_SETRANGE, 1, uintptr(pitchMin)|uintptr(pitchMax)<<16)
	send(A.trkPitch, TBM_SETLINESIZE, 0, 5)
	send(A.trkPitch, TBM_SETPAGESIZE, 0, 50)
	send(A.trkSens, TBM_SETRANGE, 1, 0|100<<16)
	send(A.trkSens, TBM_SETPAGESIZE, 0, 10)

	createAIControls()
	createTXControls()
	A.devs = listInputs()
	sel := 0
	for i, d := range A.devs {
		sendStr(A.combo, CB_ADDSTRING, 0, d.Name)
		if A.devName != "" && d.Name == A.devName {
			sel = i
		}
	}
	send(A.combo, CB_SETCURSEL, uintptr(sel), 0)
	showPitch()
	showSens()
}

func move(h uintptr, x, y, w, ht int32) {
	pMoveWindow.Call(h, uintptr(x), uintptr(y), uintptr(w), uintptr(ht), 1)
}

func layout(w, h int32) {
	if A.edit == 0 {
		return
	}
	m := px(10)
	bh := px(28)
	y := m
	move(A.lblInput, m, y+px(5), px(48), px(20))
	x := m + px(52)
	move(A.combo, x, y+px(1), px(330), px(300))
	x += px(338)
	move(A.btnStart, x, y, px(90), bh)
	x += px(98)
	move(A.btnWav, x, y, px(150), bh)

	y += px(40)
	move(A.lblPitch, m, y+px(5), px(48), px(20))
	x = m + px(48)
	move(A.trkPitch, x, y, px(230), px(30))
	x += px(232)
	move(A.valPitch, x, y+px(5), px(64), px(20))
	x += px(66)
	move(A.btnAuto, x, y, px(90), bh)
	x += px(108)
	move(A.lblSens, x, y+px(5), px(78), px(20))
	x += px(78)
	move(A.trkSens, x, y, px(150), px(30))
	x += px(152)
	move(A.valSens, x, y+px(5), px(50), px(20))

	y += px(40)
	layoutAI(m, y)
	y += px(40)
	A.specRect = RECT{m, y, w - m, y + px(100)}
	y = A.specRect.Bottom + px(6)
	move(A.status, m, y, w-2*m, px(20))
	y += px(26)
	bottom := h - m - bh
	txH := txHeight()
	editBottom := bottom - px(8) - txH
	move(A.edit, m, y, w-2*m, editBottom-y)
	if TX.visible {
		layoutTX(m, editBottom+px(6), w)
	}
	move(A.btnClear, m, bottom, px(90), bh)
	move(A.btnCopy, m+px(98), bottom, px(90), bh)
	move(A.btnSave, m+px(196), bottom, px(90), bh)
	move(A.chkImproved, m+px(300), bottom+px(4), w-m-px(300)-px(160)-m, px(22))
	move(OPS.chkShow, w-m-px(156), bottom+px(4), px(60), px(22))
	move(TX.chkShow, w-m-px(90), bottom+px(4), px(90), px(22))
	pInvalidateRect.Call(A.hwnd, 0, 1)
}

func wndProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case WM_SIZE:
		layout(int32(loword(lp)), int32(hiword(lp)))
		return 0
	case WM_GETMINMAXINFO:
		mm := (*MINMAXINFO)(unsafe.Pointer(lp))
		mm.PtMinTrackSize = POINT{px(860), px(410) + txHeight()}
		return 0
	case WM_PAINT:
		var ps PAINTSTRUCT
		hdc, _, _ := pBeginPaint.Call(hwnd, ptr(&ps))
		paintSpectrum(hdc)
		paintTXStrip(hdc)
		pEndPaint.Call(hwnd, ptr(&ps))
		return 0
	case WM_TIMER:
		onTimer()
		return 0
	case WM_HSCROLL:
		switch lp {
		case A.trkPitch:
			setPitch(float32(send(A.trkPitch, TBM_GETPOS, 0, 0)), false)
		case A.trkSens:
			A.sens = float32(send(A.trkSens, TBM_GETPOS, 0, 0)) / 100
			if A.dec != nil {
				A.dec.SetSensitivity(A.sens)
			}
			showSens()
		case TX.trkWPM:
			TX.wpm = int(send(TX.trkWPM, TBM_GETPOS, 0, 0))
			setText(TX.valWPM, strconv.Itoa(TX.wpm))
			if TX.keyer != nil {
				TX.keyer.SetWPM(TX.wpm)
			}
		}
		return 0
	case WM_LBUTTONDOWN, WM_MOUSEMOVE:
		if msg == WM_MOUSEMOVE && wp&1 == 0 {
			break
		}
		x, y := int32(int16(loword(lp))), int32(int16(hiword(lp)))
		r := A.specRect
		if x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom {
			f := specLo + float32(x-r.Left)/float32(r.Right-r.Left-1)*(specHi-specLo)
			setPitch(f, true)
		}
		return 0
	case WM_COMMAND:
		id, code := loword(wp), hiword(wp)
		if aiCommand(id, code) || txCommand(id, code) || opsCommand(id, code) {
			return 0
		}
		switch {
		case id == idStart:
			if A.cap.running {
				stopCapture()
			} else {
				startCapture(true)
			}
		case id == idCombo && code == CBN_SELCHANGE:
			if A.cap.running {
				stopCapture()
			}
			startCapture(true)
		case id == idOpenWav:
			if p := openDialog(); p != "" {
				decodeFile(p)
			}
		case id == idAuto:
			autoTune()
		case id == idClear:
			setText(A.edit, "")
			A.atBreak, A.pending = true, false
		case id == idCopy:
			send(A.edit, EM_SETSEL, 0, ^uintptr(0))
			send(A.edit, WM_COPY, 0, 0)
			n, _, _ := pGetWindowTextLengthW.Call(A.edit)
			send(A.edit, EM_SETSEL, n, n)
			setStatusNote("Copied to clipboard.")
		case id == idSave:
			saveLog()
		case id == idImproved:
			A.improved = send(A.chkImproved, BM_GETCHECK, 0, 0) == 1
			if A.dec != nil {
				A.dec.SetImproved(A.improved)
			}
		}
		return 0
	case WM_CTLCOLORSTATIC:
		if b, ok := opsCtlColor(wp, lp); ok {
			return b
		}
		if lp == A.edit {
			pSetTextColor.Call(wp, rgb(130, 235, 150))
			pSetBkColor.Call(wp, rgb(14, 18, 22))
			return A.brEdit
		}
	case WM_DROPFILES:
		var buf [1024]uint16
		pDragQueryFileW.Call(wp, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		pDragFinish.Call(wp)
		decodeFile(syscall.UTF16ToString(buf[:]))
		return 0
	case WM_CLOSE:
		saveSettings()
		closeTX()
		stopCapture()
		if AI.live != nil {
			AI.live.Close()
		}
	case WM_DESTROY:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

// ---- decoding ----

func startCapture(complain bool) {
	i := int(send(A.combo, CB_GETCURSEL, 0, 0))
	if i < 0 || i >= len(A.devs) {
		i = 0
	}
	d := A.devs[i]
	if err := A.cap.open(d.ID); err != nil {
		if complain {
			messageBox(A.hwnd, err.Error(), appTitle, MB_ICONWARNING)
		}
		setText(A.btnStart, "Start")
		setStatus(fmt.Sprintf("Not listening (%s). Pick an input and press Start.", d.Name))
		return
	}
	A.devName = d.Name
	A.dec = cw.NewStreamDecoder(float32(A.cap.rate), A.pitch)
	A.dec.SetSensitivity(A.sens)
	A.dec.SetImproved(A.improved)
	A.hist = A.hist[:0]
	setText(A.btnStart, "Stop")
	applyAI()
}

func stopCapture() {
	A.cap.close()
	applyAI()
	setText(A.btnStart, "Start")
	setStatus("Stopped. Press Start to listen again.")
	pInvalidateRect.Call(A.hwnd, ptr(&A.specRect), 0)
}

func onTimer() {
	updateTick()
	txTick()
	opsTick()
	if !A.cap.running {
		aiTick(nil, 0) // still collect AI results (model load, benchmark, file decode)
		return
	}
	A.buf = A.cap.poll(A.buf[:0])
	now := time.Now()
	if len(A.buf) > 0 {
		// The classic decoder always runs (it drives the KEY light and speed readout);
		// its text is shown only in classic mode.
		A.dec.Push(decoderAudio(A.buf)) // silence while we transmit
		A.hist = append(A.hist, A.buf...)
		if keep := int(A.cap.rate) * 4; len(A.hist) > keep {
			A.hist = append(A.hist[:0], A.hist[len(A.hist)-keep:]...)
		}
		if t := A.dec.TakeText(); t != "" {
			if !ops.IsNoiseText(t) {
				OPS.lastClassic = now // the classic decoder is copying real CW, not static
			}
			if AI.mode == decClassic {
				appendText(t)
				A.lastChar = now
			}
		}
	}
	aiTick(decoderAudio(A.buf), int(A.cap.rate))
	if AI.mode == decClassic && A.pending && now.Sub(A.lastChar) > breakAfter {
		appendRaw("\r\n")
		A.pending, A.atBreak = false, true
	}
	A.tick++
	if A.tick%4 == 0 {
		updateSpectrum()
		state := "no signal"
		if A.dec.KeyDown() || A.dec.Present() {
			state = "signal"
		}
		wpm := "--"
		if w := A.dec.WPM(); w > 0 {
			wpm = strconv.Itoa(int(w))
		}
		if AI.mode == decAI {
			setStatus(fmt.Sprintf("%s  |  ~%s WPM, %s", aiStatus(), wpm, state))
		} else {
			setStatus(fmt.Sprintf("Listening: %s  (%d Hz)     Speed: %s WPM     %s", A.devName, A.cap.rate, wpm, state))
		}
	}
	pInvalidateRect.Call(A.hwnd, ptr(&A.specRect), 0)
}

func appendText(t string) {
	opsRx(t, A.atBreak)
	if A.atBreak {
		t = strings.TrimLeft(t, " ")
		if t == "" {
			return
		}
	}
	appendRaw(t)
	A.atBreak = false
	A.pending = true
}

func appendRaw(t string) {
	n, _, _ := pGetWindowTextLengthW.Call(A.edit)
	send(A.edit, EM_SETSEL, n, n)
	sendStr(A.edit, EM_REPLACESEL, 0, t)
}

func setPitch(f float32, moveSlider bool) {
	f = float32(math.Round(float64(f)/5) * 5)
	if f < pitchMin {
		f = pitchMin
	}
	if f > pitchMax {
		f = pitchMax
	}
	if f == A.pitch {
		return
	}
	A.pitch = f
	if A.dec != nil {
		A.dec.Retune(f)
	}
	applyAI()
	if moveSlider {
		send(A.trkPitch, TBM_SETPOS, 1, uintptr(int(f)))
	}
	showPitch()
	pInvalidateRect.Call(A.hwnd, ptr(&A.specRect), 0)
}

func showPitch() {
	send(A.trkPitch, TBM_SETPOS, 1, uintptr(int(A.pitch)))
	setText(A.valPitch, fmt.Sprintf("%d Hz", int(A.pitch)))
}

func showSens() {
	send(A.trkSens, TBM_SETPOS, 1, uintptr(int(math.Round(float64(A.sens)*100))))
	setText(A.valSens, fmt.Sprintf("%d%%", int(math.Round(float64(A.sens)*100))))
}

func autoTune() {
	if !A.cap.running || len(A.hist) < int(A.cap.rate) {
		setStatusNote("Auto tune needs a second or two of audio. Press Start and let it listen.")
		return
	}
	f, ratio := cw.FindPitch(A.hist, float32(A.cap.rate), pitchMin, pitchMax, 5)
	if ratio < 6 {
		setStatusNote("No clear CW tone found. Click the signal in the spectrum instead.")
		return
	}
	setPitch(f, true)
}

func updateSpectrum() {
	rate := float32(A.cap.rate)
	n := int(rate * 0.3)
	if len(A.hist) < n {
		return
	}
	p := cw.Spectrum(A.hist[len(A.hist)-n:], rate, A.freqs)
	sorted := append([]float32(nil), p...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	med := sorted[len(sorted)/2]
	if med < 1e-12 {
		med = 1e-12
	}
	for i, v := range p {
		db := float32(10 * math.Log10(float64(v/med)+1e-12))
		if db < 0 {
			db = 0
		}
		if db > 40 {
			db = 40
		}
		if db > A.spec[i] {
			A.spec[i] = db
		} else {
			A.spec[i] = A.spec[i]*0.6 + db*0.4
		}
	}
}

func decodeFile(path string) {
	samples, rate, err := cw.ReadWAV(path)
	if err != nil {
		messageBox(A.hwnd, "Could not read "+filepath.Base(path)+":\n"+err.Error(), appTitle, MB_ICONWARNING)
		return
	}
	sr := float32(rate)
	head := samples
	if max := int(sr * 60); len(head) > max {
		head = head[:max]
	}
	pitch := A.pitch
	if f, ratio := cw.FindPitch(head, sr, pitchMin, pitchMax, 5); ratio >= 6 {
		pitch = f
	}
	if AI.mode == decAI {
		decodeFileAI(path, samples, rate, pitch)
		return
	}
	d := cw.NewStreamDecoder(sr, pitch)
	d.SetSensitivity(A.sens)
	d.SetImproved(A.improved)
	var out strings.Builder
	chunk := int(sr)
	for i := 0; i < len(samples); i += chunk {
		e := i + chunk
		if e > len(samples) {
			e = len(samples)
		}
		d.Push(samples[i:e])
		out.WriteString(d.TakeText())
	}
	d.Push(make([]float32, int(sr*1.5))) // trailing silence flushes the last character
	out.WriteString(d.TakeText())
	text := strings.TrimSpace(out.String())
	if text == "" {
		text = "(nothing decoded; try a different sensitivity, or set the pitch and decode again)"
	}
	if !A.atBreak {
		appendRaw("\r\n")
	}
	appendRaw(fmt.Sprintf("--- %s  (%d Hz, ~%d WPM) ---\r\n%s\r\n",
		filepath.Base(path), int(pitch), d.WPM(), text))
	A.atBreak, A.pending = true, false
}

// ---- spectrum strip ----

func paintSpectrum(hdc uintptr) {
	r := A.specRect
	w, h := r.Right-r.Left, r.Bottom-r.Top
	if w <= 0 || h <= 0 {
		return
	}
	mdc, _, _ := pCreateCompatibleDC.Call(hdc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(h))
	oldBmp, _, _ := pSelectObject.Call(mdc, bmp)

	fill := func(x0, y0, x1, y1 int32, b uintptr) {
		rc := RECT{x0, y0, x1, y1}
		pFillRect.Call(mdc, ptr(&rc), b)
	}
	fx := func(f float32) int32 { return int32(float32(w-1) * (f - specLo) / (specHi - specLo)) }
	plotH := h - px(16)

	fill(0, 0, w, h, A.brBg)
	half := float32(30) // classic decoder's effective listening width
	if AI.mode == decAI {
		half = 400 // AI hears the whole 400-1200 Hz band...
		if AI.focus {
			half = float32(deepcw.FocusWidth / 2) // ...unless focused on the pitch
		}
	}
	if AI.mode == decAI && !AI.focus {
		fill(fx(400), 0, fx(1200), plotH, A.brPass)
	} else {
		fill(fx(A.pitch-half), 0, fx(A.pitch+half), plotH, A.brPass)
	}
	for i, f := range A.freqs {
		x0, x1 := fx(f-specStep/2), fx(f+specStep/2)-1
		if x1 <= x0 {
			x1 = x0 + 1
		}
		bh := int32(A.spec[i] / 40 * float32(plotH-px(4)))
		if bh > 0 {
			fill(x0, plotH-bh, x1, plotH, A.brBar)
		}
	}
	oldPen, _, _ := pSelectObject.Call(mdc, A.penTick)
	pMoveToEx.Call(mdc, 0, uintptr(plotH), 0)
	pLineTo.Call(mdc, uintptr(w), uintptr(plotH))
	pSetBkMode.Call(mdc, TRANSPARENT)
	pSetTextColor.Call(mdc, rgb(150, 158, 170))
	oldFont, _, _ := pSelectObject.Call(mdc, A.fontSmall)
	for f := 400; f <= 1200; f += 200 {
		x := fx(float32(f))
		pMoveToEx.Call(mdc, uintptr(x), uintptr(plotH), 0)
		pLineTo.Call(mdc, uintptr(x), uintptr(plotH+px(3)))
		s := strconv.Itoa(f)
		pTextOutW.Call(mdc, uintptr(x-px(10)), uintptr(plotH+px(2)), uintptr(unsafe.Pointer(u16(s))), uintptr(len(s)))
	}
	pSelectObject.Call(mdc, A.penPitch)
	pMoveToEx.Call(mdc, uintptr(fx(A.pitch)), 0, 0)
	pLineTo.Call(mdc, uintptr(fx(A.pitch)), uintptr(plotH))

	// key LED: bright = key down, dim = signal present, grey = nothing
	led := A.brLedOff
	if A.cap.running && A.dec != nil {
		if A.dec.KeyDown() {
			led = A.brLedOn
		} else if A.dec.Present() {
			led = A.brLedSig
		}
	}
	fill(w-px(64), px(7), w-px(50), px(21), led)
	key := "KEY"
	pTextOutW.Call(mdc, uintptr(w-px(44)), uintptr(px(6)), uintptr(unsafe.Pointer(u16(key))), uintptr(len(key)))
	hint := "click a signal to tune"
	pTextOutW.Call(mdc, uintptr(px(6)), uintptr(px(4)), uintptr(unsafe.Pointer(u16(hint))), uintptr(len(hint)))

	pSelectObject.Call(mdc, oldFont)
	pSelectObject.Call(mdc, oldPen)
	pBitBlt.Call(hdc, uintptr(r.Left), uintptr(r.Top), uintptr(w), uintptr(h), mdc, 0, 0, SRCCOPY)
	pSelectObject.Call(mdc, oldBmp)
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(mdc)
}

// ---- status, files, settings ----

var noteUntil time.Time

func setStatus(s string) {
	if time.Now().Before(noteUntil) || s == A.statusTxt {
		return
	}
	A.statusTxt = s
	setText(A.status, s)
}

func setStatusNote(s string) {
	A.statusTxt = s
	setText(A.status, s)
	noteUntil = time.Now().Add(4 * time.Second)
}

func fileDialog(save bool, defName string) string {
	buf := make([]uint16, 1024)
	copy(buf, syscall.StringToUTF16(defName))
	var filter []uint16
	var defExt *uint16
	if save {
		filter = utf16Multi("Text files (*.txt)", "*.txt", "All files", "*.*")
		defExt = u16("txt")
	} else {
		filter = utf16Multi("WAV audio (*.wav)", "*.wav", "All files", "*.*")
	}
	ofn := OPENFILENAMEW{
		HwndOwner: A.hwnd, LpstrFilter: &filter[0], NFilterIndex: 1,
		LpstrFile: &buf[0], NMaxFile: uint32(len(buf)), LpstrDefExt: defExt,
	}
	ofn.LStructSize = uint32(unsafe.Sizeof(ofn))
	var r uintptr
	if save {
		ofn.Flags = OFN_OVERWRITEPROMPT | OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR
		r, _, _ = pGetSaveFileNameW.Call(ptr(&ofn))
	} else {
		ofn.Flags = OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR
		r, _, _ = pGetOpenFileNameW.Call(ptr(&ofn))
	}
	// Windows used these for the whole (modal) dialog; keep them alive until here.
	runtime.KeepAlive(&ofn)
	runtime.KeepAlive(filter)
	runtime.KeepAlive(defExt)
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func openDialog() string { return fileDialog(false, "") }

func utf16Multi(parts ...string) []uint16 {
	var out []uint16
	for _, p := range parts {
		out = append(out, syscall.StringToUTF16(p)...) // includes the NUL
	}
	return append(out, 0)
}

func saveLog() {
	p := fileDialog(true, "cw-log-"+time.Now().Format("2006-01-02-1504")+".txt")
	if p == "" {
		return
	}
	if err := os.WriteFile(p, []byte(windowText(A.edit)), 0o644); err != nil {
		messageBox(A.hwnd, "Could not save: "+err.Error(), appTitle, MB_ICONWARNING)
		return
	}
	setStatusNote("Saved " + filepath.Base(p))
}

func settingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "CW Decoder", "settings.ini")
}

func loadSettings() {
	f, err := os.Open(settingsPath())
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if !ok {
			continue
		}
		switch k {
		case "pitch":
			if x, err := strconv.ParseFloat(v, 32); err == nil && x >= pitchMin && x <= pitchMax {
				A.pitch = float32(x)
			}
		case "sensitivity":
			if x, err := strconv.ParseFloat(v, 32); err == nil && x >= 0 && x <= 1 {
				A.sens = float32(x)
			}
		case "device":
			A.devName = v
		case "improved":
			A.improved = v != "0"
		case "decoder":
			if v == "ai" {
				AI.mode = decAI
			}
		case "aispeed":
			if x, err := strconv.Atoi(v); err == nil && x >= 0 && x < len(deepcw.SpeedNames) {
				AI.speed = deepcw.Speed(x)
			}
		case "aifocus":
			AI.focus = v != "0"
		case "txport":
			TX.portName = v
		case "txline":
			TX.useRTS = v == "rts"
		case "wpm":
			if x, err := strconv.Atoi(v); err == nil && x >= 5 && x <= 50 {
				TX.wpm = x
			}
		case "txmode":
			TX.sendOnEnter = v == "enter"
		case "sender":
			TX.visible = v != "0"
		default:
			opsLoadSetting(k, v)
		}
	}
}

func saveSettings() {
	p := settingsPath()
	if p == "" {
		return
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	imp := 0
	if A.improved {
		imp = 1
	}
	dec, foc := "classic", 0
	if AI.mode == decAI {
		dec = "ai"
	}
	if AI.focus {
		foc = 1
	}
	txl, txm, snd := "dtr", "type", 0
	if TX.useRTS {
		txl = "rts"
	}
	if TX.sendOnEnter {
		txm = "enter"
	}
	if TX.visible {
		snd = 1
	}
	s := fmt.Sprintf("pitch=%d\nsensitivity=%.2f\nimproved=%d\ndecoder=%s\naispeed=%d\naifocus=%d\ntxport=%s\ntxline=%s\nwpm=%d\ntxmode=%s\nsender=%d\ndevice=%s\n",
		int(A.pitch), A.sens, imp, dec, int(AI.speed), foc, TX.portName, txl, TX.wpm, txm, snd, A.devName)
	os.WriteFile(p, []byte(s+opsSettings()), 0o644)
}
