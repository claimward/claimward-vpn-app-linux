// Package viewmodel is the Claimward Linux app's state as go-widgets/mvvm
// observables and commands. It imports no widget package: the window
// (internal/view) and the tray (internal/trayview) bind to what is here, and
// tests drive it with no display at all.
//
// The view model itself is claimward-vpn-client's pkg/appvm, shared with the
// other apps. This package is the adapter: it copies every appvm.State into
// observables on the UI goroutine (through the Dispatcher) and turns the
// commands into calls on the appvm.Model. No decision is taken here.
//
// Threading: observables belong to the UI goroutine (see Dispatcher). Every
// exported method and command must be called there.
package viewmodel

import (
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/appvm"
	"github.com/go-widgets/mvvm"
)

// Service is what the view model needs from appcore.Core, which satisfies it.
type Service = appvm.Service

// Screens of the window (the value of ViewModel.Screen).
const (
	ScreenLoading      = int(appvm.ScreenLoading)
	ScreenUnconfigured = int(appvm.ScreenUnconfigured)
	ScreenMain         = int(appvm.ScreenMain)
	ScreenSettings     = int(appvm.ScreenSettings)
)

// What the slot under the status shows (the value of ViewModel.Slot).
const (
	SlotNone         = int(appvm.SlotNone)
	SlotSignIn       = int(appvm.SlotSignIn)
	SlotTenants      = int(appvm.SlotTenants)
	SlotChooseTenant = int(appvm.SlotChooseTenant)
)

// Which actions are on offer (the value of ViewModel.Actions).
const (
	ActionsSignedOut    = int(appvm.ActionsSignedOut)
	ActionsDisconnected = int(appvm.ActionsDisconnected)
	ActionsConnected    = int(appvm.ActionsConnected)
)

// ProviderLabels are what the settings drop-down shows, in the order of
// appvm.Providers.
var ProviderLabels = func() []string {
	l := make([]string, len(appvm.Providers))
	for i, p := range appvm.Providers {
		l[i] = p.Label
	}
	return l
}()

// TenantOption is one row of the tenant drop-down.
type TenantOption = appvm.TenantOption

// Options configures a ViewModel.
type Options struct {
	// Dispatcher carries results back to the UI goroutine. Required.
	Dispatcher *Dispatcher
	// Open shows a URL in the person's browser (application.OpenURL).
	Open func(url string) error
	// Interval and FastInterval are the polling intervals (see appvm).
	Interval, FastInterval time.Duration
	// ConfigPath is shown in the settings form.
	ConfigPath string
}

// ViewModel is the app's state. Its exported fields are bound by the views;
// it never references them.
type ViewModel struct {
	m    *appvm.Model
	disp *Dispatcher

	syncing bool // an observable is being set from a State

	// What the window shows.
	Screen   *mvvm.Observable[int]
	State    *mvvm.Observable[string] // "Connected", "Disconnected", ...
	Account  *mvvm.Observable[string]
	Address  *mvvm.Observable[string]
	Tenant   *mvvm.Observable[string]
	Helper   *mvvm.Observable[string]
	Server   *mvvm.Observable[string]
	Problem  *mvvm.Observable[string]
	Busy     *mvvm.Observable[string]
	Error    *mvvm.Observable[string]
	Slot     *mvvm.Observable[int]
	Actions  *mvvm.Observable[int]
	TrayText *mvvm.Observable[string]

	// Connected and LoggedIn are also what the tray icon follows.
	Connected *mvvm.Observable[bool]
	LoggedIn  *mvvm.Observable[bool]

	// The device-flow prompt of a sign-in in progress.
	SignInURL  *mvvm.Observable[string]
	SignInCode *mvvm.Observable[string]
	SignInHint *mvvm.Observable[string]

	// The tenant choice.
	TenantOptions *mvvm.ObservableList[TenantOption]
	TenantIndex   *mvvm.Observable[int]
	TenantLocked  *mvvm.Observable[bool]

	// The connection log, oldest first, and the row it is scrolled to.
	Log       *mvvm.ObservableList[string]
	LogScroll *mvvm.Observable[int]

	// The settings form.
	ServerURL      *mvvm.Observable[string]
	ProviderIndex  *mvvm.Observable[int]
	GitHubClientID *mvvm.Observable[string]
	OIDCIssuer     *mvvm.Observable[string]
	OIDCClientID   *mvvm.Observable[string]
	SettingsError  *mvvm.Observable[string]
	ConfigPath     *mvvm.Observable[string]

	// Commands.
	SignIn         *mvvm.Command
	CancelSignIn   *mvvm.Command
	OpenSignInPage *mvvm.Command
	ChooseTenant   *mvvm.Command
	Connect        *mvvm.Command
	Disconnect     *mvvm.Command
	SignOut        *mvvm.Command
	OpenSettings   *mvvm.Command
	SaveSettings   *mvvm.Command
	CloseSettings  *mvvm.Command

	can *mvvm.Observable[appvm.Can]
}

