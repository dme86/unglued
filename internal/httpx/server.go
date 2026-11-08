package httpx

import (
	"html/template"
    "net/http"
    "strings"
	"unglued/internal/store"
)

/*
Server hält Store, Config und bereits geparste Templates.
*/
type Server struct {
	Store     *store.Store
	Config    Config
	IndexTmpl *template.Template
	ViewTmpl  *template.Template
	EditTmpl  *template.Template
}

/*
Config: aktuell nur PublicBase; bei Bedarf erweiterbar (ListenAddr usw.).
*/
type Config struct {
	PublicBase string
}

/*
NewServer: du gibst geparste Templates rein (siehe MustParseTemplates in templates.go).
*/
func NewServer(cfg Config, st *store.Store, index, view, edit *template.Template) *Server {
	return &Server{
		Store:     st,
		Config:    cfg,
		IndexTmpl: index,
		ViewTmpl:  view,
		EditTmpl:  edit,
	}
}

// HTTP bodies may include JSON or multipart framing beyond the paste content.
const (
	MaxPasteBytes   = 1 << 20
	MaxRequestBytes = 2 << 20
)

func LimitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func parseAnyForm(r *http.Request) error {
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/form-data") {
		// 16 MiB In-Memory, Rest geht in Tempfiles.
		return r.ParseMultipartForm(16 << 20)
	}
	return r.ParseForm()
}


/*
Lang-/Theme-Optionen zentral hier.
*/
var (
	Langs  = []string{"plaintext", "go", "javascript", "typescript", "json", "yaml", "toml", "python", "bash", "html", "css", "sql", "markdown"}
	Themes = []string{"dark", "light"}
)

