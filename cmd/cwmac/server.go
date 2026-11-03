package main

import (
	"crypto/rand"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Jnubbz/cw-console/deepcw"
)

//go:embed web/index.html web/fonts web/img
var webFS embed.FS

// workletJS taps the audio input in the browser's audio thread and hands blocks to the page.
const workletJS = `class Tap extends AudioWorkletProcessor {
  process(inputs) { const ch = inputs[0] && inputs[0][0]; if (ch) this.port.postMessage(ch.slice(0)); return true; }
}
registerProcessor("cw-tap", Tap);
`

// ---- event hub (server-sent events) ----

type event struct {
	T     string    `json:"t"`
	S     string    `json:"s"` // always present (an empty history is still a history)
	State *stateMsg `json:"state,omitempty"`
}

type hub struct {
	mu       sync.Mutex
	subs     map[chan []byte]struct{}
	lastSeen time.Time
}

func newHub() *hub { return &hub{subs: map[chan []byte]struct{}{}, lastSeen: time.Now()} }

func (h *hub) broadcast(ev event) {
	b, _ := json.Marshal(ev)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs {
		select {
		case c <- b:
		default: // a stalled page: drop it; it reconnects and gets the full history
			close(c)
			delete(h.subs, c)
		}
	}
}

func (h *hub) add() chan []byte {
	c := make(chan []byte, 2048)
	h.mu.Lock()
	h.subs[c] = struct{}{}
	h.mu.Unlock()
	return c
}

func (h *hub) remove(c chan []byte) {
	h.mu.Lock()
	if _, ok := h.subs[c]; ok {
		delete(h.subs, c)
		close(c)
	}
	h.lastSeen = time.Now()
	h.mu.Unlock()
}

// idleFor is how long no page has been connected (0 while one is).
func (h *hub) idleFor() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) > 0 {
		return 0
	}
	return time.Since(h.lastSeen)
}

// ---- server ----

type server struct {
	e     *Engine
	h     *hub
	token string
	port  int
	quit  chan struct{}
	sess  *sessions // appliance: logged-in browsers
}

// hostOK: on the Mac only localhost may talk to us; the Pi box is reached by its
// Wi-Fi address, so there the login session does that job instead.
func (s *server) hostOK(r *http.Request) bool {
	if appliance {
		return true
	}
	return r.Host == "localhost:"+strconv.Itoa(s.port) || r.Host == "127.0.0.1:"+strconv.Itoa(s.port)
}

