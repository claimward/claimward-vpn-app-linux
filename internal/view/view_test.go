package view

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
	"github.com/go-widgets/application"
	"github.com/go-widgets/toolkit"

	"github.com/claimward/claimward-vpn-app-linux/internal/viewmodel"
)

// service is a stand-in for appcore.Core that records what the window made
// the view model ask of it.
type service struct {
	mu    sync.Mutex
	st    appcore.Status
	cfg   appcore.Config
	calls []string
}

func (s *service) call(c string) {
	s.mu.Lock()
	s.calls = append(s.calls, c)
	s.mu.Unlock()
}

func (s *service) called() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *service) Status() appcore.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}
func (s *service) Config() appcore.Config { return s.cfg }
func (s *service) UpdateConfig(c appcore.Config) error {
	s.call("update:" + c.ServerURL)
	return nil
}
func (s *service) Login(context.Context) error { s.call("login"); return nil }
func (s *service) Tenants(context.Context) ([]hproto.Tenant, error) {
	s.call("tenants")
	return nil, nil
}
func (s *service) SetTenant(id string) error        { s.call("set-tenant:" + id); return nil }
func (s *service) Connect(context.Context) error    { s.call("connect"); return nil }
func (s *service) Disconnect(context.Context) error { s.call("disconnect"); return nil }
func (s *service) Logout(context.Context) error     { s.call("logout"); return nil }
func (s *service) has(c string) bool                { return contains(s.called(), c) }
func contains(list []string, c string) bool {
	for _, x := range list {
		if x == c {
			return true
		}
	}
	return false
}

const winW, winH = 480, 760

type fixture struct {
	svc  *service
	vm   *viewmodel.ViewModel
	disp *viewmodel.Dispatcher
	v    *View
}

func newFixture(t *testing.T, st appcore.Status) *fixture {
	t.Helper()
	svc := &service{st: st, cfg: appcore.Config{ServerURL: st.ServerURL, Provider: "github", GitHubClientID: "Iv1.abc"}}
	d := viewmodel.NewDispatcher()
	vm := viewmodel.New(svc, viewmodel.Options{Dispatcher: d, ConfigPath: "/home/ada/.config/Claimward/config.json"})
	v := New(vm, d)
	v.Resize(winW, winH, 1)
	f := &fixture{svc: svc, vm: vm, disp: d, v: v}
	f.poll()
	t.Cleanup(func() { vm.Stop(); vm.Wait(); v.Close() })
	return f
}

// poll is a status poll, applied the way the running app applies it: posted
// by a goroutine, drained by the view.
func (f *fixture) poll() {
	st := f.svc.Status()
	f.vm.Post(func() { f.vm.Apply(st) })
	f.v.Pump()
}

// settle lets every action finish and drains what it posted.
func (f *fixture) settle() {
	for {
		f.vm.Wait()
		if f.v.Pump() == 0 {
			return
		}
	}
}

func center(w toolkit.Widget) (int, int) {
	r := w.Bounds()
	return r.X + r.W/2, r.Y + r.H/2
}

func (f *fixture) click(w toolkit.Widget) {
	f.v.mu.Lock()
	x, y := center(w)
	f.v.mu.Unlock()
	f.v.MouseDown(x, y)
	f.v.MouseUp(x, y)
}

func visible(w toolkit.Widget) bool { r := w.Bounds(); return r.W > 0 && r.H > 0 }

func signedOut() appcore.Status {
	return appcore.Status{ConfigOK: true, HelperInstalled: true, ServerURL: "https://vpn.example.org"}
}

func signedIn() appcore.Status {
	st := signedOut()
	st.LoggedIn, st.Email = true, "ada@example.org"
	return st
}

func TestTheScreenFollowsTheViewModel(t *testing.T) {
	f := newFixture(t, appcore.Status{ConfigError: "missing config: server_url"})
	if !visible(f.v.unconfiguredPage) || visible(f.v.mainPage) {
		t.Fatal("an incomplete configuration should show its own screen")
	}
	if got := f.v.leaves.problem.Text().Get(); got != "missing config: server_url" {
		t.Fatalf("problem %q", got)
	}
	f.click(f.v.leaves.openSettings)
	if !visible(f.v.settingsPage) || visible(f.v.unconfiguredPage) {
		t.Fatal("Open settings did not show the form")
	}
	f.click(f.v.leaves.cancel)
	if !visible(f.v.unconfiguredPage) {
		t.Fatal("Cancel did not go back")
	}
}

