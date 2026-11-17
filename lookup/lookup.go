// Package lookup turns a decoded call into the card the operator sees:
// country and CQ zone from cty.dat (offline, immediate) and name, state,
// grid and DXCC entity number from a callbook (QRZ XML, cached).
//
// contest.CTY remains the cty.dat parser. A Prefixes implementation wraps it
// in phase 1. This package does not fetch cty.dat and does not call QRZ.
package lookup

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

// ErrOffline means the callbook could not be reached and the returned card
// is a cached one, which may be stale. ErrNotFound means the callbook
// answered and does not know the call.
var (
	ErrOffline  = errors.New("lookup: offline")
	ErrNotFound = errors.New("lookup: callsign not found")
)

// Prefix is one cty.dat entity. DXCC is 0 until a QRZ or LoTW answer supplies
// the ARRL entity number; cty.dat itself does not carry that number.
type Prefix struct {
	Name   string
	Prefix string
	Cont   string
	CQ     int
	ITU    int
	DXCC   int
}

// Prefixes resolves a call the way contest.CTY.Lookup does. The concrete
// wrapper is added when the decode card is wired; the interface is what the
// card calls so a test can substitute a fixed table.
type Prefixes interface {
	Lookup(call string) (Prefix, bool)
}

// Card is what the decode line shows for one callsign.
type Card struct {
	Call     string
	Name     string
	Country  string
	State    string
	Grid     string
	County   string
	DXCC     int
	CQZone   int
	ITUZone  int
	Source   string // "qrz", "cache", or "cty"
	Message  string // a subscription or session notice, shown as-is
	CachedAt time.Time
}

// Source is a networked callbook. QRZ is lookup/qrz.Client.
type Source interface {
	Lookup(ctx context.Context, call string) (Card, error)
}

// Cache stores callbook cards on disk in a later phase. The memory cache is
// what the process uses within one session; phase 1 persists it beside the log.
type Cache struct {
	// TTL is how long a successful card is reused without a network call.
	// Zero means 30 days.
	TTL time.Duration
	// Now is the clock, set by tests.
	Now func() time.Time

	mu sync.Mutex
	m  map[string]Card
}

func (c *Cache) ttl() time.Duration {
	if c.TTL > 0 {
		return c.TTL
	}
	return 30 * 24 * time.Hour
}

func (c *Cache) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func key(call string) string { return strings.ToUpper(strings.TrimSpace(call)) }

// Get returns a card that is still inside TTL.
func (c *Cache) Get(call string) (Card, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	card, ok := c.m[key(call)]
	if !ok || c.now().Sub(card.CachedAt) > c.ttl() {
		return Card{}, false
	}
	return card, true
}

// Put stores a successful lookup. A zero CachedAt is stamped with Now.
func (c *Cache) Put(card Card) {
	card.Call = key(card.Call)
	if card.Call == "" {
		return
	}
	if card.CachedAt.IsZero() {
		card.CachedAt = c.now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]Card{}
	}
	c.m[card.Call] = card
}

// stale returns an expired card, for the offline fallback.
func (c *Cache) stale(call string) (Card, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	card, ok := c.m[key(call)]
	return card, ok
}

// Resolve answers from the cache when the card is fresh. Otherwise it asks
// src. A network failure with an old card returns that card and ErrOffline.
// ErrNotFound is not cached and does not fall back: the callbook said no.
func Resolve(ctx context.Context, c *Cache, src Source, call string) (Card, error) {
	if c == nil {
		c = &Cache{}
	}
	if card, ok := c.Get(call); ok {
		card.Source = "cache"
		return card, nil
	}
	if src == nil {
		if card, ok := c.stale(call); ok {
			card.Source = "cache"
			return card, ErrOffline
		}
		return Card{}, ErrOffline
	}
	card, err := src.Lookup(ctx, call)
	if err == nil {
		if card.CachedAt.IsZero() {
			card.CachedAt = c.now()
		}
		c.Put(card)
		return card, nil
	}
	if errors.Is(err, ErrNotFound) {
		return Card{}, err
	}
	if card, ok := c.stale(call); ok {
		card.Source = "cache"
		return card, ErrOffline
	}
	return Card{}, err
}
