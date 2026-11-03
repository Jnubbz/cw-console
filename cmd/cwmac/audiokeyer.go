package main

// CW as audio, for radios with USB audio but no usable key input over USB (the FX-4CR:
// its RTS is PTT and it ignores CAT CW). The box keys the rig for the whole over (CAT TX1;
// or a PTT line: Setup > Radio > PTT, as for FT8) and plays a clean, shaped single tone in Morse into the
// rig's USB sound card, with the rig in USB: one keyed tone through an SSB transmitter is
// one keyed carrier on the air (emission J2A, which Part 97 counts as CW). The tone is at
// the decoder's pitch, so the signal lands on the station tuned in the spectrum.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"cwdecoder/cw"
)

const (
	akBlock    = 10 * time.Millisecond  // audio is made in blocks this long
	akAhead    = 150 * time.Millisecond // written ahead of the clock at most
	akLatency  = 100 * time.Millisecond // from written to heard (aplay's buffer), roughly
	akPTTDelay = 200 * time.Millisecond // audio flowing (silence) this long before keying, as for FT8
	akRelay    = 60 * time.Millisecond  // silence after keying, before the first element (T/R relay)
	akRamp     = 5 * time.Millisecond   // rise and fall of every element: no key clicks
	akRecorder = 120 * time.Millisecond // for the recorder to let go of the sound card
	akWriteMax = 500 * time.Millisecond // a write to aplay taking longer: the sound card is stuck
)

// akHang: how long the rig stays keyed after the last element with nothing more to send
// (semi break-in): a word gap and a bit, so typing a little behind the keying doesn't
// drop the rig between words.
func akHang(wpm int) time.Duration {
	dit := time.Duration(float64(1200*time.Millisecond) / float64(wpm))
	return max(400*time.Millisecond, 7*dit+100*time.Millisecond)
}

type audioKeyer struct {
	mu         sync.Mutex
	ptt        keyLine
	dev, out   string
	pauseRx    func(bool)
	pitch      float64
	gain       float64
	wpm        int
	queue      []rune // fed, not started
	generating bool   // a character is being made
	over       bool   // keyed (or about to be): an over is in progress
	lastEnd    time.Time
	epoch      int // bumps on Abort: echoes scheduled before it are dropped

	sent  chan rune
	errs  chan error
	abort chan struct{}
	quit  chan struct{}
	done  chan struct{}
	once  sync.Once
}

func newAudioKeyer(ptt keyLine, dev, out string, pauseRx func(bool), pitch, gain float64, wpm int) *audioKeyer {
	k := &audioKeyer{ptt: ptt, pauseRx: pauseRx,
		sent: make(chan rune, 512), errs: make(chan error, 4), abort: make(chan struct{}, 1),
		quit: make(chan struct{}), done: make(chan struct{})}
	k.configure(pitch, gain, out, dev)
	k.SetWPM(wpm)
	go k.run()
	return k
}

// configure: the tone (the decoder's pitch), its level (the TX level), the sound card
// format (Setup > Radio > Audio out) and the rig's sound card ("" = keep the one it has).
// Takes effect from the next element (pitch, level) or the next over.
func (k *audioKeyer) configure(pitch, gain float64, out, dev string) {
	k.mu.Lock()
	k.pitch = math.Max(pitchMin, math.Min(pitchMax, pitch))
	k.gain = math.Max(0, math.Min(1, gain))
	k.out = out
	if dev != "" {
		k.dev = dev
	}
	k.mu.Unlock()
}

func (k *audioKeyer) SetWPM(w int) {
	k.mu.Lock()
	k.wpm = max(5, min(50, w))
	k.mu.Unlock()
}

// Wants: the next character only once this one has started, so what's typed ahead stays
// editable (as with the box's own keyer).
func (k *audioKeyer) Wants() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.queue) == 0
}

func (k *audioKeyer) Feed(ch rune) bool {
	if ch == '\n' {
		ch = ' '
	}
	if _, ok := cw.MorseCode(ch); !ok && ch != ' ' {
		return true // skipped, as the box's own keyer does
	}
	k.mu.Lock()
	k.queue = append(k.queue, []rune(strings.ToUpper(string(ch)))...)
	k.mu.Unlock()
	return true
}

func (k *audioKeyer) Sent() <-chan rune    { return k.sent }
func (k *audioKeyer) Errors() <-chan error { return k.errs }

func (k *audioKeyer) Busy() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.queue) > 0 || k.generating
}

