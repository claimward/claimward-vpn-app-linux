package helperd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/helper"
	"github.com/claimward/claimward-vpn-client/pkg/helperclient"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
	"github.com/claimward/claimward-vpn-client/pkg/protocol"
	"github.com/claimward/claimward-vpn-client/pkg/wgkey"
	"github.com/claimward/claimward-vpn-client/pkg/wgtun"
)

// syncBuffer is a log the helper writes from several goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// socketPath is a socket in a short directory: a unix socket path is limited
// to about a hundred bytes, which a test's temporary directory can exceed.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "cw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "h.sock")
}

func fakeDeps(t *testing.T, sock string) (deps, chan os.Signal, *string, *string) {
	sig := make(chan os.Signal, 1)
	var loaded, group string
	return deps{
		load: func(path string) (*helper.Config, error) {
			loaded = path
			return &helper.Config{Servers: []string{"https://vpn.example.org"}, Group: "claimward", Socket: sock}, nil
		},
		listen: func(path, g string) (net.Listener, error) {
			group = g
			return net.Listen("unix", path)
		},
		signals: func() (<-chan os.Signal, func()) { return sig, func() {} },
		euid:    func() int { return 0 },
		setup:   func(*helper.Server) {},
	}, sig, &loaded, &group
}

func TestServesTheAppUntilSIGTERM(t *testing.T) {
	sock := socketPath(t)
	d, sig, loaded, group := fakeDeps(t, sock)
	var log syncBuffer
	exit := make(chan int)
	go func() { exit <- run(nil, &log, d) }()

	c := helperclient.New(sock)
	deadline := time.Now().Add(5 * time.Second)
	for !c.Available() {
		if time.Now().After(deadline) {
			t.Fatalf("the helper never listened; log:\n%s", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, err := c.Status()
	if err != nil || !st.OK || st.Connected {
		t.Fatalf("status %+v %v", st, err)
	}
	// It is pinned to its configured servers: another is refused.
	if _, err := c.Tenants("https://evil.example.net", "bearer"); err == nil || !strings.Contains(err.Error(), "not one this helper is configured for") {
		t.Fatalf("an unconfigured server was accepted: %v", err)
	}

	sig <- syscall.SIGTERM
	select {
	case code := <-exit:
		if code != 0 {
			t.Fatalf("exit %d; log:\n%s", code, log.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGTERM did not stop the helper")
	}
	if *loaded != DefaultConfigPath || *group != "claimward" {
		t.Fatalf("config %q group %q", *loaded, *group)
	}
	if !strings.Contains(log.String(), "stopping: taking the tunnel down") {
		t.Fatalf("log:\n%s", log.String())
	}
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the socket outlived the helper: %v", err)
	}
}

func TestTheConfigFlag(t *testing.T) {
	sock := socketPath(t)
	d, sig, loaded, _ := fakeDeps(t, sock)
	sig <- syscall.SIGINT
	var log syncBuffer
	if code := run([]string{"-config", "/tmp/h.json"}, &log, d); code != 0 || *loaded != "/tmp/h.json" {
		t.Fatalf("exit %d config %q", code, *loaded)
	}
}

func TestUsageErrors(t *testing.T) {
	var log syncBuffer
	if code := run([]string{"-nope"}, &log, deps{}); code != 2 {
		t.Fatalf("an unknown flag exits %d", code)
	}
	if code := run([]string{"extra"}, &log, deps{}); code != 2 || !strings.Contains(log.String(), "usage:") {
		t.Fatalf("an extra argument exits %d", code)
	}
	if code := run([]string{"-h"}, &log, deps{}); code != 0 {
		t.Fatalf("-h exits %d", code)
	}
}

func TestAConfigurationItRefusesStopsIt(t *testing.T) {
	var log syncBuffer
	d := deps{
		load: func(string) (*helper.Config, error) { return nil, errors.New("must be owned by root") },
		euid: func() int { return 1000 },
	}
	if code := run(nil, &log, d); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(log.String(), "not running as root") || !strings.Contains(log.String(), "must be owned by root") {
		t.Fatalf("log:\n%s", log.String())
	}
}

func TestASocketItCannotOpenStopsIt(t *testing.T) {
	var log syncBuffer
	d := deps{
		load: func(string) (*helper.Config, error) { return &helper.Config{Socket: "/x", Group: "nogroup"}, nil },
		listen: func(string, string) (net.Listener, error) {
			return nil, errors.New(`the helper's group "nogroup": unknown group`)
		},
		euid: func() int { return 0 },
	}
	if code := run(nil, &log, d); code != 1 || !strings.Contains(log.String(), "unknown group") {
		t.Fatalf("exit %d log:\n%s", code, log.String())
	}
}

// brokenListener fails its first Accept with something other than a close.
type brokenListener struct{ net.Listener }

func (brokenListener) Accept() (net.Conn, error) {
	return nil, errors.New("accept: too many open files")
}
func (brokenListener) Close() error { return nil }

func TestAServeFailureStopsIt(t *testing.T) {
	var log syncBuffer
	d := deps{
		load:    func(string) (*helper.Config, error) { return &helper.Config{Servers: []string{"x"}}, nil },
		listen:  func(string, string) (net.Listener, error) { return brokenListener{}, nil },
		signals: func() (<-chan os.Signal, func()) { return make(chan os.Signal), func() {} },
		euid:    func() int { return 0 },
		setup:   func(*helper.Server) {},
	}
	if code := run(nil, &log, d); code != 1 || !strings.Contains(log.String(), "too many open files") {
		t.Fatalf("exit %d log:\n%s", code, log.String())
	}
}

func TestTheSystemSignalsAreWired(t *testing.T) {
	ch, stop := system.signals()
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-ch:
		if s != syscall.SIGINT {
			t.Fatalf("signal %v", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGINT was not delivered")
	}
}

func TestMainUsesTheSystem(t *testing.T) {
	var log syncBuffer
	// A configuration file that does not exist: the system loader refuses it,
	// which is as far as an unprivileged test can take the real thing.
	if code := Main([]string{"-config", filepath.Join(t.TempDir(), "missing.json")}, &log); code != 1 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(log.String(), "missing.json") {
		t.Fatalf("log:\n%s", log.String())
	}
}

// fakeTunnel stands in for the WireGuard tunnel, which needs root.
type fakeTunnel struct {
	mu     sync.Mutex
	closed bool
}

func (f *fakeTunnel) Name() string                { return "utun" }
func (f *fakeTunnel) UpdateRoutes([]string) error { return nil }
func (f *fakeTunnel) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}
func (f *fakeTunnel) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func TestSIGTERMTakesTheTunnelDown(t *testing.T) {
	// A claimward-vpn-server, as far as the helper sees one.
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+protocol.PathEnroll, func(w http.ResponseWriter, r *http.Request) {
		pair, _ := wgkey.Generate()
		json.NewEncoder(w).Encode(protocol.EnrollResponse{AssignedIP: "10.80.0.7/32", ServerPublicKey: pair.Public.String(),
			Endpoint: "vpn.example.org:51820", AllowedIPs: []string{"10.2.0.0/16"}})
	})
	vpn := httptest.NewServer(mux)
	defer vpn.Close()

	sock := socketPath(t)
	d, sig, _, _ := fakeDeps(t, sock)
	d.load = func(string) (*helper.Config, error) {
		return &helper.Config{Servers: []string{vpn.URL}, Group: "claimward", Socket: sock}, nil
	}
	tun := &fakeTunnel{}
	d.setup = func(s *helper.Server) {
		s.UseTunnels(func(wgtun.Config) (helper.Tunnel, error) { return tun, nil })
	}
	var log syncBuffer
	exit := make(chan int)
	go func() { exit <- run(nil, &log, d) }()

	c := helperclient.New(sock)
	for deadline := time.Now().Add(5 * time.Second); !c.Available(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the helper never listened; log:\n%s", log.String())
		}
	}
	pair, _ := wgkey.Generate()
	resp, err := c.Connect(hproto.ConnectSpec{ServerURL: vpn.URL, Bearer: "b", PrivateKey: pair.Private.String(), DeviceName: "laptop"})
	if err != nil || !resp.Connected || resp.AssignedIP != "10.80.0.7/32" {
		t.Fatalf("connect %+v %v", resp, err)
	}
	if tun.isClosed() {
		t.Fatal("the tunnel is down already")
	}
	sig <- syscall.SIGTERM
	if code := <-exit; code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !tun.isClosed() {
		t.Fatal("SIGTERM left the tunnel up")
	}
}
