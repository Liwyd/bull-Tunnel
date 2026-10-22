// Package web is the web interface of the panel: the pages are rendered by
// Go, and the few files that must be a browser's job — the stylesheet, the
// theme and the canvas — are served beside them.
//
// The interface is plain HTML, CSS and JavaScript: no build step, no
// framework, no bundler. The template is embedded in the binary, so the
// panel needs nothing beside itself to show its pages.
package web

import (
	"embed"
	"html/template"
	"sync"
)

//go:embed templates/*.html
var files embed.FS

// Pages are the pages of the panel, rendered by Go.
var Pages = struct {
	sync.Mutex
	shell *template.Template
}{}

// Shell is the dashboard's page: the sidebar, the header, the views and the
// sign-in card. The browser fills the views with the state the API answers.
func Shell(version string) ([]byte, error) {
	Pages.Lock()
	defer Pages.Unlock()
	if Pages.shell == nil {
		parsed, err := template.ParseFS(files, "templates/*.html")
		if err != nil {
			return nil, err
		}
		Pages.shell = parsed
	}
	var out struct{ Version string }
	out.Version = version
	return render(Pages.shell, "shell.html", out)
}
