package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"unglued/internal/model"
	"unglued/internal/store"
	"unglued/internal/util"
)

func TestViewEditKeyIsNotDisclosedToReaders(t *testing.T) {
	st := store.New(time.Hour)
	defer st.Close()

	const key = "private-edit-capability"
	p := model.Paste{
		ID: "example", Code: "hello", Lang: "plaintext", Theme: "dark",
		ExpiresAt: time.Now().Add(time.Hour), Editable: true, EditKey: key,
		Versions: []model.Version{{ZCode: util.GzipEncode("hello"), Lang: "plaintext", At: time.Now()}},
	}
	st.Put(p)
	index, view, edit := LoadTemplates()
	srv := NewServer(Config{}, st, index, view, edit)
	router := chi.NewRouter()
	MountRoutes(router, srv)

	t.Run("public reader", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/p/example", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, key) || strings.Contains(body, "Editieren") {
			t.Fatalf("public reader received edit capability or edit control")
		}
	})

	t.Run("authorized cookie", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/p/example", nil)
		req.AddCookie(&http.Cookie{Name: "npk_example", Value: key})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		body := rec.Body.String()
		if !strings.Contains(body, "Editieren") {
			t.Fatalf("authorized editor missing edit control")
		}
		if strings.Contains(body, key) {
			t.Fatalf("cookie-based editor did not need key embedded in HTML")
		}
	})

	t.Run("authorized query key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/p/example?key="+key, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), "Editieren") {
			t.Fatalf("valid edit-key link did not enable editing")
		}
	})

	t.Run("invalid query key", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/p/example?key=invalid", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if strings.Contains(rec.Body.String(), key) || strings.Contains(rec.Body.String(), "Editieren") {
			t.Fatalf("invalid key must not reveal edit capability")
		}
	})
}
