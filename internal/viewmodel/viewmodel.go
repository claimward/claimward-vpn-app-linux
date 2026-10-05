// Package viewmodel is the Claimward Linux app's whole state, as go-widgets/mvvm
// observables and commands. It imports no widget package: the window
// (internal/view) and the tray (internal/trayview) bind to what is here, and
// tests drive it with no display at all.
//
// The business logic is not here either. It is claimward-vpn-client's
// appcore.Core — shared with the macOS and Windows apps — reached through the
// Service interface, so a test can stand in for the helper, the identity
// provider and the server.
//
// Threading: observables belong to the UI goroutine (see Dispatcher). Every
// exported method and command must be called there; slow work runs on a
// goroutine of its own and Posts its result back.
package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
	"github.com/go-widgets/mvvm"
)

// Service is what the view model needs from appcore.Core, which satisfies it.
type Service interface {
	Status() appcore.Status
	Config() appcore.Config
	UpdateConfig(appcore.Config) error
	Login(ctx context.Context) error
	Tenants(ctx context.Context) ([]hproto.Tenant, error)
	SetTenant(id string) error
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Logout(ctx context.Context) error
}

var _ Service = (*appcore.Core)(nil)

// Screens of the window (the value of ViewModel.Screen).
const (
	ScreenLoading      = iota // before the first status arrives
	ScreenUnconfigured        // the app configuration is incomplete
	ScreenMain                // status, actions, log
	ScreenSettings            // the configuration form
)

// What the slot under the status shows (the value of ViewModel.Slot).
const (
	SlotNone         = iota // nothing: signed out
	SlotSignIn              // a sign-in is running: the device-flow prompt
	SlotTenants             // the tenants offered, to choose from
	SlotChooseTenant        // signed in, no tenant list yet: offer to fetch it
)

// Which actions are on offer (the value of ViewModel.Actions).
const (
	ActionsSignedOut    = iota // Sign in
	ActionsDisconnected        // Connect, Sign out
	ActionsConnected           // Disconnect, Sign out
)

// Providers lists the identity providers the settings form offers, in the
// order of its drop-down; ProviderLabels are what it shows for each.
var (
	Providers      = []string{"github", "oidc", "go-authn"}
	ProviderLabels = []string{"GitHub (device flow)", "OIDC (browser)", "go-authn (device flow)"}
)

// TenantOption is one row of the tenant drop-down. The first row always has
// an empty ID: no tenant chosen, which lets the server use the person's only
// one.
type TenantOption struct {
	ID    string
	Label string
}

// Options configures a ViewModel.
type Options struct {
	// Dispatcher carries results back to the UI goroutine. Required.
	Dispatcher *Dispatcher
	// Open shows a URL in the person's browser (pkg/browser.Open).
	Open func(url string) error
	// Interval is how often the status is polled (default 2s), and
	// FastInterval how often while an action runs (default 500ms), so a
	// device-flow code appears without a two-second wait.
	Interval, FastInterval time.Duration
	// ConfigPath is shown in the settings form.
	ConfigPath string
}

// ViewModel is the app's state. Its exported fields are bound by the views;
// it never references them.
type ViewModel struct {
	svc  Service
	disp *Dispatcher
	open func(string) error

	interval, fastInterval time.Duration
	fast                   atomic.Bool
	ctx                    context.Context
	stop                   context.CancelFunc
	wg                     sync.WaitGroup

	// What the window shows.
	Screen   *mvvm.Observable[int]
	State    *mvvm.Observable[string] // "Connected", "Disconnected", ...
	Account  *mvvm.Observable[string] // "Signed in as ..."
	Address  *mvvm.Observable[string] // assigned address and interface
	Tenant   *mvvm.Observable[string] // the session's tenant
	Helper   *mvvm.Observable[string] // whether the privileged helper answers
	Server   *mvvm.Observable[string] // the configured server
	Problem  *mvvm.Observable[string] // why the configuration is incomplete
	Busy     *mvvm.Observable[string] // what is in progress, or ""
	Error    *mvvm.Observable[string] // the last action's failure, or ""
	Slot     *mvvm.Observable[int]
	Actions  *mvvm.Observable[int]
	TrayText *mvvm.Observable[string] // the tray menu's status line

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
	TenantLocked  *mvvm.Observable[bool] // no change while connected or busy

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
	ChooseTenant   *mvvm.Command // ask the server which tenants are offered
	Connect        *mvvm.Command
	Disconnect     *mvvm.Command
	SignOut        *mvvm.Command
	OpenSettings   *mvvm.Command
	SaveSettings   *mvvm.Command
	CloseSettings  *mvvm.Command

	// Private state the commands' CanExecute reads.
	configOK  *mvvm.Observable[bool]
	helperOK  *mvvm.Observable[bool]
	signingIn *mvvm.Observable[bool]

	cancel        context.CancelFunc // the action in progress
	tenant        string             // the session's tenant, as last reported
	syncingTenant bool               // TenantIndex is being set from a status
	settingsFrom  appcore.Config     // the configuration the form was opened on
	lastStatus    appcore.Status     // the status last applied
}