// New builds the view model. Call Start to begin polling.
func New(svc Service, o Options) *ViewModel {
	m := appvm.New(svc, appvm.Options{
		Open: o.Open, Interval: o.Interval, FastInterval: o.FastInterval, ConfigPath: o.ConfigPath,
	})
	vm := &ViewModel{
		m: m, disp: o.Dispatcher,

		Screen:   mvvm.NewObservable(ScreenLoading),
		State:    mvvm.NewObservable(""),
		Account:  mvvm.NewObservable(""),
		Address:  mvvm.NewObservable(""),
		Tenant:   mvvm.NewObservable(""),
		Helper:   mvvm.NewObservable(""),
		Server:   mvvm.NewObservable(""),
		Problem:  mvvm.NewObservable(""),
		Busy:     mvvm.NewObservable(""),
		Error:    mvvm.NewObservable(""),
		Slot:     mvvm.NewObservable(SlotNone),
		Actions:  mvvm.NewObservable(ActionsSignedOut),
		TrayText: mvvm.NewObservable(""),

		Connected: mvvm.NewObservable(false),
		LoggedIn:  mvvm.NewObservable(false),

		SignInURL:  mvvm.NewObservable(""),
		SignInCode: mvvm.NewObservable(""),
		SignInHint: mvvm.NewObservable(""),

		TenantOptions: mvvm.NewObservableList[TenantOption](),
		TenantIndex:   mvvm.NewObservable(0),
		TenantLocked:  mvvm.NewObservable(false),

		Log:       mvvm.NewObservableList[string](),
		LogScroll: mvvm.NewObservable(0),

		ServerURL:      mvvm.NewObservable(""),
		ProviderIndex:  mvvm.NewObservable(0),
		GitHubClientID: mvvm.NewObservable(""),
		OIDCIssuer:     mvvm.NewObservable(""),
		OIDCClientID:   mvvm.NewObservable(""),
		SettingsError:  mvvm.NewObservable(""),
		ConfigPath:     mvvm.NewObservable(o.ConfigPath),

		can: mvvm.NewObservable(appvm.Can{}),
	}
	vm.commands()
	vm.TenantIndex.Subscribe(func(i int) {
		if !vm.syncing {
			m.SelectTenant(i)
		}
	})
	vm.apply(m.State())
	// Subscriber callbacks run on any goroutine and must not touch the
	// observables: the state is carried to the UI goroutine.
	m.Subscribe(func(st appvm.State) { vm.disp.Post(func() { vm.apply(st) }) })
	return vm
}

func (vm *ViewModel) commands() {
	cmd := func(run func(), ok func(appvm.Can) bool) *mvvm.Command {
		c := mvvm.NewCommand(run, func() bool { return ok(vm.can.Get()) })
		mvvm.BindCanExecute(c, vm.can)
		return c
	}
	vm.SignIn = cmd(vm.m.SignIn, func(c appvm.Can) bool { return c.SignIn })
	vm.CancelSignIn = cmd(vm.m.CancelSignIn, func(c appvm.Can) bool { return c.CancelSignIn })
	vm.OpenSignInPage = cmd(vm.m.OpenSignInPage, func(c appvm.Can) bool { return c.OpenSignInPage })
	vm.ChooseTenant = cmd(vm.m.ChooseTenant, func(c appvm.Can) bool { return c.ChooseTenant })
	vm.Connect = cmd(vm.m.Connect, func(c appvm.Can) bool { return c.Connect })
	vm.Disconnect = cmd(vm.m.Disconnect, func(c appvm.Can) bool { return c.Disconnect })
	vm.SignOut = cmd(vm.m.SignOut, func(c appvm.Can) bool { return c.SignOut })
	vm.OpenSettings = cmd(vm.openSettings, func(c appvm.Can) bool { return c.OpenSettings })
	vm.SaveSettings = cmd(vm.saveSettings, func(c appvm.Can) bool { return c.SaveSettings })
	vm.CloseSettings = cmd(vm.m.CloseSettings, func(c appvm.Can) bool { return c.CloseSettings })
}

