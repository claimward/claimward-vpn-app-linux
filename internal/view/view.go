// Package view is the Claimward window: go-widgets/toolkit widgets bound to the
// view model through go-widgets/mvvmtk, and the application.Handler that
// go-widgets/application drives.
//
// It never writes a widget's state: every label, list, field, drop-down,
// button and page follows an observable or a command of the view model
// (internal/viewmodel), through a binding made once in New. Nothing here copies
// state into widgets on a frame; a frame only paints what the bindings already
// put there. CI checks this with go-widgets/mvvmlint, and checks with
// go-widgets/bricolint that nothing here draws by hand.
//
// Threading: the window calls Frame and the input methods on its goroutine,
// the dispatcher's work is drained by Run on another, and the tray's menu
// arrives on a third. One mutex orders them, so a binding never moves a widget
// while it is being drawn.
package view

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/go-widgets/application"
	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/mvvmtk"
	"github.com/go-widgets/painter"
	"github.com/go-widgets/toolkit"

	"github.com/claimward/claimward-vpn-app-linux/assets"
	"github.com/claimward/claimward-vpn-app-linux/internal/viewmodel"
)

// Brand colours (github.com/claimward/brand).
var (
	brandTeal = toolkit.RGB(0x0D, 0x94, 0x88)
	errorInk  = toolkit.RGB(0xD9, 0x3F, 0x4C)
	mutedInk  = toolkit.RGB(0x80, 0x86, 0x90)
)

// Layout metrics, in logical pixels; scaled to the display at layout time.
const (
	margin  = 16
	gap     = 8
	rowH    = 22
	headerH = 36
	buttonH = 32
	fieldH  = 30
	titleH  = 34
	codeH   = 40
	buttonW = 168
	titlePx = 22
	codePx  = 28
	headPx  = 18
	logRows = 6
)

// View is the window's contents.
type View struct {
	mu   sync.Mutex
	vm   *viewmodel.ViewModel
	disp *viewmodel.Dispatcher

	theme  *toolkit.Theme
	root   *toolkit.PopoverHost
	unbind []func()

	// The pages of the three switchers, and the layouts the view model's
	// page observables drive.
	screens, slot, actions            *toolkit.Container
	screenCard, slotCard, actionsCard *toolkit.CardLayout
	loadingPage, unconfiguredPage     toolkit.Widget
	mainPage, settingsPage            toolkit.Widget
	noSlot, signInSlot, tenantSlot    toolkit.Widget
	chooseSlot                        toolkit.Widget
	signedOutRow, disconnectedRow     toolkit.Widget
	connectedRow                      toolkit.Widget
	header                            *toolkit.HBox
	content                           *toolkit.VBox
	leaves                            leaves
	w, h                              int
	scale                             float64
	buf                               []byte
	dirty                             atomic.Bool
	pressed                           bool
	lastX, lastY                      int
}

// leaves are the widgets the view model's state reaches. The boxes that place
// them are rebuilt when the display scale changes; these are made once.
type leaves struct {
	mark                                         *toolkit.StatusIcon
	title                                        *toolkit.Label
	settings                                     *toolkit.Button
	busy, err                                    *toolkit.Label
	loading                                      *toolkit.Label
	unconfTitle, problem                         *toolkit.Label
	openSettings                                 *toolkit.Button
	state, account, address, tenant, helper, srv *toolkit.Label
	hint, code, url                              *toolkit.Label
	openPage, cancelSignIn                       *toolkit.Button
	tenantTitle                                  *toolkit.Label
	tenants                                      *toolkit.DropDown
	refreshTenants                               *toolkit.Button
	chooseHint                                   *toolkit.Label
	chooseTenant                                 *toolkit.Button
	signIn, connect, disconnect                  *toolkit.Button
	signOut1, signOut2                           *toolkit.Button
	logTitle                                     *toolkit.Label
	log                                          *toolkit.ListBox
	formTitle                                    *toolkit.Label
	server, ghClient, issuer, oidcClient         *toolkit.Entry
	provider                                     *toolkit.DropDown
	fServer, fProvider, fGH, fIssuer, fOIDC      *toolkit.FormField
	cfgPath, cfgErr                              *toolkit.Label
	save, cancel                                 *toolkit.Button
}

