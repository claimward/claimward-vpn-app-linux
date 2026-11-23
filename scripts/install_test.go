package scripts

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Audit 2026-11-23: install.sh accepted an http:// server and wrote it into
// the helper's configuration, so the person's bearer token travelled in clear.
// The URL is judged before anything needs root, so this runs as anybody.
func TestInstallRefusesAServerThatIsNotHTTPS(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	for _, tc := range []struct {
		server, want string
	}{
		{"http://vpn.example.com", "must be an https URL"},
		{"ftp://vpn.example.com", "must be an https URL"},
		{"vpn.example.com", "must be an https URL"},
		{`https://vpn.example.com/"x`, "quotes or backslashes"},
		{`https://vpn.example.com/\x`, "quotes or backslashes"},
	} {
		out, err := exec.Command("sh", "install.sh", "--server", tc.server).CombinedOutput()
		if err == nil {
			t.Errorf("--server %q was accepted: %s", tc.server, out)
			continue
		}
		if !strings.Contains(string(out), tc.want) {
			t.Errorf("--server %q: %s, want %q", tc.server, out, tc.want)
		}
	}
}

// An https URL passes the check and stops at the next one (root).
func TestInstallAcceptsAnHTTPSServerUpToTheRootCheck(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	if os.Geteuid() == 0 {
		t.Skip("as root the installer would really run")
	}
	out, err := exec.Command("sh", "install.sh", "--server", "https://vpn.example.com").CombinedOutput()
	s := string(out)
	if err == nil {
		t.Fatalf("the installer ran as a user: %s", s)
	}
	if strings.Contains(s, "https URL") || strings.Contains(s, "quotes") {
		t.Errorf("an https URL was refused: %s", s)
	}
}
