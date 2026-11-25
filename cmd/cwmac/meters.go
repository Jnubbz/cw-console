package main

import (
	"errors"
	"time"

	"github.com/Jnubbz/cw-console/rig"
	"github.com/Jnubbz/cw-console/rig/ft710"
)

// Meter polling budget from docs/unified/PLAN.md. One command at a time, at
// least 50 ms apart. S every 200 ms on receive. The nine state queries once a
// second, and not while keyed. While keyed, PO, ALC and SWR rotate every 200 ms.
const (
	meterGap   = 50 * time.Millisecond
	meterEvery = 200 * time.Millisecond
	stateEvery = time.Second
)

// meterSched decides the next CAT poll. It does not touch a radio.
type meterSched struct {
	last      time.Time
	nextS     time.Time
	nextTX    time.Time
	nextState time.Time
	txIdx     int
	stateIdx  int
	stateAt   time.Time
	UseSM     bool     // RM1 failed; ask with SM0 instead
	Queries   []string // the radio's state queries (nil: the FT-710's)
}

// next returns the command to send, or "" if the caller should only wait.
func (s *meterSched) next(now time.Time, keyed bool) (cmd string, pri rig.Priority, wait time.Duration) {
	pri = rig.PriMeter
	wait = meterGap
	if !s.last.IsZero() && now.Sub(s.last) < meterGap {
		return "", pri, meterGap - now.Sub(s.last)
	}
	if keyed {
		s.stateAt = time.Time{}
		s.stateIdx = 0
		if now.Before(s.nextTX) {
			d := s.nextTX.Sub(now)
			if d < wait {
				wait = d
			}
			return "", pri, wait
		}
		cmds := []string{ft710.MeterPO, ft710.MeterALC, ft710.MeterSWR}
		cmd = cmds[s.txIdx%len(cmds)]
		s.txIdx++
		s.nextTX = now.Add(meterEvery)
		s.last = now
		return cmd, pri, wait
	}
	if s.stateAt.IsZero() && (s.nextState.IsZero() || !now.Before(s.nextState)) {
		s.stateAt = now
		s.stateIdx = 0
	}
	queries := s.Queries
	if queries == nil {
		queries = ft710.StateQueries
	}
	if !s.stateAt.IsZero() && s.stateIdx < len(queries) {
		cmd = queries[s.stateIdx]
		s.stateIdx++
		s.last = now
		if s.stateIdx == len(queries) {
			s.stateAt = time.Time{}
			s.nextState = now.Add(stateEvery)
		}
		return cmd, rig.PriState, wait
	}
	if now.Before(s.nextS) {
		d := s.nextS.Sub(now)
		if d < wait {
			wait = d
		}
		return "", pri, wait
	}
	cmd = ft710.MeterS
	if s.UseSM {
		cmd = ft710.MeterSM
	}
	s.nextS = now.Add(meterEvery)
	s.last = now
	return cmd, pri, wait
}

type meterMsg struct {
	OK     bool    `json:"ok"`
	TX     bool    `json:"tx"`
	S      int     `json:"s"`
	Watts  float64 `json:"watts"`
	ALC    float64 `json:"alc"`
	SWR    float64 `json:"swr"`
	HasPO  bool    `json:"hasPo,omitempty"`
	HasALC bool    `json:"hasAlc,omitempty"`
	HasSWR bool    `json:"hasSwr,omitempty"`
}

type liveMeter struct {
	s                     int
	sValid                bool
	watts, alc, swr       float64
	hasPO, hasALC, hasSWR bool
}

func (e *Engine) radioKeyed() bool {
	e.mu.Lock()
	cw := e.tx.busyLocked() || e.tx.sweeping
	e.mu.Unlock()
	e.ft8tx.mu.Lock()
	ft := e.ft8tx.active || e.ft8tx.tuning
	e.ft8tx.mu.Unlock()
	return cw || ft
}

func (e *Engine) meterPort() (catSpec, bool) {
	e.mu.Lock()
	key, typ, catPort := e.st.TxPort, e.st.CATType, e.st.CATPort
	e.mu.Unlock()
	if key == "" && catPort == "" {
		return catSpec{}, false
	}
	if s := catSpecFor(key, typ); s.Kenwood && s.Port != "" {
		return s, true
	}
	spec, err := e.ft710Spec()
	if err != nil || spec.Port == "" {
		return catSpec{}, false
	}
	return spec, true
}

func (e *Engine) meterLoop() {
	var sched meterSched
	for {
		keyed := e.radioKeyed()
		sched.Queries = e.rigModel().StateQueries()
		cmd, pri, wait := sched.next(time.Now(), keyed)
		if cmd != "" {
			if spec, ok := e.meterPort(); ok {
				out, err := e.rig.doAt(spec, pri, cmd)
				switch {
				case errors.Is(err, rig.ErrBusy):
					// The key lease owns the port. Skip this reading.
				case err != nil && (cmd == ft710.MeterS || cmd == ft710.MeterSM):
					if !sched.UseSM && cmd == ft710.MeterS {
						sched.UseSM = true
					}
				case err == nil && len(out) > 0:
					e.storeMeter(cmd, out[0])
				}
			}
		}
		if wait < meterGap {
			wait = meterGap
		}
		time.Sleep(wait)
	}
}

func (e *Engine) storeMeter(cmd, answer string) {
	m := e.rigModel() // its meter scales (before e.mu: rigModel takes rp.mu)
	e.mu.Lock()
	defer e.mu.Unlock()
	switch cmd {
	case ft710.MeterS, ft710.MeterSM:
		if n, err := ft710.SignalRaw(answer); err == nil {
			e.liveMeter.s, e.liveMeter.sValid = n, true
		}
	case ft710.MeterPO:
		if n, err := ft710.MeterRaw(answer, cmd); err == nil {
			e.liveMeter.watts, e.liveMeter.hasPO = m.Watts(n), true
		}
	case ft710.MeterALC:
		if n, err := ft710.MeterRaw(answer, cmd); err == nil {
			e.liveMeter.alc, e.liveMeter.hasALC = ft710.ALC(n), true
		}
	case ft710.MeterSWR:
		if n, err := ft710.MeterRaw(answer, cmd); err == nil {
			e.liveMeter.swr, e.liveMeter.hasSWR = m.SWR(n), true
		}
	default:
		e.rigFreq.note(answer, m.HasVHF(), time.Now())
	}
}

func (e *Engine) metersLocked(keyed bool) *meterMsg {
	m := e.liveMeter
	if keyed {
		if !m.hasPO && !m.hasALC && !m.hasSWR {
			return nil
		}
		return &meterMsg{OK: true, TX: true, Watts: m.watts, ALC: m.alc, SWR: m.swr,
			HasPO: m.hasPO, HasALC: m.hasALC, HasSWR: m.hasSWR}
	}
	if !m.sValid {
		return nil
	}
	return &meterMsg{OK: true, S: m.s}
}
