package store

import (
	"errors"
	"strings"
	"testing"
	"time"

	"unglued/internal/model"
)

func TestStoreEnforcesPasteCountAndBudget(t *testing.T) {
	s := NewWithLimits(time.Hour, Limits{MaxBytes: 500, MaxPastes: 1, MaxVersions: 2})
	defer s.Close()
	p := testPaste()
	if err := s.Put(p); err != nil {
		t.Fatal(err)
	}
	second := testPaste()
	second.ID = "second"
	if err := s.Put(second); !errors.Is(err, ErrStoreFull) {
		t.Fatalf("second paste: got %v, want ErrStoreFull", err)
	}
	if _, err := s.Update("test", 1, func(p *model.Paste) {
		p.Code = strings.Repeat("A", 1000)
	}); !errors.Is(err, ErrStoreFull) {
		t.Fatalf("oversized update: got %v, want ErrStoreFull", err)
	}
	after, _ := s.Get("test")
	if after.Code != p.Code {
		t.Fatal("failed update changed stored content")
	}
}

func TestStoreLimitsVersionHistory(t *testing.T) {
	s := NewWithLimits(time.Hour, Limits{MaxBytes: 1 << 20, MaxPastes: 10, MaxVersions: 1})
	defer s.Close()
	p := testPaste()
	if err := s.Put(p); err != nil {
		t.Fatal(err)
	}
	_, err := s.Update("test", 1, func(p *model.Paste) {
		p.Versions = append(p.Versions, model.Version{ZCode: []byte("next")})
	})
	if !errors.Is(err, ErrVersionLimit) {
		t.Fatalf("update error = %v, want ErrVersionLimit", err)
	}
	after, _ := s.Get("test")
	if len(after.Versions) != 1 {
		t.Fatal("failed version update changed stored history")
	}
}

func TestExpiredPasteReleasesMemoryAndSlot(t *testing.T) {
	s := NewWithLimits(time.Hour, Limits{MaxBytes: 500, MaxPastes: 1, MaxVersions: 2})
	defer s.Close()
	expired := testPaste()
	expired.ExpiresAt = time.Now().Add(-time.Second)
	if err := s.Put(expired); err != nil {
		t.Fatal(err)
	}
	next := testPaste()
	next.ID = "fresh"
	if err := s.Put(next); err != nil {
		t.Fatalf("expired paste should be collected on new insert: %v", err)
	}
	if _, ok := s.Get("test"); ok {
		t.Fatal("expired paste still exists")
	}
}
