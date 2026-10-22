package panel

import (
	"os"
	"strings"
	"testing"
)

// The copyright line of the licence and of the notice names the author
// of this project, and nobody else. It is the line a reader of the
// repository asks first, and it must say the same thing in both files.
//
// This test is not about the credit of the tunnel engine: that one is
// named in the notice and in the README because the AGPL the engine is
// under asks for it, and no copyright line is going to take it away.
func TestTheCopyrightLinesNameTheAuthor(t *testing.T) {
	for _, file := range []string{"../../LICENSE", "../../NOTICE"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}

		found := ""
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "Copyright") {
				found = line
				break
			}
		}
		if found == "" {
			t.Errorf("%s has no copyright line at all", file)
			continue
		}
		if !strings.Contains(found, "Liwyd") {
			t.Errorf("%s: the copyright line does not name Liwyd: %q", file, strings.TrimSpace(found))
		}
		if strings.Contains(found, "contributors") {
			t.Errorf("%s: the copyright line names contributors instead of the author: %q", file, strings.TrimSpace(found))
		}
	}
}
