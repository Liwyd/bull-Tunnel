package web

import (
	"bytes"
	"html/template"
)

// render writes a page.
func renderPage(tmpl *template.Template, name string, data any) ([]byte, error) {
	var out bytes.Buffer
	if err := tmpl.ExecuteTemplate(&out, name, data); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