func (k *audioKeyer) Active(hang time.Duration) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.over || (!k.lastEnd.IsZero() && time.Now().Before(k.lastEnd.Add(hang)))
}

// Abort: key up and stop now (the audio already handed to the sound card is dropped).
func (k *audioKeyer) Abort() {
	k.mu.Lock()
	k.queue = nil
	k.epoch++
	k.mu.Unlock()
	select {
	case k.abort <- struct{}{}:
	default:
	}
}

func (k *audioKeyer) Close() {
	k.once.Do(func() { close(k.quit) })
	<-k.done
	k.ptt.Close()
}

func (k *audioKeyer) run() {
	defer close(k.done)
	for {
		select {
		case <-k.quit:
			return
		case <-k.abort: // nothing playing: nothing to stop
			continue
		case <-time.After(10 * time.Millisecond):
		}
		k.mu.Lock()
		n := len(k.queue)
		k.mu.Unlock()
		if n == 0 {
			continue
		}
		if err := k.oneOver(); err != nil {
			k.mu.Lock()
			k.queue = nil
			k.mu.Unlock()
			select {
			case k.errs <- err:
			default:
			}
		}
	}
}

// oneOver: start the audio, key the rig, send until there's been nothing to send for a
// hang time, then key up and stop the audio. A nil error means it ended normally or was
// aborted.
func (k *audioKeyer) oneOver() (err error) {
	k.mu.Lock()
	dev, out, pause := k.dev, k.out, k.pauseRx
	k.over = true
	k.mu.Unlock()
	defer func() {
		k.mu.Lock()
		k.over, k.generating = false, false
		k.lastEnd = time.Now()
		k.mu.Unlock()
	}()
	if dev == "" {
		return errors.New("no rig sound card (Setup > Radio > Audio input)")
	}
	t0 := time.Now()
	if pause != nil {
		pause(true)
		defer pause(false) // last: after the sound card is closed
	}
	setPlaybackVolume(dev) // while the recorder lets go of the sound card
	if pause != nil {
		time.Sleep(akRecorder - time.Since(t0))
	}
	pdev, rate, ch := audioFormat(dev, out)
	cmd := exec.Command(aplayPath, "-q", "-D", pdev, "-t", "raw", "-f", "S16_LE", "-r", strconv.Itoa(rate),
		"-c", strconv.Itoa(ch), "--buffer-time=100000", "--period-time=20000")
	// our own pipe, so every write has a deadline: a sound card that stops taking audio
	// can't leave the rig keyed with the sender stuck
	pr, stdin, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("audio: %v", err)
	}
	cmd.Stdin = pr
	var stderr strings.Builder
	cmd.Stderr = &limitedWriter{&stderr, 2000}
	err = cmd.Start()
	pr.Close() // aplay has it now
	if err != nil {
		stdin.Close()
		return fmt.Errorf("audio: %v", err)
	}
	exited := make(chan struct{})
	go func() { cmd.Wait(); close(exited) }()
	w := &akWriter{k: k, out: stdin, rate: rate, ch: ch, start: time.Now(), exited: exited}
	keyed := false
	defer func() {
		// key up first (what's still buffered is silence, or is dropped), then stop the
		// audio; then, if the sound card failed, say what aplay said
		if keyed {
			k.ptt.Key(false)
		}
		cmd.Process.Kill()
		stdin.Close()
		<-exited
		if err == nil && w.err != nil {
			err = fmt.Errorf("the rig's USB sound card failed (%s)", firstLine(strings.TrimSpace(stderr.String()+" "+w.err.Error())))
		}
	}()
	// audio flowing (silence) first, then key the rig, as for FT8
	for time.Since(w.start) < akPTTDelay {
		if !w.silence(akBlock) {
			return nil
		}
	}
	if err := k.ptt.Key(true); err != nil {
		return fmt.Errorf("PTT failed: %v", err)
	}
	keyed = true
	if !w.silence(akRelay) {
		return nil
	}
	toneEnd := w.heardAt() // when the last element ends on the air
	for {
		k.mu.Lock()
		have := len(k.queue) > 0
		var c rune
		if have {
			c, k.queue = k.queue[0], k.queue[1:]
		}
		k.generating = have
		wpm, f, gain := k.wpm, k.pitch, k.gain
		k.mu.Unlock()
		dit := time.Duration(float64(1200*time.Millisecond) / float64(wpm))
		if !have {
			// nothing to send: stay keyed a hang time after the last element, then drop
			if time.Now().After(toneEnd.Add(akHang(wpm))) {
				return nil
			}
			if !w.silence(akBlock) {
				return nil
			}
			continue
		}
		k.emitAt(c, w.heardAt())
		if c == ' ' { // word gap: 7 dits, 3 already after the last character
			if !w.silence(4 * dit) {
				return nil
			}
			continue
		}
		code, _ := cw.MorseCode(c)
		for i, el := range code {
			if i > 0 && !w.silence(dit) {
				return nil
			}
			n := dit
			if el == '-' {
				n = 3 * dit
			}
			if !w.tone(n, f, gain) {
				return nil
			}
		}
		toneEnd = w.heardAt()
		if !w.silence(3 * dit) {
			return nil
		}
	}
}

