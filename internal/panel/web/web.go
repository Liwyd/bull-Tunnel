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

// Dashboard is the dashboard's page with the network view in it: the cards,
// the titles of the columns and the numbers of the strip are in the page
// before the script runs, and the script draws the same view over them when
// the state changes.
func Dashboard(version string, view *NetworkView) ([]byte, error) {
	return render(version, view)
}

// Shell is the dashboard's page: the sidebar, the header, the views and the
// sign-in card, without the network view in it.
func Shell(version string) ([]byte, error) {
	return render(version, nil)
}

// render writes the page.
func render(version string, view *NetworkView) ([]byte, error) {
	Pages.Lock()
	defer Pages.Unlock()
	if Pages.shell == nil {
		parsed, err := template.ParseFS(files, "templates/*.html")
		if err != nil {
			return nil, err
		}
		Pages.shell = parsed
	}
	out := struct {
		Version string
		Graph   *NetworkView
	}{Version: version, Graph: view}
	return renderPage(Pages.shell, "shell.html", out)
}
