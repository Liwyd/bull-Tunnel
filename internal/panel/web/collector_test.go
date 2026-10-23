package web

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The marks of the collector come without a value: an attribute with no
// value reads back as an empty string in JavaScript, so a mark read as
// truthy is not there at all — a number went out as the text on the page
// (minutes, not seconds), a group of choices as the last switch of it, a
// field of several lines as one string the API refuses with a 400, and
// the file input, which has no key of its own, was sent under a key that
// does not exist and threw before it got there.
//
// The collector looks for these marks with `in`, and this test keeps the
// panel from reading them as values again — every one of them failed in
// the browser, where none of the Go tests can see it.
func TestTheMarksOfTheCollectorAreLookedForNotRead(t *testing.T) {
	raw, err := os.ReadFile("../../../static/app.js")
	if err != nil {
		t.Fatalf("the script of the panel: %v", err)
	}
	script := string(raw)

	// a value read of the bare mark: el.dataset.lines) — in a condition,
	// a call or an assignment, the mark is empty and the branch is dead.
	read := regexp.MustCompile(`dataset\.(num|checks|lines|file)\s*\)`)
	for _, found := range read.FindAllString(script, -1) {
		t.Errorf("the script reads %q as a value; a mark without a value is empty, it must be looked for with `in`", found)
	}

	for _, mark := range []string{"num", "checks", "lines", "file"} {
		want := `"` + mark + `" in el.dataset`
		if !strings.Contains(script, want) {
			t.Errorf("the collector does not look for the mark %q; it must say %s", mark, want)
		}
	}
}
