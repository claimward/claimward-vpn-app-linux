package view

import (
	"testing"

	"github.com/go-widgets/toolkit"
)

// settingsControls opens the settings form and returns what the host would embed.
func settingsControls(t *testing.T) (*fixture, []toolkit.NativeControl) {
	t.Helper()
	f := newFixture(t, signedOut())
	f.click(f.v.leaves.settings)
	return f, f.v.NativeControls()
}

func TestNativeControlsAreOneEntryPerTextInput(t *testing.T) {
	_, cs := settingsControls(t)
	want := []string{"server_url", "github_client_id", "oidc_issuer", "oidc_client_id"}
	var got []string
	for _, c := range cs {
		if c.Kind != toolkit.NativeEntry {
			t.Fatalf("control %q is kind %v, want NativeEntry", c.Key, c.Kind)
		}
		if c.Rect.W <= 0 || c.Rect.H <= 0 {
			t.Fatalf("control %q was never placed: %v", c.Key, c.Rect)
		}
		got = append(got, c.Key)
	}
	if len(got) != len(want) {
		t.Fatalf("native controls %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("native controls %v, want %v", got, want)
		}
	}
	if cs[0].Text != "https://vpn.example.org" {
		t.Fatalf("server control text %q", cs[0].Text)
	}
}

// Off the settings page the four inputs are still described, but none is
// Visible, so the host keeps its controls hidden instead of floating them over
// the status page.
func TestNoNativeControlIsVisibleOffTheSettingsPage(t *testing.T) {
	f := newFixture(t, signedOut())
	cs := f.v.NativeControls()
	if len(cs) != 4 {
		t.Fatalf("%d controls described, want 4", len(cs))
	}
	for _, c := range cs {
		if c.Visible {
			t.Fatalf("%q is visible on the status page: %+v", c.Key, c.Rect)
		}
	}
	f.click(f.v.leaves.settings)
	for _, c := range f.v.NativeControls() {
		if !c.Visible {
			t.Fatalf("%q is hidden on the settings page", c.Key)
		}
	}
}

func TestNativeTextReachesTheViewModelAndBack(t *testing.T) {
	f, cs := settingsControls(t)
	cs[0].OnText("https://other.example.org")
	if got := f.vm.ServerURL.Get(); got != "https://other.example.org" {
		t.Fatalf("view model server %q", got)
	}
	f.vm.ServerURL.Set("https://third.example.org")
	if got := f.v.NativeControls()[0].Text; got != "https://third.example.org" {
		t.Fatalf("native control did not follow the view model: %q", got)
	}
	if got := f.v.leaves.server.Text().Get(); got != "https://third.example.org" {
		t.Fatalf("fallback did not follow the view model: %q", got)
	}
}

func TestClaimedNativeStandsTheFallbackDown(t *testing.T) {
	f, cs := settingsControls(t)
	cs[0].OnClaim(true)
	if !f.v.leaves.nServer.Claimed().Get() {
		t.Fatal("OnClaim did not claim the region")
	}
	if cs[0].OnActivate == nil {
		t.Fatal("Return in a native field has no handler")
	}
	cs[0].OnText("not a url") // an invalid value: saving must refuse, not crash
	cs[0].OnActivate()        // Return saves the form, like Enter in the painted field
	f.v.Pump()
	if visible(f.v.settingsPage) && f.vm.SettingsError.Get() == "" {
		t.Fatal("Return in a native field did not run Save")
	}
}
