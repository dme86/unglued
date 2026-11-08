package httpx

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"unglued/internal/model"
	"unglued/internal/store"
	"unglued/internal/util"
)

func testLimitedRouter(t *testing.T) (*chi.Mux, *store.Store) {
	t.Helper()
	st := store.New(time.Hour)
	index, view, edit := LoadTemplates()
	router := chi.NewRouter()
	MountRoutes(router, NewServer(Config{}, st, index, view, edit))
	return router, st
}

func TestCreateRejectsOversizedRequestBody(t *testing.T) {
	router, st := testLimitedRouter(t)
	defer st.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/paste", strings.NewReader(strings.Repeat("x", MaxRequestBytes+1)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit: HTTP %d, want 413", rec.Code)
	}
	if st.CountActive() != 0 {
		t.Fatal("oversized request created a paste")
	}
}

func TestCreateRejectsOversizedPaste(t *testing.T) {
	router, st := testLimitedRouter(t)
	defer st.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/paste", strings.NewReader(strings.Repeat("x", MaxPasteBytes+1)))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("paste limit: HTTP %d, want 413", rec.Code)
	}
}

func TestFormAndAPIEditEnforcePasteLimit(t *testing.T) {
	router, st := testLimitedRouter(t)
	defer st.Close()
	p := model.Paste{
		ID: "limited", Code: "old", Lang: "plaintext", Theme: "dark",
		Editable: true, EditKey: "private", ExpiresAt: time.Now().Add(time.Hour),
		Versions: []model.Version{{ZCode: util.GzipEncode("old"), Lang: "plaintext"}},
	}
	if err := st.Put(p); err != nil { t.Fatal(err) }

	form := url.Values{"code": {strings.Repeat("z", MaxPasteBytes+1)}, "lang": {"plaintext"}, "version": {"1"}}
	req := httptest.NewRequest(http.MethodPost, "/p/limited/edit?key=private", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("browser oversized edit: HTTP %d, want 413", rec.Code)
	}

	jsonBody := `{"code":"`+strings.Repeat("z", MaxPasteBytes+1)+`","lang":"plaintext"}`
	req = httptest.NewRequest(http.MethodPost, "/api/paste/limited/edit?key=private", strings.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("API oversized edit: HTTP %d, want 413", rec.Code)
	}
	after, _ := st.Get(p.ID)
	if after.Code != "old" || len(after.Versions) != 1 {
		t.Fatal("oversized edit mutated stored paste")
	}
}
