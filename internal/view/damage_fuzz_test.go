package view

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/claimward/claimward-vpn-client/pkg/hproto"
	"github.com/go-widgets/toolkit"
)

// explain names where a presented frame is stale and the widgets there, so a
// failure says which one reported too little.
func explain(v *View, shown []byte) string {
	x0, y0, x1, y1 := 1<<30, 1<<30, -1, -1
	for i := range shown {
		if shown[i] != v.buf[i] {
			x, y := (i/4)%v.w, (i/4)/v.w
			x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
		}
	}
	out := fmt.Sprintf("stale x %d-%d y %d-%d of %dx%d at scale %v; widgets there:", x0, x1, y0, y1, v.w, v.h, v.scale)
	var walk func(w toolkit.Widget, depth int)
	walk = func(w toolkit.Widget, depth int) {
		if b := w.Bounds(); b.Contains(x0, y0) || b.Contains(x1, y1) {
			out += fmt.Sprintf("\n  %*s%T %v", depth*2, "", w, b)
		}
		if cp, ok := w.(interface{ Children() []toolkit.Widget }); ok {
			for _, c := range cp.Children() {
				walk(c, depth+1)
			}
		}
	}
	walk(v.root, 0)
	return out
}

// An open list floats over the window outside its widget: it is damaged where
// it is drawn while it shows, and where it was when it goes.
func TestAnOpenPopoverIsDamagedWhereItIsDrawnAndWhereItWas(t *testing.T) {
	st := signedIn()
	st.TenantRequired = true
	st.Tenants = []hproto.Tenant{{ID: "acme", Name: "Acme"}, {ID: "lab", Name: "Lab"}}
	f := newFixture(t, st)
	var o host
	o.present(f.v)
	same := func(what string) {
		t.Helper()
		o.present(f.v)
		if !bytes.Equal(o.shown, f.v.buf) {
			t.Fatalf("%s: %s", what, explain(f.v, o.shown))
		}
	}
	dd := f.v.leaves.tenants
	f.click(dd)
	same("the list opening")
	pb := dd.PopoverBounds()
	f.v.MouseMove(pb.X+pb.W/2, pb.Y+pb.H/2)
	same("the pointer over the list")
	f.v.MouseDown(1, 1) // anywhere else closes it
	f.v.MouseUp(1, 1)
	same("the list closing")
}

// FuzzIncrementalPresent: after any sequence of view-model states, pointer
// moves, presses, keys, wheel turns, frames nobody presented, theme changes
// and resizes (above and below the layout's minimum), what a damage-driven host
// shows equals the full frame, pixel for pixel. The seed picks the sequence.
func FuzzIncrementalPresent(f *testing.F) {
	for _, s := range []int64{1, 2, 3, 7, 42, 99, 1234} {
		f.Add(s, uint8(60))
	}
	f.Fuzz(func(t *testing.T, seed int64, steps uint8) {
		r := rand.New(rand.NewSource(seed))
		fx := newFixture(t, randomStatus(r))
		var o host
		o.present(fx.v)
		for step := 0; step < int(steps%90); step++ {
			op := r.Intn(14)
			switch op {
			case 0: // a resize, at the minimum or above, or below it
				w, h := 400+r.Intn(500), 600+r.Intn(500)
				if r.Intn(2) == 0 {
					w, h = 480+r.Intn(400), 760+r.Intn(300)
				}
				fx.v.Resize(w, h, 1+float64(r.Intn(2)))
			case 1, 2, 3: // the pointer
				fx.v.MouseMove(r.Intn(fx.v.w), r.Intn(fx.v.h))
			case 4:
				fx.v.MouseDown(r.Intn(fx.v.w), r.Intn(fx.v.h))
				fx.v.MouseMove(r.Intn(fx.v.w), r.Intn(fx.v.h))
				fx.v.MouseUp(r.Intn(fx.v.w), r.Intn(fx.v.h))
				fx.v.Pump()
			case 5:
				fx.v.Scroll(r.Intn(200) - 100)
			case 6:
				fx.v.Key("Tab", 0)
			case 7: // frames the host skips
				fx.v.Frame()
			default: // a new state
				fx.svc.mu.Lock()
				fx.svc.st = randomStatus(r)
				fx.svc.mu.Unlock()
				fx.poll()
			}
			if r.Intn(3) != 0 { // the host does not ask after every frame
				o.present(fx.v)
				if !bytes.Equal(o.shown, fx.v.buf) {
					t.Fatalf("seed %d step %d op %d: the incrementally presented frame differs from a full repaint: %s", seed, step, op, explain(fx.v, o.shown))
				}
			}
		}
		fx.v.Frame()
		o.present(fx.v)
		if !bytes.Equal(o.shown, fx.v.buf) {
			t.Fatalf("seed %d: the last frame differs from a full repaint", seed)
		}
	})
}
