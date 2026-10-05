package viewmodel

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
)

// fakeService stands in for appcore.Core: the helper, the identity provider and
// the server, as the view model sees them.
type fakeService struct {
	mu    sync.Mutex
	st    appcore.Status
	cfg   appcore.Config
	calls []string

	login        func(ctx context.Context, f *fakeService) error
	tenants      []hproto.Tenant
	tenantsErr   error
	setTenantErr error
	connectErr   error
	downErr      error
	logoutErr    error
	updateErr    error
}

func (f *fakeService) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeService) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeService) edit(fn func(st *appcore.Status)) {
	f.mu.Lock()
	fn(&f.st)
	f.mu.Unlock()
}

func (f *fakeService) Status() appcore.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.st
	st.Tenants = append([]hproto.Tenant(nil), f.st.Tenants...)
	st.Log = append([]string(nil), f.st.Log...)
	return st
}

func (f *fakeService) Config() appcore.Config {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cfg
}

func (f *fakeService) UpdateConfig(c appcore.Config) error {
	f.record("update")
	if f.updateErr != nil {
		return f.updateErr
	}
	f.mu.Lock()
	f.cfg = c
	f.st.ConfigOK, f.st.ConfigError, f.st.ServerURL = true, "", c.ServerURL
	f.mu.Unlock()
	return nil
}

func (f *fakeService) Login(ctx context.Context) error {
	f.record("login")
	if f.login != nil {
		return f.login(ctx, f)
	}
	f.edit(func(st *appcore.Status) { st.LoggedIn, st.Email = true, "ada@example.org" })
	return nil
}

func (f *fakeService) Tenants(context.Context) ([]hproto.Tenant, error) {
	f.record("tenants")
	if f.tenantsErr != nil {
		return nil, f.tenantsErr
	}
	f.edit(func(st *appcore.Status) { st.Tenants = f.tenants })
	return f.tenants, nil
}

func (f *fakeService) SetTenant(id string) error {
	f.record("set-tenant:" + id)
	if f.setTenantErr != nil {
		return f.setTenantErr
	}
	f.edit(func(st *appcore.Status) { st.Tenant, st.TenantRequired = id, false })
	return nil
}

func (f *fakeService) Connect(context.Context) error {
	f.record("connect")
	if f.connectErr != nil {
		if errors.Is(f.connectErr, appcore.ErrTenantRequired) {
			f.edit(func(st *appcore.Status) { st.Tenants, st.TenantRequired = f.tenants, true })
		}
		return f.connectErr
	}
	f.edit(func(st *appcore.Status) {
		st.Connected, st.AssignedIP, st.Interface = true, "10.80.0.5/32", "utun"
	})
	return nil
}

func (f *fakeService) Disconnect(context.Context) error {
	f.record("disconnect")
	f.edit(func(st *appcore.Status) { st.Connected, st.AssignedIP, st.Interface = false, "", "" })
	return f.downErr
}

func (f *fakeService) Logout(context.Context) error {
	f.record("logout")
	f.edit(func(st *appcore.Status) {
		st.Connected, st.LoggedIn, st.Email, st.Tenant, st.Tenants = false, false, "", "", nil
	})
	return f.logoutErr
}

// configured is a service whose configuration is complete, with the helper
// running and nobody signed in.
func configured() *fakeService {
	return &fakeService{
		st:  appcore.Status{ConfigOK: true, Provider: "github", HelperInstalled: true, ServerURL: "https://vpn.example.org"},
		cfg: appcore.Config{ServerURL: "https://vpn.example.org", Provider: "github", GitHubClientID: "Iv1.abc", SocketPath: "/run/x.sock"},
	}
}

func newVM(t *testing.T, svc Service) (*ViewModel, *Dispatcher) {
	t.Helper()
	d := NewDispatcher()
	opened := []string{}
	vm := New(svc, Options{Dispatcher: d, Open: func(u string) error {
		opened = append(opened, u)
		return nil
	}, ConfigPath: "/home/ada/.config/Claimward/config.json"})
	t.Cleanup(func() { vm.Stop(); vm.Wait() })
	return vm, d
}

