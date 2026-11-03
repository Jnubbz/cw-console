//go:build windows

package main

// AI (DeepCW) decoder integration: decoder choice, speed setting, pitch focus,
// benchmark, and file decoding on a background goroutine. All Win32 calls stay on the
// UI thread; worker goroutines hand results back through channels drained by the timer.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"github.com/Jnubbz/cw-console/deepcw"
)

const (
	idDecoder = 201 + iota
	idSpeed
	idFocus
	idBench
)

const (
	decClassic = 0
	decAI      = 1
)

type aiState struct {
	lblDec, cmbDec, lblSpeed, cmbSpeed, chkFocus, btnBench uintptr

	mode      int
	speed     deepcw.Speed
	focus     bool
	onBattery bool

	model     *deepcw.Model
	live      *deepcw.Live
	loading   bool
	loadDone  chan loadResult
	benchDone chan string
	fileDone  chan string
	fileBusy  bool
	benching  bool
	powerAt   time.Time
}

type loadResult struct {
	m   *deepcw.Model
	err error
}

var AI = aiState{mode: decClassic, speed: deepcw.SpeedAuto, focus: true,
	loadDone: make(chan loadResult, 1), benchDone: make(chan string, 1), fileDone: make(chan string, 4)}

func modelDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "deepcw"
	}
	return filepath.Join(filepath.Dir(exe), "deepcw")
}

func createAIControls() {
	AI.lblDec = child("STATIC", "Decoder:", SS_LEFT, 0, 0)
	AI.cmbDec = child("COMBOBOX", "", CBS_DROPDOWNLIST|CBS_HASSTRINGS|WS_VSCROLL|WS_TABSTOP, idDecoder, 0)
	sendStr(AI.cmbDec, CB_ADDSTRING, 0, "Classic (light on CPU)")
	sendStr(AI.cmbDec, CB_ADDSTRING, 0, "AI - DeepCW neural net")
	send(AI.cmbDec, CB_SETCURSEL, uintptr(AI.mode), 0)
	AI.lblSpeed = child("STATIC", "AI speed:", SS_LEFT, 0, 0)
	AI.cmbSpeed = child("COMBOBOX", "", CBS_DROPDOWNLIST|CBS_HASSTRINGS|WS_VSCROLL|WS_TABSTOP, idSpeed, 0)
	for _, n := range deepcw.SpeedNames {
		sendStr(AI.cmbSpeed, CB_ADDSTRING, 0, n)
	}
	send(AI.cmbSpeed, CB_SETCURSEL, uintptr(AI.speed), 0)
	AI.chkFocus = child("BUTTON", "Focus on pitch", BS_AUTOCHECKBOX|WS_TABSTOP, idFocus, 0)
	if AI.focus {
		send(AI.chkFocus, BM_SETCHECK, 1, 0)
	}
	AI.btnBench = child("BUTTON", "Benchmark...", BS_PUSHBUTTON|WS_TABSTOP, idBench, 0)
	AI.onBattery = onBattery()
	AI.powerAt = time.Now()
	updateModeControls()
	if AI.mode == decAI {
		ensureModel()
	}
}

func layoutAI(m, y int32) {
	bh := px(28)
	move(AI.lblDec, m, y+px(5), px(62), px(20))
	x := m + px(64)
	move(AI.cmbDec, x, y+px(1), px(200), px(200))
	x += px(212)
	move(AI.lblSpeed, x, y+px(5), px(62), px(20))
	x += px(64)
	move(AI.cmbSpeed, x, y+px(1), px(170), px(220))
	x += px(180)
	move(AI.chkFocus, x, y+px(4), px(120), px(22))
	x += px(124)
	move(AI.btnBench, x, y, px(110), bh)
}

func updateModeControls() {
	ai := AI.mode == decAI
	en := func(h uintptr, on bool) {
		v := uintptr(0)
		if on {
			v = 1
		}
		pEnableWindow.Call(h, v)
	}
	en(AI.cmbSpeed, ai)
	en(AI.chkFocus, ai)
	en(AI.btnBench, ai)
	en(A.trkSens, !ai) // classic-only controls
	en(A.chkImproved, !ai)
	pInvalidateRect.Call(A.hwnd, ptr(&A.specRect), 0)
}

// aiCommand handles the AI row's controls; returns true if it was one of them.
func aiCommand(id, code uint32) bool {
	switch {
	case id == idDecoder && code == CBN_SELCHANGE:
		AI.mode = int(send(AI.cmbDec, CB_GETCURSEL, 0, 0))
		updateModeControls()
		if AI.mode == decAI {
			ensureModel()
		}
		applyAI()
		markBreak()
	case id == idSpeed && code == CBN_SELCHANGE:
		AI.speed = deepcw.Speed(send(AI.cmbSpeed, CB_GETCURSEL, 0, 0))
		applyAI()
	case id == idFocus:
		AI.focus = send(AI.chkFocus, BM_GETCHECK, 0, 0) == 1
		applyAI()
		pInvalidateRect.Call(A.hwnd, ptr(&A.specRect), 0)
	case id == idBench:
		runBenchmark()
	default:
		return false
	}
	return true
}

func ensureModel() {
	if AI.model != nil || AI.loading {
		return
	}
	AI.loading = true
	setStatusNote("Loading the AI model...")
	go func() {
		m, err := deepcw.Load(modelDir())
		AI.loadDone <- loadResult{m, err}
	}()
}

func currentPlan() deepcw.Plan {
	return deepcw.PlanFor(AI.speed, runtime.NumCPU(), AI.onBattery)
}

