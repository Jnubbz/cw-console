package main

// Appliance mode: the Raspberry Pi box. The same app as on the Mac, but
//   - it listens on the box's own Wi-Fi (not just localhost), so the page needs a login
//     (password + session cookie) on top of the per-launch token;
//   - it records the rig's USB audio itself (ALSA, via arecord) instead of the browser;
//   - it sets the clock from the phone (the Pi has no clock battery by default, and POTA
//     logs need the right UTC date and time);
//   - settings, macros and logs live on the card's CWDATA partition;
//   - it never quits on its own, and the page can power the box off safely.

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	appliance bool   // running as the Pi box
	dataDir   string // overrides the support folder (the CWDATA partition)
)

// ---- box config (CWDATA/config.txt, editable on any computer) ----

const defaultBoxConfig = `# POTA Pi settings. You can edit this file on any computer (it's on the
# CWDATA drive of the SD card) or from the page (the gear button).
#
# WIFI_NAME / WIFI_PASSWORD: the box's own Wi-Fi network (password: 8-63 characters).
# PAGE_PASSWORD: needed to open the page (empty = same as WIFI_PASSWORD).
# WIFI_CHANNEL: 1-11 (2.4 GHz).
# HOME_WIFI_NAME / HOME_WIFI_PASSWORD: your home network (optional). When it's in range
#   at power-on the box joins it, and the page is at http://potapi.local; anywhere else
#   the box makes its own network as above.
# CHANGE THE PASSWORD from the default before you take the box out.

WIFI_NAME     = POTA-Pi
WIFI_PASSWORD = cwdecoder73
PAGE_PASSWORD =
WIFI_CHANNEL  = 6
HOME_WIFI_NAME     =
HOME_WIFI_PASSWORD =
`

type boxConfig struct {
	WifiName, WifiPassword, PagePassword, Channel string
	HomeName, HomePassword                        string
}

func boxConfigPath() string { return filepath.Join(dataDir, "config.txt") }

func loadBoxConfig() boxConfig {
	c := boxConfig{WifiName: "POTA-Pi", WifiPassword: "cwdecoder73", Channel: "6"}
	b, err := os.ReadFile(boxConfigPath())
	if err != nil {
		os.WriteFile(boxConfigPath(), []byte(strings.ReplaceAll(defaultBoxConfig, "\n", "\r\n")), 0o644)
		return c
	}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "WIFI_NAME":
			if v != "" {
				c.WifiName = v
			}
		case "WIFI_PASSWORD":
			if v != "" {
				c.WifiPassword = v
			}
		case "PAGE_PASSWORD":
			c.PagePassword = v
		case "HOME_WIFI_NAME":
			c.HomeName = v
		case "HOME_WIFI_PASSWORD":
			c.HomePassword = v
		case "WIFI_CHANNEL":
			if v != "" {
				c.Channel = v
			}
		}
	}
	return c
}

func (c boxConfig) pagePassword() string {
	if c.PagePassword != "" {
		return c.PagePassword
	}
	return c.WifiPassword
}

func (c boxConfig) isDefault() bool { return c.WifiPassword == "cwdecoder73" }

func validateBoxConfig(c boxConfig) error {
	if n := len(c.WifiName); n < 1 || n > 32 || strings.ContainsAny(c.WifiName, "\r\n=#") {
		return errors.New("Wi-Fi name: 1-32 characters")
	}
	if n := len(c.WifiPassword); n < 8 || n > 63 || strings.ContainsAny(c.WifiPassword, "\r\n") {
		return errors.New("Wi-Fi password: 8-63 characters")
	}
	if strings.ContainsAny(c.PagePassword, "\r\n") || len(c.PagePassword) > 63 {
		return errors.New("page password: up to 63 characters")
	}
	if len(c.HomeName) > 32 || strings.ContainsAny(c.HomeName, "\r\n\"\\") ||
		(c.HomeName != "" && strings.TrimSpace(c.HomeName) != c.HomeName) {
		return errors.New("home Wi-Fi name: up to 32 characters, no quotes or backslashes, no spaces at the ends")
	}
	if n := len(c.HomePassword); (n > 0 && n < 8) || n > 63 || strings.ContainsAny(c.HomePassword, "\r\n\"\\") {
		return errors.New("home Wi-Fi password: 8-63 characters (or empty for an open network), no quotes or backslashes")
	}
	switch c.Channel {
	case "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11":
	default:
		return errors.New("channel: 1-11")
	}
	return nil
}

func saveBoxConfig(c boxConfig) error {
	if err := validateBoxConfig(c); err != nil {
		return err
	}
	head, _, _ := strings.Cut(defaultBoxConfig, "\nWIFI_NAME")
	out := head + "\nWIFI_NAME     = " + c.WifiName + "\nWIFI_PASSWORD = " + c.WifiPassword +
		"\nPAGE_PASSWORD = " + c.PagePassword + "\nWIFI_CHANNEL  = " + c.Channel +
		"\nHOME_WIFI_NAME     = " + c.HomeName + "\nHOME_WIFI_PASSWORD = " + c.HomePassword + "\n"
	tmp := boxConfigPath() + ".new"
	if err := os.WriteFile(tmp, []byte(strings.ReplaceAll(out, "\n", "\r\n")), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, boxConfigPath()); err != nil {
		return err
	}
	syncDisk()
	return nil
}

