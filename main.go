// main.go
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

/* =========================
   Datenmodelle & Speicher
   ========================= */

type Version struct {
	ZCode  []byte
	Lang   string
	Author string
	At     time.Time
}

type Paste struct {
	ID        string
	Lang      string
	Code      string
	Theme     string
	ExpiresAt time.Time

	// Neu:
	Editable bool
	EditKey  string
	Author   string

	Versions  []Version
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Store struct {
	mu     sync.RWMutex
	items  map[string]*Paste
	quitCh chan struct{}
}

func NewStore() *Store {
	s := &Store{
		items:  make(map[string]*Paste),
		quitCh: make(chan struct{}),
	}
	go s.janitor()
	return s
}
func (s *Store) Close() { close(s.quitCh) }
func (s *Store) Put(p Paste) {
	s.mu.Lock()
	s.items[p.ID] = &p
	s.mu.Unlock()
}

func (s *Store) Get(id string) (Paste, bool) {
	s.mu.RLock()
	ptr, ok := s.items[id] // map[string]*Paste
	s.mu.RUnlock()

	if !ok || time.Now().After(ptr.ExpiresAt) {
		return Paste{}, false
	}
	return *ptr, true
}

func (s *Store) janitor() {
	t := time.NewTicker(30 * time.Second)
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
			t.Stop()
			return
		}
	}
}

/* ===============
   Globals/Flags
   =============== */

var tmplFuncs = template.FuncMap{
	"inc": func(i int) int { return i + 1 },
	"dec": func(i int) int { return i - 1 },
}

var (
	langs     = []string{"plaintext", "go", "javascript", "typescript", "json", "yaml", "toml", "python", "bash", "html", "css", "sql", "markdown"}
	themes    = []string{"dark", "light"}
	indexTmpl = template.Must(template.New("index").Parse(indexHTML))
	viewTmpl  = template.Must(template.New("view").Funcs(tmplFuncs).Parse(viewHTML)) // 👈 hier Funcs()
	editTmpl  = template.Must(template.New("edit").Parse(editHTML))
	store     = NewStore()

	listenAddr string
	publicBase string
)

/* ========
   main
   ======== */

func main() {
	defer store.Close()

	flag.StringVar(&listenAddr, "listen", ":8080", "HTTP listen address")
	flag.StringVar(&publicBase, "public", "", "public base URL (e.g. https://paste.example.com)")
	flag.Parse()

	r := chi.NewRouter()
	r.Use(seoNoIndex)

	// Web UI
	r.Get("/", handleIndex)
	r.Post("/paste", handleCreate)
	r.Get("/p/{id}", handleView)
	r.Get("/raw/{id}", handleRaw)
	r.Get("/p/{id}/edit", handleEditForm)
	r.Post("/p/{id}/edit", handleEditSave)

	// HTTP API
	r.Post("/api/paste", handleAPIPaste)
	r.Post("/api/paste/{id}/edit", handleAPIEdit)

	log.Printf("HTTP:  http://localhost%s\n", listenAddr)
	log.Fatal(http.ListenAndServe(listenAddr, r))
}

/* ==========
   Handlers
   ========== */

func gzipEncode(s string) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed) // Speed reicht, Code komprimiert gut
	_, _ = zw.Write([]byte(s))
	_ = zw.Close()
	return buf.Bytes()
}
func gzipDecode(b []byte) (string, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		return "", err
	}
	return string(out), nil
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

func memUsage() (alloc, sys uint64) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.Alloc, m.Sys
}

func humanBytes(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	author := readAuthorCookie(r)
	alloc, sys := memUsage()
	_ = indexTmpl.Execute(w, map[string]any{
		"Langs":  langs,
		"Themes": themes,
		"Author": author,
		"Alloc":  humanBytes(alloc),
		"Sys":    humanBytes(sys),
		"Count":  store.CountActive(),
	})
}

func handleCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(r.FormValue("code"))
	lang := strings.TrimSpace(r.FormValue("lang"))
	ttl := strings.TrimSpace(r.FormValue("ttl"))
	theme := strings.TrimSpace(r.FormValue("theme"))
	editable := isTruthy(r.FormValue("editable"))
	author := strings.TrimSpace(r.FormValue("author"))
	if author == "" {
		author = readAuthorCookie(r)
	}

	p, err := buildPaste(code, lang, ttl, theme, editable, author)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	store.Put(p)

	// Cookies setzen
	if author != "" {
		writeCookie(w, "np_author", author, 180*24*time.Hour)
	}
	if p.Editable {
		writeCookie(w, "npk_"+p.ID, p.EditKey, 365*24*time.Hour)
	}

	http.Redirect(w, r, "/p/"+p.ID, http.StatusSeeOther)
}

