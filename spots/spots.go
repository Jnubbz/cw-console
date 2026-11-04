// Package spots collects who's on the air: Reverse Beacon Network skimmer spots (CW) and
// POTA activator spots, kept for a while and grouped by station, for the band strip and the
// "heard by" line (skimmers that copied your own call).
//
//   - RBN: telnet.reversebeacon.net port 7000 (CW and RTTY skimmers; log in with your
//     call). Lines look like
//     DX de W3LPL-#:   14025.0  KN0BLE       CW    24 dB  22 WPM  CQ      1802Z
//   - POTA: https://api.pota.app/spot/activator (no key), polled once a minute: an array of
//     {activator, reference, frequency (kHz string), mode, name, spotTime (UTC),
//     spotter, comments, grid6/grid4, locationDesc}.
package spots

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Spot: one station on one frequency, merged from every report of it.
type Spot struct {
	Call     string    `json:"call"`
	KHz      float64   `json:"khz"`
	Source   string    `json:"source"` // "rbn" | "pota" (a POTA spot wins: it carries the park)
	SNR      int       `json:"snr"`    // best skimmer SNR, dB (RBN)
	WPM      int       `json:"wpm,omitempty"`
	Skimmers int       `json:"skimmers,omitempty"` // how many skimmers heard it (RBN)
	Kind     string    `json:"kind,omitempty"`     // RBN: CQ, BEACON, NCDXF B, DX
	Park     string    `json:"park,omitempty"`     // POTA reference
	ParkName string    `json:"parkName,omitempty"`
	Location string    `json:"location,omitempty"` // POTA locationDesc (US-FL)
	Spotter  string    `json:"spotter,omitempty"`
	Time     time.Time `json:"time"`
	spotters map[string]bool
}

// Heard: one skimmer that copied your call.
type Heard struct {
	Skimmer string    `json:"skimmer"`
	KHz     float64   `json:"khz"`
	SNR     int       `json:"snr"`
	WPM     int       `json:"wpm"`
	Time    time.Time `json:"time"`
}

// Store keeps recent spots. Safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	spots  map[string]*Spot // key: call + 1 kHz slot
	heard  map[string]Heard // your call, by skimmer
	MyCall string
	Keep   time.Duration // how long a spot stays (default 15 min)
	now    func() time.Time
	rbnUp  bool
	rbnErr string
	potaOK time.Time
}

func NewStore() *Store {
	return &Store{spots: map[string]*Spot{}, heard: map[string]Heard{}, Keep: 15 * time.Minute, now: time.Now}
}

func key(call string, khz float64) string { return call + "@" + strconv.Itoa(int(khz+0.5)) }

// AddRBN records a skimmer spot.
func (s *Store) AddRBN(r RBNSpot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.MyCall != "" && strings.EqualFold(baseCall(r.Call), baseCall(s.MyCall)) {
		s.heard[r.Skimmer] = Heard{Skimmer: r.Skimmer, KHz: r.KHz, SNR: r.SNR, WPM: r.WPM, Time: now}
	}
	k := key(r.Call, r.KHz)
	sp := s.spots[k]
	if sp == nil {
		sp = &Spot{Call: r.Call, KHz: r.KHz, Source: "rbn", spotters: map[string]bool{}}
		s.spots[k] = sp
	}
	if sp.spotters == nil {
		sp.spotters = map[string]bool{}
	}
	sp.spotters[r.Skimmer] = true
	sp.Skimmers = len(sp.spotters)
	if r.SNR > sp.SNR || sp.Time.IsZero() {
		sp.SNR = r.SNR
	}
	if r.WPM > 0 {
		sp.WPM = r.WPM
	}
	if sp.Kind == "" || r.Kind == "CQ" {
		sp.Kind = r.Kind
	}
	sp.KHz = r.KHz
	sp.Time = now
}

// SetPOTA replaces the POTA spots (CW ones only; the strip is for CW).
func (s *Store) SetPOTA(list []POTASpot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, sp := range s.spots {
		if sp.Source == "pota" {
			delete(s.spots, k)
		}
	}
	now := s.now()
	for _, p := range list {
		if !strings.EqualFold(p.Mode, "CW") || p.KHz <= 0 {
			continue
		}
		k := key(p.Activator, p.KHz)
		sp := s.spots[k]
		if sp == nil {
			sp = &Spot{Call: p.Activator, KHz: p.KHz}
			s.spots[k] = sp
		}
		sp.Source, sp.Park, sp.ParkName, sp.Location, sp.Spotter = "pota", p.Reference, p.Name, p.Location, p.Spotter
		t := p.Time
		if t.IsZero() || t.After(now) {
			t = now
		}
		if t.After(sp.Time) {
			sp.Time = t
		}
	}
	s.potaOK = now
}

// Spots on fromKHz..toKHz, newest first, dropping old ones.
func (s *Store) Spots(fromKHz, toKHz float64) []Spot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	var out []Spot
	for _, sp := range s.spots {
		if sp.KHz >= fromKHz && sp.KHz <= toKHz {
			c := *sp
			c.spotters = nil
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// HeardMe: skimmers that copied your call recently, strongest first.
func (s *Store) HeardMe() []Heard {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	var out []Heard
	for _, h := range s.heard {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SNR > out[j].SNR })
	return out
}

func (s *Store) expireLocked() {
	cut := s.now().Add(-s.Keep)
	for k, sp := range s.spots {
		if sp.Time.Before(cut) {
			delete(s.spots, k)
		}
	}
	for k, h := range s.heard {
		if h.Time.Before(cut) {
			delete(s.heard, k)
		}
	}
}

// Status of the feeds, for the page.
type Status struct {
	RBN      bool      `json:"rbn"`
	RBNError string    `json:"rbnError,omitempty"`
	POTA     time.Time `json:"pota"` // last good POTA fetch
}

func (s *Store) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{RBN: s.rbnUp, RBNError: s.rbnErr, POTA: s.potaOK}
}