// guard rejects requests that aren't from our own page: the Host must be ours (blocks
// DNS-rebinding sites) and the per-launch token must match (blocks other pages from
// POSTing to localhost, e.g. to key your transmitter).
func (s *server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hostOK(r) || (appliance && !s.sess.valid(r)) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		tok := r.Header.Get("X-CW-Token")
		if tok == "" {
			tok = r.URL.Query().Get("token")
		}
		if tok != s.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func (s *server) routes() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if !s.hostOK(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if appliance && !s.sess.valid(r) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			io.WriteString(w, strings.Replace(loginPage, "{{MSG}}", "", 1))
			return
		}
		b, _ := webFS.ReadFile("web/index.html")
		page := strings.Replace(string(b), "{{TOKEN}}", s.token, 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, page)
	})
	m.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "cwdecoder") })
	m.HandleFunc("/api/hello", s.guard(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	m.HandleFunc("/fonts/", func(w http.ResponseWriter, r *http.Request) { // IBM Plex (SIL OFL), built in: no internet in the field
		name := strings.TrimPrefix(r.URL.Path, "/fonts/")
		if !strings.HasSuffix(name, ".woff2") || strings.ContainsAny(name, "/\\") {
			http.NotFound(w, r)
			return
		}
		b, err := webFS.ReadFile("web/fonts/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "font/woff2")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Write(b)
	})
	m.HandleFunc("/img/", func(w http.ResponseWriter, r *http.Request) { // the POTA badge (header, login, icon)
		name := strings.TrimPrefix(r.URL.Path, "/img/")
		if !strings.HasSuffix(name, ".png") || strings.ContainsAny(name, "/\\") {
			http.NotFound(w, r)
			return
		}
		b, err := webFS.ReadFile("web/img/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(b)
	})
	m.HandleFunc("/worklet.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		io.WriteString(w, workletJS)
	})

	m.HandleFunc("/api/events", s.guard(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no streaming", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		c := s.h.add()
		defer s.h.remove(c)
		s.e.mu.Lock()
		hist, _ := json.Marshal(event{T: "history", S: s.e.text.String()})
		st, _ := json.Marshal(s.e.stateLocked())
		s.e.mu.Unlock()
		fmt.Fprintf(w, "data: %s\n\ndata: %s\n\n", hist, st)
		fl.Flush()
		ping := time.NewTicker(15 * time.Second)
		defer ping.Stop()
		for {
			select {
			case b, ok := <-c:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", b)
				fl.Flush()
			case <-ping.C:
				io.WriteString(w, ": ping\n\n")
				fl.Flush()
			case <-r.Context().Done():
				return
			case <-s.quit:
				return
			}
		}
	}))

	m.HandleFunc("/api/audio", s.guard(func(w http.ResponseWriter, r *http.Request) {
		rate, _ := strconv.Atoi(r.URL.Query().Get("rate"))
		b, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil || len(b)%4 != 0 {
			http.Error(w, "bad audio", 400)
			return
		}
		f := make([]float32, len(b)/4)
		for i := range f {
			v := math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
			if v != v || v > 10 || v < -10 { // NaN or nonsense
				v = 0
			}
			f[i] = v
		}
		s.e.pushAudio(f, rate)
		w.WriteHeader(204)
	}))

	m.HandleFunc("/api/settings", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := readJSON(r, &raw); err != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.applySettings(raw)
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/clear", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.e.clearTranscript()
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/autotune", s.guard(func(w http.ResponseWriter, r *http.Request) {
		p, msg := s.e.autoTune()
		writeJSON(w, map[string]any{"pitch": p, "msg": msg})
	}))
	m.HandleFunc("/api/decodefile", s.guard(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 300<<20))
		if err != nil {
			http.Error(w, "read failed", 400)
			return
		}
		name := r.URL.Query().Get("name")
		go s.e.decodeFile(name, b)
		w.WriteHeader(202)
	}))
	m.HandleFunc("/api/benchmark", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.e.mu.Lock()
		model, batt := s.e.model, s.e.onBattery
		if model == nil {
			s.e.ensureModelLocked()
		}
		s.e.mu.Unlock()
		if model == nil {
			writeJSON(w, map[string]string{"report": "The AI model is loading; try again in a moment."})
			return
		}
		rep, err := deepcw.BenchReport(model, runtime.NumCPU(), batt)
		if err != nil {
			rep = "Benchmark failed: " + err.Error()
		}
		writeJSON(w, map[string]string{"report": rep})
	}))

	// sender
	m.HandleFunc("/api/ports", s.guard(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, listPorts()) }))
	m.HandleFunc("/api/tx/enable", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			On         bool
			Port, Line string
		}
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		err := s.e.tx.enableLocked(q.On, q.Port, q.Line)
		s.e.mu.Unlock()
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{})
	}))
	m.HandleFunc("/api/tx/type", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Append    string
			Backspace int
		}
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		if s.e.tx.keyer != nil {
			if q.Backspace > 0 {
				s.e.tx.backspaceLocked(q.Backspace)
			}
			if q.Append != "" {
				s.e.tx.typedLocked(q.Append)
			}
		}
		s.e.mu.Unlock()
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/tx/send", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Text string }
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		if s.e.tx.keyer != nil && strings.TrimSpace(q.Text) != "" {
			s.e.tx.typedLocked(strings.TrimSpace(q.Text) + " ")
		}
		s.e.mu.Unlock()
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/tx/macro", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			I    int
			Call string
		}
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		var err error
		if s.e.tx.keyer == nil {
			err = fmt.Errorf("tick 'Transmit enabled' first")
		} else {
			err = s.e.tx.macroLocked(q.I, q.Call)
		}
		s.e.mu.Unlock()
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{})
	}))
	// ---- operating aids ----
	reply := func(w http.ResponseWriter, err error) {
		if err != nil {
			writeJSON(w, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, map[string]string{})
	}
	m.HandleFunc("/api/call", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Call string }
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		s.e.setCallLocked(q.Call, false)
		s.e.mu.Unlock()
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/autocq", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ On bool }
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		err := s.e.setAutoCQLocked(q.On)
		s.e.mu.Unlock()
		reply(w, err)
	}))
	m.HandleFunc("/api/log", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Call string }
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		s.e.setCallLocked(q.Call, false)
		err := s.e.logLocked(s.e.call)
		s.e.mu.Unlock()
		reply(w, err)
	}))
	m.HandleFunc("/api/log/undo", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.e.mu.Lock()
		err := s.e.undoLogLocked()
		s.e.mu.Unlock()
		reply(w, err)
	}))
	m.HandleFunc("/api/log/today", s.guard(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.e.logRows()) }))
	m.HandleFunc("/api/log/assign", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q struct{ Day, Park string }
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		s.e.mu.Lock()
		n, err := s.e.logbook.AssignPark(q.Day, q.Park)
		if err == nil {
			s.e.setNoteLocked(fmt.Sprintf("%d contacts put in the %s log", n, strings.ToUpper(strings.TrimSpace(q.Park))))
		}
		s.e.mu.Unlock()
		reply(w, err)
	}))
	m.HandleFunc("/api/log/open", s.guard(func(w http.ResponseWriter, r *http.Request) {
		os.MkdirAll(logDir(), 0o755)
		openBrowser(logDir()) // "open" shows the folder in Finder
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/rig", s.guard(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.e.rigState()) }))
	m.HandleFunc("/api/rig/do", s.guard(func(w http.ResponseWriter, r *http.Request) {
		var q rigAction
		if readJSON(r, &q) != nil {
			http.Error(w, "bad json", 400)
			return
		}
		writeJSON(w, s.e.doRig(q))
	}))
	m.HandleFunc("/api/rig/sweep", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var q sweepRequest
			if readJSON(r, &q) != nil {
				http.Error(w, "bad json", 400)
				return
			}
			if err := s.e.startSweep(q); err != nil {
				writeJSON(w, map[string]string{"error": err.Error()})
				return
			}
		}
		writeJSON(w, s.e.sweepStatus())
	}))
	m.HandleFunc("/api/rig/sweep/stop", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.e.stopSweep()
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/tx/stop", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.e.mu.Lock()
		s.e.tx.stopLocked()
		s.e.mu.Unlock()
		w.WriteHeader(204)
	}))
	m.HandleFunc("/api/macros", s.guard(func(w http.ResponseWriter, r *http.Request) {
		s.e.mu.Lock()
		defer s.e.mu.Unlock()
		if r.Method == http.MethodPost {
			b, _ := io.ReadAll(io.LimitReader(r.Body, 200_000))
			if err := s.e.tx.saveMacrosLocked(string(b)); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			w.WriteHeader(204)
			return
		}
		io.WriteString(w, s.e.tx.macroText)
	}))
	if appliance {
		s.applianceRoutes(m, reply)
	} else { // the Mac has its own clock
		m.HandleFunc("/api/clock", s.guard(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	}
	m.HandleFunc("/api/quit", s.guard(func(w http.ResponseWriter, r *http.Request) {
		if appliance {
			http.Error(w, "use power off", 400)
			return
		}
		w.WriteHeader(204)
		go func() { time.Sleep(200 * time.Millisecond); s.shutdown() }()
	}))
	return m
}

var shutdownOnce sync.Once

func (s *server) shutdown() {
	shutdownOnce.Do(func() {
		s.e.mu.Lock()
		s.e.tx.closeLocked() // key up, close the port
		s.e.mu.Unlock()
		close(s.quit)
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	})
}

func main() {
	port := flag.Int("port", 7373, "local port (73!)")
	noBrowser := flag.Bool("nobrowser", false, "don't open the browser")
	idleExit := flag.Duration("idle-exit", time.Minute, "quit after this long with no page open and transmit off")
	flag.BoolVar(&appliance, "appliance", false, "run as the Raspberry Pi box (Wi-Fi page with login, local audio)")
	flag.StringVar(&dataDir, "data", "", "folder for settings, macros and logs")
	listen := flag.String("listen", "", "address to listen on (appliance default :80)")
	flag.Parse()
	if appliance {
		runAppliance(*listen)
		return
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		// Already running? Then just show it.
		if resp, e := http.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping", *port)); e == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(b) == "cwdecoder" {
				if !*noBrowser {
					openBrowser(fmt.Sprintf("http://localhost:%d/", *port))
				}
				return
			}
		}
		if ln, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	tb := make([]byte, 16)
	rand.Read(tb)
	h := newHub()
	s := &server{h: h, token: hex.EncodeToString(tb), port: ln.Addr().(*net.TCPAddr).Port, quit: make(chan struct{})}
	s.e = newEngine(h)
	go s.e.run()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; s.shutdown() }()

	go func() { // quit once nobody has had the page open for a while (and we're not keying)
		for range time.Tick(10 * time.Second) {
			s.e.mu.Lock()
			txOn := s.e.tx.keyer != nil
			s.e.mu.Unlock()
			if !txOn && h.idleFor() > *idleExit {
				s.shutdown()
			}
		}
	}()

	url := fmt.Sprintf("http://localhost:%d/", s.port)
	fmt.Println("CW Decoder running at", url)
	if !*noBrowser {
		openBrowser(url)
	}
	srv := &http.Server{Handler: s.routes(), ReadHeaderTimeout: 10 * time.Second}
	srv.Serve(ln)
}

func itoa(i int) string { return strconv.Itoa(i) }

func ftoa(f float64, prec int) string { return strconv.FormatFloat(f, 'f', prec, 64) }
