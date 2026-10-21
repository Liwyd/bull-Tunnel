package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the file the panel writes on the server.
type Config struct {
	// CommandRecord is the TXT record the agent polls, the AnswerRecord the
	// one it writes. Both are full names.
	CommandRecord string `toml:"command_record"`
	AnswerRecord  string `toml:"answer_record"`
	// Token is the key of the signature: the key the panel and the agent
	// share, and nobody else.
	Token string `toml:"token"`
	// The provider the answer is written with. It should be a zone of its
	// own: a server that is broken must not be able to touch the records
	// of the real domains.
	Provider string `toml:"provider"`
	Zone     string `toml:"zone"`
	APIKey   string `toml:"api_key"`
	// PollSeconds is the number of seconds between two polls.
	PollSeconds int `toml:"poll_seconds"`
	// TunnelDir is where the configs and the metrics of the tunnels are.
	TunnelDir string `toml:"tunnel_dir"`
	// Allowed are the commands the agent runs. Nothing else is ever run.
	Allowed []string `toml:"allowed"`
	// UnitPrefix is the prefix of the systemd units of the tunnels.
	UnitPrefix string `toml:"unit_prefix"`
}

// LoadConfig reads the file of the agent.
func LoadConfig(path string) (*Config, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if _, err := toml.Decode(string(blob), &c); err != nil {
		return nil, err
	}
	if c.CommandRecord == "" || c.AnswerRecord == "" {
		return nil, fmt.Errorf("the agent needs both a command and an answer record")
	}
	if c.Token == "" {
		return nil, fmt.Errorf("the agent needs the key it shares with the panel")
	}
	if c.PollSeconds <= 0 {
		c.PollSeconds = 30
	}
	if c.TunnelDir == "" {
		c.TunnelDir = "/etc/bulltunn"
	}
	if c.UnitPrefix == "" {
		c.UnitPrefix = "bulltunn"
	}
	if len(c.Allowed) == 0 {
		c.Allowed = []string{"status", "restart"}
	}
	return &c, nil
}

// Agent polls the command record and runs what it finds there.
type Agent struct {
	cfg     *Config
	pub     Publisher
	log     *slog.Logger
	done    map[string]bool
	answers int
}

// New builds the agent.
func New(cfg *Config, pub Publisher, log *slog.Logger) *Agent {
	return &Agent{cfg: cfg, pub: pub, log: log, done: map[string]bool{}}
}

// Run polls until the context is done.
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("the command channel is open", "command", a.cfg.CommandRecord, "answer", a.cfg.AnswerRecord)
	t := time.NewTicker(time.Duration(a.cfg.PollSeconds) * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			a.poll(ctx)
		}
	}
}

// poll reads the command record once and runs what it holds.
func (a *Agent) poll(ctx context.Context) {
	texts, err := LookupTXT(ctx, a.cfg.CommandRecord)
	if err != nil {
		a.log.Debug("the command record could not be read", "error", err)
		return
	}
	for _, text := range texts {
		a.run(ctx, text)
	}
}

// run executes one command.
func (a *Agent) run(ctx context.Context, text string) {
	cmd, err := Decode(a.cfg.Token, text)
	if err != nil {
		a.log.Warn("a command was not accepted", "error", err)
		return
	}
	if a.done[cmd.ID] {
		return
	}
	a.done[cmd.ID] = true
	if len(a.done) > 100 {
		a.done = map[string]bool{cmd.ID: true}
	}
	a.log.Info("a command came in", "action", cmd.Action, "id", cmd.ID)
	answer := Answer{ID: cmd.ID, TS: Now().Unix()}
	result, err := a.execute(ctx, cmd)
	if err != nil {
		answer.OK = false
		answer.Error = err.Error()
	} else {
		answer.OK = true
		answer.Result = result
	}
	blob, err := EncodeAnswer(answer)
	if err != nil {
		a.log.Error("the answer could not be built", "error", err)
		return
	}
	if err := a.pub.SetTXT(ctx, a.cfg.AnswerRecord, blob); err != nil {
		a.log.Error("the answer could not be written", "error", err)
	}
}

// execute runs one command. The list is short on purpose: there is no
// shell anywhere near it.
func (a *Agent) execute(ctx context.Context, cmd Command) (json.RawMessage, error) {
	if !a.allowed(cmd.Action) {
		return nil, fmt.Errorf("the command %q is not allowed", cmd.Action)
	}
	switch cmd.Action {
	case "status":
		return a.status()
	case "restart":
		var args struct {
			Tunnel string `json:"tunnel"`
		}
		if len(cmd.Args) > 0 {
			if err := json.Unmarshal(cmd.Args, &args); err != nil {
				return nil, fmt.Errorf("the arguments are not readable: %w", err)
			}
		}
		return nil, a.restart(ctx, args.Tunnel)
	}
	return nil, fmt.Errorf("the command %q is not known", cmd.Action)
}

func (a *Agent) allowed(action string) bool {
	for _, x := range a.cfg.Allowed {
		if x == action {
			return true
		}
	}
	return false
}

// status gathers the state of the tunnels and of the machine.
func (a *Agent) status() (json.RawMessage, error) {
	out := map[string]any{
		"tunnels": a.tunnels(),
		"system":  a.system(),
		"time":    Now().Unix(),
	}
	blob, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return blob, nil
}

// tunnels reads the metrics of every tunnel of this server.
func (a *Agent) tunnels() []map[string]any {
	entries, err := os.ReadDir(a.cfg.TunnelDir)
	if err != nil {
		return nil
	}
	var out []map[string]any
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".metrics.json") {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(a.cfg.TunnelDir, e.Name()))
		if err != nil {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(blob, &m); err != nil {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".metrics.json")
		m["name"] = name
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["name"]) < fmt.Sprint(out[j]["name"])
	})
	return out
}

// system reads a few numbers of the machine.
func (a *Agent) system() map[string]any {
	out := map[string]any{"hostname": hostname(), "uptime": uptime()}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		out["load"] = strings.TrimSpace(strings.Split(string(b), " ")[0])
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "MemAvailable:") {
				out["mem_available_kb"] = strings.TrimSpace(strings.TrimPrefix(line, "MemAvailable:"))
				break
			}
		}
	}
	return out
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func uptime() float64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0
	}
	var v float64
	fmt.Sscanf(fields[0], "%f", &v)
	return v
}

// nameRe is what a tunnel name may look like.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]+$`)

// restart restarts the unit of a tunnel. The name is checked twice: it
// must look right, and the unit must be one of the tunnels of this server.
func (a *Agent) restart(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("the command needs the name of a tunnel")
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("the name of the tunnel is not allowed")
	}
	unit := a.cfg.UnitPrefix + "-" + name + ".service"
	if !a.known(name) {
		return fmt.Errorf("there is no tunnel named %q on this server", name)
	}
	cmd := exec.CommandContext(ctx, "systemctl", "restart", unit)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("the unit %s did not restart: %s", unit, strings.TrimSpace(string(out)))
	}
	return nil
}

// known tells whether a tunnel of that name is on this server.
func (a *Agent) known(name string) bool {
	_, err := os.Stat(filepath.Join(a.cfg.TunnelDir, name+".toml"))
	return err == nil
}