func handleView(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Version wählen (1..N), Default = letzte
	vParam := strings.TrimSpace(r.URL.Query().Get("v"))
	vIdx := len(p.Versions) - 1
	if vParam != "" {
		if n, err := strconv.Atoi(vParam); err == nil && n >= 1 && n <= len(p.Versions) {
			vIdx = n - 1
		}
	}
	currVer := p.Versions[vIdx]
	code, _ := gzipDecode(currVer.ZCode)
	lang := currVer.Lang

	// Theme-Override via ?t=light|dark
	tOverride := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("t")))
	currTheme := p.Theme
	if slices.Contains(themes, tOverride) {
		currTheme = tOverride
	}

	// Zeilen-Highlights via ?hl=…
	hlParam := strings.TrimSpace(r.URL.Query().Get("hl"))
	hlSet := parseHL(hlParam)

	rendered, err := renderWithLineNumbers(code, lang, currTheme, hlSet)
	if err != nil {
		http.Error(w, "Renderfehler", http.StatusInternalServerError)
		return
	}

	canEdit := canEditPaste(r, p)
	// ✅ Edit-URL immer anbieten, wenn editierbar
	editURL := ""
	if p.Editable {
		editURL = "/p/" + p.ID + "/edit?key=" + p.EditKey
	}

	data := map[string]any{
		"ID":        p.ID,
		"Lang":      lang,
		"Theme":     currTheme,
		"ExpiresAt": p.ExpiresAt.Format("2006-01-02 15:04:05 -0700"),
		"HTML":      template.HTML(rendered),
		"HL":        hlParam,

		// Versionen
		"HasHistory": len(p.Versions) > 1,
		"VIndex":     vIdx + 1,
		"VTotal":     len(p.Versions),
		"VAuthor":    orDash(currVer.Author),
		"VTime":      currVer.At.Format("2006-01-02 15:04:05 -0700"),

		// Edit
		"Editable": p.Editable,
		"CanEdit":  canEdit,
		"EditURL":  editURL,
	}
	_ = viewTmpl.Execute(w, data)
}

func handleRaw(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Rohtext = letzte Version
	if len(p.Versions) > 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		s, _ := gzipDecode(p.Versions[len(p.Versions)-1].ZCode)
		_, _ = io.WriteString(w, s)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, p.Code)
}

func handleEditForm(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !canEditPaste(r, p) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	curr := p.Versions[len(p.Versions)-1]
	code, _ := gzipDecode(curr.ZCode)
	author := readAuthorCookie(r)
	if author == "" {
		author = p.Author
	}

	// ✅ Key aus der URL für das POST beibehalten
	key := r.URL.Query().Get("key")

	_ = editTmpl.Execute(w, map[string]any{
		"ID": id, "Code": code, "Langs": langs, "Lang": curr.Lang,
		"Author": author,
		"Key":    key, // <-- neu
	})
}

func handleEditSave(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !canEditPaste(r, p) {
		http.Error(w, "Forbidden (kein Edit-Zugriff)", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "Bad form", http.StatusBadRequest)
		return
	}

	now := time.Now()
	code := strings.TrimSpace(r.FormValue("code"))
	lang := strings.TrimSpace(r.FormValue("lang"))
	author := strings.TrimSpace(r.FormValue("author"))

	if code == "" {
		http.Error(w, "Code darf nicht leer sein", http.StatusBadRequest)
		return
	}

	// letzte Version
	last := p.Versions[len(p.Versions)-1]
	prevCode, _ := gzipDecode(last.ZCode)
	lang = normalizeLang(lang)

	// nur Version anhängen, wenn sich etwas geändert hat
	if code != prevCode || lang != last.Lang {
		p.Versions = append(p.Versions, Version{
			ZCode:  gzipEncode(code),
			Lang:   lang,
			Author: author,
			At:     now,
		})
		// optional: History deckeln
		// if len(p.Versions) > maxVersions {
		//     p.Versions = p.Versions[len(p.Versions)-maxVersions:]
		// }
	}

	// aktuelle Felder pflegen
	p.Code = code
	p.Lang = lang
	if author != "" {
		p.Author = author
	}
	p.UpdatedAt = now
	store.Put(p)

	// Cookies
	if author != "" {
		writeCookie(w, "np_author", author, 180*24*time.Hour)
	}
	if k := r.URL.Query().Get("key"); k != "" && k == p.EditKey {
		writeCookie(w, "npk_"+p.ID, p.EditKey, 365*24*time.Hour)
	}

	http.Redirect(w, r, "/p/"+p.ID+"?v="+strconv.Itoa(len(p.Versions)), http.StatusSeeOther)
}