// New builds the widgets and binds them to vm. disp is the dispatcher vm
// posts to; Run drains it.
func New(vm *viewmodel.ViewModel, disp *viewmodel.Dispatcher) *View {
	// Anti-aliased, shaped text. On the impossible error the toolkit keeps
	// its bitmap font and the window still renders.
	_ = toolkit.UseOpenTypeText()
	v := &View{vm: vm, disp: disp, theme: themeFor(false)}
	v.build()
	v.bind()
	v.layout()
	v.dirty.Store(true)
	return v
}

// themeFor is the toolkit's light or dark theme with the brand's accent.
func themeFor(dark bool) *toolkit.Theme {
	t := toolkit.DefaultLight()
	if dark {
		t = toolkit.DefaultDark()
	}
	t.Accent = brandTeal
	return t
}

func label(text string) *toolkit.Label {
	l := toolkit.NewLabel(text)
	l.Ellipsis = true
	return l
}

func muted(l *toolkit.Label) *toolkit.Label {
	l.Ink = mutedInk
	return l
}

// build makes every leaf widget, once.
func (v *View) build() {
	l := &v.leaves
	l.mark = toolkit.NewStatusIcon(toolkit.SVGIcon(assets.Mark))
	l.title = label("Claimward VPN").SetFontSize(toolkit.Scaled(headPx))
	l.settings = toolkit.NewButton("Settings", nil)
	l.busy = muted(label(""))
	l.err = label("")
	l.err.Ink = errorInk

	l.loading = muted(label("Starting…"))
	l.unconfTitle = label("Not configured").SetFontSize(toolkit.Scaled(titlePx))
	l.problem = label("")
	l.openSettings = toolkit.NewButton("Open settings", nil)
	l.openSettings.Style = toolkit.ButtonProminent

	l.state = label("").SetFontSize(toolkit.Scaled(titlePx))
	l.account = label("")
	l.address = label("")
	l.tenant = label("")
	l.helper = muted(label(""))
	l.srv = muted(label(""))

	l.hint = label("")
	l.code = label("").SetFontSize(toolkit.Scaled(codePx))
	l.url = muted(label(""))
	l.openPage = toolkit.NewButton("Open sign-in page", nil)
	l.openPage.Style = toolkit.ButtonProminent
	l.cancelSignIn = toolkit.NewButton("Cancel", nil)

	l.tenantTitle = label("Tenant for the next connection")
	l.tenants = toolkit.NewDropDown(nil, 0)
	l.refreshTenants = toolkit.NewButton("Refresh", nil)
	l.chooseHint = muted(label("In several tenants? Ask the server which ones you may use."))
	l.chooseTenant = toolkit.NewButton("Choose tenant", nil)

	l.signIn = toolkit.NewButton("Sign in", nil)
	l.signIn.Style = toolkit.ButtonProminent
	l.connect = toolkit.NewButton("Connect", nil)
	l.connect.Style = toolkit.ButtonProminent
	l.disconnect = toolkit.NewButton("Disconnect", nil)
	l.disconnect.Style = toolkit.ButtonDanger
	l.signOut1 = toolkit.NewButton("Sign out", nil)
	l.signOut2 = toolkit.NewButton("Sign out", nil)

	l.logTitle = muted(label("Connection log"))
	l.log = toolkit.NewListBox(nil)

	l.formTitle = label("Configuration").SetFontSize(toolkit.Scaled(headPx))
	l.server = toolkit.NewEntry("")
	l.server.Placeholder = "https://vpn.example.com"
	l.provider = toolkit.NewDropDown(viewmodel.ProviderLabels, 0)
	l.ghClient = toolkit.NewEntry("")
	l.ghClient.Placeholder = "Iv1.0123456789abcdef"
	l.issuer = toolkit.NewEntry("")
	l.issuer.Placeholder = "https://issuer.example.com"
	l.oidcClient = toolkit.NewEntry("")
	l.fServer = toolkit.NewFormField("Server URL", l.server)
	l.fProvider = toolkit.NewFormField("Identity provider", l.provider)
	l.fGH = toolkit.NewFormField("GitHub client ID (GitHub provider)", l.ghClient)
	l.fIssuer = toolkit.NewFormField("OIDC issuer (OIDC and go-authn)", l.issuer)
	l.fOIDC = toolkit.NewFormField("OIDC client ID (OIDC and go-authn)", l.oidcClient)
	l.cfgPath = muted(label(""))
	l.cfgErr = label("")
	l.cfgErr.Ink = errorInk
	l.save = toolkit.NewButton("Save", nil)
	l.save.Style = toolkit.ButtonProminent
	l.cancel = toolkit.NewButton("Cancel", nil)

	// The three switchers. Their pages are the boxes layout builds; which
	// one shows is the view model's.
	//
	// They are Container+CardLayout rather than toolkit.Stack. Stack takes
	// part in focus traversal since toolkit v0.326.0 (go-widgets/toolkit#480),
	// so this is no longer needed for the settings form's fields to take
	// keys; it is kept because the view model's page observables are ints
	// that bind straight to CardLayout.Active, while Stack.Visible is a
	// string observable and mvvm has no converting binding. Both hide the
	// inactive pages from the focus walk, so swapping them changes nothing
	// the user can see.
	v.screenCard, v.slotCard, v.actionsCard = &toolkit.CardLayout{}, &toolkit.CardLayout{}, &toolkit.CardLayout{}
	v.screens = toolkit.NewContainer(v.screenCard)
	v.slot = toolkit.NewContainer(v.slotCard)
	v.actions = toolkit.NewContainer(v.actionsCard)
}

