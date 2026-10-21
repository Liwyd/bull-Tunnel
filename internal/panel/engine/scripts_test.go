package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// TestTheScriptsAreTheOnesOfThePythonPanel: the scripts the panel runs on
// the servers must be the scripts the Python panel runs — the same bytes.
// The parsers read what the scripts answer, and a script that changes
// without its parser would leave the panel deaf.
func TestTheScriptsAreTheOnesOfThePythonPanel(t *testing.T) {
	dir := fixtures(t)
	_, _, link := mk()

	check := readFixture(t, dir, "script-check.sh")
	if got := CheckScript([]*model.Link{link}); got != check {
		t.Errorf("the health script is not the one of the Python panel:\ngot:\n%s\nwant:\n%s", got, check)
	}

	wait := readFixture(t, dir, "script-wait.sh")
	if got := WaitScript(61000, 30, 5); got != wait {
		t.Errorf("the waiting script is not the one of the Python panel:\ngot:\n%s\nwant:\n%s", got, wait)
	}

	scan := readFixture(t, dir, "script-scan.sh")
	if got := ScanScriptFor(ConfDir); got != scan {
		t.Errorf("the scan script is not the one of the Python panel:\ngot:\n%s\nwant:\n%s", got, scan)
	}

	listen := readFixture(t, dir, "script-listen.sh")
	if got := ListenScript; got != listen {
		t.Errorf("the listening script is not the one of the Python panel:\ngot:\n%s\nwant:\n%s", got, listen)
	}

	exits := readFixture(t, dir, "script-exits.sh")
	got := ExitsScript([]int{443, 8443}, []string{"1.2.3.4:443", "[2001:db8::1]:443"})
	if got != exits {
		t.Errorf("the exits script is not the one of the Python panel:\ngot:\n%s\nwant:\n%s", got, exits)
	}
}

// readFixture reads one of the files of the contract.
func readFixture(t *testing.T, dir string, name string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	return string(content)
}
