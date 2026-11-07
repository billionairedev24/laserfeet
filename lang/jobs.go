package lang

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Job is work started with fast. wait gives its result, or raises its problem.
type Job struct {
	done  chan struct{}
	value Value
	err   *RuntimeError
}

// startJob runs work at the same time as the rest of the program: in
// parallel with other fast work, on every core, and between turns of the
// main code (see parallel.go). Started inside parallel work, it's done on the
// spot instead.
func (in *Interp) startJob(line int, work func() Value) *Job {
	j := &Job{done: make(chan struct{})}
	if in.parallel.Load() > 0 {
		// Inside parallel work (a fast each item, a web request), the work is
		// done right here, before going on. Run beside the code that started
		// it, both could change the values they share at the same time.
		func() {
			defer func() {
				if r := recover(); r != nil {
					if re, ok := r.(*RuntimeError); ok {
						j.err = re
					} else {
						j.err = &RuntimeError{Line: line, Msg: fmt.Sprint(r)}
					}
				}
				close(j.done)
			}()
			j.value = work()
		}()
		return j
	}
	atomic.AddInt32(&in.fastLive, 1)
	in.wg.Add(1)
	go func() {
		in.gil.RLock()
		in.enterRegion()
		defer func() {
			if r := recover(); r != nil {
				if re, ok := r.(*RuntimeError); ok {
					j.err = re
				} else {
					j.err = &RuntimeError{Line: line, Msg: fmt.Sprint(r)}
				}
			}
			in.leaveRegion(false)
			close(j.done)
			atomic.AddInt32(&in.fastLive, -1)
			in.gil.RUnlock()
			in.wg.Done()
		}()
		j.value = work()
	}()
	in.tick(line) // let the work get started
	return j
}

// waitJob waits for a job, letting other work run in the meantime.
func (in *Interp) waitJob(line int, j *Job, timeout time.Duration) Value {
	var timedOut bool
	in.outside(func() {
		if timeout > 0 {
			select {
			case <-j.done:
			case <-time.After(timeout):
				timedOut = true
			}
		} else {
			<-j.done
		}
	})
	if timedOut {
		in.fail(line, "the job didn't finish within %s", timeout)
	}
	if j.err != nil {
		panic(&RuntimeError{Line: j.err.Line, Msg: j.err.Msg, Value: j.err.Value, Status: j.err.Status})
	}
	return j.value
}

// fastEach runs the block once per item, truly in parallel on a pool of
// workers (one per CPU core), and gives back what each run sent, in the same
// order as the items. While the items run, this code holds the engine's lock
// and waits, so only the items run Laser Feet code; see parallel.go for why
// that, plus refusing changes to older values, keeps parallel work safe.
func (in *Interp) fastEach(x *FastEach, env *Env) Value {
	over := in.eval(x.Over, env)
	l, ok := over.(*List)
	if !ok {
		in.fail(x.Line, "'fast each' needs a list, but got %s", typeName(over))
	}
	items := append([]Value{}, l.Items...)
	results := make([]Value, len(items))
	errs := make([]*RuntimeError, len(items))
	workers := runtime.NumCPU()
	if workers > len(items) {
		workers = len(items)
	}
	body := in.compileBlock(x.Body)
	if in.NoCompile {
		body = func(e *Env) (ctrl, Value) { return in.execBlock(x.Body, e) }
	}
	// the code that started fast each waits while the items run, so it lets
	// go of the lock; each item holds it shared
	main := in.writing.Load()
	if main {
		in.unlockWhole()
	} else {
		in.gil.RUnlock()
	}
	started := in.enterRegion()
	var next int64 = -1
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			in.gil.RLock()
			defer in.gil.RUnlock()
			defer wg.Done()
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= len(items) {
					return
				}
				func() {
					defer func() {
						if r := recover(); r != nil {
							if re, ok := r.(*RuntimeError); ok {
								errs[i] = re
							} else {
								errs[i] = &RuntimeError{Line: x.Line, Msg: fmt.Sprint(r)}
							}
						}
					}()
					e := newScope(env)
					e.define(x.Var, items[i], false)
					c, v := body(e)
					if c == cSend {
						results[i] = v
					}
				}()
			}
		}()
	}
	wg.Wait()
	in.leaveRegion(started)
	if main {
		in.lockWhole()
	} else {
		in.gil.RLock()
	}
	for _, err := range errs {
		if err != nil {
			panic(err)
		}
	}
	return newList(results)
}