func (vm *ViewModel) openSettings() {
	f, ok := vm.m.OpenSettings()
	if !ok {
		return
	}
	vm.ServerURL.Set(f.ServerURL)
	vm.ProviderIndex.Set(appvm.ProviderIndex(f.Provider))
	vm.GitHubClientID.Set(f.GitHubClientID)
	vm.OIDCIssuer.Set(f.OIDCIssuer)
	vm.OIDCClientID.Set(f.OIDCClientID)
}

func (vm *ViewModel) saveSettings() {
	i := vm.ProviderIndex.Get()
	if i < 0 || i >= len(appvm.Providers) {
		i = 0
	}
	vm.m.SaveSettings(appvm.Form{
		ServerURL: vm.ServerURL.Get(), Provider: appvm.Providers[i].ID,
		GitHubClientID: vm.GitHubClientID.Get(),
		OIDCIssuer:     vm.OIDCIssuer.Get(), OIDCClientID: vm.OIDCClientID.Get(),
	})
}

// Model is the shared view model, for what the app does with its events.
func (vm *ViewModel) Model() *appvm.Model { return vm.m }

// Post queues fn for the UI goroutine: how the tray, whose menu callbacks
// arrive on its own goroutine, executes a command.
func (vm *ViewModel) Post(fn func()) { vm.disp.Post(fn) }

// Start polls the status until Stop. The first poll is immediate.
func (vm *ViewModel) Start() { vm.m.Start() }

// Stop ends the polling and cancels the action in progress.
func (vm *ViewModel) Stop() { vm.m.Stop() }

// Wait blocks until the poller and every action have finished and published
// their results. Results still have to be drained on the UI goroutine.
func (vm *ViewModel) Wait() { vm.m.Wait() }

// Apply shows a status, as a poll does: the new State reaches the observables
// at the next Drain.
func (vm *ViewModel) Apply(st appcore.Status) { vm.m.Apply(st) }

// apply copies a State into the observables. It runs on the UI goroutine.
func (vm *ViewModel) apply(st appvm.State) {
	vm.syncing = true
	defer func() { vm.syncing = false }()

	vm.can.Set(st.Can)
	vm.Screen.Set(int(st.Screen))
	vm.State.Set(st.Headline)
	vm.Account.Set(st.Account)
	vm.Address.Set(st.Address)
	vm.Tenant.Set(st.Tenant)
	vm.Helper.Set(st.Helper)
	vm.Server.Set(st.Server)
	vm.Problem.Set(st.Problem)
	vm.Busy.Set(st.Busy)
	vm.Error.Set(st.Error)
	vm.Slot.Set(int(st.Slot))
	vm.Actions.Set(int(st.Actions))
	vm.TrayText.Set(st.TrayText)
	vm.Connected.Set(st.Connected)
	vm.LoggedIn.Set(st.LoggedIn)
	vm.SignInURL.Set(st.SignInURL)
	vm.SignInCode.Set(st.SignInCode)
	vm.SignInHint.Set(st.SignInHint)
	vm.SettingsError.Set(st.SettingsError)
	vm.ConfigPath.Set(st.ConfigPath)
	vm.TenantLocked.Set(st.TenantLocked)

	if !sameOptions(vm.TenantOptions.Slice(), st.TenantOptions) {
		vm.TenantOptions.Clear()
		vm.TenantOptions.Append(st.TenantOptions...)
	}
	vm.TenantIndex.Set(st.TenantIndex)

	lines, appended := appvm.LogDelta(vm.Log.Slice(), st.Log)
	if !appended {
		vm.Log.Clear()
	}
	if len(lines) > 0 || !appended {
		vm.Log.Append(lines...)
		// Follow the newest line; the list clamps a row past its end.
		vm.LogScroll.Set(vm.Log.Len())
	}
}

func sameOptions(a, b []TenantOption) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