// New builds the view model. Call Start to begin polling.
func New(svc Service, o Options) *ViewModel {
	if o.Interval <= 0 {
		o.Interval = 2 * time.Second
	}
	if o.FastInterval <= 0 {
		o.FastInterval = 500 * time.Millisecond
	}
	if o.Open == nil {
		o.Open = func(string) error { return errors.New("no browser to open") }
	}
	ctx, stop := context.WithCancel(context.Background())
	vm := &ViewModel{
		svc: svc, disp: o.Dispatcher, open: o.Open,
		interval: o.Interval, fastInterval: o.FastInterval,
		ctx: ctx, stop: stop,

		Screen:   mvvm.NewObservable(ScreenLoading),
		State:    mvvm.NewObservable("Starting…"),
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
		TrayText: mvvm.NewObservable("Claimward: starting…"),

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

		configOK:  mvvm.NewObservable(false),
		helperOK:  mvvm.NewObservable(false),
		signingIn: mvvm.NewObservable(false),
	}
	vm.TenantOptions.Append(TenantOption{Label: "No tenant chosen"})
	vm.commands()
	vm.TenantIndex.Subscribe(vm.chooseTenant)
	// The tenant cannot change under a running connection: the helper keeps
	// the tenant it connected to, and the next status would put it back.
	locked := func() { vm.TenantLocked.Set(vm.Connected.Get() || vm.Busy.Get() != "") }
	vm.Connected.SubscribeChanged(locked)
	vm.Busy.SubscribeChanged(locked)
	return vm
}

func (vm *ViewModel) idle() bool { return vm.Busy.Get() == "" }

// commands defines every command and what its CanExecute depends on.
func (vm *ViewModel) commands() {
	vm.SignIn = mvvm.NewCommand(vm.signIn, func() bool {
		return vm.idle() && vm.configOK.Get() && !vm.LoggedIn.Get()
	})
	vm.CancelSignIn = mvvm.NewCommand(func() {
		if vm.cancel != nil {
			vm.cancel()
		}
	}, func() bool { return vm.signingIn.Get() })
	vm.OpenSignInPage = mvvm.NewCommand(func() {
		if err := vm.open(vm.SignInURL.Get()); err != nil {
			vm.Error.Set(err.Error())
		}
	}, func() bool { return vm.SignInURL.Get() != "" })
	vm.ChooseTenant = mvvm.NewCommand(vm.listTenants, func() bool {
		return vm.idle() && vm.LoggedIn.Get() && vm.helperOK.Get()
	})
	vm.Connect = mvvm.NewCommand(vm.connect, func() bool {
		return vm.idle() && vm.LoggedIn.Get() && vm.helperOK.Get() && !vm.Connected.Get()
	})
	vm.Disconnect = mvvm.NewCommand(vm.disconnect, func() bool {
		return vm.idle() && vm.Connected.Get()
	})
	vm.SignOut = mvvm.NewCommand(vm.signOut, func() bool {
		return vm.idle() && vm.LoggedIn.Get()
	})
	vm.OpenSettings = mvvm.NewCommand(vm.openSettings, func() bool {
		return vm.idle() && vm.Screen.Get() != ScreenSettings
	})
	vm.SaveSettings = mvvm.NewCommand(vm.saveSettings, func() bool {
		return vm.idle() && vm.Screen.Get() == ScreenSettings
	})
	vm.CloseSettings = mvvm.NewCommand(vm.closeSettings, func() bool {
		return vm.idle() && vm.Screen.Get() == ScreenSettings
	})

	mvvm.BindCanExecute(vm.SignIn, vm.Busy, vm.configOK, vm.LoggedIn)
	mvvm.BindCanExecute(vm.CancelSignIn, vm.signingIn)
	mvvm.BindCanExecute(vm.OpenSignInPage, vm.SignInURL)
	mvvm.BindCanExecute(vm.ChooseTenant, vm.Busy, vm.LoggedIn, vm.helperOK)
	mvvm.BindCanExecute(vm.Connect, vm.Busy, vm.LoggedIn, vm.helperOK, vm.Connected)
	mvvm.BindCanExecute(vm.Disconnect, vm.Busy, vm.Connected)
	mvvm.BindCanExecute(vm.SignOut, vm.Busy, vm.LoggedIn)
	mvvm.BindCanExecute(vm.OpenSettings, vm.Busy, vm.Screen)
	mvvm.BindCanExecute(vm.SaveSettings, vm.Busy, vm.Screen)
	mvvm.BindCanExecute(vm.CloseSettings, vm.Busy, vm.Screen)
}

