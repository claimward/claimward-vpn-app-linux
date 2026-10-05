// Package assets holds the Claimward brand files the app ships, copied from
// github.com/claimward/brand: the application icon (claimward.svg, the badge),
// the mark shown in the window (claimward-mark.svg) and the tray icon
// (tray.png, the 32 px favicon).
package assets

import _ "embed"

// Icon is the application icon (white mark on the brand teal), installed for
// the .desktop entry.
//
//go:embed claimward.svg
var Icon []byte

// Mark is the brand mark, teal on transparent, shown in the window header.
//
//go:embed claimward-mark.svg
var Mark string

// TrayPNG is the tray icon.
//
//go:embed tray.png
var TrayPNG []byte
