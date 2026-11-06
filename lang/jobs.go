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

// startJob runs work at the same time as the rest of the program.
func (in *Interp) startJob(line int, work func() Value) *Job {
	j := &Job{done: make(chan struct{})}
	atomic.AddInt32(&in.fastLive, 1)
	in.wg.Add(1)
	go func() {
		in.gil.Lock()
		defer func() {
			if r := recover(); r != nil {
				if re, ok := r.(*RuntimeError); ok {
					j.err = re
				} else {
					j.err = &RuntimeError{Line: line, Msg: fmt.Sprint(r)}
				}
			}
			atomic.AddInt32(&in.fastLive, -1)
			close(j.done)
			in.gil.Unlock()
			in.wg.Done()
		}()
		j.value = work()
	}()
	in.tick(line) // let the work get started
	return j
}

// waitJob waits for a job, letting other work run in the meantime.
func (in *Interp) waitJob(line int, j *Job, timeout time.Duration) Value {
	in.gil.Unlock()
	var timedOut bool
	if timeout > 0 {
		select {
		case <-j.done:
		case <-time.After(timeout):
			timedOut = true
		}
	} else {
		<-j.done
	}
	in.gil.Lock()
	if timedOut {
		in.fail(line, "the job didn't finish within %s", timeout)
	}
	if j.err != nil {
		panic(&RuntimeError{Line: j.err.Line, Msg: j.err.Msg, Value: j.err.Value, Status: j.err.Status})
	}
	return j.value
}

// fastEach runs the block once per item on a pool of workers (one per CPU core),
// and gives back what each run sent, in the same order as the items.
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
	var next int64 = -1
	var wg sync.WaitGroup
	atomic.AddInt32(&in.fastLive, int32(workers))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer atomic.AddInt32(&in.fastLive, -1)
			for {
				i := int(atomic.AddInt64(&next, 1))
				if i >= len(items) {
					return
				}
				func() {
					in.gil.Lock()
					defer in.gil.Unlock()
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
					c, v := in.execBlock(x.Body, e)
					if c == cSend {
						results[i] = v
					}
				}()
			}
		}()
	}
	in.gil.Unlock()
	wg.Wait()
	in.gil.Lock()
	for _, err := range errs {
		if err != nil {
			panic(err)
		}
	}
	return &List{Items: results}
}
