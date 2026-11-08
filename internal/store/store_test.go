package store

import (
	"errors"
	"sync"
	"testing"
	"time"

	"unglued/internal/model"
)

func testPaste() model.Paste {
	return model.Paste{
		ID: "test", Code: "original", Lang: "plaintext",
		ExpiresAt: time.Now().Add(time.Hour),
		Versions: []model.Version{{ZCode: []byte("compressed"), Lang: "plaintext"}},
	}
}

func TestGetReturnsIndependentVersionData(t *testing.T) {
	s := New(time.Hour)
	defer s.Close()
	p := testPaste()
	s.Put(p)
	p.Versions[0].ZCode[0] = 'X' // caller cannot mutate stored data
	got, ok := s.Get("test")
	if !ok || string(got.Versions[0].ZCode) != "compressed" {
		t.Fatalf("Put retained an external mutable slice: %#v", got)
	}
	got.Versions[0].ZCode[0] = 'Y'
	got.Versions[0].Lang = "modified"
	got2, _ := s.Get("test")
	if string(got2.Versions[0].ZCode) != "compressed" || got2.Versions[0].Lang != "plaintext" {
		t.Fatalf("Get returned mutable stored data: %#v", got2)
	}
}

func TestConcurrentEditsAreAtomic(t *testing.T) {
	s := New(time.Hour)
	defer s.Close()
	s.Put(testPaste())

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Update("test", 1, func(p *model.Paste) {
				p.Code = "changed"
				p.Versions = append(p.Versions, model.Version{Lang: "plaintext"})
			})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected update error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d; want 1 each", successes, conflicts)
	}
	p, ok := s.Get("test")
	if !ok || len(p.Versions) != 2 || p.Code != "changed" {
		t.Fatalf("unexpected final paste: %+v (exists=%v)", p, ok)
	}
}

func TestUpdateRejectsExpiredPaste(t *testing.T) {
	s := New(time.Hour)
	defer s.Close()
	p := testPaste()
	p.ExpiresAt = time.Now().Add(-time.Second)
	s.Put(p)
	_, err := s.Update(p.ID, 1, func(*model.Paste) {
		t.Fatal("must not mutate expired paste")
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
