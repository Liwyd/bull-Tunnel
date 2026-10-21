// Command bulltunn-engine is the tunnel engine of Bull-Tunn.
//
// The engine is BackPack's. Everything under this module — config/, cmd/,
// internal/client, internal/server, internal/tunnel, internal/utils and the
// rest — is the BackPack engine (github.com/AminMGMT/BackPack), vendored
// unchanged; the module path is kept as github.com/backpack/backpack so the
// copy can be re-synced with a plain rsync and never drifts.
//
// This file is the only Bull-Tunn addition at the top level. It gives the
// engine the three extra entry points the control panel needs, and nothing
// else:
//
//	bulltunn-engine -c /etc/bulltunn/<name>.toml    run one tunnel
//	bulltunn-engine -c <name>.toml -check           only read and check the file
//	bulltunn-engine -v                              print the version and the carriers
//	bulltunn-engine probe -c <client>.toml          measure the path, one line per carrier
//	bulltunn-engine agent -c <agent>.toml            the command channel
//
// The tunnel itself is BackPack's, unmodified: the same control channel, the
// same carriers, the same pool, the same metrics file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/backpack/backpack/cmd"
	"github.com/backpack/backpack/config"
	"github.com/backpack/backpack/internal/agent"
	"github.com/backpack/backpack/internal/app"
	"github.com/backpack/backpack/internal/client/transport"
)

// version is stamped at link time by engine/build.sh. It falls back to
// BackPack's own version, so a build without the stamp still says something
// true.
var version = ""

// carriers are the reverse carriers this build speaks: the ones that carry
// TCP over the tunnel and can therefore be probed with a TCP probe. They are
// the ones the control panel offers; "udp" is not among them because it
// carries datagrams only, and the experimental carriers (xdi, pck, spoof)
// need raw sockets and root, so they are never offered to an unattended
// repair.
//
// The list is the panel's contract with the engine: it is printed by -v, the
// panel reads it, and the panel never tests a carrier that is not on it.
var carriers = []string{
	string(config.TCP),
	string(config.TCPMUX),
	string(config.STEALTH),
	string(config.WS),
	string(config.WSS),
	string(config.WSMUX),
	string(config.WSSMUX),
	string(config.KCP),
	string(config.QUIC),
}

// Version is the version this engine reports.
func Version() string {
	if version != "" {
		return version
	}
	return app.Version
}

func main() {
	// The two subcommands with flags of their own are handled before the
	// flag package, which would stop at the subcommand and report the rest as
	// unknown.
	if len(os.Args) > 1 && os.Args[1] == "agent" {
		runAgent(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "probe" {
		runProbe(os.Args[2:])
		return
	}

	var (
		configPath = flag.String("c", "", "path to a tunnel configuration file (TOML) — runs the tunnel")
		check      = flag.Bool("check", false, "read the configuration, report the problem and exit")
		showVer    = flag.Bool("v", false, "print the version and the carriers and exit")
	)
	flag.Parse()

	switch {
	case *showVer:
		// Two lines, and the panel reads both: the version, then the
		// carriers this build speaks.
		fmt.Printf("bulltunn-engine %s\n", Version())
		fmt.Printf("transports: %s\n", strings.Join(carriers, ","))
		return

	case *configPath == "":
		fmt.Fprintln(os.Stderr, "usage: bulltunn-engine -c <config file>")
		os.Exit(2)
	}

	if *check {
		if err := cmd.CheckConfigFile(*configPath); err != nil {
			fmt.Fprintf(os.Stderr, "the configuration is not usable: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("the configuration of %s is valid\n", *configPath)
		return
	}

	runEngine(*configPath)
}

// runEngine runs one tunnel until the process is asked to stop.
func runEngine(configPath string) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// BackPack's own engine loop: it loads the file, applies the defaults,
	// validates it, starts the transport and reloads it when the file
	// changes. Nothing of it is re-implemented here.
	cmd.Run(configPath, ctx)
}

// runProbe measures the path between this server and the far end of a
// tunnel, once per carrier. The result is one JSON object per line, so the
// panel can read it whatever happens in between.
func runProbe(args []string) {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	configPath := fs.String("c", "", "path to the client configuration file")
	only := fs.String("transports", "", "the carriers to test (default: every one this build speaks)")
	samples := fs.Int("samples", 3, "how many round trips to measure")
	timeout := fs.Int("timeout", 15, "how long one carrier may take to come up, in seconds")
	fs.Parse(args)
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "usage: bulltunn-engine probe -c <config file>")
		os.Exit(2)
	}

	list := carriers
	if *only != "" {
		list = nil
		for _, t := range strings.Split(*only, ",") {
			if t = strings.TrimSpace(t); t != "" {
				list = append(list, t)
			}
		}
	}

	results := transport.Probe(context.Background(), *configPath, list, transport.ProbeOptions{
		Samples: *samples,
		Timeout: time.Duration(*timeout) * time.Second,
	})
	fmt.Print(transport.ProbeJSON(results))
}

// runAgent runs the command channel: the panel writes a signed command into
// a TXT record and the agent runs it.
func runAgent(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	configPath := fs.String("c", "", "path to the agent configuration file")
	fs.Parse(args)
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "usage: bulltunn-engine agent -c <agent config file>")
		os.Exit(2)
	}
	if err := agent.Run(*configPath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
