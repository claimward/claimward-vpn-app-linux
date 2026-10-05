package trayview

import (
	"bytes"
	"context"
	"image/png"
	"sync"
	"testing"
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
	"github.com/go-widgets/tray"

	"github.com/claimward/claimward-vpn-app-linux/assets"
	"github.com/claimward/claimward-vpn-app-linux/internal/viewmodel"
)

type service struct {
	mu    sync.Mutex
	st    appcore.Status
	calls []string
}

func (s *service) Status() appcore.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}
func (s *service) Config() appcore.Config                           { return appcore.Config{} }
func (s *service) UpdateConfig(appcore.Config) error                { return nil }
func (s *service) Login(context.Context) error                      { return nil }
func (s *service) Tenants(context.Context) ([]hproto.Tenant, error) { return nil, nil }
func (s *service) SetTenant(string) error                           { return nil }
func (s *service) Logout(context.Context) error                     { return nil }
func (s *service) Connect(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "connect")
	s.st.Connected, s.st.AssignedIP = true, "10.80.0.5/32"
	return nil
}
func (s *service) Disconnect(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "disconnect")
	s.st.Connected, s.st.AssignedIP = false, ""
	return nil
}

func labels(m *tray.Menu) []string {
	var out []string
	for _, it := range m.Items {
		out = append(out, it.Label)
	}
	return out
}

// waitIcon waits for the icon animator, which draws on its own goroutine.
func waitIcon(t *testing.T, h *tray.Headless, want []byte) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if icon, _, _ := h.Snapshot(); bytes.Equal(icon, want) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the tray icon did not follow the connection")
}

func TestTheTrayFollowsTheViewModelAndRunsItsCommands(t *testing.T) {
	svc := &service{st: appcore.Status{ConfigOK: true, LoggedIn: true, HelperInstalled: true}}
	d := viewmodel.NewDispatcher()
	vm := viewmodel.New(svc, viewmodel.Options{Dispatcher: d})
	defer vm.Stop()
	vm.Apply(svc.Status())

	h := tray.NewHeadless()
	tr := tray.New(assets.TrayPNG).WithBackend(h)
	go func() { _ = tr.Run() }()
	defer tr.Quit()

	opened, quit := 0, 0
	unbind := Bind(tr, vm, assets.TrayPNG, Actions{OpenWindow: func() { opened++ }, Quit: func() { quit++ }})
	defer unbind()

	m := tr.Menu()
	want := []string{"Claimward: disconnected", "", "Connect", "Disconnect", "", "Open window", "Quit Claimward"}
	if got := labels(m); len(got) != len(want) {
		t.Fatalf("menu %q", got)
	}
	for i, l := range labels(m) {
		if l != want[i] {
			t.Fatalf("menu %q, want %q", labels(m), want)
		}
	}
	if !m.Items[0].Disabled || m.Items[2].Disabled || !m.Items[3].Disabled {
		t.Fatal("disconnected: the status line is inert, Connect enabled, Disconnect not")
	}
	if tr.Tooltip() != "Claimward: disconnected" {
		t.Fatalf("tooltip %q", tr.Tooltip())
	}
	waitIcon(t, h, Greyscale(assets.TrayPNG))

	// A click lands on the tray's goroutine: it is posted, not executed there.
	m.ByLabel("Connect").Activate()
	if d.Pending() == false || len(svc.calls) != 0 {
		t.Fatal("the click ran on the tray's goroutine")
	}
	for {
		d.Drain()
		vm.Wait()
		if !d.Pending() {
			break
		}
	}
	if len(svc.calls) != 1 || svc.calls[0] != "connect" {
		t.Fatalf("calls %v", svc.calls)
	}
	m = tr.Menu()
	if m.Items[0].Label != "Claimward: connected (10.80.0.5/32)" || !m.Items[2].Disabled || m.Items[3].Disabled {
		t.Fatalf("connected menu %q", labels(m))
	}
	waitIcon(t, h, assets.TrayPNG)

	m.ByLabel("Open window").Activate()
	m.ByLabel("Quit Claimward").Activate()
	if opened != 1 || quit != 1 {
		t.Fatalf("open %d quit %d", opened, quit)
	}
}

func TestGreyscale(t *testing.T) {
	grey := Greyscale(assets.TrayPNG)
	img, err := png.Decode(bytes.NewReader(grey))
	if err != nil {
		t.Fatal(err)
	}
	coloured := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a > 0 && (r != g || g != bl) {
				coloured++
			}
		}
	}
	if coloured != 0 {
		t.Fatalf("%d pixels still carry colour", coloured)
	}
	if bytes.Equal(grey, assets.TrayPNG) {
		t.Fatal("the icon was not changed")
	}
	if got := Greyscale([]byte("not a png")); string(got) != "not a png" {
		t.Fatal("an undecodable icon should be returned as is")
	}
}
