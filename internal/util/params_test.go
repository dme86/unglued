package util

import "testing"

func TestParseHighlightBounds(t *testing.T) {
	hl := ParseHL("1-2000000000")
	if len(hl) != 10000 || !hl[1] || !hl[10000] {
		t.Fatalf("unexpected clipped range size %d", len(hl))
	}
	hl = ParseHL("15000")
	if len(hl) != 0 {
		t.Fatal("out-of-range highlight was accepted")
	}
}

func TestTTLIsBounded(t *testing.T) {
	for _, ttl := range []string{"-1h", "0s", "240h"} {
		if _, err := ParseTTL(ttl); err == nil {
			t.Fatalf("expected invalid TTL for %q", ttl)
		}
	}
	for _, ttl := range []string{"1h", "24h", "7d", "168h"} {
		if _, err := ParseTTL(ttl); err != nil {
			t.Fatalf("valid TTL %q rejected: %v", ttl, err)
		}
	}
}
