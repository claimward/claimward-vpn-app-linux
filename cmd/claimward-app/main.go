// Command claimward-app is the Claimward VPN desktop app for Linux: a window
// and a tray icon, drawn with go-widgets (pure Go, no webview, no cgo).
//
// This file only wires the pieces together — the shared core
// (claimward-vpn-client/pkg/appcore), the view model, the window and the tray
// — and runs the window loop. Opening a real window is a launch-verified
// boundary, so this file is outside the coverage gate; everything it wires is
// tested in internal/.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sync"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/browser"
	"github.com/go-widgets/application"
	"github.com/go-widgets/toolkit"
	"github.com/go-widgets/tray"
	"github.com/godbus/dbus/v5"

	"github.com/claimward/claimward-vpn-app-linux/assets"
	"github.com/claimward/claimward-vpn-app-linux/internal/trayview"
	"github.com/claimward/claimward-vpn-app-linux/internal/view"
	"github.com/claimward/claimward-vpn-app-linux/internal/viewmodel"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	hidden := flag.Bool("hidden", false, "start in the tray, without opening the window (for autostart)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println("claimward-app", version)
		return
	}

	cfg, err := appcore.LoadConfig()
	if err != nil {
		// A configuration that does not parse: start unconfigured, and let
		// the settings form write a good one.
		log.Printf("claimward: %s: %v", appcore.ConfigPath(), err)
		cfg = &appcore.Config{Provider: "github"}
	}
	core := appcore.New(cfg)
	disp := viewmodel.NewDispatcher()
	vm := viewmodel.New(core, viewmodel.Options{
		Dispatcher: disp,
		Open:       browser.Open,
		ConfigPath: appcore.ConfigPath(),
	})
	win := view.New(vm, disp)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go win.Run(ctx)
	vm.Start()

	openWindow := make(chan struct{}, 1)
	quit := make(chan struct{})
	var quitOnce sync.Once
	t := tray.New(assets.TrayPNG)
	haveTray := trayHost()
	if haveTray {
		win.Locked(func() {
			trayview.Bind(t, vm, assets.TrayPNG, trayview.Actions{
				OpenWindow: func() {
					select {
					case openWindow <- struct{}{}:
					default:
					}
				},
				Quit: func() { quitOnce.Do(func() { close(quit) }) },
			})
		})
		go func() {
			if err := t.Run(); err != nil {
				log.Printf("claimward: tray: %v", err)
			}
		}()
		// Quit leaves the tunnel as it is: the helper owns it.
		go func() {
			<-quit
			vm.Stop()
			t.Quit()
			os.Exit(0)
		}()
	} else {
		log.Print("claimward: no StatusNotifierItem host on the session bus: no tray icon, and closing the window quits")
	}

	show := !*hidden || !haveTray
	w, h := view.MinSize()
	for {
		if show {
			err := application.Run(application.Spec{
				Name:       "Claimward VPN",
				Identifier: "org.claimward.vpn",
				Version:    version,
				Icon:       assets.TrayPNG,
			}, application.Config{Title: "Claimward VPN", Width: float64(w), Height: float64(h)}, win, nil)
			if err != nil {
				log.Printf("claimward: window: %v", err)
			}
			closeWindow()
			// An "Open window" clicked while the window was open is not a
			// request to open another one now that it has closed.
			select {
			case <-openWindow:
			default:
			}
		}
		if !haveTray {
			vm.Stop()
			return
		}
		select {
		case <-openWindow:
			show = true
		case <-quit:
			select {} // the quit goroutine exits the process
		}
	}
}

// closeWindow takes down the window application.Run leaves behind.
//
// ⛔ go-widgets/application v0.x returns from Run when the window is closed
// but never closes the backend: on X11 the window stays mapped, frozen, with
// its connection open, until the garbage collector happens to finalize it.
// The one reference to the backend left after Run is the toolkit clipboard,
// which Run installed; closing it through that reference ends the connection,
// and the X server removes the window.
func closeWindow() {
	if c, ok := toolkit.CurrentClipboard().(io.Closer); ok {
		_ = c.Close()
	}
	toolkit.SetClipboard(nil)
}

// trayHost reports whether the desktop shows StatusNotifierItems: KDE, and
// GNOME with the AppIndicator extension, do; a bare GNOME does not, and a tray
// icon registered there is invisible. Without one, closing the window must
// quit, or the app would keep running with no way back to it.
func trayHost() bool {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	var has bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, "org.kde.StatusNotifierWatcher").Store(&has)
	return err == nil && has
}