func (v *View) invalidate() { v.dirty.Store(true) }

// bind ties every leaf to the view model. It is the only place widget state
// is reached, and it reaches it through the binders.
func (v *View) bind() {
	l, vm, inv := &v.leaves, v.vm, v.invalidate
	page := func(c *toolkit.Container) func() {
		return func() {
			c.SetBounds(c.Bounds()) // re-arrange for the new page
			inv()
		}
	}
	v.unbind = append(v.unbind,
		mvvm.OneWay(vm.Screen, &v.screenCard.Active, page(v.screens)),
		mvvm.OneWay(vm.Slot, &v.slotCard.Active, page(v.slot)),
		mvvm.OneWay(vm.Actions, &v.actionsCard.Active, page(v.actions)),

		mvvmtk.BindLabel(l.busy, vm.Busy, inv),
		mvvmtk.BindLabel(l.err, vm.Error, inv),
		mvvmtk.BindLabel(l.problem, vm.Problem, inv),
		mvvmtk.BindLabel(l.state, vm.State, inv),
		mvvmtk.BindLabel(l.account, vm.Account, inv),
		mvvmtk.BindLabel(l.address, vm.Address, inv),
		mvvmtk.BindLabel(l.tenant, vm.Tenant, inv),
		mvvmtk.BindLabel(l.helper, vm.Helper, inv),
		mvvmtk.BindLabel(l.srv, vm.Server, inv),
		mvvmtk.BindLabel(l.hint, vm.SignInHint, inv),
		mvvmtk.BindLabel(l.code, vm.SignInCode, inv),
		mvvmtk.BindLabel(l.url, vm.SignInURL, inv),
		mvvmtk.BindLabel(l.cfgPath, vm.ConfigPath, inv),
		mvvmtk.BindLabel(l.cfgErr, vm.SettingsError, inv),

		mvvmtk.BindCommand(l.settings, vm.OpenSettings, inv),
		mvvmtk.BindCommand(l.openSettings, vm.OpenSettings, inv),
		mvvmtk.BindCommand(l.openPage, vm.OpenSignInPage, inv),
		mvvmtk.BindCommand(l.cancelSignIn, vm.CancelSignIn, inv),
		mvvmtk.BindCommand(l.refreshTenants, vm.ChooseTenant, inv),
		mvvmtk.BindCommand(l.chooseTenant, vm.ChooseTenant, inv),
		mvvmtk.BindCommand(l.signIn, vm.SignIn, inv),
		mvvmtk.BindCommand(l.connect, vm.Connect, inv),
		mvvmtk.BindCommand(l.disconnect, vm.Disconnect, inv),
		mvvmtk.BindCommand(l.signOut1, vm.SignOut, inv),
		mvvmtk.BindCommand(l.signOut2, vm.SignOut, inv),
		mvvmtk.BindCommand(l.save, vm.SaveSettings, inv),
		mvvmtk.BindCommand(l.cancel, vm.CloseSettings, inv),

		mvvmtk.BindDropDownOptions(l.tenants, vm.TenantOptions, func(o viewmodel.TenantOption) string { return o.Label }, inv),
		mvvmtk.BindSelectedIndex(l.tenants, vm.TenantIndex, inv),
		mvvm.BindTwoWay(vm.TenantLocked, l.tenants.Disabled(), inv),

		mvvmtk.BindListItems(l.log, vm.Log, func(s string) string { return s }, inv),
		mvvm.BindTwoWay(vm.LogScroll, l.log.ScrollRow(), inv),

		mvvmtk.BindEntryText(l.server, vm.ServerURL, inv),
		mvvmtk.BindSelectedIndex(l.provider, vm.ProviderIndex, inv),
		mvvmtk.BindEntryText(l.ghClient, vm.GitHubClientID, inv),
		mvvmtk.BindEntryText(l.issuer, vm.OIDCIssuer, inv),
		mvvmtk.BindEntryText(l.oidcClient, vm.OIDCClientID, inv),
	)
	// Enter in any field of the form saves it.
	for _, e := range []*toolkit.Entry{l.server, l.ghClient, l.issuer, l.oidcClient} {
		e.OnSubmit = func(string) { vm.SaveSettings.Execute() }
	}
}

