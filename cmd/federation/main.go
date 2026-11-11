// Federation walks an operator, phase by phase, through peering our goatnet
// with a host nation's, and keeps a record of how far each peering has got.
//
// Each phase has a Run button: its script runs in the background, from the
// container on the core host, and its log is kept for when it fails.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/philosoma/federation/internal/runner"
	"github.com/philosoma/federation/internal/server"
	"github.com/philosoma/federation/internal/store"
)

var version = "dev"

type fileConfig struct {
	Site struct {
		Name string `json:"name"`
		Flag string `json:"flag"`
	} `json:"site"`
	Auth struct {
		RequiredGroups []string `json:"required_groups"`
	} `json:"auth"`
	// Origins allowed to show Federation in a frame (Signpost opens apps
	// under its header). Each must be https://host[:port].
	FrameAncestors []string `json:"frame_ancestors"`
	// Flashlight's address, for the Status view's "see their data" links.
	FlashlightURL string `json:"flashlight_url"`
	// The forwarder's name partners dial when we listen.
	PublicName string `json:"public_name"`
	State      string `json:"state"`
	Us         string `json:"sovereign_id"`
}

func main() {
	configPath := flag.String("config", "", "path to the config file (optional)")
	listen := flag.String("listen", "127.0.0.1:8090", "address to listen on")
	state := flag.String("state", "", "state folder: records, logs, work (overrides the config; default ./data)")
	healthcheck := flag.Bool("healthcheck", false, "check a running instance and exit")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck(*listen))
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(*configPath, *listen, *state, log); err != nil {
		log.Error("federation stopped", "error", err)
		os.Exit(1)
	}
}

func run(configPath, listen, stateDir string, log *slog.Logger) error {
	var fc fileConfig
	fc.Site.Name = "Federation"
	if configPath != "" {
		b, err := os.ReadFile(configPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &fc); err != nil {
			return fmt.Errorf("%s: %w", configPath, err)
		}
	}
	if fc.Site.Flag != "" && !server.HasFlag(fc.Site.Flag) {
		return fmt.Errorf("site.flag %q: no such flag; use an ISO 3166-1 alpha-2 code, lower case", fc.Site.Flag)
	}
	if stateDir == "" {
		stateDir = fc.State
	}
	if stateDir == "" {
		stateDir = "data"
	}
	if fc.Us == "" {
		return fmt.Errorf("set sovereign_id in the config: your goatnet's id, usually its ISO alpha-3 code")
	}
	if !store.ValidSovereign(fc.Us) {
		return fmt.Errorf("sovereign_id %q is not an alpha-3 code", fc.Us)
	}

	for _, o := range fc.FrameAncestors {
		u, err := url.Parse(o)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.RawQuery != "" || strings.ContainsAny(o, " ;'") {
			return fmt.Errorf("frame_ancestors: %q is not an https origin", o)
		}
	}

	// Without a group check, the only door is the loopback interface.
	if len(fc.Auth.RequiredGroups) == 0 && !loopback(listen) {
		return fmt.Errorf("no auth.required_groups configured, so -listen must be a loopback address, not %q", listen)
	}

	st, err := store.Open(filepath.Join(stateDir, "federation.json"))
	if err != nil {
		return err
	}
	// Scripts get this process's environment: the image sets PATH and where
	// the keys, secrets and checkouts are.
	rn, err := runner.New(st, stateDir, fc.Us, os.Environ(), log)
	if err != nil {
		return err
	}
	handler, err := server.New(server.Config{SiteName: fc.Site.Name, Flag: fc.Site.Flag, RequiredGroups: fc.Auth.RequiredGroups, FrameAncestors: fc.FrameAncestors, FlashlightURL: fc.FlashlightURL, Us: fc.Us, PublicName: fc.PublicName}, st, rn, version, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if os.Getenv("FED_NO_STATUS") == "" {
		go rn.WatchStatus(ctx, time.Minute)
	}
	srv := &http.Server{
		Addr:              listen,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("federation listening", "listen", listen, "state", stateDir, "version", version)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func runHealthcheck(listen string) int {
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		return 1
	}
	c := http.Client{Timeout: 3 * time.Second}
	res, err := c.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
