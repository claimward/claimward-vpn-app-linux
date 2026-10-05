// Package helperd is the Claimward privileged helper's process: it reads the
// helper's configuration, opens its socket and serves the app until it is told
// to stop, taking the tunnel down on the way out.
//
// What the helper will do is claimward-vpn-client's pkg/helper, shared with the
// macOS and Windows apps; this is only the Linux daemon around it, run by
// systemd (deploy/claimward-helper.service).
package helperd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/claimward/claimward-vpn-client/pkg/helper"
)

// DefaultConfigPath is where the installer puts the helper's configuration.
const DefaultConfigPath = "/etc/claimward/helper.json"

// deps are the seams a test replaces: the configuration loader (which refuses a
// file not owned by root), the socket (which needs root to give to its group),
// the signals, and the effective user.
type deps struct {
	load    func(path string) (*helper.Config, error)
	listen  func(path, group string) (net.Listener, error)
	signals func() (<-chan os.Signal, func())
	euid    func() int
}

var system = deps{
	load:   helper.LoadConfig,
	listen: helper.Listen,
	signals: func() (<-chan os.Signal, func()) {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
		return ch, func() { signal.Stop(ch) }
	},
	euid: os.Geteuid,
}

// Main runs the helper with the process's arguments (without the program
// name) and returns its exit status.
func Main(args []string, stderr io.Writer) int { return run(args, stderr, system) }

func run(args []string, stderr io.Writer, d deps) int {
	fs := flag.NewFlagSet("claimward-helper", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", DefaultConfigPath, "the helper's configuration: owned by root, writable by root alone")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: claimward-helper [-config path]")
		fmt.Fprintln(stderr, "The Claimward privileged helper: brings the WireGuard tunnel up for the app. Runs as root, under systemd.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fs.Usage()
		return 2
	}
	log := slog.New(slog.NewTextHandler(stderr, nil))
	if d.euid() != 0 {
		log.Warn("not running as root: the tunnel and the socket's group both need it")
	}

	cfg, err := d.load(*cfgPath)
	if err != nil {
		log.Error("configuration", "err", err)
		return 1
	}
	ln, err := d.listen(cfg.Socket, cfg.Group)
	if err != nil {
		log.Error("listen", "socket", cfg.Socket, "group", cfg.Group, "err", err)
		return 1
	}
	srv := helper.New(*cfg, "linux", "app-linux", log)
	sig, stop := d.signals()
	defer stop()

	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	log.Info("claimward-helper listening", "socket", cfg.Socket, "group", cfg.Group, "servers", cfg.Servers)

	select {
	case s := <-sig:
		log.Info("stopping: taking the tunnel down", "signal", s.String())
		srv.Shutdown()
		_ = ln.Close()
		<-served
		return 0
	case err := <-served:
		srv.Shutdown()
		log.Error("serve", "err", err)
		return 1
	}
}