// applyAI pushes the current settings into the live decoder.
func applyAI() {
	if AI.live == nil {
		return
	}
	AI.live.SetEnabled(AI.mode == decAI && A.cap.running)
	AI.live.SetPlan(currentPlan())
	if AI.focus {
		AI.live.SetFocus(deepcw.DefaultFocus(float64(A.pitch)))
	} else {
		AI.live.SetFocus(nil)
	}
}

// aiTick runs on every UI timer tick with the newly captured audio.
func aiTick(samples []float32, rate int) {
	select {
	case r := <-AI.loadDone:
		AI.loading = false
		if r.err != nil {
			messageBox(A.hwnd, "The AI decoder couldn't start:\n\n"+r.err.Error()+
				"\n\nIt needs the 'deepcw' folder (model.onnx and model.onnx.json) next to CW-Decoder.exe. "+
				"Switching back to the classic decoder.", appTitle, MB_ICONWARNING)
			AI.mode = decClassic
			send(AI.cmbDec, CB_SETCURSEL, decClassic, 0)
			updateModeControls()
		} else {
			AI.model = r.m
			AI.live = deepcw.NewLive(r.m)
			applyAI()
			setStatusNote("AI model ready (" + deepcw.SIMDName() + "). Text appears after 15 s of audio.")
		}
	case msg := <-AI.benchDone:
		AI.benching = false
		pEnableWindow.Call(AI.btnBench, 1)
		messageBox(A.hwnd, msg, appTitle+" - AI benchmark", MB_ICONINFORMATION)
	case text := <-AI.fileDone:
		AI.fileBusy = false
		pEnableWindow.Call(A.btnWav, 1)
		if !A.atBreak {
			appendRaw("\r\n")
		}
		appendRaw(text)
		A.atBreak, A.pending = true, false
	default:
	}
	if time.Since(AI.powerAt) > 10*time.Second {
		AI.powerAt = time.Now()
		if b := onBattery(); b != AI.onBattery {
			AI.onBattery = b
			applyAI()
		}
	}
	if AI.live == nil {
		return
	}
	if AI.mode == decAI && len(samples) > 0 {
		AI.live.Push(samples, rate)
	}
	if t := AI.live.TakeText(); t != "" && AI.mode == decAI {
		for i, part := range strings.Split(t, "\n") {
			if i > 0 && !A.atBreak {
				appendRaw("\r\n")
				A.atBreak, A.pending = true, false
			}
			appendText(part)
		}
	}
}

// aiStatus is the status-line text in AI mode.
func aiStatus() string {
	if AI.loading {
		return "Loading the AI model..."
	}
	if AI.live == nil {
		return "AI model not loaded."
	}
	st, pass := AI.live.Status()
	p := currentPlan()
	mode := deepcw.SpeedNames[AI.speed]
	if AI.speed == deepcw.SpeedAuto {
		mode = "Auto: Balanced"
		if AI.onBattery {
			mode = "Auto: Eco (on battery)"
		}
	}
	s := fmt.Sprintf("AI %s  |  %s, %d thread(s)", st, mode, p.Threads)
	if pass > 0 {
		s += fmt.Sprintf("  |  pass %.2f s, new text every ~%.1f s", pass.Seconds(), p.Interval(pass).Seconds())
	}
	return s
}

func markBreak() {
	if !A.atBreak {
		appendRaw("\r\n")
		A.atBreak, A.pending = true, false
	}
}

func runBenchmark() {
	if AI.model == nil {
		ensureModel()
		setStatusNote("Loading the AI model first; press Benchmark again in a moment.")
		return
	}
	if AI.benching {
		return
	}
	AI.benching = true
	pEnableWindow.Call(AI.btnBench, 0)
	setStatusNote("Benchmarking the AI decoder (about 10-30 seconds)...")
	m, cores, battery := AI.model, runtime.NumCPU(), AI.onBattery
	go func() {
		rep, err := deepcw.BenchReport(m, cores, battery)
		if err != nil {
			AI.benchDone <- "Benchmark failed: " + err.Error()
			return
		}
		AI.benchDone <- rep
	}()
}

// decodeFileAI decodes a recording with the AI on a worker; the result is appended later.
func decodeFileAI(path string, samples []float32, rate uint32, pitch float32) {
	if AI.model == nil {
		ensureModel()
		messageBox(A.hwnd, "The AI model is still loading. Try the file again in a moment.", appTitle, MB_ICONINFORMATION)
		return
	}
	if AI.fileBusy {
		return
	}
	AI.fileBusy = true
	pEnableWindow.Call(A.btnWav, 0)
	setStatusNote(fmt.Sprintf("AI decoding %s (%.0f s of audio)...", filepath.Base(path), float64(len(samples))/float64(rate)))
	m, focusOn := AI.model, AI.focus
	go func() {
		var focus *deepcw.Focus
		if focusOn {
			focus = deepcw.DefaultFocus(float64(pitch))
		}
		var text string
		var err error
		t0 := time.Now()
		deepcw.Exclusive(runtime.NumCPU(), func() {
			text, err = deepcw.DecodeStream(m, samples, int(rate), 2, focus)
		})
		if err != nil {
			AI.fileDone <- fmt.Sprintf("--- %s: AI decode failed: %v ---\r\n", filepath.Base(path), err)
			return
		}
		if text == "" {
			text = "(nothing decoded)"
		}
		AI.fileDone <- fmt.Sprintf("--- %s  (AI, %d Hz focus, %.1f s) ---\r\n%s\r\n",
			filepath.Base(path), int(pitch), time.Since(t0).Seconds(), text)
	}()
}

// SYSTEM_POWER_STATUS
type powerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

var pGetSystemPowerStatus = kernel32.NewProc("GetSystemPowerStatus")

func onBattery() bool {
	var ps powerStatus
	r, _, _ := pGetSystemPowerStatus.Call(uintptr(unsafe.Pointer(&ps)))
	return r != 0 && ps.ACLineStatus == 0
}
