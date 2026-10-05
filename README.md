# claimward-vpn-app-linux

[![CI](https://github.com/claimward/claimward-vpn-app-linux/actions/workflows/ci.yml/badge.svg)](https://github.com/claimward/claimward-vpn-app-linux/actions/workflows/ci.yml)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

The Claimward VPN client for Linux: a **window and a tray icon drawn in pure Go**
with [go-widgets](https://github.com/go-widgets) — no webview, no GTK, no cgo
(`CGO_ENABLED=0`) — and a **privileged helper** run by systemd that owns the
WireGuard tunnel.

It signs you in (GitHub, OIDC or a go-authn provider), lets you choose a tenant
when you belong to several, and connects you to a
[claimward-vpn-server](https://github.com/claimward/claimward-vpn-server). The
logic is the same as the macOS app's: both use the shared core in
[claimward-vpn-client](https://github.com/claimward/claimward-vpn-client)
(`pkg/appcore` for the app, `pkg/helper` for the helper).

## How it's put together

```
┌────────────────────────── claimward-app (your session) ──────────────────────────┐
│  internal/view        go-widgets/toolkit widgets, bound with go-widgets/mvvmtk    │
│  internal/trayview    tray icon + menu (StatusNotifierItem over D-Bus)            │
│        │  bind                                                                    │
│        ▼                                                                          │
│  internal/viewmodel   all state as go-widgets/mvvm observables and commands       │
│        │                                                                          │
│        ▼                                                                          │
│  appcore (claimward-vpn-client)   sign-in, tenant, session, drives the helper     │
└───────────────┬────────────────────────────────────────┬─────────────────────────┘
                │ X11 / Wayland                           │ unix socket, JSON
                ▼ (go-widgets/application)                ▼ 0660 root:claimward
   ┌─────────────────────────┐              ┌────────────────────────────────────┐
   │ the window               │              │ claimward-helper (root, systemd)   │
   │ the tray (SNI host)      │              │ pkg/helper: enroll with a server   │
   └─────────────────────────┘              │ it is configured for, wireguard-go │
                                             │ TUN + ip(8), route pushes          │
                                             └────────────────────────────────────┘
```

- **The app** runs as you. It never touches the network configuration and never
  talks to the VPN server itself: everything goes through the helper, as on
  macOS.
- **MVVM throughout.** `internal/viewmodel` holds every piece of state as an
  `mvvm.Observable`, `mvvm.ObservableList` or `mvvm.Command`, and imports no
  widget. The window and the tray bind to it once, at start-up; nothing copies
  state into a widget on a frame. Slow work (the status poll every 2 s, sign-in,
  connecting) runs on goroutines and posts its result back to the UI goroutine,
  because observables are single-threaded.
- **No hand-drawn UI.** Every visible element is a toolkit widget (labels,
  buttons, entries, drop-downs, list); CI enforces it with
  [bricolint](https://github.com/go-widgets/bricolint), and MVVM with
  [mvvmlint](https://github.com/go-widgets/mvvmlint).
- **The helper** is the only root process. Its systemd unit drops every
  capability but `CAP_NET_ADMIN`, `CAP_NET_RAW` and `CAP_CHOWN`, and sees the file
  system read-only except `/run`.

## Layout

| Path | What |
|------|------|
| `cmd/claimward-app` | the app: wires the core, view model, window and tray, runs the window loop |
| `cmd/claimward-helper` | the privileged helper |
| `internal/viewmodel` | the state: observables, commands, the status poll |
| `internal/view` | the window: widgets, bindings, the `application.Handler` |
| `internal/trayview` | the tray icon and menu |
| `internal/helperd` | the helper daemon around `pkg/helper` |
| `assets/` | icon, mark and tray icon, from [claimward/brand](https://github.com/claimward/brand) |
| `deploy/` | the systemd unit, a sample `helper.json`, the `.desktop` entry |
| `scripts/` | `install.sh`, `uninstall.sh` |
| `hack/` | the lint negative control CI runs |

## What it does

- **Status**: who is signed in, connected or not, the assigned address and
  interface, the session's tenant, whether the helper answers, the server.
- **Sign in**: runs the provider's flow; for a device flow the window shows the
  code and an *Open sign-in page* button (`xdg-open`), and the sign-in can be
  cancelled.
- **Tenant**: a person in one tenant never chooses. When the server refuses a
  connection because the person belongs to several, the window says so and offers
  a drop-down of the tenants; *Choose tenant* asks the server for the list ahead
  of time. The tenant cannot be changed while connected.
- **Connect / Disconnect / Sign out.**
- **Settings**: server URL, provider (`github`, `oidc`, `go-authn`), GitHub
  client ID, OIDC issuer and client ID. An incomplete configuration is refused
  with what is missing.
- **Connection log**, following its newest line.
- **Tray**: a status line, *Connect*, *Disconnect*, *Open window*, *Quit*. The icon
  is in colour while connected and grey otherwise. Closing the window leaves the
  app in the tray; *Quit* leaves the tunnel as it is (the helper owns it).

The tray needs a StatusNotifierItem host: KDE Plasma, or GNOME with the
*AppIndicator and KStatusNotifierItem Support* extension, sway/waybar, and so on.
Without one the app says so on start-up and closing the window quits it.

## Install

Build as yourself (Go 1.27.1 or later), then install as root:

```sh
CGO_ENABLED=0 go build -trimpath -o bin/ ./cmd/...
sudo ./scripts/install.sh --server https://vpn.example.com
```

`install.sh`:

1. creates the `claimward` group and adds you to it (`$SUDO_USER`, or `--user`):
   **log out and back in** for the membership to apply;
2. installs `claimward-app` to `/usr/local/bin` and `claimward-helper` to
   `/usr/local/sbin` (`--prefix` to change);
3. writes `/etc/claimward/helper.json`, **owned by root:root, mode 0644**, with the
   server you gave (an existing file is kept);
4. installs and starts `claimward-helper.service`, and waits for its socket;
5. installs the desktop entry and the icon.

Then start **Claimward VPN** from your desktop's menu. `claimward-app -hidden`
starts in the tray without opening the window (for an autostart entry).

`sudo ./scripts/uninstall.sh` removes it all and stops the helper, which takes
the tunnel down; `--purge` also removes `/etc/claimward` and the group. Your own
configuration in `~/.config` is left alone.

## Configure

### The app

`~/.config/Claimward/config.json`, written by the settings form (or by hand), mode
0600. `CLAIMWARD_*` environment variables override it, as in the other apps.

```json
{
  "server_url": "https://vpn.example.com",
  "provider": "github",
  "github_client_id": "Iv1.0123456789abcdef"
}
```

For OIDC or [go-authn](https://github.com/go-authn/bridge), set `"provider"` to
`"oidc"` or `"go-authn"` and give `"oidc_issuer"` and `"oidc_client_id"` instead.
`"socket_path"` overrides the helper's socket.

The session (tokens and the device's WireGuard private key) is kept in
`~/.config/claimward/session.json`, mode 0600.

### The helper

`/etc/claimward/helper.json`:

```json
{
  "servers": ["https://vpn.example.com"],
  "group": "claimward",
  "socket": "/var/run/claimward-helper.sock"
}
```

| Key | Meaning |
|-----|---------|
| `servers` | **required**: the servers the helper may enroll with. The app's `server_url` must be one of them. |
| `group` | who may use the socket beside root (default `claimward`) |
| `socket` | where it listens (default `/var/run/claimward-helper.sock`) |

After editing it: `sudo systemctl restart claimward-helper`.

## Security notes

- **The helper is pinned to its configured servers.** Anything that can reach its
  socket can ask it to connect, so it enrolls only with a server `helper.json`
  names and takes no tunnel configuration from the request: the tunnel is what
  that server answered. A local process cannot point it at a server of its own
  and route the machine's traffic there.
- **`helper.json` must be owned by root and writable by root alone**; the helper
  refuses to start otherwise (whoever writes it chooses the servers trusted).
- **The socket is 0660 root:claimward**, created in a root-only directory with a
  restrictive umask so it never exists with a wider mode. Only members of the
  group can use it: being in `claimward` means being allowed to bring the VPN up
  and down, so add only the people who use this machine's VPN.
- **The unit is hardened**: capabilities limited to `CAP_NET_ADMIN CAP_NET_RAW
  CAP_CHOWN`, `NoNewPrivileges`, `ProtectSystem=strict` with `/run` writable,
  `ProtectHome`, only `/dev/net/tun` from `/dev`, address families limited to
  unix, inet, inet6 and netlink, `@system-service` system calls. `systemd-analyze
  security claimward-helper` rates it 2.5 (OK).
- **Stopping the helper takes the tunnel down** (SIGTERM).
- The session file holds a bearer token and the device's private key, mode 0600 in
  your home directory. It is not yet in a keyring.

## Development

```sh
GOWORK=off go vet ./...
test -z "$(gofmt -l .)"
GOWORK=off go test -race ./...
GOWORK=off go test -coverprofile=coverage.out ./internal/... && go tool cover -func=coverage.out | tail -1   # 100.0%
```

The view model is tested with no display: sign-in (the device-flow prompt,
cancel, failure), the tenant choice (the refusal that asks for one, choosing,
a status poll that must not change it), connect errors, settings, the log. The
view is tested by driving the real widget tree — clicks at the widgets' places,
typed keys, Tab, paste, the drop-down — and the tray against go-widgets/tray's
headless back-end. `cmd/` is outside the coverage gate: it only wires those
together and opens the window, a launch-verified boundary (go-widgets/application
excludes its own run loop the same way).

The UI gates, as CI runs them:

```sh
go install github.com/go-widgets/mvvmlint/cmd/mvvmlint@v0.4.0
go install github.com/go-widgets/bricolint/cmd/bricolint@v0.4.0
go vet -vettool="$(go env GOPATH)/bin/mvvmlint" ./...
go vet -vettool="$(go env GOPATH)/bin/bricolint" ./...
bash hack/lint-negative-control.sh   # proves each guard still fails on a real leak
```

## Status

Verified in an Ubuntu 24.04 (arm64) virtual machine, with Xvfb, openbox and a
StatusNotifierItem host on a D-Bus session bus, against a stand-in server: the
installer, the hardened unit, the socket's permissions (a user outside the group
is refused), a configuration others could write refused, connecting with the
tenant choice (a real TUN device, address and route), disconnecting, stopping
the helper taking the tunnel down, editing the settings from the keyboard, the
tray menu over D-Bus (status line, *Connect*, *Open window*, *Quit*), closing the
window to the tray and reopening it.

Not verified yet: a real desktop (GNOME with the AppIndicator extension, KDE),
Wayland, HiDPI on a real screen, a sign-in against a real identity provider, and
a real claimward-vpn-server.

## License

BSD 3-Clause — see [LICENSE](LICENSE).
