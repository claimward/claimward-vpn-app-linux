// Package trayview is the Claimward tray icon and menu, bound to the view
// model: a status line, Connect, Disconnect, Open window and Quit.
//
// The menu is rebuilt whenever what it shows changes — the status line, or
// whether Connect and Disconnect can run — and its items run the view model's
// commands. A menu click arrives on the tray's own goroutine, so it is Posted
// to the UI goroutine rather than executed where it lands.
package trayview

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"time"

	"github.com/go-widgets/mvvm"
	"github.com/go-widgets/tray"

	"github.com/claimward/claimward-vpn-app-linux/internal/viewmodel"
)

// Actions are what the menu does outside the view model.
type Actions struct {
	OpenWindow func()
	Quit       func()
}

// Bind keeps t's icon, tooltip and menu in step with vm. It must be called on
// the UI goroutine, like every subscription to vm. The returned function
// detaches.
func Bind(t *tray.Tray, vm *viewmodel.ViewModel, icon []byte, a Actions) (unbind func()) {
	refresh := func() {
		t.SetTooltip(vm.TrayText.Get())
		t.SetMenu(Menu(vm, a))
	}
	refresh()
	idle := Greyscale(icon)
	stopIcon := tray.BindIcon(t, vm.Connected, tray.Icons[bool]{
		true:  {icon},
		false: {idle},
	}, time.Second)
	unsubs := []func(){
		stopIcon,
		vm.TrayText.SubscribeChanged(refresh),
		vm.Connect.SubscribeCanExecuteChanged(refresh),
		vm.Disconnect.SubscribeCanExecuteChanged(refresh),
	}
	return func() {
		for _, u := range unsubs {
			u()
		}
	}
}

// Menu is the tray menu for vm's current state.
func Menu(vm *viewmodel.ViewModel, a Actions) *tray.Menu {
	status := tray.Item(vm.TrayText.Get(), nil)
	status.Disabled = true
	return tray.NewMenu().Add(
		status,
		tray.Separator(),
		command("Connect", vm, vm.Connect),
		command("Disconnect", vm, vm.Disconnect),
		tray.Separator(),
		tray.Item("Open window", a.OpenWindow),
		tray.Item("Quit Claimward", a.Quit),
	)
}

// command is a menu item that executes c on the UI goroutine, greyed while c
// cannot execute.
func command(label string, vm *viewmodel.ViewModel, c *mvvm.Command) *tray.MenuItem {
	it := tray.Item(label, func() { vm.Post(c.Execute) })
	it.Disabled = !c.CanExecute()
	return it
}

// Greyscale is the icon with its colour taken out, for the tray while the
// tunnel is down. An icon that cannot be decoded is returned unchanged.
func Greyscale(icon []byte) []byte {
	src, err := png.Decode(bytes.NewReader(icon))
	if err != nil {
		return icon
	}
	b := src.Bounds()
	dst := image.NewNRGBA(b)
	draw.Draw(dst, b, src, b.Min, draw.Src)
	for i := 0; i+3 < len(dst.Pix); i += 4 {
		opaque := color.RGBA{R: dst.Pix[i], G: dst.Pix[i+1], B: dst.Pix[i+2], A: 0xff}
		y := color.GrayModel.Convert(opaque).(color.Gray).Y
		dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2] = y, y, y
	}
	var out bytes.Buffer
	_ = png.Encode(&out, dst) // encoding an in-memory NRGBA cannot fail
	return out.Bytes()
}