// Post queues fn for the UI goroutine: how the tray, whose menu callbacks
// arrive on its own goroutine, executes a command.
func (vm *ViewModel) Post(fn func()) { vm.disp.Post(fn) }

// Start polls the status until Stop. The first poll is immediate.
func (vm *ViewModel) Start() {
	vm.wg.Add(1)
	go func() {
		defer vm.wg.Done()
		for {
			st := vm.svc.Status()
			vm.disp.Post(func() { vm.Apply(st) })
			wait := vm.interval
			if vm.fast.Load() {
				wait = vm.fastInterval
			}
			select {
			case <-vm.ctx.Done():
				return
			case <-time.After(wait):
			}
		}
	}()
}

// Stop ends the polling and cancels the action in progress.
func (vm *ViewModel) Stop() { vm.stop() }

// Wait blocks until the poller and every action have finished and posted
// their results. Results still have to be drained on the UI goroutine.
func (vm *ViewModel) Wait() { vm.wg.Wait() }

// run starts a slow action: busy says what it is doing, work runs on its own
// goroutine with a context the person can cancel, and done (on the UI
// goroutine, after the status that follows it has been applied) handles its
// outcome.
func (vm *ViewModel) run(busy string, work func(context.Context) error, done func(error)) {
	ctx, cancel := context.WithCancel(vm.ctx)
	vm.cancel = cancel
	vm.Error.Set("")
	vm.Busy.Set(busy)
	vm.fast.Store(true)
	vm.wg.Add(1)
	go func() {
		defer vm.wg.Done()
		err := work(ctx)
		st := vm.svc.Status()
		vm.disp.Post(func() {
			cancel()
			vm.cancel = nil
			vm.fast.Store(false)
			vm.Busy.Set("")
			vm.Apply(st)
			done(err)
		})
	}()
}

func (vm *ViewModel) signIn() {
	vm.signingIn.Set(true)
	vm.SignInHint.Set("Waiting for the identity provider…")
	vm.Slot.Set(SlotSignIn)
	vm.run("Signing in…", vm.svc.Login, func(err error) {
		vm.signingIn.Set(false)
		vm.SignInHint.Set("")
		vm.Slot.Set(vm.slotFor(vm.lastStatus))
		switch {
		case errors.Is(err, context.Canceled):
			vm.Error.Set("Sign-in cancelled.")
		case err != nil:
			vm.Error.Set("Sign-in failed: " + err.Error())
		}
	})
}

func (vm *ViewModel) listTenants() {
	vm.run("Asking the server for your tenants…", func(ctx context.Context) error {
		_, err := vm.svc.Tenants(ctx)
		return err
	}, func(err error) {
		if err != nil {
			vm.Error.Set("Listing tenants failed: " + err.Error())
		}
	})
}

func (vm *ViewModel) connect() {
	vm.run("Connecting…", vm.svc.Connect, func(err error) {
		switch {
		case errors.Is(err, appcore.ErrTenantRequired):
			vm.Error.Set("You belong to several tenants: choose one, then connect again.")
		case err != nil:
			vm.Error.Set("Connection failed: " + err.Error())
		}
	})
}

func (vm *ViewModel) disconnect() {
	vm.run("Disconnecting…", vm.svc.Disconnect, func(err error) {
		if err != nil {
			vm.Error.Set("Disconnecting failed: " + err.Error())
		}
	})
}

func (vm *ViewModel) signOut() {
	vm.run("Signing out…", vm.svc.Logout, func(err error) {
		if err != nil {
			vm.Error.Set("Signing out failed: " + err.Error())
		}
	})
}

