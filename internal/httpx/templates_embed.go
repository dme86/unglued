// internal/httpx/templates_embed.go
package httpx

import _ "embed"

//go:embed templates/index.html
var indexHTML string

//go:embed templates/view.html
var viewHTML string

//go:embed templates/edit.html
var editHTML string

