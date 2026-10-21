// Command bull-tunnel-panel is the control panel of bull-Tunnel.
//
// The panel watches the servers it manages, checks the tunnels, moves the
// records of the domains when a path breaks, and serves the web interface
// and the API the management tool talks to.
//
// It runs on the machine the panel runs on, and everything it knows is in
// one file: the state.
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/Liwyd/bull-Tunnel/internal/panel/api"
	"github.com/Liwyd/bull-Tunnel/internal/panel/auth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/backup"
	"github.com/Liwyd/bull-Tunnel/internal/panel/dns"
	"github.com/Liwyd/bull-Tunnel/internal/panel/engine"
	"github.com/Liwyd/bull-Tunnel/internal/panel/localauth"
	"github.com/Liwyd/bull-Tunnel/internal/panel/model"
	"github.com/Liwyd/bull-Tunnel/internal/panel/orch"
	"github.com/Liwyd/bull-Tunnel/internal/panel/ssh"
	"github.com/Liwyd/bull-Tunnel/internal/panel/store"
	"github.com/Liwyd/bull-Tunnel/internal/panel/telegram"
)

// version is the version of the panel. It is stamped at link time by
// build.sh.
var version = "1.1.3"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		// the health of the panel: it asks its own API
		os.Exit(check())
	}
	data := dataDir()
	host := envOr("BT_HOST", "0.0.0.0")
	port := envOr("BT_PORT", "8080")
	demo := os.Getenv("BT_DEMO") == "1"

	if err := os.MkdirAll(data, 0o700); err != nil {
		log.Fatalf("the data directory cannot be made: %v", err)
	}
	state := store.New(filepath.Join(data, "state.json"))
	login := auth.New(filepath.Join(data, "auth.json"))
	keys := ssh.NewHostKeys(filepath.Join(data, "hostkeys.json"))
	pool := ssh.NewPool(10*time.Second, keys)
	defer pool.Close()

	driver := engine.NewDriver(pool, filepath.Join(data, "cache"), "latest", state.Settings.EngineRelease, state.Settings.EngineSource)
	brain := orch.NewWith(state, orch.Options{
		Driver: driver,
		DNS: func(provider *model.DNSProvider) (dns.Client, error) {
			return dns.New(provider, os.Getenv("BT_DNS_PROXY"))
		},
		Resolver: func(fqdn string, resolvers []string) map[string][]string {
			return dns.ResolveA(fqdn, resolvers, os.Getenv("BT_DNS_PROXY"))
		},
	})

	bot := telegram.New(func() model.Telegram { return state.Settings.TG() },
		func(command string, args []string) (string, error) {
			return brain.BotCommand(command, args), nil
		})
	panel := api.New(api.Options{
		Store:        state,
		Orchestrator: brain,
		Auth:         login,
		Limiter:      auth.NewLimiter(8, 300, 900),
		HostKeys:     keys,
		LocalKey:     localauth.ReadLocalKey(filepath.Join(data, localauth.KeyName)),
		Version:      version,
		Demo:         demo,
		Static:       staticDir(),
		DNS: func(provider *model.DNSProvider) (dns.Client, error) {
			return dns.New(provider, os.Getenv("BT_DNS_PROXY"))
		},
		Bot:     bot,
		Backups: backup.NewStore(data, "/etc/default/bull-tunnel"),
		Driver:  driver,
	})
	brain.SetBroadcast(panel.Broadcast)

	// the monitor: the cycle that checks every path and moves what must be
	// moved
	if os.Getenv("BT_NO_MONITOR") != "1" {
		go monitor(brain)
	}
	go bot.Start()
	defer bot.Stop()

	server := &http.Server{
		Addr:              host + ":" + port,
		Handler:           panel.Handler(),
		ReadHeaderTimeout: 20 * time.Second,
	}
	certificate, key := os.Getenv("BT_SSL_CERT"), os.Getenv("BT_SSL_KEY")
	if certificate != "" && key != "" {
		server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{}}
		loaded, err := tls.LoadX509KeyPair(certificate, key)
		if err != nil {
			log.Fatalf("the certificate cannot be read: %v", err)
		}
		server.TLSConfig.Certificates[0] = loaded
		log.Printf("the panel answers on https://%s", server.Addr)
		if err := server.ListenAndServeTLS(certificate, key); err != nil {
			log.Fatalf("the panel stopped: %v", err)
		}
		return
	}
	log.Printf("the panel answers on http://%s", server.Addr)
	go func() {
		// a signal stops the panel the way it should
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
		<-signals
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("the panel stopped: %v", err)
	}
}

// monitor is the loop of the panel: the cycle, over and over, and a little
// sooner when something asks for it.
func monitor(brain *orch.Orchestrator) {
	for {
		brain.CheckAll()
		interval := time.Duration(brain.Store().Settings.CheckInterval) * time.Second
		if interval < 5*time.Second {
			interval = 5 * time.Second
		}
		select {
		case <-time.After(interval):
		case <-brain.Woken():
			// the panel was asked to check right now
		}
	}
}

// check asks the panel's own API and says whether it answers.
func check() int {
	host := envOr("BT_HOST", "0.0.0.0")
	if host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	url := "http://" + host + ":" + envOr("BT_PORT", "8080") + "/api/session"
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(url)
	if err != nil {
		return 1
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// dataDir is where the panel keeps its files.
func dataDir() string {
	if dir := os.Getenv("BT_DATA_DIR"); dir != "" {
		return dir
	}
	return "/var/lib/bull-tunnel"
}

// staticDir is where the files of the web interface are.
func staticDir() string {
	if dir := os.Getenv("BT_STATIC_DIR"); dir != "" {
		return dir
	}
	executable, err := os.Executable()
	if err != nil {
		return "static"
	}
	return filepath.Join(filepath.Dir(executable), "static")
}

// envOr is a variable of the environment, or something else.
func envOr(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// the port is a number.
var _ = strconv.Itoa
var _ = fmt.Sprintf
