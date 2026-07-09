package api

import (
	"sync"
	"testing"
	"time"
)

func TestIdempotency_LookupMiss(t *testing.T) {
	t.Parallel()
	s := NewIdempotencyStore()
	if _, ok := s.Lookup("k1", "idem1"); ok {
		t.Fatal("expected miss, got hit")
	}
}

func TestIdempotency_SaveAndLookup(t *testing.T) {
	t.Parallel()
	s := NewIdempotencyStore()
	var h [32]byte
	copy(h[:], "bodyhash1")
	s.Save("k1", "idem1", h, 200, []byte("resp"), 60*time.Second)

	rec, ok := s.Lookup("k1", "idem1")
	if !ok {
		t.Fatal("expected hit")
	}
	if rec.Status != 200 {
		t.Errorf("status: got %d, want 200", rec.Status)
	}
	if string(rec.Response) != "resp" {
		t.Errorf("response: got %q, want resp", string(rec.Response))
	}
	if rec.BodyHash != h {
		t.Errorf("bodyhash mismatch")
	}
	if time.Until(rec.Expires) <= 0 {
		t.Errorf("expires should be in the future")
	}
}

func TestIdempotency_BodyHashMismatch(t *testing.T) {
	t.Parallel()
	s := NewIdempotencyStore()
	var h1, h2 [32]byte
	copy(h1[:], "bodyhash1")
	copy(h2[:], "bodyhash2")
	s.Save("k1", "idem1", h1, 200, []byte("resp"), 60*time.Second)
	rec, ok := s.Lookup("k1", "idem1")
	if !ok {
		t.Fatal("expected hit")
	}
	if rec.BodyHash == h2 {
		t.Fatal("hashes should differ")
	}
}

func TestIdempotency_DifferentKeys(t *testing.T) {
	t.Parallel()
	s := NewIdempotencyStore()
	var h [32]byte
	s.Save("k1", "idem1", h, 200, []byte("r1"), 60*time.Second)
	s.Save("k1", "idem2", h, 200, []byte("r2"), 60*time.Second)
	s.Save("k2", "idem1", h, 200, []byte("r3"), 60*time.Second)

	for _, tc := range []struct{ scope, idem, resp string }{
		{"k1", "idem1", "r1"},
		{"k1", "idem2", "r2"},
		{"k2", "idem1", "r3"},
	} {
		rec, ok := s.Lookup(tc.scope, tc.idem)
		if !ok {
			t.Errorf("%s/%s: expected hit", tc.scope, tc.idem)
			continue
		}
		if string(rec.Response) != tc.resp {
			t.Errorf("%s/%s: resp got %q want %q", tc.scope, tc.idem, rec.Response, tc.resp)
		}
	}
}

func TestIdempotency_Expires(t *testing.T) {
	t.Parallel()
	s := NewIdempotencyStore()
	var h [32]byte
	// TTL of 1ms
	s.Save("k1", "idem1", h, 200, []byte("resp"), 1*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	if _, ok := s.Lookup("k1", "idem1"); ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestIdempotency_GC(t *testing.T) {
	t.Parallel()
	s := NewIdempotencyStore()
	var h [32]byte
	s.Save("k1", "idem1", h, 200, []byte("r1"), 1*time.Millisecond)
	s.Save("k1", "idem2", h, 200, []byte("r2"), 60*time.Second)

	time.Sleep(50 * time.Millisecond)
	removed := s.GC()
	if removed != 1 {
		t.Errorf("GC should remove 1 expired record, got %d", removed)
	}
	// idem1 should be gone, idem2 should remain
	if _, ok := s.Lookup("k1", "idem1"); ok {
		t.Error("idem1 should be gone after GC")
	}
	if _, ok := s.Lookup("k1", "idem2"); !ok {
		t.Error("idem2 should still exist after GC")
	}
}

func TestIdempotency_Concurrent(t *testing.T) {
	t.Parallel()
	s := NewIdempotencyStore()
	var h [32]byte
	const N = 100
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.Save("k1", "idem1", h, 200, []byte("resp"), 60*time.Second)
		}()
	}
	wg.Wait()

	rec, ok := s.Lookup("k1", "idem1")
	if !ok {
		t.Fatal("expected hit after concurrent saves")
	}
	if string(rec.Response) != "resp" {
		t.Errorf("response corrupted: %q", rec.Response)
	}
}

func TestIdempotency_ConcurrentDifferentKeys(t *testing.T) {
	t.Parallel()
	s := NewIdempotencyStore()
	var h [32]byte
	const N = 100
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			idem := "idem-" + string(rune('a'+i%26))
			s.Save("k1", idem, h, 200, []byte(idem), 60*time.Second)
		}()
	}
	wg.Wait()
	if s.Len() == 0 {
		t.Fatal("store should not be empty")
	}
}
