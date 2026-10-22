package web

import (
	"strings"
	"testing"
)

// The dashboard is rendered by Go: the page is in the binary, and the panel
// shows it even when nothing is beside it.
func TestTheShellIsRenderedByGo(t *testing.T) {
	page, err := Shell("1.2.0")
	if err != nil {
		t.Fatalf("the page cannot be rendered: %v", err)
	}
	text := string(page)

	for _, want := range []string{
		"<!doctype html>",
		`id="side"`,
		`id="view-network"`,
		`id="auth-form"`,
		`content="1.2.0"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the page does not have %q", want)
		}
	}
	if strings.Contains(text, "{{") {
		t.Error("the page still has the marks of the template in it")
	}
}