// chooseTenant runs when the tenant drop-down changes. It writes the choice
// to the session at once, on the UI goroutine — a small local file — so no
// status poll can come back in between with the previous one.
func (vm *ViewModel) chooseTenant(i int) {
	if vm.syncingTenant || i < 0 || i >= vm.TenantOptions.Len() {
		return
	}
	id := vm.TenantOptions.At(i).ID
	if id == vm.tenant {
		return
	}
	if err := vm.svc.SetTenant(id); err != nil {
		vm.Error.Set("Choosing the tenant failed: " + err.Error())
		vm.syncTenantIndex()
		return
	}
	vm.Error.Set("")
	vm.tenant = id
	vm.Tenant.Set(vm.tenantLabel(id, vm.lastStatus.Tenants))
}

func (vm *ViewModel) openSettings() {
	c := vm.svc.Config()
	vm.settingsFrom = c
	vm.ServerURL.Set(c.ServerURL)
	vm.ProviderIndex.Set(providerIndex(c.Provider))
	vm.GitHubClientID.Set(c.GitHubClientID)
	vm.OIDCIssuer.Set(c.OIDCIssuer)
	vm.OIDCClientID.Set(c.OIDCClientID)
	vm.SettingsError.Set("")
	vm.Screen.Set(ScreenSettings)
}

// form is the configuration the settings form describes. What the form does
// not show (the helper socket) is kept from the configuration it opened on.
func (vm *ViewModel) form() appcore.Config {
	c := vm.settingsFrom
	c.ServerURL = trim(vm.ServerURL.Get())
	c.Provider = Providers[clamp(vm.ProviderIndex.Get(), len(Providers))]
	c.GitHubClientID = trim(vm.GitHubClientID.Get())
	c.OIDCIssuer = trim(vm.OIDCIssuer.Get())
	c.OIDCClientID = trim(vm.OIDCClientID.Get())
	return c
}

// saveSettings refuses an incomplete configuration, saying what is missing,
// rather than saving one the app cannot sign in with.
func (vm *ViewModel) saveSettings() {
	c := vm.form()
	if err := c.Validate(); err != nil {
		vm.SettingsError.Set(err.Error())
		return
	}
	vm.SettingsError.Set("")
	vm.run("Saving the configuration…", func(context.Context) error {
		return vm.svc.UpdateConfig(c)
	}, func(err error) {
		if err != nil {
			vm.SettingsError.Set("Saving failed: " + err.Error())
			return
		}
		vm.Screen.Set(vm.screenFor(vm.lastStatus))
	})
}

func (vm *ViewModel) closeSettings() {
	vm.SettingsError.Set("")
	vm.Screen.Set(vm.screenFor(vm.lastStatus))
}

func (vm *ViewModel) screenFor(st appcore.Status) int {
	if !st.ConfigOK {
		return ScreenUnconfigured
	}
	return ScreenMain
}

func (vm *ViewModel) slotFor(st appcore.Status) int {
	switch {
	case vm.signingIn.Get() || st.DeviceVerificationURI != "":
		return SlotSignIn
	case !st.LoggedIn:
		return SlotNone
	case len(st.Tenants) > 0 || st.TenantRequired:
		return SlotTenants
	default:
		return SlotChooseTenant
	}
}

// Apply shows a status. It is the only writer of everything the status
// decides; the poller and every action call it on the UI goroutine.
func (vm *ViewModel) Apply(st appcore.Status) {
	vm.lastStatus = st
	vm.configOK.Set(st.ConfigOK)
	vm.helperOK.Set(st.HelperInstalled)
	vm.LoggedIn.Set(st.LoggedIn)
	vm.Connected.Set(st.Connected)
	if vm.Screen.Get() != ScreenSettings {
		vm.Screen.Set(vm.screenFor(st))
	}
	vm.Problem.Set(st.ConfigError)
	vm.Server.Set(st.ServerURL)

	switch {
	case !st.ConfigOK:
		vm.State.Set("Not configured")
	case st.Connected:
		vm.State.Set("Connected")
	case st.LoggedIn:
		vm.State.Set("Disconnected")
	default:
		vm.State.Set("Signed out")
	}
	switch {
	case !st.LoggedIn:
		vm.Account.Set("Not signed in")
	case st.Email != "":
		vm.Account.Set("Signed in as " + st.Email)
	default:
		vm.Account.Set("Signed in")
	}
	if st.Connected {
		addr := st.AssignedIP
		if addr == "" {
			addr = "address unknown"
		}
		vm.Address.Set(fmt.Sprintf("%s on %s", addr, orDash(st.Interface)))
	} else {
		vm.Address.Set("Not connected")
	}
	if st.HelperInstalled {
		vm.Helper.Set("Privileged helper: running")
	} else {
		vm.Helper.Set("Privileged helper: not running (see the README)")
	}
	vm.TrayText.Set(trayText(st))

	switch {
	case !st.LoggedIn:
		vm.Actions.Set(ActionsSignedOut)
	case st.Connected:
		vm.Actions.Set(ActionsConnected)
	default:
		vm.Actions.Set(ActionsDisconnected)
	}

	vm.SignInURL.Set(st.DeviceVerificationURI)
	vm.SignInCode.Set(st.DeviceUserCode)
	switch {
	case st.DeviceUserCode != "":
		vm.SignInHint.Set("Open the sign-in page, then enter this code:")
	case st.DeviceVerificationURI != "":
		vm.SignInHint.Set("Complete the sign-in in your browser.")
	}

	vm.applyTenants(st)
	vm.Slot.Set(vm.slotFor(st))
	vm.applyLog(st.Log)
}

