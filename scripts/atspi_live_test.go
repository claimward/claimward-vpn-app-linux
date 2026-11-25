//go:build atspilive

package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// ref is an AT-SPI object reference: (bus name, object path).
type ref struct {
	Name string
	Path dbus.ObjectPath
}

// cacheItem is one row of org.a11y.atspi.Cache.GetItems, a(so)(so)(so)iiassusau.
type cacheItem struct {
	Ref        ref
	App        ref
	Parent     ref
	Index      int32
	Children   int32
	Interfaces []string
	Name       string
	Role       uint32
	Desc       string
	State      []uint32
}

const (
	rolePushButton = 43
	stateEnabled   = 8
	stateSensitive = 24
)

// TestTheAppPublishesItsButtonsToATSPI starts the real app under a display, a
// session bus and the AT-SPI registry (CI: xvfb-run, dbus-run-session,
// at-spi2-core) and reads its tree the way a screen reader does: through the
// accessibility bus. It dumps every row, then requires the Settings button as a
// push button that is enabled and sensitive, and Connect (nothing is configured,
// so its command cannot execute) as a push button that is neither.
func TestTheAppPublishesItsButtonsToATSPI(t *testing.T) {
	if os.Getenv("CLAIMWARD_ATSPI_LIVE") != "1" {
		t.Skip("set CLAIMWARD_ATSPI_LIVE=1 under xvfb-run, dbus-run-session and at-spi2-core")
	}
	if os.Getenv("AT_SPI_BUS_ADDRESS") != "" {
		t.Fatal("AT_SPI_BUS_ADDRESS must be unset: the proof is the real discovery path")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claimward-app")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/claimward-app").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_DATA_HOME="+dir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer func() {
		_ = cmd.Process.Kill()
		<-exited
		t.Logf("app output:\n%s", out.String())
	}()

	sess, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	var addr string
	if err := sess.Object("org.a11y.Bus", "/org/a11y/bus").Call("org.a11y.Bus.GetAddress", 0).Store(&addr); err != nil {
		t.Fatal(err)
	}
	cl, err := dbus.Dial(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if err := cl.Auth(nil); err != nil {
		t.Fatal(err)
	}
	if err := cl.Hello(); err != nil {
		t.Fatal(err)
	}

	// Poll: the app takes a moment to open its window and publish.
	var items []cacheItem
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			t.Fatalf("the app exited (%v):\n%s", err, out.String())
		default:
		}
		items = items[:0]
		var names []string
		if err := cl.BusObject().Call("org.freedesktop.DBus.ListNames", 0).Store(&names); err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			if n == "" || n[0] != ':' || n == cl.Names()[0] {
				continue
			}
			var rows []cacheItem
			if cl.Object(n, "/org/a11y/atspi/cache").Call("org.a11y.atspi.Cache.GetItems", 0).Store(&rows) == nil {
				items = append(items, rows...)
			}
		}
		for _, it := range items {
			if it.Name == "Settings" {
				goto found
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("no element named Settings reached the accessibility bus; rows: %+v", items)
found:
	for _, it := range items {
		t.Logf("tree: role=%d name=%q state=%#x", it.Role, it.Name, it.State)
	}
	find := func(name string) cacheItem {
		for _, it := range items {
			if it.Name == name {
				return it
			}
		}
		t.Fatalf("no element named %q in the tree", name)
		return cacheItem{}
	}
	const operable = 1<<stateEnabled | 1<<stateSensitive
	s := find("Settings")
	if s.Role != rolePushButton {
		t.Fatalf("Settings has role %d, want push button %d", s.Role, rolePushButton)
	}
	if s.State[0]&operable != operable {
		t.Fatalf("Settings state %#x lacks enabled+sensitive", s.State[0])
	}
	c := find("Connect")
	if c.Role != rolePushButton {
		t.Fatalf("Connect has role %d, want push button", c.Role)
	}
	if c.State[0]&operable != 0 {
		t.Fatalf("disabled Connect state %#x still reports enabled or sensitive", c.State[0])
	}
}
