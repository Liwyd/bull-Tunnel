package engine

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheEngineAcceptsTheConfiguration: the engine itself must read the
// configuration the panel writes. A configuration the engine refuses would
// leave a tunnel that never comes up, with the reason in the log of the
// server and not in the panel.
//
// The test needs the engine's binary; it is skipped when there is none.
func TestTheEngineAcceptsTheConfiguration(t *testing.T) {
	binary := engineBinary(t)

	kharej, iran, link := mk()
	dir := t.TempDir()

	server := filepath.Join(dir, "server.toml")
	if err := os.WriteFile(server, []byte(RenderServer(link, "tcpmux", link.Ports, kharej)), 0o600); err != nil {
		t.Fatalf("the configuration cannot be written: %v", err)
	}
	client := filepath.Join(dir, "client.toml")
	if err := os.WriteFile(client, []byte(RenderClient(link, "tcpmux", iran)), 0o600); err != nil {
		t.Fatalf("the configuration cannot be written: %v", err)
	}

	for _, path := range []string{server, client} {
		out, err := exec.Command(binary, "-c", path, "-check").CombinedOutput()
		if err != nil {
			t.Errorf("the engine refuses the configuration %s: %v\n%s", filepath.Base(path), err, out)
		}
	}
}

// TestTheEngineRunsTheTunnel: the two ends of a tunnel, the configuration
// the panel writes and the engine that reads them — and the tunnel carries
// the traffic, which is what the panel waits for.
//
// The test needs the engine's binary; it is skipped when there is none.
func TestTheEngineRunsTheTunnel(t *testing.T) {
	binary := engineBinary(t)
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("there is no bash here")
	}

	// the far end of the tunnel: something that answers, the way the
	// banner of sshd does
	banner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot listen: %v", err)
	}
	defer banner.Close()
	go func() {
		for {
			conn, err := banner.Accept()
			if err != nil {
				return
			}
			conn.Write([]byte("SSH-2.0-test\r\n"))
			conn.Close()
		}
	}()

	kharej, iran, link := mk()
	kharej.IP = "127.0.0.1"
	kharej.ProbeTarget = banner.Addr().String()
	iran.IP = "127.0.0.1"
	link.TunnelPort = freePort(t)
	link.ProbePort = freePort(t)

	dir := t.TempDir()
	server := filepath.Join(dir, "server.toml")
	if err := os.WriteFile(server, []byte(RenderServer(link, "tcpmux", nil, kharej)), 0o600); err != nil {
		t.Fatalf("the configuration cannot be written: %v", err)
	}
	client := filepath.Join(dir, "client.toml")
	if err := os.WriteFile(client, []byte(RenderClient(link, "tcpmux", iran)), 0o600); err != nil {
		t.Fatalf("the configuration cannot be written: %v", err)
	}

	serverProcess := exec.Command(binary, "-c", server)
	serverProcess.Dir = dir
	if err := serverProcess.Start(); err != nil {
		t.Fatalf("the engine did not start: %v", err)
	}
	defer stop(serverProcess)

	clientProcess := exec.Command(binary, "-c", client)
	clientProcess.Dir = dir
	if err := clientProcess.Start(); err != nil {
		t.Fatalf("the engine did not start: %v", err)
	}
	defer stop(clientProcess)

	// the panel waits for the tunnel with this script, and the tunnel is
	// up when the traffic is carried through it
	out, err := exec.Command("bash", "-c", WaitScript(link.ProbePort, 15, 5)).CombinedOutput()
	if err != nil {
		t.Fatalf("the waiting script failed: %v\n%s", err, out)
	}
	probe := ParseWait(string(out))
	if !probe.OK {
		t.Fatalf("the tunnel did not carry the traffic: %s", string(out))
	}
	if probe.LatencyMS == nil {
		t.Errorf("the tunnel carries the traffic, but the time was not measured: %q", strings.TrimSpace(string(out)))
	}
}

// engineBinary is the engine's binary, when there is one.
func engineBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("BT_ENGINE_BIN")
	if binary == "" {
		t.Skip("BT_ENGINE_BIN is not set: the engine is not here")
	}
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("the engine is not there: %v", err)
	}
	return binary
}

// stop ends a process of the test.
func stop(process *exec.Cmd) {
	if process == nil || process.Process == nil {
		return
	}
	process.Process.Kill()
	process.Wait()
}

// freePort is a port nothing listens on.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot find a free port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