// settle waits for every action to finish and applies what it posted, as the
// UI goroutine would.
func settle(vm *ViewModel, d *Dispatcher) {
	for {
		vm.Wait()
		if d.Drain() == 0 {
			return
		}
	}
}

// poll is one status poll, applied.
func poll(vm *ViewModel, svc Service) { vm.Apply(svc.Status()) }

func TestStartsLoadingThenShowsTheStatus(t *testing.T) {
	svc := configured()
	vm, _ := newVM(t, svc)
	if vm.Screen.Get() != ScreenLoading {
		t.Fatalf("screen before any status = %d, want loading", vm.Screen.Get())
	}
	poll(vm, svc)
	if vm.Screen.Get() != ScreenMain || vm.State.Get() != "Signed out" || vm.Account.Get() != "Not signed in" {
		t.Fatalf("screen %d state %q account %q", vm.Screen.Get(), vm.State.Get(), vm.Account.Get())
	}
	if vm.Actions.Get() != ActionsSignedOut || vm.Slot.Get() != SlotNone {
		t.Fatalf("actions %d slot %d", vm.Actions.Get(), vm.Slot.Get())
	}
	if !vm.SignIn.CanExecute() || vm.Connect.CanExecute() || vm.SignOut.CanExecute() || vm.ChooseTenant.CanExecute() {
		t.Fatal("signed out: only Sign in should be offered")
	}
	if vm.Server.Get() != "https://vpn.example.org" || vm.TrayText.Get() != "Claimward: signed out" {
		t.Fatalf("server %q tray %q", vm.Server.Get(), vm.TrayText.Get())
	}
	if vm.Tenant.Get() != "" {
		t.Fatalf("tenant shown while signed out: %q", vm.Tenant.Get())
	}
}

func TestAnIncompleteConfigurationSaysWhatIsMissing(t *testing.T) {
	svc := &fakeService{st: appcore.Status{ConfigError: "missing config: server_url"}}
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	if vm.Screen.Get() != ScreenUnconfigured || vm.Problem.Get() != "missing config: server_url" {
		t.Fatalf("screen %d problem %q", vm.Screen.Get(), vm.Problem.Get())
	}
	if vm.State.Get() != "Not configured" || vm.TrayText.Get() != "Claimward: not configured" {
		t.Fatalf("state %q tray %q", vm.State.Get(), vm.TrayText.Get())
	}
	if vm.SignIn.CanExecute() {
		t.Fatal("Sign in offered without a configuration")
	}
}

func TestTheHelperBeingAbsentIsSaidAndBlocksConnecting(t *testing.T) {
	svc := configured()
	svc.st.LoggedIn, svc.st.HelperInstalled = true, false
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	if !strings.Contains(vm.Helper.Get(), "not running") || vm.TrayText.Get() != "Claimward: helper not running" {
		t.Fatalf("helper %q tray %q", vm.Helper.Get(), vm.TrayText.Get())
	}
	if vm.Connect.CanExecute() || vm.ChooseTenant.CanExecute() {
		t.Fatal("Connect offered with no helper to connect through")
	}
	svc.st.HelperInstalled = true
	poll(vm, svc)
	if vm.Helper.Get() != "Privileged helper: running" || !vm.Connect.CanExecute() {
		t.Fatalf("helper %q connect %v", vm.Helper.Get(), vm.Connect.CanExecute())
	}
}

