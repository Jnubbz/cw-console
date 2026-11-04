package main

// Spots for the band strip: RBN skimmers and POTA activators (package spots), started the
// first time the page asks and kept running while spots are on. RBN logs in with MYCALL
// from the macros; if that changes, it logs in again.

import (
	"context"
	"net/http"
	"strings"

	"github.com/Jnubbz/cw-console/spots"
)

// stripBands: the CW part of each band, kHz, the strip's scale.
var stripBands = []struct {
	Band     string
	From, To float64
}{
	{"160m", 1800, 1850}, {"80m", 3500, 3600}, {"40m", 7000, 7070}, {"30m", 10100, 10140},
	{"20m", 14000, 14070}, {"17m", 18068, 18100}, {"15m", 21000, 21070}, {"12m", 24890, 24930},
	{"10m", 28000, 28080}, {"6m", 50000, 50100},
}

type spotFeeds struct {
	store  *spots.Store
	cancel context.CancelFunc
	call   string
	on     bool
}

// spotsUpLocked starts (or stops, or re-logs in) the feeds to match the settings.
func (e *Engine) spotsUpLocked() {
	f := &e.feeds
	want := e.st.SpotsOn
	call := strings.ToUpper(strings.TrimSpace(e.tx.ctx.MyCall))
	if f.store == nil {
		f.store = spots.NewStore()
	}
	if f.on && (!want || call != f.call) {
		f.cancel()
		f.on = false
	}
	if !want || f.on {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel, f.on, f.call = cancel, true, call
	f.store.MyCall = call
	go f.store.RunPOTA(ctx, envOr("CWCONSOLE_POTA_URL", spots.POTAURL), nil) // (the overrides are for testing)
	if call != "" {                                                          // the RBN wants a call to log in
		go f.store.RunRBN(ctx, envOr("CWCONSOLE_RBN_ADDR", spots.RBNAddr), call)
	}
}

type spotsReply struct {
	Band   string        `json:"band"`
	From   float64       `json:"fromKHz"`
	To     float64       `json:"toKHz"`
	Spots  []spots.Spot  `json:"spots"`
	Heard  []spots.Heard `json:"heard"`
	Status spots.Status  `json:"status"`
	MyCall string        `json:"myCall"`
	On     bool          `json:"on"`
	Bands  []string      `json:"bands"`
}

func (e *Engine) spotsFor(band string) spotsReply {
	e.mu.Lock()
	e.spotsUpLocked()
	st, on := e.feeds.store, e.st.SpotsOn
	r := spotsReply{MyCall: e.feeds.call, On: on, Spots: []spots.Spot{}, Heard: []spots.Heard{}}
	e.mu.Unlock()
	for _, b := range stripBands {
		r.Bands = append(r.Bands, b.Band)
		if b.Band == band {
			r.Band, r.From, r.To = b.Band, b.From, b.To
		}
	}
	if !on {
		return r
	}
	r.Status = st.Status()
	if h := st.HeardMe(); len(h) > 0 {
		r.Heard = h
	}
	if r.Band != "" {
		if s := st.Spots(r.From, r.To); len(s) > 0 {
			r.Spots = s
		}
	}
	return r
}

func (s *server) spotRoutes(m *http.ServeMux) {
	m.HandleFunc("/api/spots", s.guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.e.spotsFor(r.URL.Query().Get("band")))
	}))
}