/* ================
   HTTP API
   ================ */

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

func handleAPIPaste(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	ct := r.Header.Get("Content-Type")
	accept := r.Header.Get("Accept")

	var code, lang, ttl, theme, author string
	var editable bool

	body, _ := io.ReadAll(r.Body)
	if strings.HasPrefix(ct, "application/json") || (len(body) > 0 && bytes.HasPrefix(bytes.TrimSpace(body), []byte("{"))) {
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
		editable = isTruthy(r.URL.Query().Get("editable"))
		author = strings.TrimSpace(r.URL.Query().Get("author"))
	}

	p, err := buildPaste(code, lang, ttl, theme, editable, author)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	store.Put(p)

	// Cookie für Autor
	if author != "" {
		writeCookie(w, "np_author", author, 180*24*time.Hour)
	}

	url := makeURL(r, "/p/"+p.ID)
	raw := makeURL(r, "/raw/"+p.ID)
	edit := ""
	if p.Editable {
		edit = makeURL(r, "/p/"+p.ID+"/edit?key="+p.EditKey)
		// auch den Edit-Key ins Cookie schreiben (Komfort)
		writeCookie(w, "npk_"+p.ID, p.EditKey, 365*24*time.Hour)
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
func handleAPIEdit(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	id := chi.URLParam(r, "id")
	p, ok := store.Get(id)
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
	lang := normalizeLang(req.Lang)
	author := strings.TrimSpace(req.Author)
	now := time.Now()

	// letzte Version zum Vergleich laden
	last := p.Versions[len(p.Versions)-1]
	prevCode, _ := gzipDecode(last.ZCode)

	// Nur neue Version anhängen, wenn sich Inhalt oder Sprache geändert haben
	if code != prevCode || lang != last.Lang {
		p.Versions = append(p.Versions, Version{
			ZCode:  gzipEncode(code),
			Lang:   lang,
			Author: author,
			At:     now,
		})
		// optional deckeln:
		// if len(p.Versions) > maxVersions {
		//     p.Versions = p.Versions[len(p.Versions)-maxVersions:]
		// }
	}

	// aktuelle Felder pflegen
	p.Code = code
	p.Lang = lang
	if author != "" {
		p.Author = author
	}
	p.UpdatedAt = now
	store.Put(p)

	// Komfort: Autor-Cookie setzen/auffrischen
	if author != "" {
		writeCookie(w, "np_author", author, 180*24*time.Hour)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":       p.ID,
		"versions": len(p.Versions),
		"url":      makeURL(r, "/p/"+p.ID+"?v="+strconv.Itoa(len(p.Versions))),
	})
}

/* ===================
   Rendering & Helpers
   =================== */

func buildPaste(code, lang, ttl, theme string, editable bool, author string) (Paste, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return Paste{}, fmt.Errorf("Code darf nicht leer sein")
	}
	lang = normalizeLang(lang)
	if !slices.Contains(themes, theme) {
		theme = "dark"
	}
	dur, err := parseTTL(ttl)
	if err != nil {
		return Paste{}, fmt.Errorf("Ungültige TTL")
	}
	now := time.Now()
	id := newID(8)
	p := Paste{
		ID:        id,
		Lang:      lang,
		Code:      code,
		Theme:     theme,
		ExpiresAt: now.Add(dur),

		Editable: editable,
		EditKey:  "",
		Author:   author,

		Versions:  []Version{{ZCode: gzipEncode(code), Lang: lang, Author: author, At: now}},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if editable {
		p.EditKey = newID(12)
	}
	return p, nil
}

func normalizeLang(lang string) string {
	if !slices.Contains(langs, lang) {
		return "plaintext"
	}
	return lang
}

func renderWithLineNumbers(code, lang, theme string, hl map[int]bool) (string, error) {
	lexer := lexers.Get(lang)
	if lexer == nil {
		lexer = lexers.Analyse(code)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	styleName := "dracula"
	if theme == "light" {
		styleName = "github"
	}
	style := styles.Get(styleName)
	if style == nil {
		style = styles.Fallback
	}

	formatter := chromahtml.New(
		chromahtml.WithLineNumbers(false), // eigene Nummern
		chromahtml.WithClasses(false),
		chromahtml.TabWidth(2),
	)

	it, err := lexer.Tokenise(nil, code)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, it); err != nil {
		return "", err
	}

	full := buf.String()
	start := strings.Index(full, "<code")
	if start == -1 {
		start = 0
	} else {
		if gt := strings.Index(full[start:], ">"); gt != -1 {
			start = start + gt + 1
		}
	}
	end := strings.LastIndex(full, "</code>")
	if end == -1 {
		end = len(full)
	}
	inner := full[start:end]

	lines := strings.Split(inner, "\n")
	var out bytes.Buffer
	out.WriteString(`<div class="codeframe"><div class="codeblock">`)
	for i, ln := range lines {
		lineNo := i + 1
		if i == len(lines)-1 && ln == "" {
			break
		}
		id := fmt.Sprintf("L%d", lineNo)
		cls := "line"
		if hl[lineNo] {
			cls += " hl"
		}
		out.WriteString(`<div id="` + id + `" class="` + cls + `">`)
		out.WriteString(`<a class="ln" href="#` + id + `">` + fmt.Sprint(lineNo) + `</a>`)
		out.WriteString(`<span class="code">` + ln + `</span>`)
		out.WriteString(`</div>`)
	}
	out.WriteString(`</div></div>`)
	return out.String(), nil
}

func parseTTL(s string) (time.Duration, error) {
	switch s {
	case "1h":
		return time.Hour, nil
	case "24h":
		return 24 * time.Hour, nil
	case "168h", "7d":
		return 168 * time.Hour, nil
	case "":
		return 24 * time.Hour, nil
	default:
		return time.ParseDuration(s)
	}
}

func parseHL(s string) map[int]bool {
	hl := map[int]bool{}
	if s == "" {
		return hl
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			chunks := strings.SplitN(part, "-", 2)
			a, errA := strconv.Atoi(strings.TrimSpace(chunks[0]))
			b, errB := strconv.Atoi(strings.TrimSpace(chunks[1]))
			if errA == nil && errB == nil {
				if a > b {
					a, b = b, a
				}
				for i := a; i <= b; i++ {
					hl[i] = true
				}
			}
		} else {
			if n, err := strconv.Atoi(part); err == nil {
				hl[n] = true
			}
		}
	}
	return hl
}

