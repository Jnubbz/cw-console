// Package hamlib is a small client for rigctld, the Hamlib daemon that talks to hundreds of
// radios. It speaks rigctld's plain-text protocol over TCP: one command per line, and a reply
// that is either the requested values (one per line) or "RPRT n" (0 = done, negative = error).
// Only what an SSTV app needs is here: frequency, mode and push-to-talk.
//
// Copied from github.com/Jnubbz/sstv-console (AGPL-3.0). SSTV transmit in this program
// keys through the serial port owner, not through a second client opened beside it.
package hamlib

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// DefaultAddr is where rigctld listens unless told otherwise.
const DefaultAddr = "127.0.0.1:4532"

// Hamlib's names for the data modes, which route audio from the radio's USB sound card.
const (
	DataUSB = "PKTUSB"
	DataLSB = "PKTLSB"
)

// Client is one connection to rigctld. It is not safe for concurrent use: the caller
// takes turns (the app holds a lock around every conversation with the radio).
type Client struct {
	c       net.Conn
	r       *bufio.Reader
	Timeout time.Duration // per command; a radio can be slow to answer
	broken  bool
}

// Broken says the connection can't be trusted any more: the link dropped, or a command
// timed out so its late reply could be mistaken for the answer to the next one. The
// caller Dials again. (A radio that answers with an error is not broken: rigctld is fine.)
func (c *Client) Broken() bool { return c.broken }

// fail marks the connection broken and passes err on.
func (c *Client) fail(err error) error {
	c.broken = true
	return err
}

// Dial connects to rigctld at host:port.
func Dial(addr string) (*Client, error) {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	return &Client{c: c, r: bufio.NewReader(c), Timeout: 3 * time.Second}, nil
}

// Close drops the connection (rigctld keeps running).
func (c *Client) Close() { c.c.Close() }

// errCode turns an "RPRT n" line into an error, or nil for "RPRT 0".
func errCode(line string) (bool, error) {
	if !strings.HasPrefix(line, "RPRT ") {
		return false, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[5:]))
	if err != nil {
		return true, fmt.Errorf("rigctld sent %q", line)
	}
	if n == 0 {
		return true, nil
	}
	return true, fmt.Errorf("rigctld error %d (%s)", n, explain(n))
}

// explain names the Hamlib error codes a person is likely to hit.
func explain(n int) string {
	switch n {
	case -1:
		return "invalid parameter"
	case -4:
		return "the radio doesn't support that"
	case -5:
		return "communication error: is the radio on, and are its port and speed right?"
	case -6:
		return "the radio timed out"
	case -8:
		return "the radio refused it"
	case -11:
		return "that command isn't available"
	}
	return "see the rigctld log"
}

// lines sends a command and reads want (at least 1) reply lines. A single "RPRT n" line is an error reply.
func (c *Client) lines(cmd string, want int) ([]string, error) {
	c.c.SetDeadline(time.Now().Add(c.Timeout))
	if _, err := fmt.Fprintf(c.c, "%s\n", cmd); err != nil {
		return nil, c.fail(err)
	}
	var out []string
	for len(out) < want {
		l, err := c.r.ReadString('\n')
		if err != nil {
			return nil, c.fail(err)
		}
		l = strings.TrimRight(l, "\r\n")
		if rp, e := errCode(l); rp {
			if e == nil { // "RPRT 0" where values were expected
				e = fmt.Errorf("rigctld sent no value")
			}
			return nil, e
		}
		out = append(out, l)
	}
	return out, nil
}

// Freq is the radio's frequency in Hz.
func (c *Client) Freq() (int, error) {
	l, err := c.lines("f", 1)
	if err != nil {
		return 0, err
	}
	hz, err := strconv.ParseFloat(strings.TrimSpace(l[0]), 64)
	if err != nil || hz <= 0 {
		return 0, c.fail(fmt.Errorf("rigctld sent %q for the frequency", l[0]))
	}
	return int(hz + 0.5), nil
}

// Mode is Hamlib's name for the radio's mode ("USB", "PKTUSB"...). The passband is dropped.
func (c *Client) Mode() (string, error) {
	l, err := c.lines("m", 2)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(l[0]), nil
}

// set sends a command that answers with a single "RPRT n".
func (c *Client) set(cmd string) error {
	c.c.SetDeadline(time.Now().Add(c.Timeout))
	if _, err := fmt.Fprintf(c.c, "%s\n", cmd); err != nil {
		return c.fail(err)
	}
	l, err := c.r.ReadString('\n')
	if err != nil {
		return c.fail(err)
	}
	l = strings.TrimRight(l, "\r\n")
	if rp, e := errCode(l); rp {
		return e
	}
	return c.fail(fmt.Errorf("rigctld sent %q, expected RPRT", l))
}

// SetFreq moves the radio to hz.
func (c *Client) SetFreq(hz int) error { return c.set(fmt.Sprintf("F %d", hz)) }

// SetMode sets a mode by Hamlib's name, with the radio's own default passband (0).
func (c *Client) SetMode(mode string) error {
	if mode == "" || strings.ContainsAny(mode, " \r\n") {
		return fmt.Errorf("bad mode %q", mode)
	}
	return c.set("M " + mode + " 0")
}

// PTT keys (true) or unkeys the radio. rigctld sends the radio's CAT or line keying itself.
func (c *Client) PTT(on bool) error {
	if on {
		return c.set("T 1")
	}
	return c.set("T 0")
}

// DisplayMode shows Hamlib's data modes the way the app names them everywhere else.
func DisplayMode(m string) string {
	switch m {
	case DataUSB:
		return "DATA-U"
	case DataLSB:
		return "DATA-L"
	}
	return m
}

// ValidAddr accepts host:port with a plain host name or address and a real port, and
// nothing else (no paths, no schemes, no spaces).
func ValidAddr(a string) bool {
	h, p, err := net.SplitHostPort(a)
	if err != nil || h == "" {
		return false
	}
	if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
		return false
	}
	for _, r := range h {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == ':') {
			return false
		}
	}
	return true
}
