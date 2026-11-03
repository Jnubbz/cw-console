package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// runAppliance starts the box: no browser, no idle exit, login required.
func runAppliance(listen string) {
	if listen == "" {
		listen = ":80"
	}
	if dataDir == "" {
		dataDir = "/data"
	}
	os.MkdirAll(dataDir, 0o755)
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tb := make([]byte, 16)
	rand.Read(tb)
	h := newHub()
	s := &server{h: h, token: hex.EncodeToString(tb), port: ln.Addr().(*net.TCPAddr).Port,
		quit: make(chan struct{}), sess: newSessions()}
	s.e = newEngine(h)
	s.e.box = loadBoxConfig()
	s.e.cap.start(s.e, s.e.st.AudioDevice) // listen from the moment it boots
	go s.e.run()
	go runMDNS()     // http://potapi.local on the home network
	go watchRigHID() // stop usbhid resetting the rig's sound chip
	go func() {      // keep the remembered time fresh (see saveLastClock)
		for range time.Tick(10 * time.Minute) {
			if time.Now().Year() >= 2025 {
				saveLastClock()
			}
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; s.shutdown() }()

	fmt.Println("CW Decoder box running on", listen)
	// IdleTimeout: drop kept-alive connections nobody uses (phones that left the Wi-Fi).
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	srv.Serve(ln)
}

func (s *server) applianceRoutes(m *http.ServeMux, reply func(http.ResponseWriter, error)) {
	m.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		r.ParseForm()
		s.e.mu.Lock()
		want := s.e.box.pagePassword()
		s.e.mu.Unlock()
		if !s.sess.login(w, r.PostFormValue("password"), want) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, strings.Replace(loginPage, "{{MSG}}", "Wrong password.", 1))
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	// The page sends the phone's clock; the box has no clock of its own unless a
	// battery is fitted, and log times must be right.
	m.HandleFunc("/api/clock", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Now int64 }
		if readJSON(r, &q) != nil || q.Now < 1_700_000_000_000 {
			http.Error(w, "bad time", 400)
			return
		}
		t := time.UnixMilli(q.Now)
		if d := time.Since(t); d > 500*time.Millisecond || d < -500*time.Millisecond { // FT8 needs it close
			if err := setClock(t); err != nil {
				reply(w, err)
				return
			}
		}
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/ft8", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost { // clear
			s.e.ft8.clear()
			w.WriteHeader(204)
			return
		}
		writeJSON(w, s.e.ft8.list())
	}))
	m.HandleFunc("/api/ft8/tx", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ On bool }
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		port, line, mode := s.e.st.TxPort, effectivePTT(s.e.st.TxLine, s.e.st.FT8PTTLine), s.e.st.Mode
		s.e.mu.Unlock()
		if q.On && mode != "ft8" {
			reply(w, errors.New("switch to FT8 first"))
			return
		}
		s.e.mu.Lock()
		s.e.ft8tx.setCATType(s.e.st.CATType)
		s.e.mu.Unlock()
		reply(w, s.e.ft8tx.enable(q.On, port, line))
	}))
	m.HandleFunc("/api/ft8/cq", s.guard(func(w http.ResponseWriter, r *http.Request) {
		reply(w, s.e.ft8tx.startCQ(s.e.ft8.offsetNow()))
	}))
	m.HandleFunc("/api/ft8/call", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Msg, Slot string
			SNR       int
		}
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		reply(w, s.e.ft8tx.call(q.Msg, q.Slot, q.SNR))
	}))
	m.HandleFunc("/api/ft8/tune", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			On   bool
			Mode string
		}
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		reply(w, s.e.ft8tx.tune(q.On, q.Mode))
	}))
	m.HandleFunc("/api/box/syslog", s.guard(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "POTA Pi %s, system %s, up %s\n\n", appVersion, strings.ToUpper(currentSlot()), uptime())
		mode, ip := wifiStatus()
		fmt.Fprintf(w, "--- Wi-Fi: %s %s ---\n", mode, ip)
		for _, f := range []string{"/tmp/wifi.log", "/tmp/wpa.log", "/tmp/udhcpc.log", "/tmp/hostapd.log"} {
			b, _ := os.ReadFile(f)
			fmt.Fprintf(w, "[%s]\n", filepath.Base(f))
			w.Write(tailLines(b, 40))
		}
		w.Write(runOut("iw", "dev"))
		fmt.Fprint(w, "\n--- kernel (last 120 lines) ---\n")
		w.Write(tailLines(runOut("dmesg"), 120))
		fmt.Fprint(w, "\n--- app (last 80 lines) ---\n")
		b, _ := os.ReadFile("/tmp/cw-decoder.log")
		w.Write(tailLines(b, 80))
		fmt.Fprint(w, "\n--- rig sound chip HID ---\n"+hidStatus())
		fmt.Fprint(w, "\n--- rig sound chip mixer ---\n"+mixerStatus(s.e.cap.device()))
		fmt.Fprint(w, "\n--- last FT8 playback ---\n"+s.e.ft8tx.lastDiag())
		fmt.Fprint(w, "\n--- sound cards ---\n")
		b, _ = os.ReadFile(cardsPath)
		w.Write(b)
		ll, _ := os.ReadFile("/sys/module/snd_usb_audio/parameters/lowlatency")
		qf, _ := os.ReadFile("/sys/module/snd_usb_audio/parameters/quirk_flags")
		as, _ := os.ReadFile("/sys/module/usbcore/parameters/autosuspend")
		fmt.Fprintf(w, "snd_usb_audio lowlatency=%s quirk_flags=%s; usbcore autosuspend=%s\n",
			strings.TrimSpace(string(ll)), strings.TrimSpace(string(qf)), strings.TrimSpace(string(as)))
		streams, _ := filepath.Glob("/proc/asound/card*/stream*")
		for _, f := range streams {
			b, _ = os.ReadFile(f)
			fmt.Fprintf(w, "\n--- %s ---\n%s", f, b)
		}
	}))
	m.HandleFunc("/api/cat", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Cmd string }
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		c := s.e.catLocked()
		s.e.mu.Unlock()
		a, err := s.e.rig.raw(c, q.Cmd)
		if err != nil {
			reply(w, err)
			return
		}
		writeJSON(w, map[string]string{"answer": a, "port": c.Port, "baud": strconv.Itoa(c.Baud)})
	}))
	m.HandleFunc("/api/radio", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Action, Name, To string }
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		reply(w, s.e.radioAction(q.Action, q.Name, q.To))
	}))
	m.HandleFunc("/api/ft8/bench", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			reply(w, s.e.ft8.benchStart(s.e.ft8tx.isEnabled()))
			return
		}
		b := s.e.ft8.benchState()
		writeJSON(w, map[string]any{"running": b.Running, "rows": len(b.Rows), "done": b.Done,
			"text": b.text(), "note": b.Note, "tempC": cpuTempC()})
	}))
	m.HandleFunc("/api/ft8/log", s.guard(func(w http.ResponseWriter, r *http.Request) {
		l, err := s.e.ft8tx.logCandidate()
		if err != nil {
			reply(w, err)
			return
		}
		s.e.logFT8(l, true)
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/ft8/stop", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.e.ft8tx.stop()
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/capture", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			On  bool
			Dev string
		}
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		if q.On {
			s.e.cap.start(s.e, q.Dev)
			s.e.mu.Lock()
			s.e.st.AudioDevice = q.Dev
			s.e.saveSettingsLocked()
			s.e.mu.Unlock()
		} else {
			s.e.cap.halt()
		}
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/box", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			s.e.mu.Lock()
			c := s.e.box
			s.e.mu.Unlock()
			mode, ip := wifiStatus()
			writeJSON(w, map[string]any{"wifiName": c.WifiName, "channel": c.Channel,
				"version": appVersion, "slot": currentSlot(), "homeName": c.HomeName,
				"homeSet": c.HomePassword != "", "wifiMode": mode, "ip": ip})
			return
		}
		var q struct {
			WifiName, WifiPassword, PagePassword, Channel string
			HomeName, HomePassword                        string
			KeepHome                                      bool // keep the saved home password
		}
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		old := s.e.box
		s.e.mu.Unlock()
		c := boxConfig{WifiName: strings.TrimSpace(q.WifiName), WifiPassword: q.WifiPassword,
			PagePassword: q.PagePassword, Channel: strings.TrimSpace(q.Channel),
			HomeName: strings.TrimSpace(q.HomeName), HomePassword: q.HomePassword}
		if c.WifiPassword == "" { // blank = unchanged
			c.WifiPassword = old.WifiPassword
		}
		if q.KeepHome && c.HomePassword == "" && c.HomeName == old.HomeName {
			c.HomePassword = old.HomePassword
		}
		if err := saveBoxConfig(c); err != nil {
			reply(w, err)
			return
		}
		s.e.mu.Lock()
		s.e.box = c
		s.e.mu.Unlock()
		if c.pagePassword() != old.pagePassword() {
			s.sess.forgetAll() // everyone logs in again with the new password
		}
		writeJSON(w, map[string]string{})
		go func() { time.Sleep(time.Second); restartHotspot() }()
	}))
	m.HandleFunc("/api/power", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.e.mu.Lock()
		s.e.tx.closeLocked() // key up, port closed
		s.e.mu.Unlock()
		s.e.cap.halt()
		w.WriteHeader(204)
		go func() { time.Sleep(500 * time.Millisecond); powerOff() }()
	}))
	m.HandleFunc("/api/update/ready", s.guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"blocked": updateBlocked()})
	}))
	m.HandleFunc("/api/update", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "post the update file", 405)
			return
		}
		note := func(msg string) { s.e.mu.Lock(); s.e.setNoteLocked(msg); s.e.mu.Unlock() }
		note("Installing the update...")
		// An upload that stalls (the phone left the Wi-Fi mid-way) must not hold the
		// update lock forever: give up after 30 s without data.
		body := &idleReader{r: http.MaxBytesReader(w, r.Body, 400<<20), rc: http.NewResponseController(w), idle: 30 * time.Second}
		// A new upload replaces one still hanging (e.g. the phone left the Wi-Fi).
		upMu.Lock()
		if upCur != nil {
			upCur.cancel()
		}
		upCur = body
		upMu.Unlock()
		defer func() {
			upMu.Lock()
			if upCur == body {
				upCur = nil
			}
			upMu.Unlock()
		}()
		ver, err := applyUpdate(body, note)
		if err != nil {
			// Read the rest of the upload before answering: if the box stops reading, the
			// browser only sees a dropped connection, never the reason.
			io.Copy(io.Discard, body)
			note("Update failed: " + err.Error())
			reply(w, err)
			return
		}
		note("Update " + ver + " installed. Restarting to try it...")
		writeJSON(w, map[string]string{"version": ver})
		go func() {
			time.Sleep(time.Second)
			s.e.mu.Lock()
			s.e.tx.closeLocked() // key up, port closed
			s.e.mu.Unlock()
			s.e.cap.halt()
			if err := trialReboot(); err != nil {
				note("Couldn't restart: " + err.Error())
			}
		}()
	}))
	m.HandleFunc("/api/log/file", s.guard(func(w http.ResponseWriter, r *http.Request) {
		name := filepath.Base(r.URL.Query().Get("name"))
		if !strings.HasSuffix(name, ".adi") || strings.HasPrefix(name, ".") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(name, `"`, "")+`"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeFile(w, r, filepath.Join(logDir(), name))
	}))
}

func runOut(name string, args ...string) []byte {
	b, _ := exec.Command(name, args...).CombinedOutput()
	return b
}

func tailLines(b []byte, n int) []byte {
	lines := strings.SplitAfter(string(b), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return []byte(strings.Join(lines, ""))
}

func uptime() string {
	b, _ := os.ReadFile("/proc/uptime")
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return "?"
	}
	sec, _ := strconv.ParseFloat(f[0], 64)
	return (time.Duration(sec) * time.Second).String()
}

var (
	upMu  sync.Mutex
	upCur *idleReader // the upload being received
)

type idleReader struct {
	r        io.Reader
	rc       *http.ResponseController
	idle     time.Duration
	mu       sync.Mutex
	canceled bool
}

func (i *idleReader) cancel() {
	i.mu.Lock()
	i.canceled = true
	i.mu.Unlock()
	i.rc.SetReadDeadline(time.Now()) // wakes a blocked Read
}

func (i *idleReader) Read(p []byte) (int, error) {
	i.mu.Lock()
	c := i.canceled
	i.mu.Unlock()
	if c {
		return 0, errors.New("replaced by a newer upload")
	}
	i.rc.SetReadDeadline(time.Now().Add(i.idle))
	n, err := i.r.Read(p)
	if err != nil && errors.Is(err, os.ErrDeadlineExceeded) {
		i.mu.Lock()
		c = i.canceled
		i.mu.Unlock()
		if c {
			err = errors.New("replaced by a newer upload")
		} else {
			err = errors.New("the upload stalled (did the phone leave the box's Wi-Fi?); try again")
		}
	}
	return n, err
}