func TestStatusLabelsAreBound(t *testing.T) {
	st := signedIn()
	st.Connected, st.AssignedIP, st.Interface = true, "10.80.0.5/32", "utun"
	f := newFixture(t, st)
	l := &f.v.leaves
	for _, c := range []struct {
		l    *toolkit.Label
		want string
	}{
		{l.state, "Connected"},
		{l.account, "Signed in as ada@example.org"},
		{l.address, "10.80.0.5/32 on utun"},
		{l.tenant, "Tenant: the server's choice"},
		{l.helper, "Privileged helper: running"},
		{l.srv, "https://vpn.example.org"},
	} {
		if got := c.l.Text().Get(); got != c.want {
			t.Errorf("label %q, want %q", got, c.want)
		}
	}
	if !visible(f.v.connectedRow) || visible(f.v.disconnectedRow) || visible(f.v.signedOutRow) {
		t.Fatal("connected: only the Disconnect row should show")
	}
	if !l.tenants.Disabled().Get() {
		t.Fatal("the tenant cannot be changed while connected")
	}
}

func TestButtonsExecuteTheirCommands(t *testing.T) {
	f := newFixture(t, signedOut())
	if !visible(f.v.signedOutRow) || f.v.leaves.signIn.Disabled().Get() {
		t.Fatal("signed out: Sign in should be offered")
	}
	if !f.v.leaves.connect.Disabled().Get() {
		t.Fatal("Connect is enabled while signed out")
	}
	f.click(f.v.leaves.signIn)
	f.settle()
	if !f.svc.has("login") {
		t.Fatalf("calls %v", f.svc.called())
	}

	f.svc.st = signedIn()
	f.poll()
	if !visible(f.v.disconnectedRow) || !visible(f.v.chooseSlot) {
		t.Fatal("signed in: Connect and Choose tenant should show")
	}
	f.click(f.v.leaves.chooseTenant)
	f.settle()
	f.click(f.v.leaves.connect)
	f.settle()
	f.click(f.v.leaves.signOut1)
	f.settle()
	for _, c := range []string{"tenants", "connect", "logout"} {
		if !f.svc.has(c) {
			t.Fatalf("%s not called: %v", c, f.svc.called())
		}
	}
	f.svc.st.Connected = true
	f.poll()
	f.click(f.v.leaves.disconnect)
	f.settle()
	if !f.svc.has("disconnect") {
		t.Fatalf("calls %v", f.svc.called())
	}
}

func TestADisabledButtonIgnoresAClick(t *testing.T) {
	st := signedIn()
	st.HelperInstalled = false // Connect needs the helper
	f := newFixture(t, st)
	f.click(f.v.leaves.connect)
	f.settle()
	if f.svc.has("connect") {
		t.Fatal("a disabled Connect connected")
	}
}

func TestTheSignInPromptIsShown(t *testing.T) {
	st := signedOut()
	st.DeviceVerificationURI, st.DeviceUserCode = "https://github.com/login/device", "ABCD-1234"
	f := newFixture(t, st)
	l := &f.v.leaves
	if !visible(f.v.signInSlot) || l.code.Text().Get() != "ABCD-1234" || l.url.Text().Get() != "https://github.com/login/device" {
		t.Fatal("the device-flow prompt is not shown")
	}
	if l.openPage.Disabled().Get() {
		t.Fatal("Open sign-in page is disabled")
	}
}

func TestChoosingATenantInTheDropDown(t *testing.T) {
	st := signedIn()
	st.TenantRequired = true
	st.Tenants = []hproto.Tenant{{ID: "acme", Name: "Acme"}, {ID: "lab", Name: "Lab"}}
	f := newFixture(t, st)
	dd := f.v.leaves.tenants
	if !visible(f.v.tenantSlot) || len(dd.Options) != 3 || dd.Options[0] != "Choose a tenant…" || dd.Options[2] != "Lab (lab)" {
		t.Fatalf("options %v", dd.Options)
	}
	f.click(dd) // opens the list
	if !dd.PopoverOpen() {
		t.Fatal("the drop-down did not open")
	}
	f.v.Frame() // the open list is drawn over the window
	pb := dd.PopoverBounds()
	row := (pb.H / len(dd.Options))
	f.v.MouseDown(pb.X+pb.W/2, pb.Y+2*row+row/2) // the third row: Lab
	if !f.svc.has("set-tenant:lab") {
		t.Fatalf("calls %v", f.svc.called())
	}
	if got := f.v.leaves.tenant.Text().Get(); got != "Tenant: Lab (lab)" {
		t.Fatalf("tenant %q", got)
	}
}

