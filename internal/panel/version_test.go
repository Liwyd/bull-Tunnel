package panel

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The version of the panel and the changelog must not part ways: a
// release whose number the changelog has never heard of is a release
// nobody can read the notes of. The number must look like a version as
// well — the build script reads it out of version.go and stamps it into
// every binary it builds.
func TestTheChangelogCarriesThisVersion(t *testing.T) {
	if parts := strings.Split(Version, "."); len(parts) != 3 {
		t.Errorf("the version %q is not three numbers", Version)
	}

	raw, err := os.ReadFile("../../CHANGELOG.md")
	if err != nil {
		t.Fatalf("the changelog: %v", err)
	}
	heading := "## " + Version + " — "
	if !strings.Contains(string(raw), heading) {
		t.Errorf("CHANGELOG.md has no %q heading, but the panel is version %s", heading, Version)
	}
}

// The design mockups are gone and they stay gone. They were 18MB of SVG
// that nothing loaded: every reference to them in the code was a
// provenance comment, and in each case the design was already written out
// beside the pointer — the palette in the header of static/style.css, the
// type-1/type-2 decision in the changelog. So the mockups carried no
// information that the repository did not already hold in text, and a
// checkout got 18MB lighter for removing them.
//
// This test is the tripwire. It fails if the directory comes back, and it
// fails if any live file points at a mockup that is not there — which is
// the state the deletion was careful to avoid: a comment naming a file a
// reader cannot open. The two references that are allowed to remain are
// the historical ones, in the changelog and in this file's own plan, which
// record what a released version was built against.
func TestTheDesignMockupsAreGoneAndNothingPointsAtThem(t *testing.T) {
	root := "../../"

	if _, err := os.Stat(root + "new-ui"); err == nil {
		t.Error("new-ui/ is back: 18MB of design mockups that nothing loads")
	}

	// The files that may still name a mockup, and why:
	//   CHANGELOG.md — a record of what a released version shipped.
	//   TODO.md      — a record of what the commits did.
	// Everything else must speak of the design, not of a file.
	historical := map[string]bool{"CHANGELOG.md": true, "TODO.md": true}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if historical[filepath.Base(rel)] {
			return nil
		}
		// This test holds the strings it searches for, the way
		// licence_test.go does.
		if rel == filepath.Join("internal", "panel", "version_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, "new-ui/") {
				t.Errorf("%s:%d points at the design mockups, which are deleted: %s",
					rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking the repository: %v", walkErr)
	}
}
