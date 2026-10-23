package panel

import (
	"io/fs"
	"os"
	"path/filepath"
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

// The two lines the engine's NOTICE asks to be kept visible and unaltered,
// wherever it asks for them.
//
// internal/engine/NOTICE carries Section 7 additional terms; clause (1)
// ATTRIBUTION requires this pair of lines, unaltered, in the NOTICE file
// itself and in the README of the distribution. They are also kept in
// internal/engine/README.md, next to the code they credit. This test is
// the tripwire: whoever next rewrites a README or a NOTICE must not lose
// the lines by accident, because the licence did not ask for them by
// accident either.
const attributionLine = "Based on BackPack by Amin Mohammadi (AminMGMT)"
const attributionURL = "https://github.com/AminMGMT/BackPack"

func TestTheAttributionTheLicenceRequiresIsWhereItMustBe(t *testing.T) {
	for _, file := range []string{
		"../../internal/engine/NOTICE",
		"../../internal/engine/README.md",
		"../../README.md",
	} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		// The lines sit inside a NOTICE that indents its clauses, so they
		// are compared trimmed: what must stay unaltered is the text of
		// each line, and that the two follow each other.
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		found := false
		for i := 0; i+1 < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == attributionLine &&
				strings.TrimSpace(lines[i+1]) == attributionURL {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s does not carry the two attribution lines, side by " +
				"side and unaltered, where the engine's NOTICE requires them", file)
		}
	}
}

// The licences say what they cover, and they do not disagree with each
// other: the engine tree is AGPL-3.0, the panel is MIT, and the root
// NOTICE points a reader at both.
func TestTheLicenceFilesSayWhatTheyCover(t *testing.T) {
	engine, err := os.ReadFile("../../internal/engine/LICENSE")
	if err != nil {
		t.Fatalf("the engine's licence: %v", err)
	}
	if !strings.Contains(string(engine), "GNU AFFERO GENERAL PUBLIC LICENSE") {
		t.Error("internal/engine/LICENSE is not the AGPL-3.0 anymore")
	}

	panel, err := os.ReadFile("../../LICENSE")
	if err != nil {
		t.Fatalf("the panel's licence: %v", err)
	}
	if !strings.Contains(string(panel), "MIT License") {
		t.Error("LICENSE is not the MIT licence anymore")
	}
	if !strings.Contains(string(panel), "internal/engine/") {
		t.Error("LICENSE does not say that internal/engine/ is under another licence")
	}

	notice, err := os.ReadFile("../../NOTICE")
	if err != nil {
		t.Fatalf("the root NOTICE: %v", err)
	}
	for _, want := range []string{"AGPL-3.0", "MIT", "internal/engine/NOTICE"} {
		if !strings.Contains(string(notice), want) {
			t.Errorf("the root NOTICE does not mention %q: a reader cannot " +
				"tell which licence covers what from it", want)
		}
	}
}

// The name of the engine's origin is gone from everything the project
// presents, and the only lines that still carry it are the attribution
// lines the licence requires — quoted, side by side, wherever it requires
// them.
//
// Two places are allowed to keep the name beyond those lines:
//
//   - internal/engine/, the vendored tree. It holds the origin's own code,
//     comments and notices, and it must stay close enough to the upstream
//     copy to be re-synced with a plain rsync (standing rule 3). The
//     product does not speak for that tree; the tree speaks for itself.
//   - the attribution lines themselves, wherever they are quoted: in the
//     READMEs, and in this file's own plan.
func TestTheNameOfTheOriginOnlyLivesInTheAttribution(t *testing.T) {
	skipped := map[string]bool{
		"vendor": true, // other people's modules
		".git":   true,
	}
	binary := map[string]bool{
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
		".ico": true, ".pdf": true, ".zip": true, ".gz": true,
		".tar": true, ".woff": true, ".woff2": true, ".ttf": true,
	}

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("the repository root: %v", err)
	}

	var namedHere []string
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if skipped[name] {
				return filepath.SkipDir
			}
			return nil
		}
		if binary[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(rel, "internal"+string(filepath.Separator)+"engine"+string(filepath.Separator)) {
			return nil // the vendored tree keeps its own words
		}
		if rel == filepath.Join("internal", "panel", "licence_test.go") ||
			rel == filepath.Join("internal", "panel", "readme_test.go") {
			return nil // these tests hold the strings they search for
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(raw), "\n") {
			if !strings.Contains(line, "BackPack") && !strings.Contains(line, "AminMGMT") {
				continue
			}
			trimmed := strings.TrimSpace(line)
			if trimmed == attributionURL || strings.Contains(line, attributionLine) {
				continue // the attribution, quoted where it is required
			}
			namedHere = append(namedHere, rel+": "+trimmed)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking the repository: %v", walkErr)
	}

	for _, hit := range namedHere {
		t.Errorf("the name of the engine's origin is still in the project's "+
			"own words: %s", hit)
	}
}

// The engine's version output is two lines — the version and the carriers
// — and nothing else. The panel parses the second one for the transports
// (internal/panel/engine/driver.go), so a third line would be read by
// nobody, and the attribution is not printed here at all: the licence
// asks for the lines in the NOTICE and in the README, and they are there.
//
// This reads the source the way internal/engine/app/assetname_test.go
// does, rather than building the binary: the output is the source of the
// output.
func TestTheEnginesVersionOutputIsTwoLinesAndNoAttribution(t *testing.T) {
	raw, err := os.ReadFile("../../cmd/bull-tunnel-engine/main.go")
	if err != nil {
		t.Fatalf("the engine's main: %v", err)
	}
	body := string(raw)

	if strings.Contains(body, attributionLine) ||
		strings.Contains(body, "const Attribution") {
		t.Error("the engine's version output names the origin again: the " +
			"attribution belongs in the NOTICE and in the README, where the " +
			"licence asks for it, and nowhere else")
	}
	for _, want := range []string{
		`fmt.Printf("bull-tunnel-engine %s\n", Version())`,
		`fmt.Printf("transports: %s\n", strings.Join(carriers, ","))`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the engine's version output no longer prints %s: the "+
				"panel parses exactly these two lines", want)
		}
	}
}
