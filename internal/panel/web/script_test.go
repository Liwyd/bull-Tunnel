package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The drawer is the panel's work now: the script names what he has open
// (open= on the view), swaps the part that comes back into the page, and
// answers the buttons of the drawer with the vocabulary every button of
// the dashboard speaks. It must not grow drawers of its own again: those
// builders knew every field of the panel by heart, and every one of them
// drifted away from what the API takes.
//
// This reads the script the way the guard of the collector does, because
// a builder that comes back only ever shows up in a browser.
func TestTheScriptAsksForTheDrawerInsteadOfDrawingIt(t *testing.T) {
	raw, err := os.ReadFile("../../../static/app.js")
	if err != nil {
		t.Fatalf("the script of the panel: %v", err)
	}
	script := string(raw)

	// what is left of the drawer: the part is swapped into the page (the
	// empty one closes it), and the countdown is the panel's span read once
	// a second — nothing of the drawer is worked out here
	for _, kept := range []string{
		`params.set("open", `,
		`PARTS["drawer-body"] === ""`,
		`querySelectorAll("span[data-at]")`,
		`el.dataset.at`,
	} {
		if !strings.Contains(script, kept) {
			t.Errorf("the script does not have %q in it any more", kept)
		}
	}

	// the builders it had are gone, with the marks they answered and the
	// globals they kept
	gone := []string{
		"renderDrawer(", "probeTable(", "failbackText(", "groupDraft", "selTransport",
		"data-apply-transport", "data-save-ports", "data-g-mode", "data-unpin",
		"data-pick=", "Measuring the path",
	}
	for _, one := range gone {
		if strings.Contains(script, one) {
			t.Errorf("the script still holds %q: the drawer is drawn by the panel", one)
		}
	}

	// and every button of the drawer is answered by the one generic
	// listener: a call to make, a question to ask first, a message when it
	// is done — and an answer written as text, never as markup
	for _, kept := range []string{
		`[data-act],[data-act-form]`,
		`b.dataset.confirm`,
		`b.dataset.done`,
		`into.textContent =`,
	} {
		if !strings.Contains(script, kept) {
			t.Errorf("the script does not have the generic button %q", kept)
		}
	}
	// the answer of a call is never written into the page as markup: the
	// one place an answer lands is the box of a data-act-into, as text
	answer := regexp.MustCompile(`(answer|r)\.[\w.]+[^;\n]*innerHTML`)
	if found := answer.FindAllString(script, -1); len(found) > 0 {
		t.Errorf("the script writes %q into the page as markup", found)
	}
}
