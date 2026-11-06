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
// value. Objects, the most common values, don't carry it (it would make each
// one bigger): objects made while a region runs are noted in regionMade instead.
var (
	genNow        atomic.Int64
	regionsActive atomic.Int32 // regions running in any engine in this program
	regionMade    sync.Map     // *Instance -> struct{}: objects made inside a region
)

func init() { genNow.Store(1) }

// mayChangeObject refuses a change to an object made before the running
// parallel region, if one is running.
func (in *Interp) mayChangeObject(o *Instance, line int) {
	if in.parallel.Load() > 0 {
		if _, made := regionMade.Load(o); !made {
			in.fail(line, "fast work can't change a %s made outside it, because other work may be reading it at the same time. Send a result instead, and collect it with fast each", o.Class.Name)
		}
	}
}

// mayChange refuses a change to something made before the running parallel
// region, if one is running.
func (in *Interp) mayChange(born int64, line int, what string) {
	if in.parallel.Load() > 0 && born < in.regionStart.Load() {
		in.fail(line, "fast work can't change %s made outside it, because other work may be reading it at the same time. Send a result instead, and collect it with fast each", what)
	}
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

// enterRegion starts a parallel region (or joins the running one, for a
// 'fast each' inside an item). It reports whether this call started it.
func (in *Interp) enterRegion() bool {
	if in.parallel.Add(1) == 1 {
		regionsActive.Add(1)
		in.regionStart.Store(genNow.Add(1))
		return true
	}
	return false
}

func (in *Interp) leaveRegion(started bool) {
	if started {
		in.regionJobs.Wait() // work started inside the region finishes inside it
		if regionsActive.Add(-1) == 0 {
			regionMade.Range(func(k, _ any) bool { // nothing runs in parallel now: forget them
				regionMade.Delete(k)
				return true
			})
		}
	}
	in.parallel.Add(-1)
}
