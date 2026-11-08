package store

import (
	"errors"
	"sync"
	"time"

	"unglued/internal/model"
)

var (
	ErrNotFound     = errors.New("paste not found or expired")
	ErrConflict     = errors.New("paste has been updated since it was read")
	ErrStoreFull    = errors.New("paste store capacity exceeded")
	ErrVersionLimit = errors.New("paste version limit exceeded")
)

// Limits apply to retained paste data, not Go process RSS or transient allocations.
const (
	DefaultMaxStoreBytes int64 = 64 << 20
	DefaultMaxPastes            = 1000
	DefaultMaxVersions          = 50
)

type Limits struct {
	MaxBytes    int64
	MaxPastes   int
	MaxVersions int
}

type Store struct {
	mu        sync.RWMutex
	items     map[string]*model.Paste
	usedBytes int64
	limits    Limits
	quitCh    chan struct{}
}

func New(janitorInterval time.Duration) *Store {
	return NewWithLimits(janitorInterval, Limits{
		MaxBytes: DefaultMaxStoreBytes, MaxPastes: DefaultMaxPastes,
		MaxVersions: DefaultMaxVersions,
	})
}

// NewWithLimits supports deterministic resource-budget testing.
func NewWithLimits(janitorInterval time.Duration, limits Limits) *Store {
	if janitorInterval <= 0 {
		janitorInterval = 30 * time.Second
	}
	if limits.MaxBytes <= 0 || limits.MaxPastes <= 0 || limits.MaxVersions <= 0 {
		panic("store limits must be positive")
	}
	s := &Store{
		items: make(map[string]*model.Paste), limits: limits,
		quitCh: make(chan struct{}),
	}
	go s.janitor(janitorInterval)
	return s
}

func (s *Store) Close() { close(s.quitCh) }

// A returned Paste never aliases mutable slices held by the store.
func clonePaste(p model.Paste) model.Paste {
	p.Versions = append([]model.Version(nil), p.Versions...)
	for i := range p.Versions {
		p.Versions[i].ZCode = append([]byte(nil), p.Versions[i].ZCode...)
	}
	return p
}

// Conservative estimate of retained bytes, including object overhead.
func pasteBytes(p model.Paste) int64 {
	n := int64(256 + len(p.ID) + len(p.Code) + len(p.Lang) +
		len(p.Theme) + len(p.EditKey) + len(p.Author))
	for _, v := range p.Versions {
		n += int64(96 + len(v.ZCode) + len(v.Lang) + len(v.Author))
	}
	return n
}

func (s *Store) removeExpiredLocked(now time.Time) {
	for id, p := range s.items {
		if !now.Before(p.ExpiresAt) {
			s.usedBytes -= pasteBytes(*p)
			delete(s.items, id)
		}
	}
}

// Put creates a paste without overwriting an existing ID.
func (s *Store) Put(p model.Paste) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeExpiredLocked(time.Now())
	if _, exists := s.items[p.ID]; exists {
		return ErrConflict
	}
	if len(p.Versions) > s.limits.MaxVersions {
		return ErrVersionLimit
	}
	size := pasteBytes(p)
	if len(s.items) >= s.limits.MaxPastes || size > s.limits.MaxBytes-s.usedBytes {
		return ErrStoreFull
	}
	copy := clonePaste(p)
	s.items[p.ID] = &copy
	s.usedBytes += size
	return nil
}

func (s *Store) Get(id string) (model.Paste, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.items[id]
	if !ok || !time.Now().Before(p.ExpiresAt) {
		return model.Paste{}, false
	}
	return clonePaste(*p), true
}

// Update atomically verifies version and budget; failures change nothing.
func (s *Store) Update(id string, expectedVersions int, mutate func(*model.Paste)) (model.Paste, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.items[id]
	if !ok || !time.Now().Before(p.ExpiresAt) {
		return model.Paste{}, ErrNotFound
	}
	if expectedVersions != len(p.Versions) {
		return model.Paste{}, ErrConflict
	}
	next := clonePaste(*p)
	mutate(&next)
	if len(next.Versions) > s.limits.MaxVersions {
		return model.Paste{}, ErrVersionLimit
	}
	oldSize, newSize := pasteBytes(*p), pasteBytes(next)
	if newSize > s.limits.MaxBytes-(s.usedBytes-oldSize) {
		return model.Paste{}, ErrStoreFull
	}
	s.items[id] = &next
	s.usedBytes += newSize - oldSize
	return clonePaste(next), nil
}

func (s *Store) CountActive() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := time.Now()
	n := 0
	for _, p := range s.items {
		if now.Before(p.ExpiresAt) {
			n++
		}
	}
	return n
}

func (s *Store) janitor(interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			s.mu.Lock()
			s.removeExpiredLocked(time.Now())
			s.mu.Unlock()
		case <-s.quitCh:
			return
		}
	}
}
