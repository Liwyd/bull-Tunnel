package engine

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/ssh"
	"github.com/Liwyd/bull-Tunnel/internal/panel/testssh"
)

// The driver is tested against a server that is right here: the
// configuration it writes must land on the server, the unit must be there
// too, and a tunnel the panel finds on a server must be read back.

// aDriver returns a driver, the server it talks to, and the directory the
// files of the engine are written in.
func aDriver(t *testing.T) (*Driver, *testssh.Server, string) {
	t.Helper()
	server, err := testssh.New(t.TempDir(), "", false)
	if err != nil {
		t.Fatalf("cannot start the test server: %v", err)
	}
	t.Cleanup(server.Close)
	pool := ssh.NewPool(5*time.Second, ssh.NewHostKeys(filepath.Join(t.TempDir(), "hostkeys.json")))
	t.Cleanup(pool.Close)
	driver := NewDriver(pool, t.TempDir(), "latest", "", "")

	// the files of the engine are written in the directory of the test, not
	// in /etc: the test must not touch the machine it runs on.
	confDir := filepath.Join(t.TempDir(), "bull-tunnel")
	if err := os.MkdirAll(confDir, 0o700); err != nil {
		t.Fatalf("cannot make the directory of the configurations: %v", err)
	}
	driver.confDir = confDir
	driver.unitDir = t.TempDir()
	driver.binPath = filepath.Join(driver.unitDir, "bull-tunnel-engine")
	return driver, server, confDir
}

// aServer is a server of the panel, pointing at the test server.
func aServer(t *testing.T, server *testssh.Server, id string, role string) *model.Server {
	t.Helper()
	return &model.Server{
		ID:       id,
		Name:     id,
		Role:     role,
		IP:       "127.0.0.1",
		SSHPort:  server.Port(),
		SSHUser:  "root",
		Priority: 100,
		Enabled:  true,
		Status:   model.Unknown,
	}
}

// TestTheDeployWritesTheConfigurationAndTheUnit: the tunnel's files are on
// the server, and the configuration is the one the Python panel writes.
func TestTheDeployWritesTheConfigurationAndTheUnit(t *testing.T) {
	driver, server, confDir := aDriver(t)
	iran := aServer(t, server, "si", model.Iran)
	kharej := aServer(t, server, "sk", model.Kharej)
	_, _, link := mk()
	link.IranName = "bt-test-iran"
	link.KharejName = "bt-test-kharej"

	// the server has no systemd here, so the unit is written and the
	// restart fails: the panel must say so, and the files must be there.
	err := driver.Deploy(link, iran, kharej, "tcpmux", link.Ports)
	if err == nil {
		t.Error("the deployment succeeded, there is no systemd on this machine")
	} else {
		t.Logf("the deployment ended with: %v", err)
	}

	config := filepath.Join(confDir, "bt-test-iran.toml")
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatalf("the configuration is not on the server: %v", err)
	}
	want := RenderServer(link, "tcpmux", link.Ports, kharej)
	if string(content) != want {
		t.Errorf("the configuration on the server is not the one the panel renders:\ngot:\n%s\nwant:\n%s", content, want)
	}

	unit := filepath.Join(driver.unitDir, "bulltunnel-bt-test-iran.service")
	if _, err := os.Stat(unit); err != nil {
		t.Errorf("the unit is not on the server: %v", err)
	}
	commands := strings.Join(server.Commands(), "\n")
	if !strings.Contains(commands, "daemon-reload") {
		t.Errorf("the units were not read again:\n%s", commands)
	}
}

