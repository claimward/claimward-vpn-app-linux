//go:build gtksmoke

package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGTKHostOpensTheWindow starts the real app with the GTK4 host on, under a
// display (CI: xvfb-run, with libgtk-4 installed), and requires that the window
// opens and stays open instead of falling back to the built-in backend. It
// proves the GTK host runs; it does not look inside the window for the GtkEntry.
func TestGTKHostOpensTheWindow(t *testing.T) {
	if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no display: run under xvfb-run")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claimward-app")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/claimward-app").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "GO_WIDGETS_GTK=1", "HOME="+dir, "XDG_CONFIG_HOME="+dir, "XDG_DATA_HOME="+dir)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case err := <-exited:
		t.Fatalf("the app exited within 6s (%v):\n%s", err, out.String())
	case <-time.After(6 * time.Second):
	}
	_ = cmd.Process.Kill()
	<-exited
	if s := out.String(); strings.Contains(s, "falling back") || strings.Contains(s, "window:") {
		t.Fatalf("the GTK4 window did not open:\n%s", s)
	}
}
