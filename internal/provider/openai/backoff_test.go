package openai

import (
	"testing"
	"time"
)

// TestBackoffWithJitter_NonDeterministic verifies the
// jitter is actually applied: the same attempt + base
// should produce different delays across calls.
func TestBackoffWithJitter_NonDeterministic(t *testing.T) {
	base := 100 * time.Millisecond
	// Collect several samples for the same attempt.
	// At least 2 of them should differ.
	seen := map[time.Duration]bool{}
	for i := 0; i < 10; i++ {
		d := backoffWithJitter(2, base)
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Errorf("backoff produced only %d unique values across 10 calls; want ≥ 2 (jitter not applied?)", len(seen))
	}
}

// TestBackoffWithJitter_Bounds verifies the delay is
// within the documented exponential range + jitter:
//
//	delay ∈ [base * 2^attempt, base * 2^attempt + base)
func TestBackoffWithJitter_Bounds(t *testing.T) {
	base := 100 * time.Millisecond
	cases := []int{0, 1, 2, 3}
	for _, attempt := range cases {
		min := base << attempt
		max := min + base
		// Try a few times to cover the jitter range.
		for i := 0; i < 50; i++ {
			d := backoffWithJitter(attempt, base)
			if d < min {
				t.Errorf("attempt %d: delay %v < min %v", attempt, d, min)
			}
			if d >= max {
				t.Errorf("attempt %d: delay %v ≥ max %v (jitter out of range)", attempt, d, max)
			}
		}
	}
}

// TestBackoffWithJitter_OverflowClamp verifies that
// very large attempt values don't produce a negative
// or absurdly large delay. The clamp caps the
// exponential term at 60s, but the jitter can push
// the final result slightly above.
func TestBackoffWithJitter_OverflowClamp(t *testing.T) {
	base := 1 * time.Second
	// attempt=63 with base=1s would overflow int64.
	for i := 0; i < 10; i++ {
		d := backoffWithJitter(63, base)
		if d > 61*time.Second {
			t.Errorf("attempt 63: delay %v > 61s (clamp+jitter should stay near 60s)", d)
		}
		if d < 60*time.Second {
			t.Errorf("attempt 63: delay %v < 60s (clamp should floor at 60s)", d)
		}
	}
}

// TestBackoffWithJitter_DefaultBase verifies that
// base <= 0 falls back to the default (1s).
func TestBackoffWithJitter_DefaultBase(t *testing.T) {
	d := backoffWithJitter(0, 0)
	// Default base is 1s; attempt 0 → [1s, 2s)
	if d < time.Second {
		t.Errorf("delay %v < 1s; want ≥ 1s (default base)", d)
	}
	if d >= 2*time.Second {
		t.Errorf("delay %v ≥ 2s; want < 2s", d)
	}
}
