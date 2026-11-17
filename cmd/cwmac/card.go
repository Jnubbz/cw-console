package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jnubbz/cw-console/contest"
	"github.com/Jnubbz/cw-console/internal/secrets"
	"github.com/Jnubbz/cw-console/logbook"
	"github.com/Jnubbz/cw-console/lookup"
	"github.com/Jnubbz/cw-console/lookup/qrz"
	"github.com/Jnubbz/cw-console/ops"
)

// cardMsg is the decode line. It is built from the local log and cty.dat
// first. QRZ fields are filled when a lookup has already returned; the lookup
// itself runs on qrzLoop, never on the goroutine that keys the radio.
type cardMsg struct {
	Call    string   `json:"call"`
	Country string   `json:"country,omitempty"`
	Prefix  string   `json:"prefix,omitempty"`
	Cont    string   `json:"cont,omitempty"`
	CQ      int      `json:"cq,omitempty"`
	Worked  bool     `json:"worked"`
	Last    string   `json:"last,omitempty"` // "2006-01-02 20m CW"
	Needs   []string `json:"needs,omitempty"`
	Name    string   `json:"name,omitempty"`
	State   string   `json:"state,omitempty"`
	Grid    string   `json:"grid,omitempty"`
	DXCC    int      `json:"dxcc,omitempty"`
	QRZ     string   `json:"qrz"` // off, wait, ok, offline, miss
	Message string   `json:"message,omitempty"`
}

type qrzMemo struct {
	card lookup.Card
	err  error
}

func (e *Engine) openSecrets() (*secrets.File, error) {
	e.secOnce.Do(func() {
		e.sec, e.secErr = secrets.Open(filepath.Join(supportDir(), "secrets.json"))
	})
	return e.sec, e.secErr
}

func (e *Engine) qrzReady() bool {
	if !e.qrzFlagAt.IsZero() && time.Since(e.qrzFlagAt) < 2*time.Second {
		return e.qrzHave
	}
	st := e.qrzFlags()
	e.qrzHave = st.User && st.Password
	e.qrzFlagAt = time.Now()
	return e.qrzHave
}

// qrzStatus is what the page is allowed to know. The values stay in the file.
type qrzStatus struct {
	User     bool `json:"user"`
	Password bool `json:"password"`
}

func (e *Engine) qrzFlags() qrzStatus {
	f, err := e.openSecrets()
	if err != nil {
		return qrzStatus{}
	}
	u, _, _ := f.Get(secrets.QRZUsername)
	p, _, _ := f.Get(secrets.QRZPassword)
	return qrzStatus{User: u != "", Password: p != ""}
}

func (e *Engine) saveQRZ(user, pass string) error {
	if len(user) > 64 || len(pass) > 200 {
		return errors.New("that QRZ login is too long")
	}
	f, err := e.openSecrets()
	if err != nil {
		return err
	}
	if user != "" {
		if err := f.Set(secrets.QRZUsername, strings.TrimSpace(user)); err != nil {
			return err
		}
	}
	if pass != "" {
		if err := f.Set(secrets.QRZPassword, pass); err != nil {
			return err
		}
		// The old session belonged to the previous password.
		_ = f.Set(secrets.QRZSession, "")
	}
	e.mu.Lock()
	e.qrzFlagAt = time.Time{}
	if e.qrz != nil {
		if user != "" {
			e.qrz.User = strings.TrimSpace(user)
		}
		if pass != "" {
			e.qrz.Password = pass
			e.qrz.Session = ""
		}
	}
	e.mu.Unlock()
	return nil
}

func (e *Engine) loadQRZCache() {
	b, err := os.ReadFile(filepath.Join(supportDir(), "qrz-cache.json"))
	if err != nil || len(b) == 0 {
		return
	}
	c := &lookup.Cache{}
	if c.UnmarshalJSON(b) == nil {
		e.qrzMem = c
	}
}

