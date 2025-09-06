package httpx

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"unglued/internal/model"
	"unglued/internal/render"
	"unglued/internal/util"
)

/* ======================
   Konfiguration & Helper
   ====================== */

func (s *Server) normalizeLang(lang string) string {
	if !slices.Contains(Langs, lang) {
		return "plaintext"
	}
	return lang
}

func (s *Server) makeURL(r *http.Request, path string) string {
	if s.Config.PublicBase != "" {
		return strings.TrimRight(s.Config.PublicBase, "/") + path
	}
	scheme := "http"
	if r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

func readAuthorCookie(r *http.Request) string {
	if c, err := r.Cookie("np_author"); err == nil {
		return c.Value
	}
	return ""
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

func (s *Server) canEditPaste(r *http.Request, p model.Paste) bool {
	if !p.Editable {
		return false
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		if c, err := r.Cookie("npk_" + p.ID); err == nil {
			key = c.Value
		}
	}
	return key != "" && key == p.EditKey
}

func (s *Server) buildPaste(code, lang, ttl, theme string, editable bool, author string) (model.Paste, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return model.Paste{}, fmt.Errorf("Code darf nicht leer sein")
	}
	lang = s.normalizeLang(lang)
	if !slices.Contains(Themes, theme) {
		theme = "dark"
	}
	dur, err := util.ParseTTL(ttl)
	if err != nil {
		return model.Paste{}, fmt.Errorf("Ungültige TTL")
	}
	now := time.Now()
	id := util.NewID(8)
	p := model.Paste{
		ID:        id,
		Lang:      lang,
		Code:      code,
		Theme:     theme,
		ExpiresAt: now.Add(dur),

		Editable: editable,
		EditKey:  "",
		Author:   author,

		Versions:  []model.Version{{ZCode: util.GzipEncode(code), Lang: lang, Author: author, At: now}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if editable {
		p.EditKey = util.NewID(12)
	}
	return p, nil
}

/* =============
   API Payloads
   ============= */

type apiReq struct {
	Code     string `json:"code"`
	Lang     string `json:"lang"`
	TTL      string `json:"ttl"`
	Theme    string `json:"theme"`
	Editable bool   `json:"editable"`
	Author   string `json:"author"`
}
type apiResp struct {
	ID        string `json:"id"`
	URL       string `json:"url"`
	RawURL    string `json:"raw_url"`
	EditURL   string `json:"edit_url,omitempty"`
	ExpiresAt string `json:"expires_at"`
}

/* ==========
   Handlers
   ========== */

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	author := readAuthorCookie(r)
	alloc, sys := util.MemUsage()
	_ = s.IndexTmpl.Execute(w, map[string]any{
		"Langs":  Langs,
		"Themes": Themes,
		"Author": author,
		"Alloc":  util.HumanBytes(alloc),
		"Sys":    util.HumanBytes(sys),
		"Count":  s.Store.CountActive(),
	})
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))
	lang := strings.TrimSpace(r.FormValue("lang"))
	ttl := strings.TrimSpace(r.FormValue("ttl"))
	theme := strings.TrimSpace(r.FormValue("theme"))
	editable := util.IsTruthy(r.FormValue("editable"))
	author := strings.TrimSpace(r.FormValue("author"))
	if author == "" {
		author = readAuthorCookie(r)
	}

	p, err := s.buildPaste(code, lang, ttl, theme, editable, author)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.Store.Put(p)

	// Cookies
	if author != "" {
		util.WriteCookie(w, "np_author", author, 180*24*time.Hour)
	}
	if p.Editable {
		util.WriteCookie(w, "npk_"+p.ID, p.EditKey, 365*24*time.Hour)
	}

	http.Redirect(w, r, "/p/"+p.ID, http.StatusSeeOther)
}

