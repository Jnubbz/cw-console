package lookup

import (
	"context"
	"errors"
	"testing"
	"time"
)

type src struct {
	card Card
	err  error
	hits int
}

func (s *src) Lookup(context.Context, string) (Card, error) {
	s.hits++
	return s.card, s.err
}

func TestFreshCacheSkipsTheNetwork(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	c := &Cache{Now: func() time.Time { return now }}
	c.Put(Card{Call: "w1aw", Country: "United States", State: "CT", DXCC: 291})
	s := &src{err: errors.New("should not be called")}
	got, err := Resolve(context.Background(), c, s, "W1AW")
	if err != nil {
		t.Fatal(err)
	}
	if s.hits != 0 || got.Source != "cache" || got.State != "CT" || got.DXCC != 291 {
		t.Fatalf("hits %d card %+v", s.hits, got)
	}
}

func TestOfflineFallsBackToStale(t *testing.T) {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	c := &Cache{TTL: time.Hour, Now: func() time.Time { return now }}
	c.Put(Card{Call: "W1AW", Country: "United States", CachedAt: now.Add(-2 * time.Hour)})
	s := &src{err: errors.New("dial tcp: offline")}
	got, err := Resolve(context.Background(), c, s, "W1AW")
	if !errors.Is(err, ErrOffline) {
		t.Fatalf("err %v", err)
	}
	if got.Country != "United States" || got.Source != "cache" || s.hits != 1 {
		t.Fatalf("hits %d card %+v", s.hits, got)
	}
}

func TestNotFoundIsNotCached(t *testing.T) {
	c := &Cache{}
	s := &src{err: ErrNotFound}
	if _, err := Resolve(context.Background(), c, s, "N0CALL"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := Resolve(context.Background(), c, s, "N0CALL"); !errors.Is(err, ErrNotFound) || s.hits != 2 {
		t.Fatalf("hits %d err %v", s.hits, err)
	}
}
