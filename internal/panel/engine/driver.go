package engine

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/ssh"
)

// Driver is the panel's hand on the tunnel engine: it puts the engine on a
// server, writes the configuration of a tunnel, starts it, asks it how it
// is doing and looks for the tunnels that are already running.
type Driver struct {
	pool *ssh.Pool
	// confDir, unitDir and binPath are where the files of the engine live
	// on the server. They are fields so that the tests can write into a
	// directory of their own; on a real server they are the ones below.
	confDir string
	unitDir string
	binPath string
	// cacheDir is where the release files are kept. The servers in Iran
	// often cannot reach the artifact host, so the panel downloads the
	// engine and pushes it over SSH.
	cacheDir string
	release  string
	source   string
	token    string

	mu sync.Mutex
}

// NewDriver returns a driver.
func NewDriver(pool *ssh.Pool, cacheDir string, release string, source string, token string) *Driver {
	return &Driver{
		pool:     pool,
		confDir:  ConfDir,
		unitDir:  UnitDir,
		binPath:  BinPath,
		cacheDir: cacheDir,
		release:  release,
		source:   source,
		token:    token,
	}
}

// confPath is where the configuration of a tunnel is on the server.
func (d *Driver) confPath(name string) string {
	return d.confDir + "/" + name + ".toml"
}

// Ping asks a server how long it takes to answer.
func (d *Driver) Ping(s *model.Server) (float64, error) {
	return d.pool.Ping(s)
}

// Run runs a command on a server.
func (d *Driver) Run(s *model.Server, command string, timeout time.Duration) (int, string, string, error) {
	result, err := d.pool.Run(s, command, timeout, nil)
	if err != nil {
		return 0, "", "", err
	}
	return result.Code, result.Out, result.Err, nil
}

// PutFile writes a file on a server.
func (d *Driver) PutFile(s *model.Server, path string, data []byte, mode os.FileMode) error {
	return d.pool.Put(s, data, path, mode)
}

