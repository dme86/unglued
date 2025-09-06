package store

import (
	"sync"
	"time"

	"unglued/internal/model"
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

func (s *Store) Put(p model.Paste) {
	s.mu.Lock()
	s.items[p.ID] = &p
	s.mu.Unlock()
}

func (s *Store) Get(id string) (model.Paste, bool) {
	s.mu.RLock()
	ptr, ok := s.items[id]
	s.mu.RUnlock()
	if !ok || time.Now().After(ptr.ExpiresAt) {
		return model.Paste{}, false
	}
	return *ptr, true
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
				if now.After(p.ExpiresAt) {
					delete(s.items, id)
				}
			}
			s.mu.Unlock()
		case <-s.quitCh:
			return
		}
	}
}