func newID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return base64.RawURLEncoding.EncodeToString([]byte(time.Now().Format("150405.000")))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func makeURL(r *http.Request, path string) string {
	if publicBase != "" {
		return strings.TrimRight(publicBase, "/") + path
	}
	scheme := "http"
	if r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

func seoNoIndex(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

func isTruthy(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "1" || s == "true" || s == "on" || s == "yes"
}

func writeCookie(w http.ResponseWriter, name, value string, life time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  time.Now().Add(life),
		MaxAge:   int(life / time.Second),
		SameSite: http.SameSiteLaxMode,
	})
}

func readAuthorCookie(r *http.Request) string {
	if c, err := r.Cookie("np_author"); err == nil {
		return c.Value
	}
	return ""
}

func canEditPaste(r *http.Request, p Paste) bool {
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

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

/* ===============
   Templates
   =============== */

const indexHTML = `
<!doctype html><meta charset="utf-8">
<title>unglued</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
*,*::before,*::after{ box-sizing: border-box }

:root{ --bg:#0b0c0e; --fg:#e6e6e6; --muted:#c8c8c8; --card:#0f1115; --border:#1b1f2a; --link:#9ecbff; }
@media (prefers-color-scheme:light){
  :root{ --bg:#ffffff; --fg:#111; --muted:#444; --card:#f8f9fb; --border:#e5e7eb; --link:#0b57d0; }
}

body{font:16px/1.5 system-ui,-apple-system,Segoe UI,Roboto,Ubuntu,Cantarell,sans-serif;margin:0;background:var(--bg);color:var(--fg)}
main{max-width:900px;margin:0 auto;padding:24px}
label{display:block;margin:.5rem 0 .25rem;color:var(--muted)}

textarea,input,select,button{
  width:100%; display:block;
  padding:.75rem; border-radius:12px; border:1px solid var(--border);
  background:var(--card); color:var(--fg)
}

button{cursor:pointer;font-weight:600}
.row{display:grid; grid-template-columns:1fr; gap:12px}
.card{background:var(--card);padding:20px;border:1px solid var(--border);border-radius:16px;box-shadow:0 6px 20px rgba(0,0,0,.12)}
small{opacity:.7}
a{color:var(--link);text-decoration:none} a:hover{text-decoration:underline}
.inline{display:flex;gap:12px;align-items:center}
.checkbox{display:flex;gap:8px;align-items:center}

.codeeditor{
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  tab-size: 2;                /* Darstellung von \t */
  -moz-tab-size: 2;
  white-space: pre;
  resize: vertical;
}


.topbar{display:flex;justify-content:space-between;align-items:baseline;margin:0 0 8px 0}
.stats{font-size:14px;opacity:.8}

</style>
<main>
  <h1>unglued</h1>
    <div class="stats">
    Aktuell {{.Alloc}} von {{.Sys}} (OS) · Pastes: {{.Count}}
  </div>
  <div class="card">
    <form method="post" action="/paste">
      <label for="lang">Sprache</label>
      <select id="lang" name="lang">
        {{range .Langs}}<option value="{{.}}">{{.}}</option>{{end}}
      </select>

      <label for="theme">Theme (Default)</label>
      <select id="theme" name="theme">
        <option value="dark" selected>Dark</option>
        <option value="light">Light</option>
      </select>

      <label for="code">Code / Text</label>
      <textarea id="code" name="code" rows="16" class="codeeditor"
  spellcheck="false" autocapitalize="off" autocomplete="off" autocorrect="off"
  placeholder="Füge deinen Code hier ein…"></textarea>


      <div class="row">
        <div>
          <label for="ttl">Ablauf</label>
          <select id="ttl" name="ttl">
            <option value="1h">1 Stunde</option>
            <option value="24h" selected>24 Stunden</option>
            <option value="168h">7 Tage</option>
          </select>
        </div>
        <div>
          <label for="author">Name (optional)</label>
          <input id="author" name="author" value="{{.Author}}" placeholder="Dein Name oder Nick">
          <div class="checkbox" style="margin-top:.5rem">
            <input id="editable" type="checkbox" name="editable">
            <label for="editable" style="margin:0">Editierbar (nur mit geheimem Link / Cookie)</label>
          </div>
        </div>
      </div>

      <div style="text-align:right;margin-top:12px">
        <button type="submit">Link erzeugen</button>
      </div>

      <small>API: POST /api/paste – JSON-Felder: code, lang, ttl, theme, editable, author.</small>
    </form>
  </div>
</main>
`

const viewHTML = `
<!doctype html><meta charset="utf-8">
<title>unglued – {{.ID}}</title>
<meta name="viewport" content="width=device-width,initial-scale=1">

<style>
{{if eq .Theme "light"}}
:root{
  --bg:#ffffff; --fg:#111; --muted:#444; --card:#f8f9fb; --border:#e5e7eb; --link:#0b57d0;
  --hlbg:#fff3bf; --hlline:#e6b800;
}
{{else}}
:root{
  --bg:#0b0c0e; --fg:#e6e6e6; --muted:#c8c8c8; --card:#0f1115; --border:#1b1f2a; --link:#9ecbff;
  --hlbg:rgba(255,210,87,.28); --hlline:#ffd257;
}
{{end}}
body{font:16px/1.5 system-ui,-apple-system,Segoe UI,Roboto,Ubuntu,Cantarell,sans-serif;margin:0;background:var(--bg);color:var(--fg)}
main{max-width:900px;margin:0 auto;padding:24px}
.card{background:var(--card);padding:20px;border:1px solid var(--border);border-radius:16px;box-shadow:0 6px 20px rgba(0,0,0,.12)}
header{display:flex;gap:12px;justify-content:space-between;align-items:center;margin-bottom:8px;flex-wrap:wrap}
a{color:var(--link);text-decoration:none}
a:hover{text-decoration:underline}
.badge{font-size:12px;opacity:.8}
.button{border:1px solid var(--border);background:var(--card);padding:.35rem .6rem;border-radius:10px}

/* Codeblock */
.codeframe{overflow:auto;border-radius:12px;border:1px solid var(--border)}
.codeblock{
  font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace;
  white-space:pre;
  font-size:13px;
  line-height:1.2;
}
.line{
  display:flex;
  padding:0 .5rem;
  scroll-margin-top:72px;
  align-items:center;
  gap:8px;
}
.line .ln{
  display:flex; align-items:center; justify-content:flex-end;
  width:3.2ch;
  text-decoration:none; opacity:.55; padding-right:.4rem; user-select:none;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-variant-numeric: tabular-nums;
  font-size:13px; line-height:1.2;
}
.line .code{ white-space:pre; display:block; font:inherit; line-height:inherit; }

/* Highlights */
.line.hl, .line:target{ background:var(--hlbg); box-shadow: inset 4px 0 0 var(--hlline) }
.line.hl .ln, .line:target .ln{ opacity:1; color:var(--hlline); font-weight:700 }
.meta{display:flex;gap:8px;flex-wrap:wrap;align-items:center}

.codeeditor{
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  tab-size: 2;                /* Darstellung von \t */
  -moz-tab-size: 2;
  white-space: pre;
  resize: vertical;
}


</style>

<main>
  <header>
    <div>Paste <strong>{{.ID}}</strong> <span class="badge">Sprache: {{.Lang}}</span></div>
    <div class="meta">
      <div class="badge">Ablauf: {{.ExpiresAt}}</div>
      {{if .HasHistory}}<div class="badge">Version {{.VIndex}} / {{.VTotal}} – Autor: {{.VAuthor}} – {{.VTime}}</div>{{end}}
      <nav>
        {{if eq .Theme "light"}}
          <a class="button" href="?t=dark{{if .HL}}&hl={{.HL}}{{end}}{{if .HasHistory}}&v={{.VIndex}}{{end}}"  title="zu Dark wechseln">Dark</a>
          <span class="badge">• Aktuell: Light</span>
        {{else}}
          <a class="button" href="?t=light{{if .HL}}&hl={{.HL}}{{end}}{{if .HasHistory}}&v={{.VIndex}}{{end}}" title="zu Light wechseln">Light</a>
          <span class="badge">• Aktuell: Dark</span>
        {{end}}
	{{if .Editable}} • <a class="button" href="{{.EditURL}}">Editieren</a>{{end}}
      </nav>
    </div>
  </header>

  <div class="card">
    {{.HTML}}
  </div>

  <p>
    <a href="/">Neue Paste erstellen</a>
    • <a href="/raw/{{.ID}}">Raw</a>
    {{if .HL}}• <span class="badge">Markiert: {{.HL}}</span>{{end}}
    {{if .HasHistory}}
      • <span class="badge">Version wechseln:</span>
      {{if gt .VIndex 1}}<a href="/p/{{.ID}}?v={{dec .VIndex}}">« Vorherige</a>{{end}}
      {{if lt .VIndex .VTotal}} {{if gt .VIndex 1}}•{{end}} <a href="/p/{{.ID}}?v={{inc .VIndex}}">Nächste »</a>{{end}}
    {{end}}
  </p>

  <!-- JS: hl=… / #L… markieren & Click-Range -->
  <script>
  (function(){
    function parseHLParam(str){
      var set = new Set(); if(!str) return set;
      var parts = str.split(',');
      for(var k=0;k<parts.length;k++){
        var part = parts[k].trim(); if(!part) continue;
        if(part.indexOf('-') !== -1){
          var ab = part.split('-',2); var a = +ab[0], b = +ab[1];
          if(Number.isInteger(a) && Number.isInteger(b)){
            var lo = Math.min(a,b), hi = Math.max(a,b);
            for(var i=lo;i<=hi;i++) set.add(i);
          }
        } else {
          var n = +part; if(Number.isInteger(n)) set.add(n);
        }
      }
      return set;
    }
    function apply(set){
      var marked = document.querySelectorAll('.line.hl');
      for(var i=0;i<marked.length;i++) marked[i].classList.remove('hl');
      set.forEach(function(n){
        var el = document.getElementById('L'+n);
        if(el) el.classList.add('hl');
      });
    }
    var params = new URLSearchParams(location.search);
    var set = parseHLParam(params.get('hl'));
    apply(set);
    if(location.hash.slice(0,2) === '#L'){
      var nHash = +location.hash.slice(2);
      if(Number.isInteger(nHash)){ set.add(nHash); apply(set); }
    }
    var last = null;
    var links = document.querySelectorAll('.line .ln');
    for(var i=0;i<links.length;i++){
      links[i].addEventListener('click', function(e){
        e.preventDefault();
        var n = +this.getAttribute('href').slice(2);
        if(!Number.isInteger(n)) return;
        if(e.shiftKey && last !== null){
          var lo = Math.min(last,n), hi = Math.max(last,n);
          for(var j=lo;j<=hi;j++) set.add(j);
        } else {
          if(set.has(n)) set.delete(n); else set.add(n);
          last = n;
        }
        apply(set);
        var list = Array.from(set).sort(function(a,b){return a-b;});
        var out = [];
        for(var p=0;p<list.length;p++){
          var q=p;
          while(q+1<list.length && list[q+1]===list[q]+1) q++;
          if(q>p) out.push(String(list[p]) + '-' + String(list[q]));
          else out.push(String(list[p]));
          p=q;
        }
        params.set('hl', out.join(','));
        var url = location.pathname + '?' + params.toString() + location.hash;
        history.replaceState(null, '', url);
      });
    }
  })();
  </script>


<script>
(function(){
  var ta = document.getElementById('code');
  if(!ta) return;

  var TAB = "  "; // Soft-Tab: 2 Spaces (ggf. "    " für 4)
  function getLineStart(text, pos){
    var i = text.lastIndexOf("\n", pos-1);
    return i === -1 ? 0 : i+1;
  }

  ta.addEventListener('keydown', function(e){
    // Ctrl/Cmd+Enter -> submit
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      if (ta.form) ta.form.submit();
      return;
    }

    // Tab/Shift+Tab -> indent/outdent
    if (e.key === "Tab") {
      e.preventDefault();
      var val = ta.value, start = ta.selectionStart, end = ta.selectionEnd;

      // Selektion über mehrere Zeilen?
      if (start !== end) {
        var selStart = getLineStart(val, start);
        var sel = val.slice(selStart, end);
        var lines = sel.split("\n");

        if (e.shiftKey) {
          // ausrücken
          for (var i=0;i<lines.length;i++){
            if (lines[i].startsWith(TAB)) lines[i] = lines[i].slice(TAB.length);
            else if (lines[i].startsWith("\t")) lines[i] = lines[i].slice(1);
          }
        } else {
          // einrücken
          for (var i=0;i<lines.length;i++){
            lines[i] = TAB + lines[i];
          }
        }

        var replaced = lines.join("\n");
        var before = val.slice(0, selStart);
        var after  = val.slice(end);
        ta.value = before + replaced + after;

        // Selektion neu setzen: umfasst weiter alle geänderten Zeilen
        ta.selectionStart = selStart;
        ta.selectionEnd = selStart + replaced.length;
      } else {
        // Caret-Indent
        var before = val.slice(0, start);
        var after  = val.slice(end);
        if (e.shiftKey) {
          // ausrücken an Zeilenanfang
          var ls = getLineStart(val, start);
          if (val.slice(ls, ls+TAB.length) === TAB) {
            ta.value = val.slice(0, ls) + val.slice(ls+TAB.length);
            var delta = TAB.length;
            ta.selectionStart = ta.selectionEnd = Math.max(start - delta, ls);
          } else if (val[ls] === "\t") {
            ta.value = val.slice(0, ls) + val.slice(ls+1);
            ta.selectionStart = ta.selectionEnd = Math.max(start - 1, ls);
          }
        } else {
          ta.value = before + TAB + after;
          ta.selectionStart = ta.selectionEnd = start + TAB.length;
        }
      }
      return;
    }

    // Enter -> Auto-Indent
    if (e.key === "Enter") {
      e.preventDefault();
      var val = ta.value, start = ta.selectionStart, end = ta.selectionEnd;
      var ls = getLineStart(val, start);
      var linePrefix = val.slice(ls, start);
      var m = linePrefix.match(/^[ \t]+/);
      var indent = m ? m[0] : "";
      var insert = "\n" + indent;
      ta.value = val.slice(0, start) + insert + val.slice(end);
      ta.selectionStart = ta.selectionEnd = start + insert.length;
      return;
    }
  });
})();
</script>



</main>
`

// kleine Template-Funktionen für {{inc}}/{{dec}}
var _ = func() struct{} {
	viewTmpl = template.Must(template.New("view").Funcs(template.FuncMap{
		"inc": func(i int) int { return i + 1 },
		"dec": func(i int) int { return i - 1 },
	}).Parse(viewHTML))
	return struct{}{}
}()

const editHTML = `
<!doctype html><meta charset="utf-8">
<title>unglued – Edit {{.ID}}</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
:root{ --bg:#0b0c0e; --fg:#e6e6e6; --muted:#c8c8c8; --card:#0f1115; --border:#1b1f2a; --link:#9ecbff; }
@media (prefers-color-scheme:light){
  :root{ --bg:#ffffff; --fg:#111; --muted:#444; --card:#f8f9fb; --border:#e5e7eb; --link:#0b57d0; }
}
body{font:16px/1.5 system-ui,-apple-system,Segoe UI,Roboto,Ubuntu,Cantarell,sans-serif;margin:0;background:var(--bg);color:var(--fg)}
main{max-width:900px;margin:0 auto;padding:24px}
label{display:block;margin:.5rem 0 .25rem;color:var(--muted)}
textarea,input,select,button{width:100%;padding:.75rem;border-radius:12px;border:1px solid var(--border);background:var(--card);color:var(--fg)}
button{cursor:pointer;font-weight:600}
.card{background:var(--card);padding:20px;border:1px solid var(--border);border-radius:16px;box-shadow:0 6px 20px rgba(0,0,0,.12)}
a{color:var(--link);text-decoration:none} a:hover{text-decoration:underline}
.actions{display:flex;gap:12px;align-items:center;justify-content:flex-end}
</style>
<main>
  <h1>Edit <code>{{.ID}}</code></h1>
  <div class="card">
  <form method="post" action="/p/{{.ID}}/edit{{if .Key}}?key={{.Key}}{{end}}">

      <label for="lang">Sprache</label>
      <select id="lang" name="lang">
        {{range .Langs}}<option value="{{.}}" {{if eq $.Lang .}}selected{{end}}>{{.}}</option>{{end}}
      </select>

      <label for="author">Name (optional)</label>
      <input id="author" name="author" value="{{.Author}}" placeholder="Dein Name oder Nick">

      <label for="code">Code / Text</label>
      <textarea id="code" name="code" rows="18" class="codeeditor"
  spellcheck="false" autocapitalize="off" autocomplete="off" autocorrect="off">{{.Code}}</textarea>


      <div class="actions">
        <a href="/p/{{.ID}}">Abbrechen</a>
        <button type="submit">Speichern</button>
      </div>
    </form>
  </div>

<script>
(function(){
  var ta = document.getElementById('code');
  if(!ta) return;

  var TAB = "  "; // Soft-Tab: 2 Spaces (ggf. "    " für 4)
  function getLineStart(text, pos){
    var i = text.lastIndexOf("\n", pos-1);
    return i === -1 ? 0 : i+1;
  }

  ta.addEventListener('keydown', function(e){
    // Ctrl/Cmd+Enter -> submit
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter") {
      e.preventDefault();
      if (ta.form) ta.form.submit();
      return;
    }

    // Tab/Shift+Tab -> indent/outdent
    if (e.key === "Tab") {
      e.preventDefault();
      var val = ta.value, start = ta.selectionStart, end = ta.selectionEnd;

      // Selektion über mehrere Zeilen?
      if (start !== end) {
        var selStart = getLineStart(val, start);
        var sel = val.slice(selStart, end);
        var lines = sel.split("\n");

        if (e.shiftKey) {
          // ausrücken
          for (var i=0;i<lines.length;i++){
            if (lines[i].startsWith(TAB)) lines[i] = lines[i].slice(TAB.length);
            else if (lines[i].startsWith("\t")) lines[i] = lines[i].slice(1);
          }
        } else {
          // einrücken
          for (var i=0;i<lines.length;i++){
            lines[i] = TAB + lines[i];
          }
        }

        var replaced = lines.join("\n");
        var before = val.slice(0, selStart);
        var after  = val.slice(end);
        ta.value = before + replaced + after;

        // Selektion neu setzen: umfasst weiter alle geänderten Zeilen
        ta.selectionStart = selStart;
        ta.selectionEnd = selStart + replaced.length;
      } else {
        // Caret-Indent
        var before = val.slice(0, start);
        var after  = val.slice(end);
        if (e.shiftKey) {
          // ausrücken an Zeilenanfang
          var ls = getLineStart(val, start);
          if (val.slice(ls, ls+TAB.length) === TAB) {
            ta.value = val.slice(0, ls) + val.slice(ls+TAB.length);
            var delta = TAB.length;
            ta.selectionStart = ta.selectionEnd = Math.max(start - delta, ls);
          } else if (val[ls] === "\t") {
            ta.value = val.slice(0, ls) + val.slice(ls+1);
            ta.selectionStart = ta.selectionEnd = Math.max(start - 1, ls);
          }
        } else {
          ta.value = before + TAB + after;
          ta.selectionStart = ta.selectionEnd = start + TAB.length;
        }
      }
      return;
    }

    // Enter -> Auto-Indent
    if (e.key === "Enter") {
      e.preventDefault();
      var val = ta.value, start = ta.selectionStart, end = ta.selectionEnd;
      var ls = getLineStart(val, start);
      var linePrefix = val.slice(ls, start);
      var m = linePrefix.match(/^[ \t]+/);
      var indent = m ? m[0] : "";
      var insert = "\n" + indent;
      ta.value = val.slice(0, start) + insert + val.slice(end);
      ta.selectionStart = ta.selectionEnd = start + insert.length;
      return;
    }
  });
})();
</script>



  </main>
`
