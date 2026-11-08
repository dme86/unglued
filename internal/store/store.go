package store

import (
	"errors"
	"sync"
	"time"

	"unglued/internal/model"
)

var (
	ErrNotFound = errors.New("paste not found or expired")
	ErrConflict = errors.New("paste has been updated since it was read")
)

type Store struct {
	mu     sync.RWMutex
	items  map[string]*model.Paste
	quitCh chan struct{}
}

func New(janitorInterval time.Duration) *Store {
	s := &Store{
		items:  make(map[string]*model.Paste),
		quitCh: make(chan struct{}),
	}
	go s.janitor(janitorInterval)
	return s
}

func (s *Store) Close() { close(s.quitCh) }

// clonePaste prevents callers from retaining mutable references to stored
// versions or compressed data. It must be called while the store is locked
// when the source is one of the stored objects.
func clonePaste(p model.Paste) model.Paste {
	p.Versions = append([]model.Version(nil), p.Versions...)
	for i := range p.Versions {
		p.Versions[i].ZCode = append([]byte(nil), p.Versions[i].ZCode...)
	}
	return p
}

func (s *Store) Put(p model.Paste) {
	s.mu.Lock()
	s.items[p.ID] = ptr(clonePaste(p))
	s.mu.Unlock()
}

func ptr(p model.Paste) *model.Paste { return &p }

func (s *Store) Get(id string) (model.Paste, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.items[id]
	if !ok || !time.Now().Before(p.ExpiresAt) {
		return model.Paste{}, false
	}
	return clonePaste(*p), true
}

// Update atomically checks the expected number of versions and applies a
// mutation. A stale concurrent editor receives ErrConflict instead of
// silently overwriting the newer paste. Mutators must not retain the pointer.
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
	s.items[id] = ptr(next)
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
			now := time.Now()
			s.mu.Lock()
			for id, p := range s.items {
				if !now.Before(p.ExpiresAt) {
					delete(s.items, id)
				}
			}
			s.mu.Unlock()
		case <-s.quitCh:
			return
		}
	}
}
