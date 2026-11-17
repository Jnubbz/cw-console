package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/Jnubbz/cw-console/contest"
)

// refreshCTY downloads AD1C's country file into the data directory when the
// download parses and is newer than the one already in use. The previous file
// is kept as cty.dat.bak. A failure leaves the current file, and the embedded
// copy, untouched. It runs off the keyer goroutine.
func (e *Engine) refreshCTY() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e.mu.Lock()
	current := e.contestCTY().Version
	e.mu.Unlock()
	body, err := fetchCTY(ctx, contest.CTYURL)
	if err != nil {
		return
	}
	ver, err := contest.InstallCTY(supportDir(), body, current)
	if err != nil {
		return
	}
	e.mu.Lock()
	e.cty = nil
	e.ctyFrom = ""
	e.book = nil
	e.setNoteLocked("Country file updated to " + ver + ".")
	e.mu.Unlock()
}

func fetchCTY(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "CWConsole/1")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 2<<20))
}