func TestSignInShowsTheDeviceFlowPromptThenSignsIn(t *testing.T) {
	svc := configured()
	release := make(chan struct{})
	prompted := make(chan struct{})
	svc.login = func(ctx context.Context, f *fakeService) error {
		f.edit(func(st *appcore.Status) {
			st.DeviceVerificationURI, st.DeviceUserCode = "https://github.com/login/device", "ABCD-1234"
		})
		close(prompted)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		f.edit(func(st *appcore.Status) {
			st.DeviceVerificationURI, st.DeviceUserCode = "", ""
			st.LoggedIn, st.Email = true, "ada@example.org"
		})
		return nil
	}
	vm, d := newVM(t, svc)
	opened := []string{}
	vm.open = func(u string) error { opened = append(opened, u); return nil }
	poll(vm, svc)

	vm.SignIn.Execute()
	if vm.Busy.Get() != "Signing in…" || vm.Slot.Get() != SlotSignIn || !vm.CancelSignIn.CanExecute() {
		t.Fatalf("busy %q slot %d", vm.Busy.Get(), vm.Slot.Get())
	}
	if vm.SignInHint.Get() == "" || vm.OpenSignInPage.CanExecute() {
		t.Fatal("before the prompt: a hint and no page to open")
	}
	if vm.SignIn.CanExecute() || vm.OpenSettings.CanExecute() {
		t.Fatal("a second action offered while signing in")
	}
	<-prompted
	poll(vm, svc) // the poller's next look, while the person is in the browser
	if vm.SignInURL.Get() != "https://github.com/login/device" || vm.SignInCode.Get() != "ABCD-1234" {
		t.Fatalf("prompt %q %q", vm.SignInURL.Get(), vm.SignInCode.Get())
	}
	if !strings.Contains(vm.SignInHint.Get(), "enter this code") || vm.Slot.Get() != SlotSignIn {
		t.Fatalf("hint %q slot %d", vm.SignInHint.Get(), vm.Slot.Get())
	}
	vm.OpenSignInPage.Execute()
	if len(opened) != 1 || opened[0] != "https://github.com/login/device" {
		t.Fatalf("opened %v", opened)
	}

	close(release)
	settle(vm, d)
	if vm.Busy.Get() != "" || vm.Error.Get() != "" {
		t.Fatalf("busy %q error %q", vm.Busy.Get(), vm.Error.Get())
	}
	if vm.Account.Get() != "Signed in as ada@example.org" || vm.State.Get() != "Disconnected" {
		t.Fatalf("account %q state %q", vm.Account.Get(), vm.State.Get())
	}
	if vm.Slot.Get() != SlotChooseTenant || vm.Actions.Get() != ActionsDisconnected || vm.SignInURL.Get() != "" {
		t.Fatalf("slot %d actions %d url %q", vm.Slot.Get(), vm.Actions.Get(), vm.SignInURL.Get())
	}
	if vm.Tenant.Get() != "Tenant: the server's choice" {
		t.Fatalf("tenant %q", vm.Tenant.Get())
	}
}

func TestABrowserSignInWithNoCodeSaysWhereToComplete(t *testing.T) {
	svc := configured()
	svc.st.DeviceVerificationURI = "https://issuer.example.org/auth?x=1"
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	if vm.SignInHint.Get() != "Complete the sign-in in your browser." || vm.Slot.Get() != SlotSignIn {
		t.Fatalf("hint %q slot %d", vm.SignInHint.Get(), vm.Slot.Get())
	}
}

func TestOpeningTheSignInPageCanFail(t *testing.T) {
	svc := configured()
	svc.st.DeviceVerificationURI = "https://github.com/login/device"
	vm, _ := newVM(t, svc)
	vm.open = func(string) error { return errors.New("xdg-open: not found") }
	poll(vm, svc)
	vm.OpenSignInPage.Execute()
	if vm.Error.Get() != "xdg-open: not found" {
		t.Fatalf("error %q", vm.Error.Get())
	}
}

func TestWithoutABrowserOpenerOpeningFails(t *testing.T) {
	svc := configured()
	svc.st.DeviceVerificationURI = "https://github.com/login/device"
	vm := New(svc, Options{Dispatcher: NewDispatcher()})
	defer vm.Stop()
	poll(vm, svc)
	vm.OpenSignInPage.Execute()
	if vm.Error.Get() == "" {
		t.Fatal("no error without an opener")
	}
}

