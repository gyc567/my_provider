package api

import (
	"sync"
	"time"
)

// IdempotencyRecord is the cached response for a previously-served
// (api_key, idem_key) pair. Lookups compare BodyHash against the incoming
// request to detect conflicting replays.
type IdempotencyRecord struct {
	BodyHash [32]byte
	Response []byte
	Status   int
	Expires  time.Time
}

// IdempotencyStore is a process-local, in-memory cache of idempotent
// responses. It is safe for concurrent use.
//
// Scope: the (api_key, idem_key) tuple is the cache key. A key collision
// across different API keys is impossible because scope is part of the key.
//
// TTL: per record, set at Save time. GC() removes expired records on
// demand; a background goroutine can call it on a timer.
type IdempotencyStore struct {
	mu    sync.RWMutex
	recs  map[string]*IdempotencyRecord
	clock func() time.Time
}

func NewIdempotencyStore() *IdempotencyStore {
	return &IdempotencyStore{
		recs:  make(map[string]*IdempotencyRecord),
		clock: time.Now,
	}
}

// Lookup returns the cached record and true on hit (non-expired), or
// (nil, false) on miss / expired.
func (s *IdempotencyStore) Lookup(scope, idemKey string) (*IdempotencyRecord, bool) {
	key := scope + "\x00" + idemKey
	s.mu.RLock()
	rec, ok := s.recs[key]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if s.clock().After(rec.Expires) {
		// Expired — lazily evict.
		s.mu.Lock()
		delete(s.recs, key)
		s.mu.Unlock()
		return nil, false
	}
	return rec, true
}

// Save stores a record with the given TTL. A zero or negative TTL means
// "store but already expired" — the record will be invisible to Lookup
// and reaped on next GC.
func (s *IdempotencyStore) Save(scope, idemKey string, bodyHash [32]byte, status int, resp []byte, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	key := scope + "\x00" + idemKey
	// Copy the response slice to decouple it from the caller's lifetime.
	respCopy := make([]byte, len(resp))
	copy(respCopy, resp)
	rec := &IdempotencyRecord{
		BodyHash: bodyHash,
		Response: respCopy,
		Status:   status,
		Expires:  s.clock().Add(ttl),
	}
	s.mu.Lock()
	s.recs[key] = rec
	s.mu.Unlock()
}

// GC removes all expired records and returns the count removed.
func (s *IdempotencyStore) GC() int {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for k, rec := range s.recs {
		if now.After(rec.Expires) {
			delete(s.recs, k)
			removed++
		}
	}
	return removed
}

// Len returns the current number of cached records (including not-yet-GC'd
// expired ones). For tests and observability.
func (s *IdempotencyStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.recs)
}