func TestTypingInTheSettingsFormReachesTheViewModel(t *testing.T) {
	f := newFixture(t, signedOut())
	f.click(f.v.leaves.settings)
	if !visible(f.v.settingsPage) {
		t.Fatal("Settings did not open the form")
	}
	l := &f.v.leaves
	if l.server.Text().Get() != "https://vpn.example.org" {
		t.Fatalf("server field %q", l.server.Text().Get())
	}
	f.click(l.server)
	f.v.Key("End", 0)
	for _, r := range "/x" {
		f.v.Key("", r)
	}
	f.v.Key("", 0) // nothing to type
	if got := f.vm.ServerURL.Get(); got != "https://vpn.example.org/x" {
		t.Fatalf("view model server %q", got)
	}
	f.v.ModifiedKey("Backspace", application.Modifiers{})
	f.v.Key("Backspace", 0)
	if got := f.vm.ServerURL.Get(); got != "https://vpn.example.org" {
		t.Fatalf("after backspaces %q", got)
	}

	// Tab moves to the provider, Shift+Tab back.
	f.v.ModifiedKey("Tab", application.Modifiers{})
	if l.server.Focused() || !l.provider.Focused() {
		t.Fatal("Tab did not move the focus")
	}
	f.v.ModifiedKey("Tab", application.Modifiers{Shift: true})
	if !l.server.Focused() {
		t.Fatal("Shift+Tab did not move the focus back")
	}

	// Paste.
	toolkit.SetClipboardText("/y")
	f.v.Shortcut('v', true, false)
	f.v.Shortcut('q', true, false) // not an editing chord: ignored
	if got := f.vm.ServerURL.Get(); got != "https://vpn.example.org/y" {
		t.Fatalf("after paste %q", got)
	}

	// Enter saves.
	f.v.Key("Enter", 0)
	f.settle()
	if !f.svc.has("update:https://vpn.example.org/y") {
		t.Fatalf("calls %v", f.svc.called())
	}
	if !visible(f.v.mainPage) {
		t.Fatal("saving did not close the form")
	}
}

func TestTheProviderDropDownIsBound(t *testing.T) {
	f := newFixture(t, signedOut())
	f.vm.OpenSettings.Execute()
	f.v.Pump()
	f.vm.ProviderIndex.Set(2)
	if f.v.leaves.provider.Selected().Get() != 2 {
		t.Fatal("the view model's provider did not reach the drop-down")
	}
	f.v.leaves.provider.Select(1)
	if f.vm.ProviderIndex.Get() != 1 {
		t.Fatal("the drop-down's choice did not reach the view model")
	}
}

func TestFramesArePaintedOnlyWhenSomethingChanged(t *testing.T) {
	f := newFixture(t, signedOut())
	buf, w, h, changed := f.v.Frame()
	if !changed || w != winW || h != winH || len(buf) != w*h*4 {
		t.Fatalf("first frame changed=%v %dx%d len %d", changed, w, h, len(buf))
	}
	bg := f.v.theme.Background
	if buf[0] != bg.R || buf[1] != bg.G || buf[2] != bg.B {
		t.Fatal("the frame is not on the theme's background")
	}
	if f.v.NeedsPresent() {
		t.Fatal("nothing changed but a present is asked for")
	}
	if _, _, _, changed := f.v.Frame(); changed {
		t.Fatal("an unchanged frame reported a change")
	}
	before := append([]byte(nil), buf...)
	f.svc.st = signedIn()
	f.poll()
	if !f.v.NeedsPresent() {
		t.Fatal("a status change did not ask for a frame")
	}
	after, _, _, changed := f.v.Frame()
	if !changed || bytes.Equal(before, after) {
		t.Fatal("the new status was not painted")
	}
	if f.v.PresentImmediate() || f.v.PresentThrottle() {
		t.Fatal("nothing here streams or animates")
	}
}

func TestNoFrameBeforeTheFirstResize(t *testing.T) {
	d := viewmodel.NewDispatcher()
	vm := viewmodel.New(&service{}, viewmodel.Options{Dispatcher: d})
	defer vm.Stop()
	v := New(vm, d)
	if buf, _, _, changed := v.Frame(); buf != nil || changed {
		t.Fatal("a frame before the window has a size")
	}
	v.Resize(0, 10, 1) // ignored
	if buf, _, _, _ := v.Frame(); buf != nil {
		t.Fatal("a zero-width resize made a frame")
	}
}

func TestAResizeAtAnotherScaleLaysOutAgain(t *testing.T) {
	f := newFixture(t, signedIn())
	before := f.v.leaves.connect.Bounds()
	f.v.Resize(2*winW, 2*winH, 2)
	after := f.v.leaves.connect.Bounds()
	if after.W < 2*before.W-2 || after.H < 2*before.H-2 {
		t.Fatalf("at scale 2 the button is %v, it was %v", after, before)
	}
	f.v.Resize(2*winW, 2*winH+20, 2) // same scale: only the bounds change
	f.v.Resize(winW, winH, 0)        // an unknown scale is 1
	if got := f.v.leaves.connect.Bounds(); got != before {
		t.Fatalf("back at scale 1 the button is %v, want %v", got, before)
	}
}