func (e *Engine) saveQRZCache() {
	e.mu.Lock()
	c := e.qrzMem
	e.mu.Unlock()
	if c == nil {
		return
	}
	b, err := c.MarshalJSON()
	if err != nil {
		return
	}
	path := filepath.Join(supportDir(), "qrz-cache.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	_ = os.Chmod(tmp, 0o600)
	_ = os.Rename(tmp, path)
}

func (e *Engine) ensureQRZ() {
	e.qrzOnce.Do(func() {
		e.qrzCh = make(chan string, 4)
		e.qrz = &qrz.Client{Agent: qrz.Agent}
		if e.qrzMem == nil {
			e.qrzMem = &lookup.Cache{}
		}
		if e.qrzHit == nil {
			e.qrzHit = map[string]qrzMemo{}
		}
		if f, err := e.openSecrets(); err == nil {
			if s, ok, _ := f.Get(secrets.QRZSession); ok {
				e.qrz.Session = s
			}
		}
		e.loadQRZCache()
		go e.qrzLoop()
	})
}

// noteQRZ asks the lookup goroutine for call. It does not wait, and it does
// not run on the keyer: qrzLoop is its own goroutine.
func (e *Engine) noteQRZ(call string) {
	call = strings.ToUpper(call)
	if !ops.IsCall(call) || !e.qrzReady() {
		return
	}
	e.ensureQRZ()
	if e.qrzMem != nil {
		if _, ok := e.qrzMem.Get(call); ok {
			return
		}
	}
	if e.qrzPend[call] {
		return
	}
	if e.qrzPend == nil {
		e.qrzPend = map[string]bool{}
	}
	select {
	case e.qrzCh <- call:
		e.qrzPend[call] = true
	default:
	}
}

func (e *Engine) qrzLoop() {
	for call := range e.qrzCh {
		call = strings.ToUpper(call)
		done := false
		finish := func() {
			if done {
				return
			}
			done = true
			e.mu.Lock()
			delete(e.qrzPend, call)
			e.mu.Unlock()
		}
		f, err := e.openSecrets()
		if err != nil {
			finish()
			continue
		}
		user, _, _ := f.Get(secrets.QRZUsername)
		pass, ok, _ := f.Get(secrets.QRZPassword)
		if user == "" || !ok {
			finish()
			continue
		}
		e.mu.Lock()
		client := e.qrz
		cache := e.qrzMem
		e.mu.Unlock()
		if client == nil || cache == nil {
			finish()
			continue
		}
		client.User, client.Password = user, pass
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		card, lookErr := lookup.Resolve(ctx, cache, client, call)
		cancel()
		if lookErr != nil && pass != "" && strings.Contains(lookErr.Error(), pass) {
			lookErr = errors.New("qrz: lookup failed")
		}
		e.mu.Lock()
		if e.qrzHit == nil {
			e.qrzHit = map[string]qrzMemo{}
		}
		delete(e.qrzPend, call)
		done = true
		e.qrzHit[call] = qrzMemo{card, lookErr}
		session := ""
		if client.Session != "" {
			session = client.Session
		}
		e.mu.Unlock()
		if session != "" {
			_ = f.Set(secrets.QRZSession, session)
		}
		e.saveQRZCache()
	}
}

func (e *Engine) bookLocked() *logbook.Book {
	cty := e.contestCTY()
	from := e.ctyFrom
	if e.book != nil && e.bookSeq == e.logbook.Seq && e.bookCTY == from {
		return e.book
	}
	b := &logbook.Book{}
	for _, q := range e.logbook.Entries() {
		lq := logbook.QSO{
			Call: q.Call, Start: q.Start, Freq: q.Freq, Band: q.Band, Mode: q.Mode,
			RSTSent: q.RSTSent, RSTRcvd: q.RSTRcvd, MyCall: q.MyCall, Grid: q.Grid,
			State: q.State, MyState: q.MyState, MyGrid: q.MyGrid, DXCC: q.DXCC,
			CTYPrefix: q.CTYPrefix, Name: q.Name, Park: q.Park, Contest: q.Contest,
			CQZ: q.CQZ, MyCQZ: q.MyCQZ,
		}
		if lq.Mode == "" {
			lq.Mode = "CW"
		}
		if lq.CTYPrefix == "" && cty != nil {
			if m, ok := cty.Lookup(q.Call); ok && m.Entity != nil {
				lq.CTYPrefix = m.Entity.Prefix
			}
		}
		_ = b.Add(lq)
	}
	e.book, e.bookSeq, e.bookCTY = b, e.logbook.Seq, from
	return b
}

func (e *Engine) memoLocked(call string) (lookup.Card, error, bool) {
	call = strings.ToUpper(call)
	if e.qrzMem != nil {
		if card, ok := e.qrzMem.Get(call); ok {
			card.Source = "cache"
			return card, nil, true
		}
	}
	if e.qrzHit != nil {
		if m, ok := e.qrzHit[call]; ok {
			return m.card, m.err, true
		}
	}
	return lookup.Card{}, nil, false
}

// focusCallLocked is the call the decode line describes. In FT8 that is the
// newest decoded station. In CW it is the Call box, or the newest call the
// decoder heard.
func (e *Engine) focusCallLocked() (call, mode string) {
	my := e.tx.ctx.MyCall
	if e.st.Mode == "ft8" {
		msgs := e.ft8.recentMessages()
		for i := len(msgs) - 1; i >= 0; i-- {
			if cs := ops.FindCalls(msgs[i], my); len(cs) > 0 {
				return cs[0], "FT8"
			}
		}
	}
	if e.call != "" {
		mode = "CW"
		if e.st.Mode == "ft8" {
			mode = "FT8"
		}
		return e.call, mode
	}
	if hs := e.assist.Calls(); len(hs) > 0 {
		return hs[len(hs)-1], "CW"
	}
	return "", ""
}

func (e *Engine) interestLocked() []struct{ call, mode string } {
	my := e.tx.ctx.MyCall
	var out []struct{ call, mode string }
	seen := map[string]bool{}
	add := func(call, mode string) {
		call = strings.ToUpper(strings.TrimSpace(call))
		if call == "" || seen[call] || !ops.IsCall(call) {
			return
		}
		seen[call] = true
		out = append(out, struct{ call, mode string }{call, mode})
	}
	if c, m := e.focusCallLocked(); c != "" {
		add(c, m)
	}
	if e.call != "" {
		mode := "CW"
		if e.st.Mode == "ft8" {
			mode = "FT8"
		}
		add(e.call, mode)
	}
	for _, c := range e.assist.Calls() {
		add(c, "CW")
	}
	if e.st.Mode == "ft8" {
		for _, msg := range e.ft8.recentMessages() {
			for _, c := range ops.FindCalls(msg, my) {
				add(c, "FT8")
			}
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func (e *Engine) fillCardsLocked(s *stateMsg) {
	_, band := ops.ParseFreq(e.st.Freq)
	book := e.bookLocked()
	cty := e.contestCTY()
	ready := e.qrzReady()
	for _, it := range e.interestLocked() {
		qz, qe, qs := e.memoFor(it.call, ready)
		card, _ := assembleCard(it.call, band, it.mode, cty, book, qz, qe, qs)
		if ready {
			e.noteQRZ(it.call)
		}
		s.Cards = append(s.Cards, card)
	}
	if len(s.Cards) > 0 {
		c := s.Cards[0]
		s.Card = &c
	}
}

func (e *Engine) memoFor(call string, ready bool) (lookup.Card, error, string) {
	card, err, ok := e.memoLocked(call)
	if !ready {
		return lookup.Card{}, nil, "off"
	}
	if !ok {
		return lookup.Card{}, nil, "wait"
	}
	switch {
	case errors.Is(err, lookup.ErrNotFound):
		return lookup.Card{}, err, "miss"
	case err != nil && card.Call == "":
		return card, err, "offline"
	case errors.Is(err, lookup.ErrOffline):
		return card, err, "offline"
	case card.Call != "":
		return card, nil, "ok"
	default:
		return card, err, "wait"
	}
}

// knownQSO copies lookup fields onto a contact that is about to be logged.
func (e *Engine) knownQSO(q ops.QSO, mode string) ops.QSO {
	_, band := ops.ParseFreq(e.st.Freq)
	if band == "" {
		band = q.Band
	}
	cty := e.contestCTY()
	qz, qe, qs := e.memoFor(q.Call, e.qrzReady())
	card, _ := assembleCard(q.Call, band, mode, cty, e.bookLocked(), qz, qe, qs)
	if q.State == "" {
		q.State = card.State
	}
	if q.Name == "" {
		q.Name = card.Name
	}
	if q.DXCC == 0 {
		q.DXCC = card.DXCC
	}
	if q.CTYPrefix == "" {
		q.CTYPrefix = card.Prefix
	}
	if q.Grid == "" {
		q.Grid = card.Grid
	}
	return q
}

// assembleCard is the decode line for one call. cty and the log answer even
// when qrzStatus is "off" or "offline".
func assembleCard(call, band, mode string, cty *contest.CTY, book *logbook.Book, qrzCard lookup.Card, qrzErr error, qrzStatus string) (cardMsg, logbook.Needs) {
	call = strings.ToUpper(strings.TrimSpace(call))
	card := cardMsg{Call: call, QRZ: qrzStatus, Needs: []string{}}
	q := logbook.Query{Call: call, Band: band, Mode: mode}
	if cty != nil {
		if m, ok := cty.Lookup(call); ok {
			card.CQ = m.CQ
			card.Cont = m.Cont
			if m.Entity != nil {
				card.Country = m.Entity.Name
				card.Prefix = m.Entity.Prefix
				q.CTYPrefix = m.Entity.Prefix
			}
		}
	}
	if qrzCard.Country != "" && card.Country == "" {
		card.Country = qrzCard.Country
	}
	if qrzCard.State != "" {
		card.State = qrzCard.State
		q.State = qrzCard.State
	}
	if qrzCard.DXCC > 0 {
		card.DXCC = qrzCard.DXCC
		q.DXCC = qrzCard.DXCC
	}
	if qrzCard.Name != "" {
		card.Name = qrzCard.Name
	}
	if qrzCard.Grid != "" {
		card.Grid = qrzCard.Grid
	}
	if qrzCard.Message != "" && qrzStatus == "ok" {
		card.Message = qrzCard.Message
	}
	if book == nil {
		book = &logbook.Book{}
	}
	n := book.Assess(q)
	card.Worked = n.Worked
	card.Needs = n.Labels()
	if card.Needs == nil {
		card.Needs = []string{}
	}
	if n.Worked && !n.Last.Start.IsZero() {
		mode := n.Last.Mode
		if mode == "" {
			mode = "CW"
		}
		card.Last = n.Last.Start.UTC().Format("2006-01-02") + " " + n.Last.Band + " " + mode
	}
	_ = qrzErr
	return card, n
}

func testingBinary() bool {
	return strings.HasSuffix(os.Args[0], ".test")
}