func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := s.Store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Version wählen: default = letzte
	vParam := strings.TrimSpace(r.URL.Query().Get("v"))
	vIdx := len(p.Versions) - 1
	if vParam != "" {
		if n, err := strconv.Atoi(vParam); err == nil && n >= 1 && n <= len(p.Versions) {
			vIdx = n - 1
		}
	}
	currVer := p.Versions[vIdx]
	code, _ := util.GzipDecode(currVer.ZCode)
	lang := currVer.Lang

	// Theme-Override via ?t=light|dark
	currTheme := p.Theme
	if tOverride := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("t"))); slices.Contains(Themes, tOverride) {
		currTheme = tOverride
	}

	// Highlights via ?hl=…
	hlParam := strings.TrimSpace(r.URL.Query().Get("hl"))
	hlSet := util.ParseHL(hlParam)

	html, err := render.CodeHTML(code, lang, currTheme, hlSet)
	if err != nil {
		http.Error(w, "Renderfehler", http.StatusInternalServerError)
		return
	}

	editURL := ""
	if p.Editable {
		editURL = "/p/" + p.ID + "/edit?key=" + p.EditKey
	}
	data := map[string]any{
		"ID":        p.ID,
		"Lang":      lang,
		"Theme":     currTheme,
		"ExpiresAt": p.ExpiresAt.Format("2006-01-02 15:04:05 -0700"),
		"HTML":      template.HTML(html),
		"HL":        hlParam,

		"HasHistory": len(p.Versions) > 1,
		"VIndex":     vIdx + 1,
		"VTotal":     len(p.Versions),
		"VAuthor":    orDash(currVer.Author),
		"VTime":      currVer.At.Format("2006-01-02 15:04:05 -0700"),

		"Editable": p.Editable,
		"CanEdit":  s.canEditPaste(r, p),
		"EditURL":  editURL,
	}
	_ = s.ViewTmpl.Execute(w, data)
}

func (s *Server) handleRaw(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := s.Store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// letzte Version
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if len(p.Versions) > 0 {
		sText, _ := util.GzipDecode(p.Versions[len(p.Versions)-1].ZCode)
		_, _ = io.WriteString(w, sText)
		return
	}
	_, _ = io.WriteString(w, p.Code)
}

func (s *Server) handleEditForm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := s.Store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.canEditPaste(r, p) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	curr := p.Versions[len(p.Versions)-1]
	code, _ := util.GzipDecode(curr.ZCode)
	author := readAuthorCookie(r)
	if author == "" {
		author = p.Author
	}
	key := r.URL.Query().Get("key")

	_ = s.EditTmpl.Execute(w, map[string]any{
		"ID": id, "Code": code, "Langs": Langs, "Lang": curr.Lang,
		"Author": author,
		"Key":    key,
	})
}