// ---- login sessions ----

type sessions struct {
	mu   sync.Mutex
	ids  map[string]time.Time
	fail time.Time
}

func newSessions() *sessions { return &sessions{ids: map[string]time.Time{}} }

const sessionCookie = "cwsession"

func (ss *sessions) valid(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	t, ok := ss.ids[c.Value]
	if !ok || time.Since(t) > 30*24*time.Hour {
		return false
	}
	return true
}

func (ss *sessions) login(w http.ResponseWriter, password, want string) bool {
	ss.mu.Lock()
	wait := time.Until(ss.fail.Add(time.Second))
	ss.mu.Unlock()
	if wait > 0 { // one guess per second, at most
		time.Sleep(wait)
	}
	if subtle.ConstantTimeCompare([]byte(password), []byte(want)) != 1 {
		ss.mu.Lock()
		ss.fail = time.Now()
		ss.mu.Unlock()
		return false
	}
	b := make([]byte, 24)
	rand.Read(b)
	id := hex.EncodeToString(b)
	ss.mu.Lock()
	ss.ids[id] = time.Now()
	ss.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 3600})
	return true
}

// forgetAll logs everyone out (after a password change).
func (ss *sessions) forgetAll() {
	ss.mu.Lock()
	ss.ids = map[string]time.Time{}
	ss.mu.Unlock()
}

const loginPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1"><title>POTA Pi</title>
<meta name="theme-color" content="#121a17"><link rel="icon" href="/img/badge32.png"><link rel="apple-touch-icon" href="/img/badge180.png">
<style>@font-face{font-family:"Plex Cond";src:url(/fonts/ibm-plex-sans-condensed-latin-400-normal.woff2) format("woff2");font-weight:400}
@font-face{font-family:"Plex Cond";src:url(/fonts/ibm-plex-sans-condensed-latin-600-normal.woff2) format("woff2");font-weight:600}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:#121a17;color:#e3ebe6;
font:16px "Plex Cond",system-ui,sans-serif}form{background:#19231f;border:1px solid #2a3832;border-radius:12px;
padding:24px;width:min(340px,90vw);display:flex;flex-direction:column;align-items:stretch;gap:12px}
img{width:132px;height:132px;align-self:center;margin-bottom:4px}h1{font-size:22px;margin:0;font-weight:600;text-align:center}
input,button{font:inherit;padding:11px;border-radius:8px;border:1px solid #2a3832;background:#0c1210;color:#e3ebe6}
input:focus-visible,button:focus-visible{outline:2px solid #e2ae4a;outline-offset:1px}
button{background:#23573c;border-color:#2f7a52;cursor:pointer;font-weight:600}p{margin:0;color:#ff8a80;text-align:center}p:empty{display:none}</style></head>
<body><form method="post" action="/login"><img src="/img/badge256.png" alt=""><h1>POTA Pi</h1>
<input type="password" name="password" placeholder="Password" aria-label="Password" autofocus autocomplete="current-password">
<button type="submit">Open</button><p>{{MSG}}</p></form></body></html>`

// ---- local audio (ALSA) ----

type audioDev struct {
	ID    string `json:"id"`    // plughw:CARD=CODEC,DEV=0
	Label string `json:"label"` // USB Audio CODEC
}

// listAudioDevs reads the sound cards the kernel knows about, USB ones first.
func listAudioDevs() []audioDev {
	out := []audioDev{}
	b, err := os.ReadFile(cardsPath)
	if err != nil {
		return out
	}
	// lines like: " 1 [CODEC          ]: USB-Audio - USB Audio CODEC"
	for _, line := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(line)
		i, j := strings.Index(l, "["), strings.Index(l, "]")
		if i < 0 || j < i {
			continue
		}
		id := strings.TrimSpace(l[i+1 : j])
		desc := strings.TrimSpace(l[j+1:])
		desc = strings.TrimPrefix(desc, ":")
		driver, label, _ := strings.Cut(desc, " - ")
		driver = strings.TrimSpace(driver)
		if strings.Contains(driver, "vc4") || strings.Contains(strings.ToLower(label), "hdmi") {
			continue // the Pi's HDMI audio can't record
		}
		out = append(out, audioDev{ID: "plughw:CARD=" + id + ",DEV=0", Label: strings.TrimSpace(label)})
	}
	sort.SliceStable(out, func(a, b int) bool {
		return strings.Contains(out[a].Label, "USB") && !strings.Contains(out[b].Label, "USB")
	})
	return out
}

// capture runs arecord on the chosen device and feeds the engine; it restarts by itself
// when the rig is unplugged and plugged back in.
type capture struct {
	mu      sync.Mutex
	want    string // device the user picked ("" = first USB one)
	on      bool
	running string // device actually recording ("" if none)
	err     string
	stop    chan struct{}
	pause   chan struct{} // closed while paused (FT8 transmitting)
	paused  bool
	lastDev string // last device that recorded
}

// setPaused stops recording for a while (the rig's sound card is busy playing our FT8
// transmission; some USB codecs reset when both directions run at once) and resumes.
func (c *capture) setPaused(p bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p == c.paused {
		return
	}
	c.paused = p
	if p {
		if c.pause != nil {
			close(c.pause)
		}
	} else {
		c.pause = make(chan struct{})
	}
}

func (c *capture) pauseCh() (chan struct{}, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pause == nil && !c.paused {
		c.pause = make(chan struct{})
	}
	return c.pause, c.paused
}

// device: the rig's sound card (recording now, or the last one that did).
func (c *capture) device() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running != "" {
		return c.running
	}
	return c.lastDev
}

const captureRate = 16000 // plenty for CW (the AI uses 3200 Hz), light on the CPU

func (c *capture) start(e *Engine, dev string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.on && c.stop != nil {
		close(c.stop)
	}
	c.want, c.on, c.stop = dev, true, make(chan struct{})
	go c.loop(e, c.stop)
}

func (c *capture) halt() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.on && c.stop != nil {
		close(c.stop)
	}
	c.on, c.stop, c.running = false, nil, ""
}

func (c *capture) status() (on bool, running, err, want string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.on, c.running, c.err, c.want
}

func (c *capture) loop(e *Engine, stop chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		pause, paused := c.pauseCh()
		if paused {
			if sleepOrStop(stop, 50*time.Millisecond) {
				return
			}
			continue
		}
		dev := c.want
		if devs := listAudioDevs(); dev == "" || !hasDev(devs, dev) {
			dev = autoAudioDev(e.keyPort(), devs) // the radio's own sound card
		}
		if dev == "" {
			c.setState("", "No audio input found. Plug in the radio's USB cable.")
			if sleepOrStop(stop, 2*time.Second) {
				return
			}
			continue
		}
		err := c.record(e, dev, stop, pause)
		select {
		case <-stop:
			return
		case <-pause:
			c.setState("", "")
			continue // paused on purpose: no error, no retry delay
		default:
		}
		msg := "Audio input stopped"
		if err != nil {
			msg += ": " + err.Error()
		}
		c.setState("", msg+" (retrying)")
		if sleepOrStop(stop, 2*time.Second) {
			return
		}
	}
}

func hasDev(devs []audioDev, id string) bool {
	for _, d := range devs {
		if d.ID == id {
			return true
		}
	}
	return false
}

func sleepOrStop(stop chan struct{}, d time.Duration) bool {
	select {
	case <-stop:
		return true
	case <-time.After(d):
		return false
	}
}

func (c *capture) setState(running, err string) {
	c.mu.Lock()
	c.running, c.err = running, err
	c.mu.Unlock()
}

func (c *capture) record(e *Engine, dev string, stop, pause chan struct{}) error {
	cmd := exec.Command(arecordPath, "-q", "-D", dev, "-f", "S16_LE", "-c", "1",
		"-r", itoa(captureRate), "-t", "raw", "--buffer-time=200000")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{&stderr, 2000}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-stop:
			cmd.Process.Kill()
		case <-pause:
			cmd.Process.Kill()
		case <-done:
		}
	}()
	defer close(done)
	c.setState(dev, "")
	c.mu.Lock()
	c.lastDev = dev
	c.mu.Unlock()
	buf := make([]byte, captureRate/20*2) // 50 ms blocks
	f := make([]float32, len(buf)/2)
	for {
		if _, err := io.ReadFull(out, buf); err != nil {
			cmd.Wait()
			if s := strings.TrimSpace(stderr.String()); s != "" {
				return errors.New(firstLine(s))
			}
			return err
		}
		for i := range f {
			f[i] = float32(int16(uint16(buf[2*i])|uint16(buf[2*i+1])<<8)) / 32768
		}
		e.pushAudio(f, captureRate)
	}
}

type limitedWriter struct {
	b *strings.Builder
	n int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if w.b.Len() < w.n {
		w.b.Write(p[:min(len(p), w.n-w.b.Len())])
	}
	return len(p), nil
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// Test hooks (the automated tests run on a PC without the rig).
var (
	arecordPath = envOr("CWBOX_ARECORD", "arecord")
	cardsPath   = envOr("CWBOX_CARDS", "/proc/asound/cards")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// ---- box status ----

func cpuTempC() float64 {
	b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	var milli int
	for _, ch := range strings.TrimSpace(string(b)) {
		if ch < '0' || ch > '9' {
			break
		}
		milli = milli*10 + int(ch-'0')
	}
	return float64(milli) / 1000
}

// logFiles lists the ADIF files for download.
func logFiles() []string {
	out := []string{}
	m, _ := filepath.Glob(filepath.Join(logDir(), "*.adi"))
	for _, p := range m {
		out = append(out, filepath.Base(p))
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}