// TestTheScanReadsATunnelThatIsAlreadyThere: a tunnel the Python panel
// built is found on the server and read back.
func TestTheScanReadsATunnelThatIsAlreadyThere(t *testing.T) {
	driver, server, confDir := aDriver(t)

	// the configuration the Python panel writes, put on the server as it
	// was found there
	config := "# Managed by bull-Tunnel (link l) - kharej K -> this Iran server\n" +
		"[server]\n" +
		"bind_addr = \"0.0.0.0:3080\"\n" +
		"transport = \"tcpmux\"\n" +
		"token = \"abc\"\n" +
		"accept_udp = false\n" +
		"log_level = \"info\"\n" +
		"ports = [\n    \"127.0.0.1:61000=127.0.0.1:22\",\n    \"443\",\n]\n"
	if err := os.WriteFile(filepath.Join(confDir, "bt-old.toml"), []byte(config), 0o600); err != nil {
		t.Fatalf("cannot write the configuration: %v", err)
	}

	found, err := driver.Scan(aServer(t, server, "si", model.Iran))
	if err != nil {
		t.Fatalf("the scan failed: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("the scan found %d tunnels, it must find one", len(found))
	}
	tunnel := found[0]
	if tunnel.Name != "bt-old" || tunnel.Role != "server" {
		t.Errorf("the tunnel is %+v, it must be the server side of bt-old", tunnel)
	}
	if tunnel.Transport() != "tcpmux" {
		t.Errorf("the carrier is %q, it must be tcpmux", tunnel.Transport())
	}
	if tunnel.Token() != "abc" {
		t.Errorf("the token is %q, it must be abc", tunnel.Token())
	}
	if tunnel.Port() != 3080 {
		t.Errorf("the port is %d, it must be 3080", tunnel.Port())
	}
	// the settings of the tunnel keep the order of the file they came from
	keys := tunnel.Data.Keys()
	if len(keys) < 4 || keys[0] != "bind_addr" || keys[1] != "transport" {
		t.Errorf("the keys of the tunnel are %v, they must keep the order of the file", keys)
	}
}

// TestTheEngineIsNotThereAndThePanelSaysSo: the panel does not install
// anything when it is told not to.
func TestTheEngineIsNotThereAndThePanelSaysSo(t *testing.T) {
	driver, server, _ := aDriver(t)
	s := aServer(t, server, "si", model.Iran)

	_, version, transports, err := driver.InstalledVersion(s)
	if err != nil {
		t.Fatalf("the version cannot be read: %v", err)
	}
	if version != "MISSING" {
		t.Errorf("the version is %q, the engine is not on the server", version)
	}
	if len(transports) != 0 {
		t.Errorf("the carriers are %v, the engine is not on the server", transports)
	}
	if _, err := driver.EnsureInstalled(s, false); err == nil {
		t.Error("the panel says the engine is there, it must not")
	}
}

// TestTheReleaseFilesAreFound: the panel knows where the engine of a
// release is, and what the checksums of the release are.
func TestTheReleaseFilesAreFound(t *testing.T) {
	driver := NewDriver(nil, t.TempDir(), "v1.1.0", "", "")
	tarURL, sumsURL := driver.artifactURLs("bull-tunnel-engine_linux_amd64.tar.gz")
	if tarURL != ReleaseBase+"/download/v1.1.0/bull-tunnel-engine_linux_amd64.tar.gz" {
		t.Errorf("the tarball is at %q, it must be at the release page", tarURL)
	}
	if sumsURL != ReleaseBase+"/download/v1.1.0/SHA256SUMS" {
		t.Errorf("the checksums are at %q, they must be next to the tarball", sumsURL)
	}

	driver = NewDriver(nil, t.TempDir(), "latest", "", "")
	tarURL, _ = driver.artifactURLs("bull-tunnel-engine_linux_amd64.tar.gz")
	if !strings.Contains(tarURL, "/latest/download/") {
		t.Errorf("the tarball is at %q, the newest release must be found", tarURL)
	}

	driver = NewDriver(nil, t.TempDir(), "v1.1.0", "https://artifacts.example.com/bull", "")
	tarURL, _ = driver.artifactURLs("bull-tunnel-engine_linux_amd64.tar.gz")
	if !strings.HasPrefix(tarURL, "https://artifacts.example.com/bull/") {
		t.Errorf("the tarball is at %q, the mirror must be used", tarURL)
	}

	driver = NewDriver(nil, t.TempDir(), "v1.1.0", "https://artifacts.example.com/{tag}/engine", "")
	tarURL, _ = driver.artifactURLs("bull-tunnel-engine_linux_amd64.tar.gz")
	if !strings.HasPrefix(tarURL, "https://artifacts.example.com/v1.1.0/engine/") {
		t.Errorf("the tarball is at %q, the template must be filled in", tarURL)
	}
}

// TestTheBinaryIsTakenOutOfTheRelease: the panel takes the engine out of
// the tarball of the release.
func TestTheBinaryIsTakenOutOfTheRelease(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "bull-tunnel-engine_linux_amd64.tar.gz")
	writeTarball(t, tarball, "bull-tunnel-engine", "the engine")

	data, err := binaryFrom(tarball, "bull-tunnel-engine_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("the binary cannot be read: %v", err)
	}
	if string(data) != "the engine" {
		t.Errorf("the binary is %q, it must be the engine", data)
	}
	if _, err := binaryFrom(filepath.Join(dir, "nothing"), "nothing"); err == nil {
		t.Error("the panel found a binary in a tarball without one")
	}
}

// writeTarball writes a tarball with one file in it.
func writeTarball(t *testing.T, path string, name string, content string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("cannot write the tarball: %v", err)
	}
	defer file.Close()
	gz := gzip.NewWriter(file)
	writer := tar.NewWriter(gz)
	header := &tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}
	if err := writer.WriteHeader(header); err != nil {
		t.Fatalf("cannot write the tarball: %v", err)
	}
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatalf("cannot write the tarball: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("cannot write the tarball: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("cannot write the tarball: %v", err)
	}
}
