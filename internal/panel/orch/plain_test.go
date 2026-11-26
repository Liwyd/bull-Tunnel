package orch

import (
	"errors"
	"strings"
	"testing"

	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
)

// The operator reads the log of the panel, and the log says what to do: a
// server that refuses the login is named with what to check, not with the
// words of the SSH library.

// refusing is a driver whose servers cannot be logged in to.
type refusing struct {
	*fakeDriver
	err error
}

func (r refusing) EnsureInstalled(*model.Server, bool) (string, error) { return "", r.err }
func (r refusing) Deploy(*model.Link, *model.Server, *model.Server, string, []string) error {
	return r.err
}

var errLogin = errors.New("SSH 10.0.2.1:22: ssh: handshake failed: ssh: unable to authenticate, attempted methods [none password], no supported methods remain")

// logged returns the messages of the events of a kind.
func (w *aWorld) logged(kind string) []string {
	w.store.RLock()
	defer w.store.RUnlock()
	out := []string{}
	for _, event := range w.store.Events {
		if event.Kind == kind {
			out = append(out, event.Message)
		}
	}
	return out
}

func TestAFailedInstallIsSaidPlainly(t *testing.T) {
	w := newWorld(t)
	w.orch.WithDriver(refusing{w.driver, errLogin})
	w.link.Status = model.StatusPending
	w.link.Transport = ""

	if w.orch.Discover(w.link, nil) {
		t.Fatal("a tunnel was built on a server that refuses the login")
	}
	logged := w.logged("install")
	if len(logged) != 1 || !strings.Contains(logged[0], "SSH login to «I1» was refused: check the user and password/key") ||
		strings.Contains(logged[0], "handshake") {
		t.Errorf("the log says %q, it must say that the login to I1 was refused, in plain words", logged)
	}
	if !strings.HasPrefix(w.link.Error, "install: SSH login to «I1» was refused") {
		t.Errorf("the error kept with the tunnel is %q, it must be the plain sentence", w.link.Error)
	}

	// the same for the installation of one server, which the page starts
	w.orch.Install(w.kharej)
	if logged := w.logged("install"); len(logged) != 2 || !strings.Contains(logged[1], "installing the tunnel engine on K1 failed: SSH login to «K1» was refused") {
		t.Errorf("the log of the installation says %q", logged)
	}

	// and the change of the protocol
	w.orch.SetTransport(w.link, "wsmux", false)
	if logged := w.logged("transport"); len(logged) != 1 || !strings.Contains(logged[0], "SSH login to «I1» was refused") {
		t.Errorf("the log of the change of the protocol says %q", logged)
	}
}

// A server that does not answer is kept with the plain sentence, and the log
// of its going offline says it.
func TestAServerThatDoesNotAnswerIsSaidPlainly(t *testing.T) {
	w := newWorld(t)
	w.orch.WithDriver(pingFails{w.driver, errors.New("SSH 10.0.2.1:22: dial tcp 10.0.2.1:22: i/o timeout")})
	w.store.Settings.FailThreshold = 1
	w.orch.checkServers()

	if !strings.HasPrefix(w.iran.Error, "«I1» does not answer over SSH (timeout)") {
		t.Errorf("the error kept with the server is %q, it must be the plain sentence", w.iran.Error)
	}
	logged := w.logged("server")
	found := false
	for _, message := range logged {
		if strings.Contains(message, "went offline: «I1» does not answer over SSH (timeout)") {
			found = true
		}
	}
	if !found {
		t.Errorf("the log says %q, it must say that I1 went offline and why", logged)
	}
}

type pingFails struct {
	*fakeDriver
	err error
}

func (p pingFails) Ping(*model.Server) (float64, error) { return 0, p.err }

// A server that runs tunnels has the engine, even when the panel did not put
// it there: the scan keeps what it finds, so the actions that need the engine
// are offered on it.
func TestTheScanFindsTheEngineOfAServerThatRunsTunnels(t *testing.T) {
	w := newWorld(t)
	if w.iran.EngineVersion != "" || w.kharej.EngineVersion != "" {
		t.Fatal("the servers of the world have an engine already")
	}
	data := model.NewOrdered()
	data.Set("bind_addr", "0.0.0.0:3080")
	data.Set("transport", "tcpmux")
	data.Set("token", "abc")
	w.driver.found["si"] = []*engine.Found{{Name: "old", Role: "server", Data: data, Active: "active", Unit: "old.service"}}

	w.orch.ScanAll(nil)
	if w.iran.EngineVersion != "1.1.3" {
		t.Errorf("the Iran server runs a tunnel and its engine is %q, it must be the version the server says", w.iran.EngineVersion)
	}
	if len(w.iran.EngineTransports) == 0 {
		t.Error("the carriers the engine speaks were not kept")
	}
	if w.kharej.EngineVersion != "" {
		t.Errorf("a server that runs nothing has the engine %q: the panel has not seen one", w.kharej.EngineVersion)
	}
}
