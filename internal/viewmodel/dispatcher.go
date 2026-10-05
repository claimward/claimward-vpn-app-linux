package viewmodel

import "sync"

// Dispatcher hands work to the UI goroutine.
//
// mvvm observables are not safe for concurrent use: they must be Set on the
// goroutine that owns the widgets bound to them. The view model's slow work —
// a status poll that dials the helper, a sign-in that waits for the person in
// their browser, a connection the helper takes seconds to bring up — runs on
// goroutines of its own, and what it learns comes back through Post. The view
// drains the queue on its goroutine, under the same lock it draws with.
type Dispatcher struct {
	mu   sync.Mutex
	q    []func()
	wake chan struct{}
}

// NewDispatcher returns an empty dispatcher.
func NewDispatcher() *Dispatcher {
	return &Dispatcher{wake: make(chan struct{}, 1)}
}

// Post queues fn for the UI goroutine. It never blocks and never runs fn
// itself: fn runs at the next Drain.
func (d *Dispatcher) Post(fn func()) {
	d.mu.Lock()
	d.q = append(d.q, fn)
	d.mu.Unlock()
	select {
	case d.wake <- struct{}{}:
	default: // a wake-up is already pending; it will see this one too
	}
}

// Wake is signalled after a Post, so a UI loop can sleep until there is work.
func (d *Dispatcher) Wake() <-chan struct{} { return d.wake }

// Pending reports whether work is queued.
func (d *Dispatcher) Pending() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.q) > 0
}

// Drain runs everything queued, in order, on the caller's goroutine, and
// reports how many functions ran. Work posted while draining runs too.
func (d *Dispatcher) Drain() int {
	n := 0
	for {
		d.mu.Lock()
		q := d.q
		d.q = nil
		d.mu.Unlock()
		if len(q) == 0 {
			return n
		}
		for _, fn := range q {
			fn()
			n++
		}
	}
}