// applyTenants rebuilds the drop-down when what is offered changed, and points
// it at the session's tenant.
func (vm *ViewModel) applyTenants(st appcore.Status) {
	first := "No tenant chosen"
	if st.TenantRequired {
		first = "Choose a tenant…"
	}
	want := []TenantOption{{Label: first}}
	for _, t := range st.Tenants {
		want = append(want, TenantOption{ID: t.ID, Label: tenantName(t)})
	}
	vm.syncingTenant = true
	if !sameOptions(vm.TenantOptions.Slice(), want) {
		vm.TenantOptions.Clear()
		vm.TenantOptions.Append(want...)
	}
	vm.tenant = st.Tenant
	vm.syncTenantIndex()
	vm.syncingTenant = false

	switch {
	case !st.LoggedIn:
		vm.Tenant.Set("")
	case st.Tenant == "" && st.TenantRequired:
		vm.Tenant.Set("Tenant: none chosen")
	default:
		vm.Tenant.Set(vm.tenantLabel(st.Tenant, st.Tenants))
	}
}

// syncTenantIndex points the drop-down at the session's tenant, without
// taking that for a choice the person made.
func (vm *ViewModel) syncTenantIndex() {
	was := vm.syncingTenant
	vm.syncingTenant = true
	idx := 0
	for i, o := range vm.TenantOptions.Slice() {
		if o.ID == vm.tenant {
			idx = i
			break
		}
	}
	vm.TenantIndex.Set(idx)
	vm.syncingTenant = was
}

func (vm *ViewModel) tenantLabel(id string, offered []hproto.Tenant) string {
	if id == "" {
		return "Tenant: the server's choice"
	}
	for _, t := range offered {
		if t.ID == id {
			return "Tenant: " + tenantName(t)
		}
	}
	return "Tenant: " + id
}

// applyLog appends what is new, or starts over when the log is not a
// continuation of what is shown (it is a ring that drops its oldest lines).
func (vm *ViewModel) applyLog(lines []string) {
	cur := vm.Log.Slice()
	if len(lines) >= len(cur) && prefix(cur, lines) {
		if len(lines) == len(cur) {
			return
		}
		vm.Log.Append(lines[len(cur):]...)
	} else {
		vm.Log.Clear()
		vm.Log.Append(lines...)
	}
	// Follow the newest line; the list clamps a row past its end.
	vm.LogScroll.Set(vm.Log.Len())
}

func trayText(st appcore.Status) string {
	switch {
	case !st.ConfigOK:
		return "Claimward: not configured"
	case st.Connected && st.AssignedIP != "":
		return "Claimward: connected (" + st.AssignedIP + ")"
	case st.Connected:
		return "Claimward: connected"
	case !st.LoggedIn:
		return "Claimward: signed out"
	case !st.HelperInstalled:
		return "Claimward: helper not running"
	default:
		return "Claimward: disconnected"
	}
}

func tenantName(t hproto.Tenant) string {
	if t.Name == "" || t.Name == t.ID {
		return t.ID
	}
	return t.Name + " (" + t.ID + ")"
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

func prefix(short, long []string) bool {
	for i := range short {
		if short[i] != long[i] {
			return false
		}
	}
	return true
}

func providerIndex(p string) int {
	for i, v := range Providers {
		if v == p {
			return i
		}
	}
	return 0
}

func clamp(i, n int) int {
	if i < 0 || i >= n {
		return 0
	}
	return i
}

func trim(s string) string { return strings.TrimSpace(s) }

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
