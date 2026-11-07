package lang

// The whole-number tier. A beam that takes Ints, gives an Int, and inside only
// works with Int values (arithmetic, comparisons, when, each over a range,
// and calls to beams like itself) is also compiled to code that keeps its
// numbers as plain machine integers in a small stack, with no boxing and no
// name lookups. Recursive and numeric code runs several times faster.
//
// It never changes what a program means. The code has no side effects, so on
// anything unusual (a number too big, dividing by zero, a beam that finishes
// without sending, a callee that isn't a whole-number beam) it gives up and
// the call is run again by the ordinary engine, which gives the usual answer
// or the usual message. A beam that gave up once doesn't try again.

import (
	"sync/atomic"
)

type istack struct{ d []int64 }

type (
	iexpr func(s *istack, b int) int64
	icond func(s *istack, b int) bool
	// istmt runs a statement; it reports what happened (cNone, cSend, cStop
	// or cSkip) and the value sent.
	istmt func(s *istack, b int) (ctrl, int64)
)

type intCode struct {
	n      int // numbers in a frame: inputs first, then names made inside
	body   istmt
	broken atomic.Bool // gave up once: use the ordinary engine from now on
}

// bail stops whole-number code; the call is run again by the engine.
type bail struct{}

func giveUp() { panic(bail{}) }

// intTier compiles f for the whole-number tier, or gives nil when it can't be.
func (in *Interp) intTier(f *Beam) *intCode {
	if f.Self != nil || f.Sig == nil || len(f.Sig.TypeParams) > 0 || f.Expr != nil && f.Body != nil {
		return nil
	}
	ptypes, rtype := in.sigTypes(f)
	if rtype == nil || rtype.Kind != KInt || len(ptypes) != len(f.Params) {
		return nil
	}
	for _, t := range ptypes {
		if t == nil || t.Kind != KInt {
			return nil
		}
	}
	c := &intCompiler{in: in, slots: map[string]int{}}
	for _, p := range f.Params {
		c.slot(p)
	}
	set := map[string]bool{}
	for _, p := range f.Params {
		set[p] = true
	}
	body := f.Body
	if f.Expr != nil {
		body = []Stmt{&SendStmt{Line: lineOf(f.Expr), Value: f.Expr}}
	}
	code := &intCode{}
	run, ok := c.block(body, set, code)
	if !ok {
		return nil
	}
	code.body, code.n = run, len(c.slots)
	return code
}

func lineOf(x Expr) int {
	if l, ok := x.(interface{ Ln() int }); ok {
		return l.Ln()
	}
	return 0
}

// runInt runs whole-number code for a call. ok is false when it gave up.
func (in *Interp) runInt(code *intCode, args []binding) (v int64, ok bool) {
	s := &istack{d: make([]int64, 64)}
	for i := range args {
		n, isInt := args[i].v.(int64)
		if !isInt {
			return 0, false
		}
		s.d[i] = n
	}
	defer func() {
		if r := recover(); r != nil {
			if _, gaveUp := r.(bail); !gaveUp {
				panic(r)
			}
			code.broken.Store(true)
			ok = false
		}
	}()
	c, r := code.body(s, 0)
	if c != cSend {
		giveUp()
	}
	return r, true
}

type intCompiler struct {
	in    *Interp
	slots map[string]int
}

func (c *intCompiler) slot(name string) int {
	if i, ok := c.slots[name]; ok {
		return i
	}
	i := len(c.slots)
	c.slots[name] = i
	return i
}

// block compiles statements. set holds the names surely set at this point; it
// is updated as names are made.
func (c *intCompiler) block(stmts []Stmt, set map[string]bool, code *intCode) (istmt, bool) {
	parts := make([]istmt, 0, len(stmts))
	lines := make([]int, 0, len(stmts))
	for _, st := range stmts {
		p, ok := c.stmt(st, set, code)
		if !ok {
			return nil, false
		}
		parts = append(parts, p)
		lines = append(lines, st.Ln())
	}
	in := c.in
	if len(parts) == 1 {
		p, l := parts[0], lines[0]
		return func(s *istack, b int) (ctrl, int64) {
			in.tick(l)
			return p(s, b)
		}, true
	}
	return func(s *istack, b int) (ctrl, int64) {
		for i, p := range parts {
			in.tick(lines[i])
			if k, v := p(s, b); k != cNone {
				return k, v
			}
		}
		return cNone, 0
	}, true
}