func TestSignInCanBeCancelled(t *testing.T) {
	svc := configured()
	svc.login = func(ctx context.Context, f *fakeService) error {
		<-ctx.Done()
		return ctx.Err()
	}
	vm, d := newVM(t, svc)
	poll(vm, svc)
	vm.SignIn.Execute()
	vm.CancelSignIn.Execute()
	settle(vm, d)
	if vm.Error.Get() != "Sign-in cancelled." || vm.Slot.Get() != SlotNone || vm.CancelSignIn.CanExecute() {
		t.Fatalf("error %q slot %d", vm.Error.Get(), vm.Slot.Get())
	}
	vm.CancelSignIn.Execute() // nothing in progress: no effect
}

func TestCancelWithNothingInProgressDoesNothing(t *testing.T) {
	svc := configured()
	vm, _ := newVM(t, svc)
	vm.signingIn.Set(true) // as if a sign-in were starting
	vm.CancelSignIn.Execute()
	if vm.Error.Get() != "" {
		t.Fatalf("error %q", vm.Error.Get())
	}
}

func TestAFailedSignInSaysWhy(t *testing.T) {
	svc := configured()
	svc.login = func(context.Context, *fakeService) error { return errors.New("access_denied") }
	vm, d := newVM(t, svc)
	poll(vm, svc)
	vm.SignIn.Execute()
	settle(vm, d)
	if vm.Error.Get() != "Sign-in failed: access_denied" || vm.LoggedIn.Get() {
		t.Fatalf("error %q", vm.Error.Get())
	}
}

func signedIn() *fakeService {
	svc := configured()
	svc.st.LoggedIn, svc.st.Email = true, "ada@example.org"
	svc.tenants = []hproto.Tenant{{ID: "acme", Name: "Acme"}, {ID: "lab", Name: "lab"}}
	return svc
}

func TestConnectAndDisconnect(t *testing.T) {
	svc := signedIn()
	vm, d := newVM(t, svc)
	poll(vm, svc)
	vm.Connect.Execute()
	if vm.Busy.Get() != "Connecting…" || !vm.TenantLocked.Get() {
		t.Fatalf("busy %q locked %v", vm.Busy.Get(), vm.TenantLocked.Get())
	}
	settle(vm, d)
	if vm.State.Get() != "Connected" || vm.Address.Get() != "10.80.0.5/32 on utun" || !vm.Connected.Get() {
		t.Fatalf("state %q address %q", vm.State.Get(), vm.Address.Get())
	}
	if vm.Actions.Get() != ActionsConnected || vm.TrayText.Get() != "Claimward: connected (10.80.0.5/32)" {
		t.Fatalf("actions %d tray %q", vm.Actions.Get(), vm.TrayText.Get())
	}
	if !vm.TenantLocked.Get() || vm.Connect.CanExecute() || !vm.Disconnect.CanExecute() {
		t.Fatal("connected: the tenant is locked and only Disconnect is offered")
	}
	vm.Disconnect.Execute()
	settle(vm, d)
	if vm.State.Get() != "Disconnected" || vm.Address.Get() != "Not connected" || vm.TenantLocked.Get() {
		t.Fatalf("state %q address %q", vm.State.Get(), vm.Address.Get())
	}
	if got := strings.Join(svc.called(), ","); got != "connect,disconnect" {
		t.Fatalf("calls %s", got)
	}
}

func TestAConnectionWithNoAddressOrInterfaceStillSaysConnected(t *testing.T) {
	svc := signedIn()
	svc.st.Connected = true
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	if vm.Address.Get() != "address unknown on —" || vm.TrayText.Get() != "Claimward: connected" {
		t.Fatalf("address %q tray %q", vm.Address.Get(), vm.TrayText.Get())
	}
}

func TestSignedInWithNoAddressInTheToken(t *testing.T) {
	svc := signedIn()
	svc.st.Email = ""
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	if vm.Account.Get() != "Signed in" {
		t.Fatalf("account %q", vm.Account.Get())
	}
}

