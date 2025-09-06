package httpx

import (
	"embed"
	"html/template"
)

//go:embed templates/*.html
var tplFS embed.FS

var tmplFuncs = template.FuncMap{
	"inc": func(i int) int { return i + 1 },
	"dec": func(i int) int { return i - 1 },
}

func LoadTemplates() (index, view, edit *template.Template) {
	index = template.Must(template.New("index").Parse(indexHTML))
	view  = template.Must(template.New("view").Funcs(tmplFuncs).Parse(viewHTML))
	edit  = template.Must(template.New("edit").Parse(editHTML))
	return
}

func MustParseTemplates() (*template.Template, *template.Template, *template.Template) {
	funcs := template.FuncMap{
		"inc": func(i int) int { return i + 1 },
		"dec": func(i int) int { return i - 1 },
	}
	index := template.Must(template.New("index").Funcs(funcs).ParseFS(tplFS, "templates/index.html"))
	view  := template.Must(template.New("view").Funcs(funcs).ParseFS(tplFS, "templates/view.html"))
	edit  := template.Must(template.New("edit").Funcs(funcs).ParseFS(tplFS, "templates/edit.html"))
	return index, view, edit
}