// layout builds the boxes that place the leaves, at the current scale.
func (v *View) layout() {
	l := &v.leaves
	s := toolkit.Scaled
	row := s(rowH)

	hbox := func(ws ...toolkit.Widget) *toolkit.HBox {
		h := toolkit.NewHBox()
		h.Spacing = s(gap)
		for _, w := range ws {
			h.AddFixed(w, s(buttonW))
		}
		return h
	}
	vbox := func() *toolkit.VBox {
		b := toolkit.NewVBox()
		b.Spacing = s(gap)
		return b
	}

	v.header = toolkit.NewHBox()
	v.header.Spacing = s(gap)
	v.header.AddFixed(l.mark, s(headerH))
	v.header.AddFlex(l.title, 1)
	v.header.AddFixed(l.settings, s(buttonW))

	// Screen: loading.
	loading := vbox()
	loading.AddFixed(l.loading, row)
	loading.AddFlex(toolkit.NewLabel(""), 1)
	v.loadingPage = loading

	// Screen: the configuration is incomplete.
	unconf := vbox()
	unconf.AddFixed(l.unconfTitle, s(titleH))
	unconf.AddFixed(l.problem, row)
	unconf.AddFixed(hbox(l.openSettings), s(buttonH))
	unconf.AddFlex(toolkit.NewLabel(""), 1)
	v.unconfiguredPage = unconf

	// The slot under the status.
	none := vbox()
	none.AddFlex(toolkit.NewLabel(""), 1)
	v.noSlot = none

	signIn := vbox()
	signIn.AddFixed(l.hint, row)
	signIn.AddFixed(l.code, s(codeH))
	signIn.AddFixed(hbox(l.openPage, l.cancelSignIn), s(buttonH))
	signIn.AddFixed(l.url, row)
	v.signInSlot = signIn

	tenants := vbox()
	tenants.AddFixed(l.tenantTitle, row)
	pick := toolkit.NewHBox()
	pick.Spacing = s(gap)
	pick.AddFlex(l.tenants, 1)
	pick.AddFixed(l.refreshTenants, s(buttonW))
	tenants.AddFixed(pick, s(buttonH))
	tenants.AddFlex(toolkit.NewLabel(""), 1)
	v.tenantSlot = tenants

	choose := vbox()
	choose.AddFixed(l.chooseHint, row)
	choose.AddFixed(hbox(l.chooseTenant), s(buttonH))
	choose.AddFlex(toolkit.NewLabel(""), 1)
	v.chooseSlot = choose

	v.slot.SetItems(
		toolkit.Item{Widget: v.noSlot},
		toolkit.Item{Widget: v.signInSlot},
		toolkit.Item{Widget: v.tenantSlot},
		toolkit.Item{Widget: v.chooseSlot},
	)

	// The actions.
	v.signedOutRow = hbox(l.signIn)
	v.disconnectedRow = hbox(l.connect, l.signOut1)
	v.connectedRow = hbox(l.disconnect, l.signOut2)
	v.actions.SetItems(
		toolkit.Item{Widget: v.signedOutRow},
		toolkit.Item{Widget: v.disconnectedRow},
		toolkit.Item{Widget: v.connectedRow},
	)

	// Screen: the status, what to do, and the log.
	main := vbox()
	main.AddFixed(l.state, s(titleH))
	main.AddFixed(l.account, row)
	main.AddFixed(l.address, row)
	main.AddFixed(l.tenant, row)
	main.AddFixed(l.helper, row)
	main.AddFixed(l.srv, row)
	main.AddFixed(v.slot, slotHeight())
	main.AddFixed(v.actions, s(buttonH))
	main.AddFixed(l.logTitle, row)
	// The list sizes its rows when it is made; at a new scale they are re-sized.
	l.log.RowHeight = row
	main.AddFlex(l.log, 1)
	v.mainPage = main

	// Screen: the configuration form.
	field := toolkit.FormFieldLabelH() + 2*s(toolkit.FormFieldPadY) + s(toolkit.FormFieldChildGap) + s(fieldH)
	form := vbox()
	form.AddFixed(l.formTitle, s(titleH))
	form.AddFixed(l.fServer, field)
	form.AddFixed(l.fProvider, field)
	form.AddFixed(l.fGH, field)
	form.AddFixed(l.fIssuer, field)
	form.AddFixed(l.fOIDC, field)
	form.AddFixed(l.cfgPath, row)
	form.AddFixed(l.cfgErr, row)
	form.AddFixed(hbox(l.save, l.cancel), s(buttonH))
	form.AddFlex(toolkit.NewLabel(""), 1)
	v.settingsPage = form

	v.screens.SetItems(
		toolkit.Item{Widget: v.loadingPage},
		toolkit.Item{Widget: v.unconfiguredPage},
		toolkit.Item{Widget: v.mainPage},
		toolkit.Item{Widget: v.settingsPage},
	)

	v.content = vbox()
	v.content.AddFixed(v.header, s(headerH))
	v.content.AddFlex(v.screens, 1)
	v.content.AddFixed(l.busy, row)
	v.content.AddFixed(l.err, row)
	v.root = toolkit.NewPopoverHost(toolkit.NewPadding(v.content, s(margin)))
	v.root.SetBounds(toolkit.Rect{W: v.w, H: v.h})
}

