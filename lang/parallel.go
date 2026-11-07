package lang

import (
	"sync"
	"sync/atomic"
)

// Running Laser Feet on every core.
//
// Two kinds of code run Laser Feet:
//
//   - the main code (launch, tests, the top of a file) holds the engine's
//     lock whole (gil.Lock) while it runs;
//   - parallel work (fast jobs, fast each items, web requests) holds the lock
//     shared (gil.RLock), all at the same time, one per core.
//
// The two never run at once: the main code runs between parallel work (while
// it waits, or when it's given a turn), and parallel work runs while the main
// code waits. Every piece lets go of the lock while it waits for a database,
// the network or a timer.
//
// While parallel work is running (a "parallel region"), one rule keeps it
// safe: parallel work can't change anything made before the region started,
// or made by the main code during it. Each list, map, object and scope
// remembers when it was made (born). Parallel work makes and changes its own
// values freely; it can't reach another piece's, because it can't put them
// anywhere shared. The main code may change anything, since nothing runs
// beside it; what it makes during a region is marked with an odd born, so
// parallel work can't change it. (lf check already refuses most such changes;
// this also covers changes made inside beams that parallel work calls.)

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
	stamp atomic.Pointer[regionStamp] // what objects made now are stamped with (nil: none)
	live  *regionStamp                // the running region's stamp
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
	if in.parallel.Load() > 0 && !in.writing.Load() && !in.madeInRegion(o) {
		in.refuseChange(line, aOrAn(o.Class.Name))
	}
}

// mayChange refuses a change to something made before the running parallel
// region, if one is running.
func (in *Interp) mayChange(born int64, line int, what string) {
	if in.parallel.Load() > 0 && !in.writing.Load() && (born < in.regionStart.Load() || born&1 == 1) {
		in.refuseChange(line, what)
	}
}

// mayChangeName is mayChange for a name, saying it only when refusing.
func (in *Interp) mayChangeName(born int64, line int, name string) {
	if in.parallel.Load() > 0 && !in.writing.Load() && (born < in.regionStart.Load() || born&1 == 1) {
		in.refuseChange(line, "'"+name+"'")
	}
}

// refuseChange explains why the change isn't allowed, for fast each or for a
// web request.
func (in *Interp) refuseChange(line int, what string) {
	if in.serving.Load() > 0 {
		in.fail(line, "a web request can't change %s made outside it, because other requests may be using it at the same time. Keep data that requests share in a store or a database", what)
	}
	in.fail(line, "fast work can't change %s made outside it, because other work may be reading it at the same time. Send a result instead, and collect it with wait or fast each", what)
}

// outside runs slow work (a database, the network, a timer) without holding
// the engine's lock, so other work can run meanwhile.
func (in *Interp) outside(work func()) {
	if in.writing.Load() { // the main code: nothing else runs Laser Feet now
		in.unlockWhole()
		defer in.lockWhole()
	} else {
		in.gil.RUnlock()
		defer in.gil.RLock()
	}
	work()
}

// lockWhole takes the lock for the main code.
func (in *Interp) lockWhole() {
	in.writerWaiting.Add(1)
	in.gil.Lock()
	in.writerWaiting.Add(-1)
	in.mainTurn(true)
}

// unlockWhole lets go of it.
func (in *Interp) unlockWhole() {
	in.mainTurn(false)
	in.gil.Unlock()
}

// mainTurn starts or ends a turn of the main code. During a parallel region
// its values are born odd (parallel work can't change them) and its objects
// carry no stamp; parallel work's values are born even.
func (in *Interp) mainTurn(on bool) {
	in.writing.Store(on)
	r := &in.region
	r.mu.Lock()
	defer r.mu.Unlock()
	if in.parallel.Load() == 0 {
		return
	}
	toParity(on)
	if on {
		r.stamp.Store(nil)
	} else {
		r.stamp.Store(r.live)
	}
}

// toParity moves genNow on to the next odd (or even) value.
func toParity(odd bool) {
	for {
		g := genNow.Load()
		n := g + 1
		if (n&1 == 1) != odd {
			n++
		}
		if genNow.CompareAndSwap(g, n) {
			return
		}
	}
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
		toParity(false)
		gen := genNow.Load()
		in.regionStart.Store(gen)
		r.live = &regionStamp{gen}
		if in.writing.Load() {
			toParity(true) // the main code carries on: its values stay out of reach
		} else {
			r.stamp.Store(r.live)
		}
	}
	in.parallel.Add(1)
	return started
}

// leaveRegion leaves the region. (Fast work started inside a region is done
// on the spot, so nothing started inside can outlive it.)
func (in *Interp) leaveRegion(bool) {
	r := &in.region
	r.mu.Lock()
	defer r.mu.Unlock()
	if in.parallel.Add(-1) == 0 {
		r.stamp.Store(nil)
		r.live = nil
	}
}
