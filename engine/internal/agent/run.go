package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Run is the command channel, from the outside: it reads the agent's
// configuration, opens the publisher and polls until the process is asked to
// stop.
//
// It is what "bulltunn-engine agent -c /etc/bulltunn/agent.toml" runs.
func Run(configPath string) error {
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return fmt.Errorf("the agent configuration is not usable: %w", err)
	}
	pub, err := NewPublisher(cfg.Provider, cfg.Zone, cfg.APIKey)
	if err != nil {
		return fmt.Errorf("the agent cannot start: %w", err)
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return New(cfg, pub, log).Run(ctx)
}
