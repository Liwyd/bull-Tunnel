package panel

import (
	"os"
	"strings"
	"testing"
)

// The README is written like a tool, not like a pitch: it opens with what
// the program is in three sentences, carries the one-line install right at
// the top, and is organised the way a new operator meets the program —
// install, first run, how the pieces fit, the dashboard, the secrets, the
// service, developing it, licences.
//
// These are the headings that order that walk. A heading that disappears
// is a step a new operator is no longer walked through, and one that
// reappears out of order is the same thing wearing a different hat.
var readmeHeadings = []string{
	"## What it is",
	"## Install",
	"## The first run",
	"## How the pieces fit",
	"## The dashboard",
	"## Where the secrets are",
	"## Running it as a service",
	"## Developing it",
	"## Licences",
}

func TestTheReadmeIsOrganisedTheWayAMeetsTheProgram(t *testing.T) {
	raw, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("the README: %v", err)
	}

	var seen []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "## ") {
			seen = append(seen, strings.TrimSpace(line))
		}
	}

	if len(seen) != len(readmeHeadings)+1 { // +1: the Contents list
		t.Errorf("the README has %d top headings plus Contents, it must have %d plus Contents:\n  got:  %s\n  want: %s",
			len(seen)-1, len(readmeHeadings),
			strings.Join(seen, " | "), strings.Join(readmeHeadings, " | "))
	}
	for i, want := range readmeHeadings {
		at := i + 1 // skip the Contents list
		if at >= len(seen) {
			break
		}
		if seen[at] != want {
			t.Errorf("heading %d is %q, it must be %q", at+1, seen[at], want)
		}
	}
}

// The README opens with the one-line install, above everything else: the
// first thing a reader can do with the program is the first thing the
// README offers. It is not buried under a description.
func TestTheOneLineInstallIsAtTheTopOfTheReadme(t *testing.T) {
	raw, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("the README: %v", err)
	}
	body := string(raw)

	install := "curl -fsSL https://raw.githubusercontent.com/Liwyd/bull-Tunnel/main/deploy/install.sh | sudo bash"
	first := strings.Index(body, install)
	if first < 0 {
		t.Fatalf("the README does not carry the one-line installer at all")
	}
	// The installer must be offered before the reader is asked to read
	// anything: within the first few hundred characters, above the
	// screenshot and the contents list.
	if first > 600 {
		t.Errorf("the one-line installer starts at byte %d, it must be offered at the top", first)
	}
	if contents := strings.Index(body, "## Contents"); contents != -1 && first > contents {
		t.Error("the one-line installer comes after the contents list, not before it")
	}
}

// The README carries the licence attribution where the engine's NOTICE
// requires it — see TestTheAttributionTheLicenceRequiresIsWhereItMustBe —
// and the section that carries it is the Licences section, the last one.
// Rewriting the README must not lose the block, and must not bury it.
func TestTheReadmeCarriesTheAttributionInItsLicenceSection(t *testing.T) {
	raw, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("the README: %v", err)
	}
	// The Licences section is the last one, and it carries the block.
	body := strings.Join(strings.Fields(string(raw)), " ") // forget the wrapping
	licence := strings.LastIndex(body, "## Licences")
	if licence < 0 {
		t.Fatalf("the README has no Licences section")
	}
	block := body[licence:]
	if !strings.Contains(block, "Based on BackPack by Amin Mohammadi (AminMGMT)") ||
		!strings.Contains(block, "https://github.com/AminMGMT/BackPack") {
		t.Error("the attribution the engine's NOTICE requires is not in the Licences section")
	}
	// The two licences it names, and only those two: the MIT for the panel,
	// the GNU Affero General Public License v3.0 for the engine.
	if !strings.Contains(block, "MIT licensed") ||
		!strings.Contains(block, "GNU Affero General Public License v3.0") {
		t.Error("the Licences section does not say which licence covers what")
	}
}

// The README names no one it does not have to name. The only lines that
// carry the name of the engine's origin are the attribution lines the
// licence requires, quoted side by side. See
// TestTheNameOfTheOriginOnlyLivesInTheAttribution for the whole tree; this
// is the same rule on the README alone, so a failure names the file a
// reader actually reads.
func TestTheReadmeNamesNobodyBeyondTheAttribution(t *testing.T) {
	raw, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("the README: %v", err)
	}

	line := 0
	for _, text := range strings.Split(string(raw), "\n") {
		line++
		if !strings.Contains(text, "BackPack") && !strings.Contains(text, "AminMGMT") {
			continue
		}
		trimmed := strings.TrimSpace(text)
		if trimmed == "https://github.com/AminMGMT/BackPack" ||
			strings.Contains(text, "Based on BackPack by Amin Mohammadi (AminMGMT)") {
			continue
		}
		t.Errorf("README.md:%d names the engine's origin outside the attribution: %s", line, trimmed)
	}
}

// The README states where the secrets are, and the statements must match
// what the code does — a README that promises encryption the panel does
// not do, or omits a secret the state file holds, is worse than no README.
//
// The rules it states, checked against the panel's own files:
//
//   - the state file is readable only by its owner (store.Save chmods it
//     to 0600), and it holds the SSH passwords and the DNS API tokens;
//   - the admin password is hashed with PBKDF2, never stored in the clear
//     (auth.hashIterations is the iteration count);
//   - the backup is AES-256 encrypted under a password the operator sets
//     (internal/panel/backup).
func TestTheReadmesClaimsAboutSecretsMatchTheCode(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("the README: %v", err)
	}
	// Forget the line wrapping: these claims are sentences, not lines.
	body := strings.Join(strings.Fields(string(readme)), " ")

	// The state file: 0600, and what it holds.
	store, err := os.ReadFile("../../internal/panel/store/store.go")
	if err != nil {
		t.Fatalf("the store: %v", err)
	}
	if !strings.Contains(string(store), "0o600") {
		t.Error("the store no longer chmods the state file to 0600: the README's claim is out of date")
	}
	for _, want := range []string{"0600", "SSH passwords", "API tokens"} {
		if !strings.Contains(body, want) {
			t.Errorf("the README does not say %q about the state file", want)
		}
	}

	// The admin password: hashed, with the iteration count the code uses.
	auth, err := os.ReadFile("../../internal/panel/auth/auth.go")
	if err != nil {
		t.Fatalf("the auth: %v", err)
	}
	if !strings.Contains(string(auth), "hashIterations") {
		t.Error("the auth no longer hashes the password with a named iteration count")
	}
	if !strings.Contains(body, "PBKDF2") {
		t.Error("the README does not say the admin password is hashed with PBKDF2")
	}

	// The backup: AES-256-GCM with a scrypt-derived key, and the README
	// says a backup password is needed.
	backup, err := os.ReadFile("../../internal/panel/backup/backup.go")
	if err != nil {
		t.Fatalf("the backup: %v", err)
	}
	if !strings.Contains(string(backup), "aes.NewCipher") ||
		!strings.Contains(string(backup), "scrypt.Key") {
		t.Error("the backup is no longer AES with a scrypt-derived key: the README's claim is out of date")
	}
	if !strings.Contains(body, "AES-256") {
		t.Error("the README does not say the backup is AES-256 encrypted")
	}
}
