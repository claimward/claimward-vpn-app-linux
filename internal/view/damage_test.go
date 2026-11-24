package view

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/claimward/claimward-vpn-client/pkg/appcore"
	"github.com/claimward/claimward-vpn-client/pkg/hproto"
	"github.com/go-widgets/toolkit"
)

// host is a window that presents only what DamageRects says changed, the way
// application's surface does: a retained framebuffer, updated rectangle by
// rectangle, or whole when no rectangle is reported.
type host struct {
	shown []byte
	w, h  int
	rects int // rectangles presented, 0 for a whole-frame present
	bytes int // bytes presented
}

func (o *host) present(v *View) {
	pix, w, h, _ := v.Frame()
	damage := v.DamageRects()
	if len(o.shown) != len(pix) || o.w != w || o.h != h {
		o.shown, o.w, o.h = make([]byte, len(pix)), w, h
		damage = nil
	}
	if len(damage) == 0 {
		copy(o.shown, pix)
		o.bytes += len(pix)
		return
	}
	for _, r := range damage {
		for y := r.Y; y < r.Y+r.H; y++ {
			a := (y*w + r.X) * 4
			copy(o.shown[a:a+r.W*4], pix[a:a+r.W*4])
			o.bytes += r.W * 4
		}
		o.rects++
	}
}

func randomStatus(r *rand.Rand) appcore.Status {
	st := appcore.Status{
		ConfigOK: r.Intn(8) != 0, HelperInstalled: r.Intn(4) != 0,
		LoggedIn: r.Intn(3) != 0, ServerURL: "https://vpn.example.org",
	}
	if !st.ConfigOK {
		st.ConfigError = "missing config: server_url"
	}
	if st.LoggedIn {
		st.Email = fmt.Sprintf("user%d@example.org", r.Intn(100))
		st.Connected = r.Intn(2) == 0
		if st.Connected {
			st.AssignedIP, st.Interface = fmt.Sprintf("10.80.0.%d/32", r.Intn(250)), "wg0"
		}
		for i := r.Intn(4); i > 0; i-- {
			st.Tenants = append(st.Tenants, hproto.Tenant{ID: fmt.Sprintf("t%d", i), Name: fmt.Sprintf("Tenant %d", i)})
		}
		st.TenantRequired = len(st.Tenants) > 1 && r.Intn(2) == 0
	} else if r.Intn(3) == 0 {
		st.DeviceUserCode, st.DeviceVerificationURI = fmt.Sprintf("ABCD-%04d", r.Intn(10000)), "https://github.com/login/device"
	}
	for i := r.Intn(30); i > 0; i-- {
		st.Log = append(st.Log, fmt.Sprintf("12:%02d:%02d event %d", r.Intn(60), r.Intn(60), r.Intn(1000)))
	}
	return st
}

// After any run of view-model updates, pointer moves and resizes, what a
// damage-driven host shows equals the full frame, pixel for pixel.
func TestIncrementalPresentEqualsAFullRepaint(t *testing.T) {
	for seed := int64(1); seed <= 3; seed++ {
		r := rand.New(rand.NewSource(seed))
		f := newFixture(t, randomStatus(r))
		var o host
		o.present(f.v)
		for step := 0; step < 60; step++ {
			switch r.Intn(10) {
			case 0: // a resize, sometimes to the same number of pixels
				w, h := 400+r.Intn(300), 600+r.Intn(300)
				if r.Intn(3) == 0 {
					w, h = h, w
				}
				f.v.Resize(w, h, 1+float64(r.Intn(2)))
			case 1, 2: // the pointer
				f.v.MouseMove(r.Intn(f.v.w), r.Intn(f.v.h))
			case 3: // a press and release somewhere
				f.v.MouseDown(r.Intn(f.v.w), r.Intn(f.v.h))
				f.v.MouseUp(r.Intn(f.v.w), r.Intn(f.v.h))
				f.v.Pump()
			default:
				f.svc.mu.Lock()
				f.svc.st = randomStatus(r)
				f.svc.mu.Unlock()
				f.poll()
			}
			o.present(f.v)
			if !bytes.Equal(o.shown, f.v.buf) {
				t.Fatalf("seed %d step %d: the incrementally presented frame differs from a full repaint", seed, step)
			}
		}
	}
}

// Two frames painted before the host asks keep both frames' damage.
func TestDamageAccumulatesAcrossFramesThePresenterSkipped(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	f := newFixture(t, randomStatus(r))
	var o host
	o.present(f.v)
	for i := 0; i < 40; i++ {
		for k := r.Intn(3); k >= 0; k-- { // frames nobody presented
			f.svc.mu.Lock()
			f.svc.st = randomStatus(r)
			f.svc.mu.Unlock()
			f.poll()
			f.v.Frame()
		}
		o.present(f.v)
		if !bytes.Equal(o.shown, f.v.buf) {
			t.Fatalf("round %d: stale pixels after skipped frames", i)
		}
	}
}

// A frame with nothing new to report, and a resize, are the whole frame.
func TestNothingPendingAndAResizeReportTheWholeFrame(t *testing.T) {
	f := newFixture(t, signedIn())
	f.v.Frame()
	if got := f.v.DamageRects(); got != nil {
		t.Fatalf("first frame: %v, want the whole frame (nil)", got)
	}
	if got := f.v.DamageRects(); got != nil {
		t.Fatalf("no frame pending: %v, want nil", got)
	}
	f.v.Resize(winW, winH, 1) // same size: nothing to redo
	f.v.Frame()
	f.v.Resize(winW+8, winH, 1)
	f.v.Frame()
	if got := f.v.DamageRects(); got != nil {
		t.Fatalf("after a resize: %v, want nil", got)
	}
}

// BenchmarkPresent measures one frame of a window whose log grows by a line:
// the CPU of painting and finding the damage, and the bytes a host presents.
func BenchmarkPresent(b *testing.B) {
	st := signedIn()
	st.Connected = true
	f := newFixture(b, st)
	svc := f.svc
	var o host
	o.present(f.v)
	o.bytes, o.rects = 0, 0
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		st.Log = append(st.Log, fmt.Sprintf("12:00:%02d event %d", i%60, i))
		if len(st.Log) > 200 {
			st.Log = st.Log[1:]
		}
		svc.mu.Lock()
		svc.st = st
		svc.mu.Unlock()
		f.poll()
		o.present(f.v)
	}
	b.ReportMetric(float64(o.bytes)/float64(b.N), "B-presented/frame")
}

var _ = toolkit.Rect{}