// emitAt echoes c when it goes out on the air (unless aborted before that).
func (k *audioKeyer) emitAt(c rune, at time.Time) {
	k.mu.Lock()
	ep := k.epoch
	k.mu.Unlock()
	time.AfterFunc(time.Until(at), func() {
		k.mu.Lock()
		ok := k.epoch == ep
		k.mu.Unlock()
		if ok {
			select {
			case k.sent <- c:
			default:
			}
		}
	})
}

// akWriter makes the audio and keeps it at most akAhead ahead of the clock, so the
// keying follows the text as it comes and Stop is quick.
type akWriter struct {
	k       *audioKeyer
	out     *os.File
	rate    int
	ch      int
	start   time.Time
	exited  chan struct{} // aplay ended
	frames  int64
	phase   float64
	aborted bool
	err     error
}

func (w *akWriter) written() time.Duration {
	return time.Duration(float64(w.frames) / float64(w.rate) * float64(time.Second))
}

// heardAt: when what's written next goes out.
func (w *akWriter) heardAt() time.Time { return w.start.Add(w.written() + akLatency) }

func (w *akWriter) stopped() bool {
	select {
	case <-w.k.abort:
		w.aborted = true
	case <-w.k.quit:
		w.aborted = true
	case <-w.exited:
		if w.err == nil {
			w.err = errors.New("aplay stopped")
		}
	default:
	}
	return w.aborted || w.err != nil
}

func (w *akWriter) pace() bool {
	for {
		if w.stopped() {
			return false
		}
		ahead := w.written() - time.Since(w.start)
		if ahead < akAhead {
			return true
		}
		time.Sleep(min(ahead-akAhead+time.Millisecond, akBlock))
	}
}

func (w *akWriter) silence(d time.Duration) bool               { return w.gen(d, 0, 0) }
func (w *akWriter) tone(d time.Duration, f, gain float64) bool { return w.gen(d, f, gain) }

// gen writes d of audio: silence (gain 0) or a tone at f Hz with raised-cosine edges.
func (w *akWriter) gen(d time.Duration, f, gain float64) bool {
	n := int(math.Round(d.Seconds() * float64(w.rate)))
	ramp := int(akRamp.Seconds() * float64(w.rate))
	if 2*ramp > n {
		ramp = n / 2
	}
	block := int(akBlock.Seconds() * float64(w.rate))
	amp := gain * 32000
	dph := 2 * math.Pi * f / float64(w.rate)
	buf := make([]byte, 0, 2*w.ch*block)
	for i := 0; i < n; {
		if !w.pace() {
			return false
		}
		buf = buf[:0]
		end := min(n, i+block)
		for ; i < end; i++ {
			var v float64
			if gain > 0 {
				env := 1.0
				if i < ramp {
					env = 0.5 - 0.5*math.Cos(math.Pi*float64(i)/float64(ramp))
				} else if i >= n-ramp {
					env = 0.5 - 0.5*math.Cos(math.Pi*float64(n-i)/float64(ramp))
				}
				v = math.Sin(w.phase) * env * amp
			}
			w.phase += dph
			if w.phase > 2*math.Pi {
				w.phase -= 2 * math.Pi
			}
			s := uint16(int16(math.Round(v)))
			for c := 0; c < w.ch; c++ {
				buf = binary.LittleEndian.AppendUint16(buf, s)
			}
		}
		w.out.SetWriteDeadline(time.Now().Add(akWriteMax))
		if _, err := w.out.Write(buf); err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				err = errors.New("it stopped taking audio")
			}
			w.err = err
			return false
		}
		w.frames += int64(len(buf) / (2 * w.ch))
	}
	return true
}