func TestATenantRequiredRefusalOffersTheTenantsThenConnects(t *testing.T) {
	svc := signedIn()
	svc.connectErr = appcore.ErrTenantRequired
	vm, d := newVM(t, svc)
	poll(vm, svc)
	if vm.Slot.Get() != SlotChooseTenant {
		t.Fatalf("slot %d before connecting", vm.Slot.Get())
	}
	vm.Connect.Execute()
	settle(vm, d)
	if !strings.Contains(vm.Error.Get(), "several tenants") {
		t.Fatalf("error %q", vm.Error.Get())
	}
	if vm.Slot.Get() != SlotTenants || vm.TenantOptions.Len() != 3 {
		t.Fatalf("slot %d options %v", vm.Slot.Get(), vm.TenantOptions.Slice())
	}
	if o := vm.TenantOptions.At(0); o.ID != "" || o.Label != "Choose a tenant…" {
		t.Fatalf("first option %+v", o)
	}
	if o := vm.TenantOptions.At(1); o.ID != "acme" || o.Label != "Acme (acme)" {
		t.Fatalf("second option %+v", o)
	}
	if o := vm.TenantOptions.At(2); o.Label != "lab" {
		t.Fatalf("a tenant named after its id is shown once: %+v", o)
	}

	vm.TenantIndex.Set(1) // the person picks Acme
	if vm.Error.Get() != "" || vm.Tenant.Get() != "Tenant: Acme (acme)" {
		t.Fatalf("error %q tenant %q", vm.Error.Get(), vm.Tenant.Get())
	}
	svc.connectErr = nil
	vm.Connect.Execute()
	settle(vm, d)
	if vm.State.Get() != "Connected" {
		t.Fatalf("state %q", vm.State.Get())
	}
	if got := strings.Join(svc.called(), ","); got != "connect,set-tenant:acme,connect" {
		t.Fatalf("calls %s", got)
	}
}

func TestPollsDoNotTakeTheShownTenantForAChoice(t *testing.T) {
	svc := signedIn()
	svc.st.Tenants, svc.st.Tenant = svc.tenants, "lab"
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	poll(vm, svc)
	if vm.TenantIndex.Get() != 2 || vm.Tenant.Get() != "Tenant: lab" {
		t.Fatalf("index %d tenant %q", vm.TenantIndex.Get(), vm.Tenant.Get())
	}
	for _, c := range svc.called() {
		if strings.HasPrefix(c, "set-tenant") {
			t.Fatalf("a poll chose a tenant: %v", svc.called())
		}
	}
	vm.TenantIndex.Set(2) // the same one again: nothing to write
	vm.TenantIndex.Set(7) // out of range: ignored
	if len(svc.called()) != 0 {
		t.Fatalf("calls %v", svc.called())
	}
}

func TestATenantUnknownToTheListIsShownByItsID(t *testing.T) {
	svc := signedIn()
	svc.st.Tenants, svc.st.Tenant = svc.tenants, "lab"
	vm, _ := newVM(t, svc)
	poll(vm, svc) // the drop-down on "lab"
	// The helper reports a tenant the list does not offer (connected to it
	// before the list was fetched, say).
	svc.st.Tenant = "gone"
	poll(vm, svc)
	if vm.Tenant.Get() != "Tenant: gone" || vm.TenantIndex.Get() != 0 {
		t.Fatalf("tenant %q index %d", vm.Tenant.Get(), vm.TenantIndex.Get())
	}
	// The drop-down shows no row for it, and that is not the person choosing
	// "no tenant": the session keeps the one it has.
	if len(svc.called()) != 0 {
		t.Fatalf("a poll changed the session's tenant: %v", svc.called())
	}
}

