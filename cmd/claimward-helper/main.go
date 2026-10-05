// Command claimward-helper is the Claimward privileged helper for Linux: a
// root daemon, run by systemd, that brings the WireGuard tunnel up and down
// for the app over a unix socket only root and the claimward group can reach.
//
// See internal/helperd for the daemon and deploy/ for its unit and
// configuration.
package main

import (
	"os"

	"github.com/claimward/claimward-vpn-app-linux/internal/helperd"
)

func main() { os.Exit(helperd.Main(os.Args[1:], os.Stderr)) }