// slotHeight fits the tallest slot page, the sign-in prompt.
func slotHeight() int {
	s := toolkit.Scaled
	return 2*s(rowH) + s(codeH) + s(buttonH) + 3*s(gap)
}

// MinSize is the smallest window the layout is drawn for, in logical pixels.
func MinSize() (w, h int) {
	return 480, 2*margin + headerH + titleH + 5*rowH + (2*rowH + codeH + buttonH + 3*gap) + buttonH + rowH + logRows*rowH + 2*rowH + 14*gap
}

// Run drains the dispatcher on its own goroutine until ctx ends, so the view
// model and the tray stay live whether or not the window is open.
func (v *View) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-v.disp.Wake():
			v.Pump()
		}
	}
}

// Pump runs what the view model posted, under the view's lock.
func (v *View) Pump() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.disp.Drain()
}

// Locked runs fn under the view's lock: for a caller outside the window and
// the dispatcher (the tray) that executes a command.
func (v *View) Locked(fn func()) {
	v.mu.Lock()
	defer v.mu.Unlock()
	fn()
}

var _ application.Handler = (*View)(nil)

// Frame paints the window when something changed since the last frame.
func (v *View) Frame() ([]byte, int, int, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.buf == nil {
		return nil, 0, 0, false
	}
	if !v.dirty.Swap(false) {
		return v.buf, v.w, v.h, false
	}
	fill(v.buf, v.theme.Background)
	p := painter.NewPixelPainter(v.buf, v.w, v.h)
	v.root.Draw(p, v.theme)
	return v.buf, v.w, v.h, true
}

// fill paints the whole frame with the background: the page the widgets are
// drawn on, not a widget.
func fill(buf []byte, c toolkit.RGBA) {
	for i := 0; i+3 < len(buf); i += 4 {
		buf[i], buf[i+1], buf[i+2], buf[i+3] = c.R, c.G, c.B, c.A
	}
}

// Resize takes the framebuffer size in device pixels and the display scale.
func (v *View) Resize(w, h int, scale float64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if w <= 0 || h <= 0 {
		return
	}
	if scale <= 0 {
		scale = 1
	}
	if len(v.buf) != w*h*4 {
		v.buf = make([]byte, w*h*4)
	}
	v.w, v.h = w, h
	if scale != v.scale {
		v.scale = scale
		toolkit.SetMetricScale(scale)
		v.leaves.title.SetFontSize(toolkit.Scaled(headPx))
		v.leaves.unconfTitle.SetFontSize(toolkit.Scaled(titlePx))
		v.leaves.state.SetFontSize(toolkit.Scaled(titlePx))
		v.leaves.code.SetFontSize(toolkit.Scaled(codePx))
		v.leaves.formTitle.SetFontSize(toolkit.Scaled(headPx))
		v.layout()
	} else {
		v.root.SetBounds(toolkit.Rect{W: w, H: h})
	}
	v.dirty.Store(true)
}

