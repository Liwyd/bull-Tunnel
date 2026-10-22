package panel

import (
	"os"
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
