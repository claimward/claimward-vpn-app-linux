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
	"log"
	"os"
	"sync"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/go-widgets/application"
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
		Open:       application.OpenURL,
		ConfigPath: appcore.ConfigPath(),
	})
	win := view.New(vm, disp)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go win.Run(ctx)
	vm.Start()

	openWindow := make(chan struct{}, 1)
	requestWindow := func() {
		select {
		case openWindow <- struct{}{}:
		default:
		}
	}
	quit := make(chan struct{})
	var quitOnce sync.Once
	// The tray is this app's own, not application.Spec.Tray. Spec.Tray
	// builds its menu once and keeps the *tray.Tray to itself, so the menu
	// and the icon could not follow the view model, and it quits the tray
	// when the window's Run returns, while here the app lives on in the tray
	// after its window has closed and opens a new one on "Open window".
	//
	// It is Attached, not Run on a goroutine: since tray v0.14.0 Attach is
	// implemented on Linux and returns once the item is on the session bus,
	// so a tray that cannot be put up is known here, and the app then quits
	// with its window rather than living on with no way back to it.
	t := tray.New(assets.TrayPNG)
	haveTray := trayHost()
	if !haveTray {
		log.Print("claimward: no StatusNotifierItem host on the session bus: no tray icon, and closing the window quits")
	}
	if haveTray {
		win.Locked(func() {
			trayview.Bind(t, vm, assets.TrayPNG, trayview.Actions{
				OpenWindow: requestWindow,
				Quit:       func() { quitOnce.Do(func() { close(quit) }) },
			})
		})
		if err := t.Attach(); err != nil {
			log.Printf("claimward: tray: %v: closing the window quits", err)
			haveTray = false
		} else {
			// Events (tunnel dropped, session expired) become desktop
			// notifications; a click brings the window back.
			trayview.BindNotifications(t, vm, requestWindow, log.Printf)
		}
	}
	if haveTray {
		// Quit leaves the tunnel as it is: the helper owns it.
		go func() {
			<-quit
			vm.Stop()
			t.Quit()
			os.Exit(0)
		}()
	}

	show := !*hidden || !haveTray
	w, h := view.MinSize()
	for {
		if show {
			run := func() error {
				return application.Run(application.Spec{
					Name:       "Claimward VPN",
					Identifier: "org.claimward.vpn",
					Version:    version,
					Icon:       assets.TrayPNG,
				}, application.Config{Title: "Claimward VPN", Width: float64(w), Height: float64(h)}, win, nil)
			}
			err := run()
			if err != nil && os.Getenv("GO_WIDGETS_GTK") != "" {
				// The GTK4 host (native text fields) needs libgtk-4. Without
				// it the window cannot open, so fall back once to the
				// built-in X11/Wayland backend and its painted fields.
				log.Printf("claimward: GTK4 window: %v: falling back to the built-in backend (painted text fields)", err)
				os.Unsetenv("GO_WIDGETS_GTK")
				err = run()
			}
			if err != nil {
				log.Printf("claimward: window: %v", err)
			}
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