func TestTheDesktopAppearanceIsFollowed(t *testing.T) {
	f := newFixture(t, signedOut())
	f.v.Frame()
	f.v.SystemAppearance(application.SystemAppearance{Dark: true})
	buf, _, _, changed := f.v.Frame()
	dark := toolkit.DefaultDark().Background
	if !changed || buf[0] != dark.R || buf[1] != dark.G || buf[2] != dark.B {
		t.Fatal("the dark appearance was not taken")
	}
	if f.v.theme.Accent != brandTeal {
		t.Fatal("the accent is not the brand's")
	}
}

func TestPointerAndWheelReachTheWidgets(t *testing.T) {
	st := signedIn()
	for i := 0; i < 40; i++ {
		st.Log = append(st.Log, "line")
	}
	f := newFixture(t, st)
	lb := f.v.leaves.log
	if lb.ScrollRow().Get() != 40 {
		t.Fatalf("the log does not follow its end: row %d", lb.ScrollRow().Get())
	}
	x, y := center(lb)
	f.v.Frame()
	f.v.MouseMove(x, y) // a hover: the widget under the pointer may change its face
	if !f.v.NeedsPresent() {
		t.Fatal("input did not ask for a frame")
	}
	f.v.Scroll(-120) // three rows up
	f.v.Scroll(-1)   // less than a row is still a row
	f.v.Scroll(0)
	if got := f.vm.LogScroll.Get(); got >= 40 {
		t.Fatalf("scrolling the log did not reach the view model: row %d", got)
	}
	f.v.MouseDown(x, y)
	f.v.MouseMove(x, y+5) // a drag
	f.v.MouseUp(x, y+5)
	f.v.Scroll(1)
}

func TestTheAccessibilityTreeNamesTheControls(t *testing.T) {
	f := newFixture(t, signedOut())
	found := false
	for _, e := range f.v.A11yElements() {
		if e.Name == "Sign in" {
			found = true
		}
		if e.W <= 0 || e.H <= 0 {
			t.Fatalf("an element with no area: %+v", e)
		}
	}
	if !found {
		t.Fatal("the Sign in button is not described")
	}
}

func TestRunDrainsUntilCancelled(t *testing.T) {
	f := newFixture(t, signedOut())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.v.Run(ctx); close(done) }()
	ran := make(chan struct{})
	f.vm.Post(func() { close(ran) })
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not drain a posted function")
	}
	cancel()
	<-done
}

func TestLockedRunsUnderTheViewLock(t *testing.T) {
	f := newFixture(t, signedOut())
	ran := false
	f.v.Locked(func() { ran = !f.v.mu.TryLock() })
	if !ran {
		t.Fatal("Locked did not hold the lock")
	}
}

func TestCloseUnbinds(t *testing.T) {
	f := newFixture(t, signedOut())
	f.v.Close()
	f.vm.Error.Set("after close")
	if f.v.leaves.err.Text().Get() == "after close" {
		t.Fatal("a binding survived Close")
	}
}

func TestMinSizeFitsTheLayout(t *testing.T) {
	w, h := MinSize()
	if w <= 0 || h <= 0 {
		t.Fatal("no minimum size")
	}
	st := signedOut()
	st.DeviceVerificationURI, st.DeviceUserCode = "https://github.com/login/device", "ABCD-1234"
	f := newFixture(t, st)
	f.v.Resize(w, h, 1)
	// Every row of the sign-in prompt sits inside its slot, and the log has
	// room for some lines.
	slot := f.v.slot.Bounds()
	for _, wd := range []toolkit.Widget{f.v.leaves.hint, f.v.leaves.code, f.v.leaves.openPage, f.v.leaves.url} {
		b := wd.Bounds()
		if b.Y < slot.Y || b.Y+b.H > slot.Y+slot.H {
			t.Fatalf("%T at %v is outside its slot %v", wd, b, slot)
		}
	}
	if lb := f.v.leaves.log.Bounds(); lb.H < 3*toolkit.Scaled(rowH) {
		t.Fatalf("the log has %d pixels", lb.H)
	}
}

func TestKeyCodes(t *testing.T) {
	if keyCode("Up") != "ArrowUp" || keyCode("Enter") != "Enter" {
		t.Fatal("keyCode")
	}
}

func TestButtonsAreDisabledBeforeTheFirstStatus(t *testing.T) {
	d := viewmodel.NewDispatcher()
	vm := viewmodel.New(&service{}, viewmodel.Options{Dispatcher: d})
	defer vm.Stop()
	v := New(vm, d)
	defer v.Close()
	for _, b := range []*toolkit.Button{v.leaves.signIn, v.leaves.connect, v.leaves.disconnect, v.leaves.signOut1, v.leaves.save} {
		if !b.Disabled().Get() {
			t.Errorf("%q is enabled before anything is known", b.Label().Get())
		}
	}
	if v.leaves.settings.Disabled().Get() {
		t.Error("Settings must be reachable before the first status")
	}
}