func TestAFailedTenantChoiceSaysWhyAndPutsTheDropDownBack(t *testing.T) {
	svc := signedIn()
	svc.st.Tenants = svc.tenants
	svc.setTenantErr = errors.New(`"acme" is not a tenant you were offered`)
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	vm.TenantIndex.Set(1)
	if !strings.HasPrefix(vm.Error.Get(), "Choosing the tenant failed") || vm.TenantIndex.Get() != 0 {
		t.Fatalf("error %q index %d", vm.Error.Get(), vm.TenantIndex.Get())
	}
}

func TestChooseTenantAsksTheServer(t *testing.T) {
	svc := signedIn()
	vm, d := newVM(t, svc)
	poll(vm, svc)
	vm.ChooseTenant.Execute()
	settle(vm, d)
	if vm.Slot.Get() != SlotTenants || vm.TenantOptions.Len() != 3 || vm.TenantOptions.At(0).Label != "No tenant chosen" {
		t.Fatalf("slot %d options %v", vm.Slot.Get(), vm.TenantOptions.Slice())
	}
	svc.tenantsErr = errors.New("server 401")
	vm.ChooseTenant.Execute()
	settle(vm, d)
	if vm.Error.Get() != "Listing tenants failed: server 401" {
		t.Fatalf("error %q", vm.Error.Get())
	}
}

func TestFailuresOfEachActionAreShown(t *testing.T) {
	svc := signedIn()
	svc.connectErr = errors.New("helper: enroll: server 403")
	vm, d := newVM(t, svc)
	poll(vm, svc)
	vm.Connect.Execute()
	settle(vm, d)
	if vm.Error.Get() != "Connection failed: helper: enroll: server 403" {
		t.Fatalf("connect error %q", vm.Error.Get())
	}

	svc.connectErr = nil
	vm.Connect.Execute()
	settle(vm, d)
	svc.downErr = errors.New("helper not reachable")
	vm.Disconnect.Execute()
	settle(vm, d)
	if vm.Error.Get() != "Disconnecting failed: helper not reachable" {
		t.Fatalf("disconnect error %q", vm.Error.Get())
	}

	svc.logoutErr = errors.New("permission denied")
	vm.SignOut.Execute()
	settle(vm, d)
	if vm.Error.Get() != "Signing out failed: permission denied" {
		t.Fatalf("sign-out error %q", vm.Error.Get())
	}
}

func TestSignOut(t *testing.T) {
	svc := signedIn()
	vm, d := newVM(t, svc)
	poll(vm, svc)
	vm.SignOut.Execute()
	settle(vm, d)
	if vm.State.Get() != "Signed out" || vm.Actions.Get() != ActionsSignedOut || vm.Error.Get() != "" {
		t.Fatalf("state %q actions %d error %q", vm.State.Get(), vm.Actions.Get(), vm.Error.Get())
	}
}

func TestTheLogFollowsTheRing(t *testing.T) {
	svc := configured()
	svc.st.Log = []string{"a", "b"}
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	if got := strings.Join(vm.Log.Slice(), ","); got != "a,b" || vm.LogScroll.Get() != 2 {
		t.Fatalf("log %s scroll %d", got, vm.LogScroll.Get())
	}
	appended := 0
	unsub := vm.Log.SubscribeChanged(func() { appended++ })
	poll(vm, svc) // unchanged: nothing happens
	svc.st.Log = []string{"a", "b", "c"}
	poll(vm, svc)
	unsub()
	if got := strings.Join(vm.Log.Slice(), ","); got != "a,b,c" || appended != 1 || vm.LogScroll.Get() != 3 {
		t.Fatalf("log %s changes %d scroll %d", got, appended, vm.LogScroll.Get())
	}
	svc.st.Log = []string{"c", "d"} // the ring dropped its oldest lines
	poll(vm, svc)
	if got := strings.Join(vm.Log.Slice(), ","); got != "c,d" {
		t.Fatalf("log %s", got)
	}
	svc.st.Log = []string{"e", "f", "g"} // longer, and not a continuation
	poll(vm, svc)
	if got := strings.Join(vm.Log.Slice(), ","); got != "e,f,g" {
		t.Fatalf("log %s", got)
	}
	svc.st.Log = []string{"x"} // shorter, and not a continuation
	poll(vm, svc)
	if got := strings.Join(vm.Log.Slice(), ","); got != "x" {
		t.Fatalf("log %s", got)
	}
}

