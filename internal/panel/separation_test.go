package panel

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// enginePrefix is the import path of the engine's tree. The panel must not
// import it: the engine is AGPL-3.0 and the panel is MIT, and a program
// that links AGPL code is AGPL whatever else is in it. The two talk over
// SSH, over the network and through the files of the configuration — never
// through a package.
const enginePrefix = "github.com/Liwyd/bull-Tunnel/internal/engine"

// panelTrees are the directories the panel's own code lives in. The engine
// is the other half of the module and is checked the other way round.
var panelTrees = []string{"internal/panel", "cmd/bull-tunnel"}

// engineTrees are the directories the engine's code lives in.
var engineTrees = []string{"internal/engine", "cmd/bull-tunnel-engine"}

// TestThePanelDoesNotImportTheEngine keeps the two licences apart.
//
// The panel and the engine are two programs that talk over the network. If
// the panel ever imports a package of the engine, the panel becomes a
// derivative work of an AGPL program and has to be published under the
// AGPL — which is not what this project wants. The engine does not import
// the panel either: it is installed alone on the servers that carry the
// tunnels, and it must not need anything that is not shipped with it.
func TestThePanelDoesNotImportTheEngine(t *testing.T) {
	root := moduleRoot(t)

	for _, tree := range panelTrees {
		forEachImport(t, root, tree, func(file string, path string) {
			if strings.HasPrefix(path, enginePrefix) {
				t.Errorf("%s imports %s: the panel must not link the engine", file, path)
			}
		})
	}

	for _, tree := range engineTrees {
		forEachImport(t, root, tree, func(file string, path string) {
			if strings.HasPrefix(path, "github.com/Liwyd/bull-Tunnel/internal/panel") {
				t.Errorf("%s imports %s: the engine must stand on its own", file, path)
			}
		})
	}
}

// moduleRoot walks up from the directory of the test to the module's own
// go.mod. The test runs in the directory of its package, and the module is
// one or two levels up.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cannot read the working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

// forEachImport calls fn for every import of every Go file of a tree. A
// tree that does not exist is skipped: the panel's half of the module is
// written phase by phase, and a tree that is not there yet cannot import
// anything.
func forEachImport(t *testing.T, root string, tree string, fn func(file string, path string)) {
	t.Helper()
	dir := filepath.Join(root, tree)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return
	}
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return err
			}
			name, err := filepath.Rel(root, path)
			if err != nil {
				name = path
			}
			fn(name, imported)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("cannot read the tree %s: %v", tree, err)
	}
}
