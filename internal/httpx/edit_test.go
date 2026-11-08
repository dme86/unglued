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

func TestFormEditRejectsStaleVersion(t *testing.T) {
	st := store.New(time.Hour)
	defer st.Close()
	p := model.Paste{ID: "edit", Code: "old", Lang: "plaintext", Theme: "dark",
		ExpiresAt: time.Now().Add(time.Hour), Editable: true, EditKey: "testsecret",
		Versions: []model.Version{{ZCode: util.GzipEncode("old"), Lang: "plaintext"}},
	}
	st.Put(p)
	index, view, edit := LoadTemplates()
	router := chi.NewRouter()
	MountRoutes(router, NewServer(Config{}, st, index, view, edit))
	send := func(code string) int {
		form := url.Values{"code": {code}, "lang": {"plaintext"}, "version": {"1"}}
		req := httptest.NewRequest(http.MethodPost, "/p/edit/edit?key=testsecret", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := send("first"); got != http.StatusSeeOther {
		t.Fatalf("first edit: HTTP %d", got)
	}
	if got := send("stale"); got != http.StatusConflict {
		t.Fatalf("stale edit: HTTP %d, want 409", got)
	}
	after, _ := st.Get("edit")
	if after.Code != "first" || len(after.Versions) != 2 {
		t.Fatalf("stale edit overwrote content: %+v", after)
	}
}
