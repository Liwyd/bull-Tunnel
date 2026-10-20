// Command bulltunn-engine is the tunnel engine of Bull-Tunn.
//
// It runs one tunnel: the [server] side that waits and publishes ports, or
// the [client] side that dials out. The control panel writes the
// configuration file, starts the unit and reads the metrics file back.
//
//	bulltunn-engine -c /etc/bulltunn/link.toml    run the tunnel
//	bulltunn-engine -c link.toml -check           only read and check the file
//	bulltunn-engine agent -c /etc/bulltunn/agent.toml   the command channel
//	bulltunn-engine probe -c link.toml            measure the path
//	bulltunn-engine -v                            print the version
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Liwyd/Bull-Tunn/engine/internal/agent"
	"github.com/Liwyd/Bull-Tunn/engine/internal/config"
	"github.com/Liwyd/Bull-Tunn/engine/internal/metrics"
	"github.com/Liwyd/Bull-Tunn/engine/internal/probe"
	"github.com/Liwyd/Bull-Tunn/engine/internal/tunnel"
)

// Version is the version of the engine. The release workflow sets it.
var Version = "1.0.0"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "agent" {
		runAgent(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "probe" {
		runProbe(os.Args[2:])
		return
	}
	var (
		configPath = flag.String("c", "", "path of the configuration file")
		check      = flag.Bool("check", false, "read the configuration, report the problem and exit")
		version    = flag.Bool("v", false, "print the version and exit")
	)
	flag.Parse()

	if *version {
		fmt.Printf("bulltunn-engine %s\n", Version)
		fmt.Printf("transports: %s\n", strings.Join(config.Transports(), ","))
		return
	}
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "usage: bulltunn-engine -c <config file>")
		os.Exit(2)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the configuration is not usable: %v\n", err)
		os.Exit(1)
	}
	if *check {
		fmt.Printf("the configuration of %s is valid\n", cfg.Name)
		return
	}

	log := newLogger(cfg)
	mw := metrics.New(*configPath)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch {
	case cfg.Server != nil:
		srv, err := tunnel.NewServer(cfg.Server, log, mw)
		if err != nil {
			log.Error("the tunnel cannot start", "error", err)
			os.Exit(1)
		}
		if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("the tunnel stopped", "error", err)
			os.Exit(1)
		}
	case cfg.Client != nil:
		cl, err := tunnel.NewClient(cfg.Client, log, mw)
		if err != nil {
			log.Error("the tunnel cannot start", "error", err)
			os.Exit(1)
		}
		if err := cl.Run(ctx); err != nil && ctx.Err() == nil {
			log.Error("the tunnel stopped", "error", err)
			os.Exit(1)
		}
	}
	// the metrics file says the tunnel is down even if it is not running
	mw.Write(metrics.Snapshot{Connected: false, Transports: config.Transports()})
}

// newLogger builds the logger of the process. The level comes from the
// configuration file.
func newLogger(cfg *config.File) *slog.Logger {
	level := slog.LevelInfo
	name := ""
	if cfg.Server != nil {
		name = cfg.Server.LogLevel
	} else {
		name = cfg.Client.LogLevel
	}
	switch strings.ToLower(name) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
}

// runAgent is the command channel: it polls a TXT record over DNS and runs
// the commands the panel puts there.
func runAgent(args []string) {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	configPath := fs.String("c", "", "path of the agent configuration file")
	fs.Parse(args)
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "usage: bulltunn-engine agent -c <agent config file>")
		os.Exit(2)
	}
	cfg, err := agent.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the agent configuration is not usable: %v\n", err)
		os.Exit(1)
	}
	pub, err := agent.NewPublisher(cfg.Provider, cfg.Zone, cfg.APIKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the agent cannot start: %v\n", err)
		os.Exit(1)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	a := agent.New(cfg, pub, log)
	if err := a.Run(ctx); err != nil {
		log.Error("the agent stopped", "error", err)
		os.Exit(1)
	}
}

// runProbe measures the path between the server it runs on and the other
// end of a tunnel, once per carrier. The result is one JSON object per
// line, so the panel can read it whatever happens in between.
func runProbe(args []string) {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	configPath := fs.String("c", "", "path of the client configuration file")
	transports := fs.String("transports", "", "the carriers to test (default: all of them)")
	samples := fs.Int("samples", 3, "how many round trips to measure")
	fs.Parse(args)
	if *configPath == "" {
		fmt.Fprintln(os.Stderr, "usage: bulltunn-engine probe -c <client config file>")
		os.Exit(2)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the configuration is not usable: %v\n", err)
		os.Exit(1)
	}
	if cfg.Client == nil {
		fmt.Fprintln(os.Stderr, "the probe needs the client side of a tunnel")
		os.Exit(2)
	}
	list := config.Transports()
	if *transports != "" {
		list = nil
		for _, t := range strings.Split(*transports, ",") {
			if t = strings.TrimSpace(t); t != "" {
				list = append(list, t)
			}
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	results := probe.MeasureAll(ctx, cfg.Client, list, probe.Options{
		Samples:     *samples,
		WithSession: true,
	})
	fmt.Print(probe.JSON(results))
}