func TestSettingsOpenOnTheCurrentConfiguration(t *testing.T) {
	svc := configured()
	svc.cfg.Provider = "go-authn"
	svc.cfg.OIDCIssuer, svc.cfg.OIDCClientID = "https://login.example.org", "claimward"
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	vm.OpenSettings.Execute()
	if vm.Screen.Get() != ScreenSettings || vm.OpenSettings.CanExecute() || !vm.SaveSettings.CanExecute() {
		t.Fatalf("screen %d", vm.Screen.Get())
	}
	if vm.ServerURL.Get() != "https://vpn.example.org" || vm.ProviderIndex.Get() != 2 ||
		vm.OIDCIssuer.Get() != "https://login.example.org" || vm.OIDCClientID.Get() != "claimward" || vm.GitHubClientID.Get() != "Iv1.abc" {
		t.Fatal("the form does not show the configuration")
	}
	poll(vm, svc) // a poll does not take the person out of the form
	if vm.Screen.Get() != ScreenSettings {
		t.Fatal("a poll left the settings")
	}
	vm.CloseSettings.Execute()
	if vm.Screen.Get() != ScreenMain {
		t.Fatalf("screen %d after closing", vm.Screen.Get())
	}
}

func TestSettingsSaveACompleteConfigurationKeepingTheSocket(t *testing.T) {
	svc := &fakeService{st: appcore.Status{ConfigError: "missing config: server_url"},
		cfg: appcore.Config{Provider: "", SocketPath: "/run/custom.sock"}}
	vm, d := newVM(t, svc)
	poll(vm, svc)
	vm.OpenSettings.Execute()
	if vm.ProviderIndex.Get() != 0 {
		t.Fatalf("an unset provider is shown as %d", vm.ProviderIndex.Get())
	}
	vm.ServerURL.Set("  https://vpn.example.org  ")
	vm.ProviderIndex.Set(1)
	vm.OIDCIssuer.Set("https://issuer.example.org")
	vm.OIDCClientID.Set("app")
	vm.SaveSettings.Execute()
	settle(vm, d)
	if vm.SettingsError.Get() != "" || vm.Screen.Get() != ScreenMain {
		t.Fatalf("error %q screen %d", vm.SettingsError.Get(), vm.Screen.Get())
	}
	want := appcore.Config{ServerURL: "https://vpn.example.org", Provider: "oidc",
		OIDCIssuer: "https://issuer.example.org", OIDCClientID: "app", SocketPath: "/run/custom.sock"}
	if svc.cfg != want {
		t.Fatalf("saved %+v\nwant %+v", svc.cfg, want)
	}
}

func TestSettingsRefuseAnIncompleteConfiguration(t *testing.T) {
	svc := configured()
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	vm.OpenSettings.Execute()
	vm.GitHubClientID.Set("")
	vm.ProviderIndex.Set(9) // out of range reads as the first provider
	vm.SaveSettings.Execute()
	if vm.SettingsError.Get() != "missing config: github_client_id" || vm.Screen.Get() != ScreenSettings {
		t.Fatalf("error %q", vm.SettingsError.Get())
	}
	if len(svc.called()) != 0 {
		t.Fatalf("saved anyway: %v", svc.called())
	}
	vm.CloseSettings.Execute()
	if vm.SettingsError.Get() != "" {
		t.Fatal("the error outlived the form")
	}
}

func TestSettingsThatCannotBeSavedStayOpen(t *testing.T) {
	svc := configured()
	svc.updateErr = errors.New("read-only file system")
	vm, d := newVM(t, svc)
	poll(vm, svc)
	vm.OpenSettings.Execute()
	vm.SaveSettings.Execute()
	settle(vm, d)
	if vm.SettingsError.Get() != "Saving failed: read-only file system" || vm.Screen.Get() != ScreenSettings {
		t.Fatalf("error %q screen %d", vm.SettingsError.Get(), vm.Screen.Get())
	}
}

