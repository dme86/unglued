package httpx

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func MountRoutes(r chi.Router, s *Server) {
	r.Get("/", s.handleIndex)
	r.Post("/paste", s.handleCreate)
	r.Get("/p/{id}", s.handleView)
	r.Get("/raw/{id}", s.handleRaw)
	r.Get("/p/{id}/edit", s.handleEditForm)
	r.Post("/p/{id}/edit", s.handleEditSave)

	// API
	r.Post("/api/paste", s.handleAPIPaste)
	r.Post("/api/paste/{id}/edit", s.handleAPIEdit)
}

func NoIndex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