// event delivers a toolkit event to the tree, and marks the frame dirty: a
// widget's own feedback (hover, press, focus, caret) changes what it draws.
func (v *View) event(ev toolkit.Event) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.root.OnEvent(ev)
	v.dirty.Store(true)
}

// MouseDown is a press: a click for the toolkit.
func (v *View) MouseDown(x, y int) {
	v.pressed, v.lastX, v.lastY = true, x, y
	v.event(toolkit.Event{Kind: toolkit.EventClick, X: x, Y: y})
}

// MouseMove is a drag while pressed, a hover otherwise.
func (v *View) MouseMove(x, y int) {
	v.lastX, v.lastY = x, y
	kind := toolkit.EventMouseMove
	if v.pressed {
		kind = toolkit.EventMouseDrag
	}
	v.event(toolkit.Event{Kind: kind, X: x, Y: y})
}

// MouseUp ends a press.
func (v *View) MouseUp(x, y int) {
	v.pressed = false
	v.event(toolkit.Event{Kind: toolkit.EventMouseUp, X: x, Y: y})
}

// wheelPixelsPerRow is what one row of a toolkit scroll is in the device
// pixels the application package reports.
const wheelPixelsPerRow = 40

// Scroll is a wheel turn, in device pixels, under the pointer.
func (v *View) Scroll(dy int) {
	rows := dy / wheelPixelsPerRow
	if rows == 0 && dy != 0 {
		rows = 1
		if dy < 0 {
			rows = -1
		}
	}
	v.event(toolkit.Event{Kind: toolkit.EventScroll, X: v.lastX, Y: v.lastY, Delta: rows})
}

// keyCodes turns the application package's key names back into the toolkit's.
var keyCodes = map[string]string{
	"Up": "ArrowUp", "Down": "ArrowDown", "Left": "ArrowLeft", "Right": "ArrowRight",
}

func keyCode(name string) string {
	if c, ok := keyCodes[name]; ok {
		return c
	}
	return name
}

// Key is a printable character (r) or a named key.
func (v *View) Key(name string, r rune) {
	if name == "" {
		if r != 0 {
			v.event(toolkit.Event{Kind: toolkit.EventChar, Code: string(r)})
		}
		return
	}
	v.event(toolkit.Event{Kind: toolkit.EventKeyDown, Code: keyCode(name)})
}

// ModifiedKey is a named key with its modifiers, so Shift+Tab goes backwards.
func (v *View) ModifiedKey(name string, m application.Modifiers) {
	v.event(toolkit.Event{Kind: toolkit.EventKeyDown, Code: keyCode(name),
		Ctrl: m.Ctrl, Shift: m.Shift, Alt: m.Alt, Meta: m.Meta})
}

// Shortcut takes copy, cut and paste to the focused field.
func (v *View) Shortcut(r rune, ctrl, meta bool) {
	code := map[rune]string{'c': "Ctrl+C", 'x': "Ctrl+X", 'v': "Ctrl+V"}[r|0x20]
	if code == "" {
		return
	}
	v.event(toolkit.Event{Kind: toolkit.EventKeyDown, Code: code, Ctrl: ctrl, Meta: meta})
}

// SystemAppearance follows the desktop's light or dark mode.
func (v *View) SystemAppearance(a application.SystemAppearance) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.theme = themeFor(a.Dark)
	v.dirty.Store(true)
}

// NeedsPresent lets the window skip blitting an unchanged frame.
func (v *View) NeedsPresent() bool { return v.dirty.Load() }

// PresentImmediate: nothing here is streamed.
func (v *View) PresentImmediate() bool { return false }

// PresentThrottle: nothing here animates.
func (v *View) PresentThrottle() bool { return false }

// A11yElements describes the window to the platform's screen reader.
func (v *View) A11yElements() []application.A11yElement {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []application.A11yElement
	for _, n := range toolkit.WalkA11y(v.root) {
		r := n.Rect
		if r.W <= 0 || r.H <= 0 {
			continue
		}
		out = append(out, application.A11yElement{Role: string(n.Role), Name: n.Name, Value: n.Value,
			X: r.X, Y: r.Y, W: r.W, H: r.H})
	}
	return out
}

// Close detaches every binding.
func (v *View) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, u := range v.unbind {
		u()
	}
	v.unbind = nil
}
