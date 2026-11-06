package lang

import (
	"sync"
	"sync/atomic"
)

// Running Laser Feet on every core.
//
// Normally one piece of Laser Feet code runs at a time: the engine has one
// lock (the gil) that code holds while it runs, and lets go of while it waits
// for a database, the network or a timer, so other work runs meanwhile.
//
// 'fast each' runs its items truly in parallel, one per core. While it does,
// the code that started it holds the lock and waits, so nothing else runs
// Laser Feet code: only the items do. That makes one rule enough for safety:
// while items run, nothing made before they started can be changed. Each
// list, map, object and scope remembers when it was made (born), and changes
// to anything older than the parallel region are refused with a clear
// problem. Items still make and change their own values freely. (lf check
// already refuses most such changes; this also covers changes made inside
// beams that items call.)

// genNow counts parallel regions; every list, map and scope made records its
// value (born). Objects, the most common values, don't carry a field for it
// (it would make each one bigger): an object made inside a region gets one
// spare slot past its fields holding the region's stamp, which nothing else
// sees. Objects made outside regions have no spare slot.
var genNow atomic.Int64

func init() { genNow.Store(1) }

// regionStamp marks the objects made inside one parallel region.
type regionStamp struct{ gen int64 }

// regions is the engine's bookkeeping for parallel regions. Web requests may
// start and finish regions at the same time, so changes go through mu.
type regions struct {
	mu    sync.Mutex
	done  sync.Cond // signalled when fast work started inside a region finishes
	jobs  int       // fast work started inside the running region, still going
	stamp atomic.Pointer[regionStamp]
}

// madeInRegion says whether the object was made inside the running region.
func (in *Interp) madeInRegion(o *Instance) bool {
	n := len(o.Slots)
	if cap(o.Slots) == n {
		return false
	}
	st, ok := o.Slots[: n+1 : n+1][n].(*regionStamp)
	return ok && st.gen >= in.regionStart.Load()
}

// mayChangeObject refuses a change to an object made before the running
// parallel region, if one is running.
func (in *Interp) mayChangeObject(o *Instance, line int) {
	if in.parallel.Load() > 0 && !in.madeInRegion(o) {
		in.refuseChange(line, aOrAn(o.Class.Name))
	}
}

// mayChange refuses a change to something made before the running parallel
// region, if one is running.
func (in *Interp) mayChange(born int64, line int, what string) {
	if in.parallel.Load() > 0 && born < in.regionStart.Load() {
		in.refuseChange(line, what)
	}
}

// refuseChange explains why the change isn't allowed, for fast each or for a
// web request.
func (in *Interp) refuseChange(line int, what string) {
	if in.serving.Load() > 0 {
		in.fail(line, "a web request can't change %s made outside it, because other requests may be using it at the same time. Keep data that requests share in a store or a database", what)
	}
	in.fail(line, "fast work can't change %s made outside it, because other work may be reading it at the same time. Send a result instead, and collect it with fast each", what)
}

// outside runs slow work (a database, the network, a timer) without holding
// the engine's lock, so other work can run meanwhile. Inside a parallel region
// nothing holds the lock but the code waiting for the region, so the work just runs.
func (in *Interp) outside(work func()) {
	if in.parallel.Load() > 0 {
		work()
		return
	}
	in.gil.Unlock()
	defer in.gil.Lock()
	work()
}

// enterRegion starts a parallel region, or joins the running one (a 'fast
// each' inside an item, or web requests at the same time). It reports whether
// this call started it.
func (in *Interp) enterRegion() bool {
	r := &in.region
	r.mu.Lock()
	defer r.mu.Unlock()
	started := in.parallel.Load() == 0
	if started {
		gen := genNow.Add(1)
		in.regionStart.Store(gen)
		r.stamp.Store(&regionStamp{gen})
	}
	in.parallel.Add(1)
	return started
}

// leaveRegion leaves the region. With wait, it first waits for fast work
// started inside the region: that work runs without the lock, so it must
// finish while the region still holds everyone else off.
func (in *Interp) leaveRegion(wait bool) {
	r := &in.region
	r.mu.Lock()
	defer r.mu.Unlock()
	if wait {
		if r.done.L == nil {
			r.done.L = &r.mu
		}
		for r.jobs > 0 {
			r.done.Wait()
		}
	}
	if in.parallel.Add(-1) == 0 {
		r.stamp.Store(nil)
	}
}

func (in *Interp) regionJobStarted() {
	in.region.mu.Lock()
	in.region.jobs++
	in.region.mu.Unlock()
}

func (in *Interp) regionJobDone() {
	r := &in.region
	r.mu.Lock()
	r.jobs--
	if r.jobs == 0 && r.done.L != nil {
		r.done.Broadcast()
	}
	r.mu.Unlock()
}