func (s *Store) setRBN(up bool, err string) {
	s.mu.Lock()
	s.rbnUp, s.rbnErr = up, err
	s.mu.Unlock()
}

// baseCall: "KN0BLE/P" -> "KN0BLE", "VE3/KN0BLE" -> "KN0BLE" (the longest part).
func baseCall(c string) string {
	best := ""
	for _, p := range strings.Split(strings.ToUpper(c), "/") {
		if len(p) > len(best) {
			best = p
		}
	}
	return best
}

// ---- RBN ----

type RBNSpot struct {
	Skimmer string
	KHz     float64
	Call    string
	Mode    string
	SNR     int
	WPM     int
	Kind    string
}

var rbnLine = regexp.MustCompile(`^DX de ([A-Z0-9/]+)(?:-#)?:\s+(\d+(?:\.\d+)?)\s+([A-Z0-9/]+)\s+(CW|RTTY)\s+(-?\d+)\s+dB\s+(\d+)\s+(WPM|BPS)\s+(.+?)\s+\d{4}Z`)

// ParseRBN reads one telnet line ("", false if it isn't a spot).
func ParseRBN(line string) (RBNSpot, bool) {
	if i := strings.Index(line, "DX de "); i > 0 { // after the login prompt the first line has its leftovers
		line = line[i:]
	}
	m := rbnLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return RBNSpot{}, false
	}
	khz, _ := strconv.ParseFloat(m[2], 64)
	snr, _ := strconv.Atoi(m[5])
	wpm, _ := strconv.Atoi(m[6])
	return RBNSpot{Skimmer: m[1], KHz: khz, Call: m[3], Mode: m[4], SNR: snr, WPM: wpm, Kind: strings.TrimSpace(m[8])}, true
}

// RBNAddr is the RBN's CW/RTTY telnet node.
const RBNAddr = "telnet.reversebeacon.net:7000"

// RunRBN stays connected (reconnecting with a back-off) until ctx ends, adding CW spots.
func (s *Store) RunRBN(ctx context.Context, addr, call string) {
	wait := 5 * time.Second
	for ctx.Err() == nil {
		start := time.Now()
		err := s.rbnSession(ctx, addr, call)
		if ctx.Err() != nil {
			break
		}
		msg := "disconnected"
		if err != nil {
			msg = err.Error()
		}
		s.setRBN(false, msg)
		if time.Since(start) > 2*time.Minute {
			wait = 5 * time.Second
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		if wait < 5*time.Minute {
			wait *= 2
		}
	}
	s.setRBN(false, "")
}

func (s *Store) rbnSession(ctx context.Context, addr, call string) error {
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("can't reach the RBN: %v", err)
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()
	r := bufio.NewReader(conn)
	// The node asks for a call ("Please enter your call:"); answer once it does (or soon).
	conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	prompt := make([]byte, 0, 256)
	for !strings.Contains(strings.ToLower(string(prompt)), "call") {
		b, err := r.ReadByte()
		if err != nil {
			return fmt.Errorf("RBN login: %v", err)
		}
		prompt = append(prompt, b)
		if len(prompt) > 4000 {
			break
		}
	}
	if _, err := io.WriteString(conn, call+"\r\n"); err != nil {
		return err
	}
	s.setRBN(true, "")
	for {
		conn.SetReadDeadline(time.Now().Add(10 * time.Minute)) // quiet bands are quiet, not dead
		line, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("RBN: %v", err)
		}
		if sp, ok := ParseRBN(line); ok && sp.Mode == "CW" {
			s.AddRBN(sp)
		}
	}
}

// ---- POTA ----

type POTASpot struct {
	Activator, Reference, Name, Mode, Spotter, Location string
	KHz                                                 float64
	Time                                                time.Time
}

// POTAURL lists the activators on the air now.
const POTAURL = "https://api.pota.app/spot/activator"

// ParsePOTA reads the activator list (rows without call, park or frequency are skipped).
func ParsePOTA(b []byte) ([]POTASpot, error) {
	var rows []map[string]any
	if err := json.Unmarshal(b, &rows); err != nil {
		return nil, err
	}
	str := func(r map[string]any, k string) string {
		if v, ok := r[k].(string); ok {
			return strings.TrimSpace(v)
		}
		return ""
	}
	var out []POTASpot
	for _, r := range rows {
		p := POTASpot{Activator: strings.ToUpper(str(r, "activator")), Reference: str(r, "reference"), Name: str(r, "name"),
			Mode: str(r, "mode"), Spotter: str(r, "spotter"), Location: str(r, "locationDesc")}
		switch f := r["frequency"].(type) {
		case string:
			p.KHz, _ = strconv.ParseFloat(strings.TrimSpace(f), 64)
		case float64:
			p.KHz = f
		}
		if t, err := time.Parse("2006-01-02T15:04:05", strings.TrimSuffix(str(r, "spotTime"), "Z")); err == nil {
			p.Time = t.UTC()
		}
		if p.Activator == "" || p.Reference == "" || p.KHz <= 0 {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// RunPOTA fetches the activator list every minute until ctx ends.
func (s *Store) RunPOTA(ctx context.Context, url string, client *http.Client) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	for ctx.Err() == nil {
		if req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil); err == nil {
			req.Header.Set("User-Agent", "cw-console")
			if resp, err := client.Do(req); err == nil {
				b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					if list, err := ParsePOTA(b); err == nil {
						s.SetPOTA(list)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Minute):
		}
	}
}
