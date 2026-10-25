package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestThePasswordIsChecked: the password of the operator, and nobody else's.
func TestThePasswordIsChecked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	a := New(path)
	if a.Configured() {
		t.Error("the panel is configured, it must not be before the password is set")
	}
	if err := a.SetPassword("admin", "s3cret"); err != nil {
		t.Fatalf("the password cannot be set: %v", err)
	}
	if !a.Configured() {
		t.Error("the panel is not configured, it must be")
	}
	if !a.Check("admin", "s3cret") {
		t.Error("the right password was refused, it must not be")
	}
	if a.Check("admin", "wrong") {
		t.Error("a wrong password was accepted, it must not be")
	}
	if a.Check("someone", "s3cret") {
		t.Error("a wrong name was accepted, it must not be")
	}
	// the file is only readable by its owner
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the login file is not there: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the login file is %o, it must be 600", mode)
	}
}

// TestTheSessionIsSigned: a session is only good when the panel wrote it.
func TestTheSessionIsSigned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	a := New(path)
	if err := a.SetPassword("admin", "s3cret"); err != nil {
		t.Fatalf("the password cannot be set: %v", err)
	}
	token := a.Issue()
	if !a.Verify(token) {
		t.Error("the session was refused, it must not be")
	}
	if a.Verify(token + "x") {
		t.Error("a tampered session was accepted, it must not be")
	}
	if a.Verify("") || a.Verify("nonsense") {
		t.Error("rubbish was accepted as a session, it must not be")
	}
	// another panel's session is refused
	other := New(filepath.Join(t.TempDir(), "auth.json"))
	if other.Verify(token) {
		t.Error("the session of another panel was accepted, it must not be")
	}
}

// TestThePasswordIsReadAgain: the password changed from the command line
// while the panel was running.
func TestThePasswordIsReadAgain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	a := New(path)
	if err := a.SetPassword("admin", "s3cret"); err != nil {
		t.Fatalf("the password cannot be set: %v", err)
	}
	// the operator changes the password with the command line
	other := New(path)
	if err := other.SetPassword("admin", "newone"); err != nil {
		t.Fatalf("the password cannot be set: %v", err)
	}
	if a.Check("admin", "s3cret") {
		t.Error("the old password was accepted, it must not be")
	}
	if !a.Check("admin", "newone") {
		t.Error("the new password was refused, it must not be")
	}
}

// TestALoginFileThatCannotBeReadDoesNotLockTheOperatorOut: the file is
// left alone and the reason is shown.
func TestALoginFileThatCannotBeReadDoesNotLockTheOperatorOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("the file cannot be written: %v", err)
	}
	a := New(path)
	if a.FileError() == "" {
		t.Error("the panel does not say that the login file is broken")
	}
	// the file is still there, untouched
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the login file is not there: %v", err)
	}
	if string(content) != "{ this is not json" {
		t.Errorf("the login file is %q, it must be left alone", content)
	}
}

// TestALoginFileWithoutASaltDoesNotCrash: a half-written file answers, it
// does not raise.
func TestALoginFileWithoutASaltDoesNotCrash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"username": "admin", "hash": "deadbeef", "secret": "x"}`), 0o600); err != nil {
		t.Fatalf("the file cannot be written: %v", err)
	}
	a := New(path)
	if a.Check("admin", "s3cret") {
		t.Error("a login without a salt was accepted, it must not be")
	}
}

// TestTheSessionOfThePythonPanelIsGoodHere: the sessions of the two panels
// are the same, so the operator does not have to log in again when the
// panel is swapped.
func TestTheSessionOfThePythonPanelIsGoodHere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte(`{"username": "admin", "secret": "the-secret-of-the-panel"}`), 0o600); err != nil {
		t.Fatalf("the file cannot be written: %v", err)
	}
	a := New(path)
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "tests", "fixtures", "session-python.txt"))
	if err != nil {
		t.Fatalf("the fixture cannot be read: %v", err)
	}
	if !a.Verify(strings.TrimSpace(string(content))) {
		t.Error("the session of the Python panel was refused, it must not be")
	}
}

// TestTheLimiterBlocksAfterTooManyFailures: a wrong password must not be
// free to try.
func TestTheLimiterBlocksAfterTooManyFailures(t *testing.T) {
	l := NewLimiter(3, 60, 30)
	if ok, _ := l.Allowed("1.2.3.4"); !ok {
		t.Error("the address is blocked, it must not be")
	}
	for i := 0; i < 3; i++ {
		l.Fail("1.2.3.4")
	}
	ok, left := l.Allowed("1.2.3.4")
	if ok || left <= 0 || left > 30 {
		t.Errorf("the address is allowed (%v, %d), it must be blocked for a while", ok, left)
	}
	// another address is not affected
	if ok, _ := l.Allowed("5.6.7.8"); !ok {
		t.Error("another address is blocked, it must not be")
	}
}

// TestAGoodPasswordClearsTheCount: a right password forgets the failures.
func TestAGoodPasswordClearsTheCount(t *testing.T) {
	l := NewLimiter(3, 60, 30)
	l.Fail("1.2.3.4")
	l.Fail("1.2.3.4")
	l.Success("1.2.3.4")
	l.Fail("1.2.3.4")
	if ok, _ := l.Allowed("1.2.3.4"); !ok {
		t.Error("the address is blocked, it must not be")
	}
}