func (s *Server) handleEditSave(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := s.Store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !s.canEditPaste(r, p) {
		http.Error(w, "Forbidden (kein Edit-Zugriff)", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}

	now := time.Now()
	code := strings.TrimSpace(r.FormValue("code"))
	lang := s.normalizeLang(strings.TrimSpace(r.FormValue("lang")))
	author := strings.TrimSpace(r.FormValue("author"))
	if code == "" {
		http.Error(w, "Code darf nicht leer sein", http.StatusBadRequest)
		return
	}

	last := p.Versions[len(p.Versions)-1]
	prevCode, _ := util.GzipDecode(last.ZCode)

	// nur neue Version, wenn sich etwas geändert hat
	if code != prevCode || lang != last.Lang {
		p.Versions = append(p.Versions, model.Version{
			ZCode:  util.GzipEncode(code),
			Lang:   lang,
			Author: author,
			At:     now,
		})
		// (optional) Deckeln:
		// if len(p.Versions) > maxVersions { p.Versions = p.Versions[len(p.Versions)-maxVersions:] }
	}

	p.Code = code
	p.Lang = lang
	if author != "" {
		p.Author = author
	}
	p.UpdatedAt = now
	s.Store.Put(p)

	// Cookies
	if author != "" {
		util.WriteCookie(w, "np_author", author, 180*24*time.Hour)
	}
	if k := r.URL.Query().Get("key"); k != "" && k == p.EditKey {
		util.WriteCookie(w, "npk_"+p.ID, p.EditKey, 365*24*time.Hour)
	}

	http.Redirect(w, r, "/p/"+p.ID+"?v="+strconv.Itoa(len(p.Versions)), http.StatusSeeOther)
}

func (s *Server) handleAPIPaste(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	ct := r.Header.Get("Content-Type")
	accept := r.Header.Get("Accept")

	var code, lang, ttl, theme, author string
	var editable bool

	body, _ := io.ReadAll(r.Body)
	if strings.HasPrefix(ct, "application/json") ||
		(len(body) > 0 && bytesHasJSONPrefix(body)) {
		var req apiReq
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		code, lang, ttl, theme = req.Code, req.Lang, req.TTL, req.Theme
		editable, author = req.Editable, strings.TrimSpace(req.Author)
	} else {
		code = string(body)
		lang = r.URL.Query().Get("lang")
		ttl = r.URL.Query().Get("ttl")
		theme = r.URL.Query().Get("theme")
		editable = util.IsTruthy(r.URL.Query().Get("editable"))
		author = strings.TrimSpace(r.URL.Query().Get("author"))
	}

	p, err := s.buildPaste(code, lang, ttl, theme, editable, author)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.Store.Put(p)

	// Cookies
	if author != "" {
		util.WriteCookie(w, "np_author", author, 180*24*time.Hour)
	}

	url := s.makeURL(r, "/p/"+p.ID)
	raw := s.makeURL(r, "/raw/"+p.ID)
	edit := ""
	if p.Editable {
		edit = s.makeURL(r, "/p/"+p.ID+"/edit?key="+p.EditKey)
		util.WriteCookie(w, "npk_"+p.ID, p.EditKey, 365*24*time.Hour)
	}

	if strings.Contains(accept, "application/json") || r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(apiResp{
			ID:        p.ID,
			URL:       url,
			RawURL:    raw,
			EditURL:   edit,
			ExpiresAt: p.ExpiresAt.Format(time.RFC3339),
		})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if edit != "" {
		fmt.Fprintf(w, "%s\n# edit: %s\n", url, edit)
	} else {
		fmt.Fprintln(w, url)
	}
}

func (s *Server) handleAPIEdit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	id := chi.URLParam(r, "id")
	p, ok := s.Store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "missing ?key", http.StatusUnauthorized)
		return
	}
	if !p.Editable || key != p.EditKey {
		http.Error(w, "invalid key", http.StatusForbidden)
		return
	}

	var req apiReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	code := strings.TrimSpace(req.Code)
	if code == "" {
		http.Error(w, "code empty", http.StatusBadRequest)
		return
	}
	lang := s.normalizeLang(req.Lang)
	author := strings.TrimSpace(req.Author)
	now := time.Now()

	// letzte Version zum Vergleich
	last := p.Versions[len(p.Versions)-1]
	prevCode, _ := util.GzipDecode(last.ZCode)

	if code != prevCode || lang != last.Lang {
		p.Versions = append(p.Versions, model.Version{
			ZCode:  util.GzipEncode(code),
			Lang:   lang,
			Author: author,
			At:     now,
		})
	}

	p.Code = code
	p.Lang = lang
	if author != "" {
		p.Author = author
	}
	p.UpdatedAt = now
	s.Store.Put(p)

	if author != "" {
		util.WriteCookie(w, "np_author", author, 180*24*time.Hour)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":       p.ID,
		"versions": len(p.Versions),
		"url":      s.makeURL(r, "/p/"+p.ID+"?v="+strconv.Itoa(len(p.Versions))),
	})
}

/* ================
   kleine Utilities
   ================ */

func bytesHasJSONPrefix(b []byte) bool {
	// tolerant: leading whitespace ok
	i := 0
	for i < len(b) && (b[i] == ' ' || b[i] == '\n' || b[i] == '\r' || b[i] == '\t') {
		i++
	}
	return i < len(b) && b[i] == '{'
}