func TestClosingSettingsOnAnIncompleteConfigurationReturnsToItsScreen(t *testing.T) {
	svc := &fakeService{st: appcore.Status{ConfigError: "missing config: server_url"}}
	vm, _ := newVM(t, svc)
	poll(vm, svc)
	vm.OpenSettings.Execute()
	vm.CloseSettings.Execute()
	if vm.Screen.Get() != ScreenUnconfigured {
		t.Fatalf("screen %d", vm.Screen.Get())
	}
}

func TestThePollerPostsStatusesUntilStopped(t *testing.T) {
	svc := signedIn()
	d := NewDispatcher()
	vm := New(svc, Options{Dispatcher: d, Interval: time.Millisecond, FastInterval: time.Millisecond})
	vm.fast.Store(true) // and the fast cadence is taken while an action runs
	vm.Start()
	deadline := time.After(5 * time.Second)
	for vm.Screen.Get() == ScreenLoading {
		select {
		case <-d.Wake():
			d.Drain()
		case <-deadline:
			t.Fatal("no status arrived")
		}
	}
	vm.fast.Store(false)
	select {
	case <-d.Wake(): // a later poll at the normal cadence
	case <-deadline:
		t.Fatal("polling stopped")
	}
	vm.Stop()
	vm.Wait()
	d.Drain()
	if vm.State.Get() != "Disconnected" {
		t.Fatalf("state %q", vm.State.Get())
	}
}

func TestPostRunsOnTheNextDrain(t *testing.T) {
	svc := configured()
	vm, d := newVM(t, svc)
	ran := false
	vm.Post(func() { ran = true })
	if ran || !d.Pending() {
		t.Fatal("Post ran its function itself")
	}
	if d.Drain() != 1 || !ran || d.Pending() {
		t.Fatal("Drain did not run the posted function")
	}
}

func TestDrainRunsWhatIsPostedWhileDraining(t *testing.T) {
	d := NewDispatcher()
	order := ""
	d.Post(func() { order += "a"; d.Post(func() { order += "c" }) })
	d.Post(func() { order += "b" })
	if n := d.Drain(); n != 3 || order != "abc" {
		t.Fatalf("ran %d in order %q", n, order)
	}
}

func TestHelpers(t *testing.T) {
	if providerIndex("oidc") != 1 || providerIndex("nope") != 0 {
		t.Fatal("providerIndex")
	}
	if clamp(-1, 3) != 0 || clamp(2, 3) != 2 {
		t.Fatal("clamp")
	}
	if orDash("") != "—" || orDash("x") != "x" {
		t.Fatal("orDash")
	}
	if !sameOptions(nil, nil) || sameOptions([]TenantOption{{ID: "a"}}, []TenantOption{{ID: "b"}}) {
		t.Fatal("sameOptions")
	}
	if len(Providers) != len(ProviderLabels) {
		t.Fatal("every provider needs a label")
	}
}

func TestOneActionAtATime(t *testing.T) {
	svc := signedIn()
	release := make(chan struct{})
	vm, d := newVM(t, svc)
	poll(vm, svc)
	// Hold an action in progress.
	block := func(ctx context.Context) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil
	}
	vm.run("Connecting…", block, func(error) {})
	for name, c := range map[string]interface{ CanExecute() bool }{
		"SignIn": vm.SignIn, "ChooseTenant": vm.ChooseTenant, "Connect": vm.Connect,
		"Disconnect": vm.Disconnect, "SignOut": vm.SignOut, "OpenSettings": vm.OpenSettings,
	} {
		if c.CanExecute() {
			t.Errorf("%s offered while another action runs", name)
		}
	}
	close(release)
	settle(vm, d)
	if !vm.Connect.CanExecute() {
		t.Fatal("Connect not offered again once the action ended")
	}
}
