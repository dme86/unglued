package render

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

func CodeHTML(code, lang, theme string, hl map[int]bool) (template.HTML, error) {
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
		chromahtml.WithLineNumbers(false),
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
	} else if gt := strings.Index(full[start:], ">"); gt != -1 {
		start = start + gt + 1
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
		if i == len(lines)-1 && ln == "" {
			break
		}
		n := i + 1
		id := fmt.Sprintf("L%d", n)
		cls := "line"
		if hl[n] {
			cls += " hl"
		}
		out.WriteString(`<div id="` + id + `" class="` + cls + `">`)
		out.WriteString(`<a class="ln" href="#` + id + `">` + fmt.Sprint(n) + `</a>`)
		out.WriteString(`<span class="code">` + ln + `</span>`)
		out.WriteString(`</div>`)
	}
	out.WriteString(`</div></div>`)
	return template.HTML(out.String()), nil
}