func copySet(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func (c *intCompiler) stmt(st Stmt, set map[string]bool, code *intCode) (istmt, bool) {
	switch s := st.(type) {
	case *SendStmt:
		if s.Value == nil {
			return nil, false
		}
		v, ok := c.expr(s.Value, set, code)
		if !ok {
			return nil, false
		}
		return func(st *istack, b int) (ctrl, int64) { return cSend, v(st, b) }, true
	case *AssignStmt:
		t, ok := s.Target.(*Ident)
		if !ok || s.Lock || s.Type != nil && (s.Type.Name != "Int" || len(s.Type.Args) > 0 || s.Type.Optional) {
			return nil, false
		}
		v, ok := c.expr(s.Value, set, code)
		if !ok {
			return nil, false
		}
		if s.Type != nil && set[t.Name] {
			return nil, false // the engine reports giving a type twice
		}
		i := c.slot(t.Name)
		set[t.Name] = true
		return func(st *istack, b int) (ctrl, int64) {
			st.d[b+i] = v(st, b)
			return cNone, 0
		}, true
	case *WhenStmt:
		cond, ok := c.cond(s.Cond, set, code)
		if !ok {
			return nil, false
		}
		thenSet := copySet(set)
		then, ok := c.block(s.Body, thenSet, code)
		if !ok {
			return nil, false
		}
		if s.Otherwise == nil {
			return func(st *istack, b int) (ctrl, int64) {
				if cond(st, b) {
					return then(st, b)
				}
				return cNone, 0
			}, true
		}
		elseSet := copySet(set)
		other, ok := c.block(s.Otherwise, elseSet, code)
		if !ok {
			return nil, false
		}
		for k := range thenSet { // surely set afterwards: set on both paths
			if elseSet[k] {
				set[k] = true
			}
		}
		return func(st *istack, b int) (ctrl, int64) {
			if cond(st, b) {
				return then(st, b)
			}
			return other(st, b)
		}, true
	case *EachStmt:
		r, ok := s.Over.(*Binary)
		if !ok || r.Op != "to" || set[s.Var] {
			return nil, false
		}
		from, ok1 := c.expr(r.Left, set, code)
		to, ok2 := c.expr(r.Right, set, code)
		if !ok1 || !ok2 {
			return nil, false
		}
		inner := copySet(set)
		inner[s.Var] = true
		vi := c.slot(s.Var)
		body, ok := c.block(s.Body, inner, code)
		if !ok {
			return nil, false
		}
		// names made in the loop may not be set if it runs no rounds
		return func(st *istack, b int) (ctrl, int64) {
			lo, hi := from(st, b), to(st, b)
			if hi-lo > 50_000_000 {
				giveUp() // the engine explains
			}
			for i := lo; i <= hi; i++ {
				st.d[b+vi] = i
				k, v := body(st, b)
				if k == cStop {
					break
				}
				if k == cSend {
					return k, v
				}
			}
			return cNone, 0
		}, true
	case *StopStmt:
		return func(*istack, int) (ctrl, int64) { return cStop, 0 }, true
	case *SkipStmt:
		return func(*istack, int) (ctrl, int64) { return cSkip, 0 }, true
	}
	return nil, false
}

func (c *intCompiler) cond(x Expr, set map[string]bool, code *intCode) (icond, bool) {
	switch x := x.(type) {
	case *BoolLit:
		v := x.Value
		return func(*istack, int) bool { return v }, true
	case *Unary:
		if x.Op != "not" {
			return nil, false
		}
		a, ok := c.cond(x.Operand, set, code)
		if !ok {
			return nil, false
		}
		return func(s *istack, b int) bool { return !a(s, b) }, true
	case *Binary:
		if x.Op == "and" || x.Op == "or" {
			l, ok1 := c.cond(x.Left, set, code)
			r, ok2 := c.cond(x.Right, set, code)
			if !ok1 || !ok2 {
				return nil, false
			}
			if x.Op == "and" {
				return func(s *istack, b int) bool { return l(s, b) && r(s, b) }, true
			}
			return func(s *istack, b int) bool { return l(s, b) || r(s, b) }, true
		}
		l, ok1 := c.expr(x.Left, set, code)
		r, ok2 := c.expr(x.Right, set, code)
		if !ok1 || !ok2 {
			return nil, false
		}
		switch x.Op {
		case "<":
			return func(s *istack, b int) bool { return l(s, b) < r(s, b) }, true
		case ">":
			return func(s *istack, b int) bool { return l(s, b) > r(s, b) }, true
		case "<=":
			return func(s *istack, b int) bool { return l(s, b) <= r(s, b) }, true
		case ">=":
			return func(s *istack, b int) bool { return l(s, b) >= r(s, b) }, true
		case "is":
			return func(s *istack, b int) bool { return l(s, b) == r(s, b) }, true
		case "is not":
			return func(s *istack, b int) bool { return l(s, b) != r(s, b) }, true
		}
	}
	return nil, false
}

func (c *intCompiler) expr(x Expr, set map[string]bool, code *intCode) (iexpr, bool) {
	switch x := x.(type) {
	case *NumberLit:
		n, ok := x.Value.(int64)
		if !ok {
			return nil, false
		}
		return func(*istack, int) int64 { return n }, true
	case *Ident:
		i, ok := c.slots[x.Name]
		if !ok || !set[x.Name] {
			return nil, false // a name from outside, or maybe not set yet
		}
		return func(s *istack, b int) int64 { return s.d[b+i] }, true
	case *Unary:
		if x.Op != "-" {
			return nil, false
		}
		a, ok := c.expr(x.Operand, set, code)
		if !ok {
			return nil, false
		}
		return func(s *istack, b int) int64 {
			v := a(s, b)
			if v == -1<<63 {
				giveUp()
			}
			return -v
		}, true
	case *Binary:
		l, ok1 := c.expr(x.Left, set, code)
		r, ok2 := c.expr(x.Right, set, code)
		if !ok1 || !ok2 {
			return nil, false
		}
		switch x.Op {
		case "+":
			return func(s *istack, b int) int64 {
				p, q := l(s, b), r(s, b)
				v := p + q
				if (p >= 0) == (q >= 0) && (v >= 0) != (p >= 0) {
					giveUp()
				}
				return v
			}, true
		case "-":
			return func(s *istack, b int) int64 {
				p, q := l(s, b), r(s, b)
				v := p - q
				if (p >= 0) != (q >= 0) && (v >= 0) != (p >= 0) {
					giveUp()
				}
				return v
			}, true
		case "*":
			return func(s *istack, b int) int64 {
				v, ok := intOp("*", l(s, b), r(s, b))
				if !ok {
					giveUp()
				}
				return v
			}, true
		case "%":
			return func(s *istack, b int) int64 {
				p, q := l(s, b), r(s, b)
				if q == 0 {
					giveUp()
				}
				return p % q
			}, true
		}
	case *Call:
		id, ok := x.Callee.(*Ident)
		if !ok || x.Tx {
			return nil, false
		}
		if _, local := c.slots[id.Name]; local {
			return nil, false
		}
		args := make([]iexpr, len(x.Args))
		for i, a := range x.Args {
			if a.Name != "" {
				return nil, false
			}
			e, ok := c.expr(a.Value, set, code)
			if !ok {
				return nil, false
			}
			args[i] = e
		}
		return c.call(id.Name, args, code), true
	}
	return nil, false
}

// call calls another beam by name. The name is found when the call first
// runs, in the place the engine would find it: the beam's surroundings, where
// top-level beams are locked, so it stays the same.
func (c *intCompiler) call(name string, args []iexpr, self *intCode) iexpr {
	in := c.in
	var target atomic.Pointer[intCode]
	var resolve func() *intCode
	resolve = func() *intCode {
		v, ok := in.globals.lookup(name)
		f, isBeam := v.(*Beam)
		if !ok || !isBeam || len(f.Params) != len(args) {
			giveUp()
		}
		if b := in.globals.find(name); b == nil || !b.locked || f.Closure != in.globals {
			giveUp() // only top-level beams, which can't be replaced
		}
		code := in.compiled(f)
		if code == nil {
			giveUp()
		}
		code.intOnce.Do(func() { code.ints = in.intTier(f) })
		if code.ints == nil || code.ints.broken.Load() {
			giveUp()
		}
		return code.ints
	}
	n := len(args)
	return func(s *istack, b int) int64 {
		t := target.Load()
		if t == nil {
			t = resolve()
			target.Store(t)
		}
		var vals [4]int64
		var more []int64
		if n > 4 {
			more = make([]int64, n)
		}
		for i, a := range args {
			if n > 4 {
				more[i] = a(s, b)
			} else {
				vals[i] = a(s, b)
			}
		}
		// the new frame goes above the caller's: the caller's frame size is
		// known once it's compiled, so it's read now
		nb := b + self.n
		if need := nb + t.n; need > len(s.d) {
			grown := make([]int64, 2*need)
			copy(grown, s.d)
			s.d = grown
		}
		if n > 4 {
			copy(s.d[nb:], more)
		} else {
			copy(s.d[nb:nb+n], vals[:n])
		}
		k, v := t.body(s, nb)
		if k != cSend {
			giveUp()
		}
		return v
	}
}
