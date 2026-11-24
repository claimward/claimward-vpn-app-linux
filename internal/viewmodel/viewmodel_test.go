package viewmodel

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
)

// The logic is appvm's and is tested there. These tests hold the adapter: a
// State reaches the observables through the dispatcher, and a command or a
// change of the bound observables reaches the appvm.Model.

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
	return slices.Clone(s.calls)
}

func (s *service) Status() appcore.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}
func (s *service) Config() appcore.Config { return s.cfg }
func (s *service) UpdateConfig(c appcore.Config) error {
	s.call("update:" + c.ServerURL + ":" + c.Provider)
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

func newVM(t *testing.T, st appcore.Status) (*ViewModel, *service, *Dispatcher) {
	t.Helper()
	svc := &service{st: st, cfg: appcore.Config{ServerURL: "https://vpn.example.org", Provider: "oidc", OIDCIssuer: "https://idp", OIDCClientID: "cid"}}
	d := NewDispatcher()
	vm := New(svc, Options{Dispatcher: d, ConfigPath: "/c.json"})
	t.Cleanup(func() { vm.Stop(); vm.Wait() })
	vm.Apply(st)
	d.Drain()
	return vm, svc, d
}

func settle(vm *ViewModel, d *Dispatcher) {
	for {
		vm.Wait()
		if d.Drain() == 0 {
			return
		}
	}
}

func signedIn() appcore.Status {
	return appcore.Status{ConfigOK: true, HelperInstalled: true, LoggedIn: true, Email: "ada@example.org", ServerURL: "https://vpn.example.org"}
}

func TestTheInitialStateIsShownBeforeAnyStatus(t *testing.T) {
	d := NewDispatcher()
	vm := New(&service{}, Options{Dispatcher: d, ConfigPath: "/c.json"})
	if vm.Screen.Get() != ScreenLoading || vm.ConfigPath.Get() != "/c.json" {
		t.Fatalf("screen %d, path %q", vm.Screen.Get(), vm.ConfigPath.Get())
	}
	if len(ProviderLabels) != 3 || vm.Model() == nil {
		t.Fatal("provider labels or model missing")
	}
}

func TestAStatusReachesTheObservablesOnlyAtDrain(t *testing.T) {
	d := NewDispatcher()
	vm := New(&service{}, Options{Dispatcher: d})
	vm.Apply(signedIn())
	if vm.LoggedIn.Get() {
		t.Fatal("the observable changed off the UI goroutine")
	}
	vm.Post(func() {})
	d.Drain()
	if !vm.LoggedIn.Get() || vm.Account.Get() != "Signed in as ada@example.org" || vm.Screen.Get() != ScreenMain {
		t.Fatalf("logged in %v, account %q, screen %d", vm.LoggedIn.Get(), vm.Account.Get(), vm.Screen.Get())
	}
	if vm.Actions.Get() != ActionsDisconnected || vm.Slot.Get() != SlotChooseTenant {
		t.Fatalf("actions %d slot %d", vm.Actions.Get(), vm.Slot.Get())
	}
	if !vm.Connect.CanExecute() || vm.Disconnect.CanExecute() || !vm.SignOut.CanExecute() || vm.SignIn.CanExecute() {
		t.Fatal("CanExecute does not follow State.Can")
	}
}

func TestCommandsReachTheModel(t *testing.T) {
	vm, svc, d := newVM(t, signedIn())
	vm.Connect.Execute()
	settle(vm, d)
	vm.ChooseTenant.Execute()
	settle(vm, d)
	vm.SignOut.Execute()
	settle(vm, d)
	if got := svc.called(); !slices.Equal(got, []string{"connect", "tenants", "logout"}) {
		t.Fatalf("calls %v", got)
	}
	// Signed out the status is still the fake's: SignIn needs a signed-out one.
	svc.mu.Lock()
	svc.st = appcore.Status{ConfigOK: true, HelperInstalled: true, ServerURL: "https://vpn.example.org"}
	svc.mu.Unlock()
	vm.Model().Refresh()
	d.Drain()
	vm.SignIn.Execute()
	settle(vm, d)
	if got := svc.called(); got[len(got)-1] != "login" {
		t.Fatalf("calls %v", got)
	}
}

func TestChoosingATenantInTheDropDownIsTheModelsChoiceAndAStatusIsNot(t *testing.T) {
	st := signedIn()
	st.TenantRequired = true
	st.Tenants = []hproto.Tenant{{ID: "acme", Name: "Acme"}, {ID: "lab", Name: "Lab"}}
	vm, svc, d := newVM(t, st)
	if vm.TenantOptions.Len() != 3 || vm.TenantOptions.At(0).Label != "Choose a tenant…" {
		t.Fatalf("options %v", vm.TenantOptions.Slice())
	}
	if len(svc.called()) != 0 {
		t.Fatalf("applying a status chose a tenant: %v", svc.called())
	}
	vm.TenantIndex.Set(2)
	d.Drain()
	if got := svc.called(); !slices.Equal(got, []string{"set-tenant:lab"}) {
		t.Fatalf("calls %v", got)
	}
}

func TestTheSettingsFormRoundTrips(t *testing.T) {
	vm, svc, d := newVM(t, signedIn())
	vm.OpenSettings.Execute()
	d.Drain()
	if vm.Screen.Get() != ScreenSettings || vm.ServerURL.Get() != "https://vpn.example.org" ||
		vm.ProviderIndex.Get() != 1 || vm.OIDCIssuer.Get() != "https://idp" || vm.OIDCClientID.Get() != "cid" {
		t.Fatalf("form: screen %d server %q provider %d", vm.Screen.Get(), vm.ServerURL.Get(), vm.ProviderIndex.Get())
	}
	// A second open is refused by the model; it must leave the form alone.
	vm.ServerURL.Set("https://edited.example.org")
	vm.openSettings()
	if vm.ServerURL.Get() != "https://edited.example.org" {
		t.Fatal("a refused open overwrote the form")
	}
	vm.ProviderIndex.Set(-1) // out of range: the first provider
	vm.SaveSettings.Execute()
	settle(vm, d)
	if got := svc.called(); !slices.Equal(got, []string{"update:https://edited.example.org:github"}) {
		t.Fatalf("calls %v", got)
	}
	vm.OpenSettings.Execute()
	d.Drain()
	vm.CloseSettings.Execute()
	d.Drain()
	if vm.Screen.Get() != ScreenMain {
		t.Fatalf("screen %d", vm.Screen.Get())
	}
}

func TestTheLogAppendsAndStartsOverWhenTheRingDropsLines(t *testing.T) {
	st := signedIn()
	st.Log = []string{"a", "b"}
	vm, _, d := newVM(t, st)
	if !slices.Equal(vm.Log.Slice(), []string{"a", "b"}) || vm.LogScroll.Get() != 2 {
		t.Fatalf("log %v scroll %d", vm.Log.Slice(), vm.LogScroll.Get())
	}
	st.Log = []string{"a", "b", "c"}
	vm.Apply(st)
	d.Drain()
	if !slices.Equal(vm.Log.Slice(), []string{"a", "b", "c"}) || vm.LogScroll.Get() != 3 {
		t.Fatalf("log %v scroll %d", vm.Log.Slice(), vm.LogScroll.Get())
	}
	st.Log = []string{"b", "c", "d"} // "a" fell off the ring
	vm.Apply(st)
	d.Drain()
	if !slices.Equal(vm.Log.Slice(), []string{"b", "c", "d"}) {
		t.Fatalf("log %v", vm.Log.Slice())
	}
	st.Log = nil // an empty log is shown as such
	vm.Apply(st)
	d.Drain()
	if vm.Log.Len() != 0 {
		t.Fatalf("log %v", vm.Log.Slice())
	}
}

func TestStartPollsAndTheDispatcherSaysWhenThereIsWork(t *testing.T) {
	d := NewDispatcher()
	svc := &service{st: signedIn()}
	vm := New(svc, Options{Dispatcher: d})
	if d.Pending() {
		t.Fatal("work pending before anything was posted")
	}
	vm.Start()
	<-d.Wake()
	vm.Stop()
	vm.Wait()
	if !d.Pending() {
		t.Fatal("the first poll was not posted")
	}
	d.Drain()
	if !vm.LoggedIn.Get() {
		t.Fatal("the polled status was not shown")
	}
}

func TestSignInCommandsAreOffWhenNoSignInRuns(t *testing.T) {
	vm, _, _ := newVM(t, signedIn())
	if vm.CancelSignIn.CanExecute() || vm.OpenSignInPage.CanExecute() {
		t.Fatal("cancel or open-page on offer with no sign-in running")
	}
}

func TestARenamedTenantChangesTheOptionsOfTheSameLength(t *testing.T) {
	st := signedIn()
	st.Tenants = []hproto.Tenant{{ID: "acme", Name: "Acme"}}
	vm, _, d := newVM(t, st)
	st.Tenants = []hproto.Tenant{{ID: "acme", Name: "Acme Corp"}}
	vm.Apply(st)
	d.Drain()
	if got := vm.TenantOptions.At(1).Label; got != "Acme Corp (acme)" {
		t.Fatalf("label %q", got)
	}
}