// InstalledVersion is the machine, the version of the engine and the
// carriers it speaks.
//
// The engine prints two lines with -v: its version, and the carriers this
// build speaks. The panel reads both, and never tests a carrier the engine
// does not have.
//
// An engine that prints something else — an older build of the engine the
// code was vendored from, which printed its source address on the second
// line — is not believed: the carriers are taken from the line that names
// them, and only the names the panel knows are kept.
func (d *Driver) InstalledVersion(s *model.Server) (string, string, []string, error) {
	_, out, _, err := d.Run(s, fmt.Sprintf("uname -m; test -x %s && %s -v 2>/dev/null | head -n2 || echo MISSING", d.binPath, d.binPath), 20*time.Second)
	if err != nil {
		return "", "", nil, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for len(lines) < 3 {
		lines = append(lines, "")
	}
	transports := []string{}
	for _, t := range strings.Split(strings.ReplaceAll(lines[2], "transports:", ""), ",") {
		t = strings.TrimSpace(t)
		for _, known := range model.AllTransports {
			if t == known {
				transports = append(transports, t)
			}
		}
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), transports, nil
}

// EngineInfo is the version of the engine on the server and the carriers it
// speaks.
func (d *Driver) EngineInfo(s *model.Server) (string, []string, error) {
	_, version, transports, err := d.InstalledVersion(s)
	return version, transports, err
}

// EnsureInstalled puts the engine on a server, if it is not there.
func (d *Driver) EnsureInstalled(s *model.Server, install bool) (string, error) {
	machine, version, transports, err := d.InstalledVersion(s)
	if err != nil {
		return "", err
	}
	// The carriers this engine speaks are remembered on the server: they
	// are what the panel tests a tunnel with.
	if len(transports) > 0 {
		s.EngineTransports = transports
	}
	if version != "" && version != "MISSING" {
		return version, nil
	}
	if !install {
		return "", deployError("the tunnel engine is not installed on %s", s.Name)
	}
	arch, there := Arch[machine]
	if !there {
		return "", deployError("unsupported architecture on %s: %s", s.Name, machine)
	}
	data, err := d.binary(arch)
	if err != nil {
		return "", err
	}
	if _, _, _, err := d.Run(s, fmt.Sprintf("mkdir -p %s && chmod 700 %s", d.confDir, d.confDir), 30*time.Second); err != nil {
		return "", err
	}
	if err := d.pool.Put(s, data, d.binPath, 0o755); err != nil {
		return "", err
	}
	_, version, transports, err = d.InstalledVersion(s)
	if err != nil {
		return "", err
	}
	if len(transports) > 0 {
		s.EngineTransports = transports
	}
	if version == "" || version == "MISSING" {
		return "", deployError("the tunnel engine did not run after install on %s", s.Name)
	}
	return version, nil
}

// artifactURLs are the tarball and the checksums of the configured release.
//
// engine_source may be a plain base URL ("https://host/artifacts"), in
// which case the layout of a GitHub release page is assumed, or a template
// containing "{tag}" for full control.
func (d *Driver) artifactURLs(name string) (string, string) {
	base := strings.TrimRight(d.source, "/")
	if base == "" {
		base = ReleaseBase
	}
	switch {
	case strings.Contains(base, "{tag}"):
		base = strings.ReplaceAll(base, "{tag}", d.release)
	case d.release == "" || d.release == "latest":
		base = base + "/latest/download"
	default:
		base = base + "/download/" + d.release
	}
	return base + "/" + name, base + "/SHA256SUMS"
}

// binary fetches the release tarball once, verifies its checksum and
// returns the binary.
func (d *Driver) binary(arch string) ([]byte, error) {
	name := strings.ReplaceAll(Asset, "{arch}", arch)
	tag := d.release
	path := filepath.Join(d.cacheDir, tag, name)

	d.mu.Lock()
	defer d.mu.Unlock()

	tarURL, sumsURL := d.artifactURLs(name)
	cached := false
	if _, err := os.Stat(path); err == nil {
		cached = true
	}
	if cached && tag != "" && tag != "latest" {
		// A named release never moves: the tarball that was fetched for it
		// is the right one, and it was checked when it was fetched.
		return binaryFrom(path, name)
	}
	// "latest" is not a release: it is a name for whatever is newest, and it
	// moves. The checksum of what is published now says whether the tarball
	// we have is still that one — a tarball cached under the name "latest"
	// would otherwise be handed out for ever, and a server would keep the
	// build it had.
	want := d.publishedChecksum(sumsURL, name)
	if !cached || want == "" || checksumOf(path) != want {
		blob, err := d.fetch(tarURL)
		if err != nil {
			return nil, err
		}
		if want != "" && checksum(blob) != want {
			return nil, deployError("checksum mismatch for %s", name)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, blob, 0o644); err != nil {
			return nil, err
		}
	}
	return binaryFrom(path, name)
}

// publishedChecksum is the checksum the release publishes for this file, if
// any.
func (d *Driver) publishedChecksum(sumsURL string, name string) string {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(sumsURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasSuffix(fields[len(fields)-1], name) {
			return fields[0]
		}
	}
	return ""
}

// fetch downloads a release file.
func (d *Driver) fetch(url string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if d.token != "" {
		request.Header.Set("Authorization", "Bearer "+d.token)
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, deployError("could not fetch %s: the server answered %s", url, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// checksum is the SHA-256 of a file, in hex.
func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// checksumOf is the SHA-256 of a file, in hex.
func checksumOf(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return checksum(data)
}

// binaryFrom takes the engine out of a release tarball.
func binaryFrom(path string, name string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(header.Name) != "bull-tunnel-engine" {
			continue
		}
		return io.ReadAll(reader)
	}
	return nil, deployError("no bull-tunnel-engine binary in %s", name)
}

// installSide writes the configuration of one side of a tunnel and starts
// it.
func (d *Driver) installSide(s *model.Server, name string, toml string, openPorts []string, unit string) error {
	if !safeName.MatchString(name) || (unit != "" && !safeName.MatchString(unit)) {
		return deployError("bad tunnel name %q", name)
	}
	conf := d.confPath(name)
	// the first time the panel touches a config it did not write, keep the
	// original next to it: <name>.toml.bt-orig (never overwritten)
	_, _, _, err := d.Run(s, fmt.Sprintf(
		"mkdir -p %s && chmod 700 %s; if [ -f %s ] && [ ! -f %s.bt-orig ]; then cp -p %s %s.bt-orig; fi; true",
		d.confDir, d.confDir, conf, conf, conf, conf), 30*time.Second)
	if err != nil {
		return err
	}
	if err := d.pool.Put(s, []byte(toml), conf, 0o600); err != nil {
		return err
	}
	u := unit
	if u == "" {
		u = UnitName(name)
	}
	// keep an existing unit (adopted tunnels have their own); write ours
	// otherwise
	_, out, _, err := d.Run(s, fmt.Sprintf("systemctl cat %s >/dev/null 2>&1 && echo yes || echo no", u), 15*time.Second)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "yes" {
		if err := d.pool.Put(s, []byte(RenderUnit(name)), filepath.Join(d.unitDir, u), 0o644); err != nil {
			return err
		}
	}
	command := ufwAllow(openPorts) +
		fmt.Sprintf("rm -f %s/%s.metrics.json; systemctl daemon-reload && systemctl enable %s >/dev/null 2>&1; systemctl restart %s",
			d.confDir, name, u, u)
	code, out, errOut, err := d.Run(s, command, 40*time.Second)
	if err != nil {
		return err
	}
	if code != 0 {
		message := strings.TrimSpace(errOut)
		if message == "" {
			message = strings.TrimSpace(out)
		}
		if message == "" {
			message = "systemctl failed"
		}
		return deployError("%s: %s", s.Name, message)
	}
	return nil
}

// Deploy writes the configuration of a tunnel on both sides and starts it.
func (d *Driver) Deploy(link *model.Link, iran *model.Server, kharej *model.Server, transport string, ports []string) error {
	openPorts := append([]string{strconv.Itoa(link.TunnelPort)}, ports...)
	first := d.installSide(iran, link.IranName, RenderServer(link, transport, ports, kharej), openPorts, IranUnit(link))
	second := d.installSide(kharej, link.KharejName, RenderClient(link, transport, iran), nil, KharejUnit(link))
	if first != nil {
		return first
	}
	return second
}

// DeployIran writes the configuration of the Iran side of a tunnel and
// starts it.
func (d *Driver) DeployIran(link *model.Link, iran *model.Server, kharej *model.Server, ports []string) error {
	openPorts := append([]string{strconv.Itoa(link.TunnelPort)}, ports...)
	return d.installSide(iran, link.IranName, RenderServer(link, link.Transport, ports, kharej), openPorts, IranUnit(link))
}

// stopSide stops one side of a tunnel, and takes it away when it is told to.
func (d *Driver) stopSide(s *model.Server, name string, purge bool, unit string) error {
	if s == nil || !safeName.MatchString(name) || (unit != "" && !safeName.MatchString(unit)) {
		return nil
	}
	u := unit
	if u == "" {
		u = UnitName(name)
	}
	command := fmt.Sprintf("systemctl disable --now %s >/dev/null 2>&1; true", u)
	if purge {
		command += fmt.Sprintf("; rm -f %s/%s %s/%s.metrics.json; systemctl daemon-reload", d.unitDir, u, d.confDir, name)
	}
	if _, _, _, err := d.Run(s, command, 30*time.Second); err != nil {
		// a server that cannot be reached is not a reason to keep the
		// tunnel in the panel: the tunnel is down either way.
		return nil
	}
	return nil
}

// Stop stops both sides of a tunnel.
func (d *Driver) Stop(link *model.Link, iran *model.Server, kharej *model.Server) error {
	first := d.stopSide(iran, link.IranName, false, IranUnit(link))
	second := d.stopSide(kharej, link.KharejName, false, KharejUnit(link))
	if first != nil {
		return first
	}
	return second
}

// Remove stops both sides of a tunnel and takes them away.
func (d *Driver) Remove(link *model.Link, iran *model.Server, kharej *model.Server) error {
	first := d.stopSide(iran, link.IranName, true, IranUnit(link))
	second := d.stopSide(kharej, link.KharejName, true, KharejUnit(link))
	if first != nil {
		return first
	}
	return second
}

// Check asks an Iran server how its tunnels are doing.
func (d *Driver) Check(iran *model.Server, links []*model.Link) (map[string]*Probe, error) {
	if len(links) == 0 {
		return map[string]*Probe{}, nil
	}
	_, out, _, err := d.Run(iran, CheckScript(links), time.Duration(10+4*len(links))*time.Second)
	if err != nil {
		return nil, err
	}
	byID := map[string]*model.Link{}
	for _, link := range links {
		byID[link.ID] = link
	}
	return ParseCheck(out, byID), nil
}

// WaitUp waits for a tunnel to carry traffic and measures it.
func (d *Driver) WaitUp(link *model.Link, iran *model.Server, timeout int) (*Probe, error) {
	_, out, _, err := d.Run(iran, WaitScript(link.ProbePort, timeout, 5), time.Duration(timeout+40)*time.Second)
	if err != nil {
		return nil, err
	}
	return ParseWait(out), nil
}

// Scan looks for the tunnels that are already running on a server.
func (d *Driver) Scan(s *model.Server) ([]*Found, error) {
	_, out, _, err := d.Run(s, ScanScriptFor(d.confDir), 30*time.Second)
	if err != nil {
		return nil, err
	}
	return ParseScan(out), nil
}

// Listening asks a server which ports it listens on.
func (d *Driver) Listening(s *model.Server) (map[int]bool, error) {
	_, out, _, err := d.Run(s, ListenScript, 20*time.Second)
	if err != nil {
		return nil, err
	}
	return ParseListening(out), nil
}

// Exits asks a server where its forwarded ports go.
func (d *Driver) Exits(s *model.Server, ports []int, targets []string) (map[int]*Exit, map[string]bool, error) {
	_, out, _, err := d.Run(s, ExitsScript(ports, targets), time.Duration(20+4*len(targets)+len(ports))*time.Second)
	if err != nil {
		return nil, nil, err
	}
	exits, reach := ParseExits(out, ports)
	return exits, reach, nil
}
