package lang

// The loop tier. An 'each' loop whose body only uses names that already
// exist when it starts (and its own loop names) is compiled so each name is
// found once, when the loop begins, instead of on every round: the loop's
// own names live in a small frame, and outside names are held by their
// binding. Whole-number arithmetic and comparisons, fields, making objects and
// adding to lists are done directly; everything else goes through the same
// code as the ordinary engine, so answers and problems are the same.
//
// It's safe because nothing can add names to the scopes around a running
// loop: the body makes no new names (that's checked), beams it calls make
// theirs in their own frames, and fast work never runs beside the main code
// or changes names it doesn't own.

import "slices"

type (
	lframe struct {
		v     []Value    // the loop's own names
		outer []*binding // outside names, found when the loop began
		born  []int64    // when the scope holding each outside name was made
	}
	lexpr func(f *lframe) Value
	lstmt func(f *lframe) ctrl
)

// loopCompiler compiles one outermost loop. Outside names are found at the
// start of each run of the loop, in the order first used.
type loopCompiler struct {
	in      *Interp
	slots   map[string]int // the loop's own names (the loop variables)
	nslots  int
	outer   map[string]int
	names   []string // outside names, by index
	written map[string]bool
	called  map[string]bool // outside names called like beams
	calls   int             // calls compiled so far (anything that could change a list)
}

// loopTier compiles s, or gives nil when it can't.
func (in *Interp) loopTier(s *EachStmt) cblock {
	if in.NoCompile || in.Cover != nil || capturesNames(s.Body) {
		return nil
	}
	c := &loopCompiler{in: in, slots: map[string]int{}, outer: map[string]int{}, written: map[string]bool{}, called: map[string]bool{}}
	run, ok := c.each(s)
	if !ok {
		return nil
	}
	for name := range c.called {
		if c.written[name] {
			return nil // what's called could change from round to round
		}
	}
	names, n := c.names, c.nslots
	written, called := c.written, c.called
	plain := in.compileEachPlain(s)
	return func(e *Env) (ctrl, Value) {
		f := &lframe{v: make([]Value, n), outer: make([]*binding, len(names)), born: make([]int64, len(names))}
		for i, name := range names {
			b, owner, inBeam := e.lookupBinding(name)
			if b == nil || written[name] && !inBeam {
				// a field, not there yet, or (for a change) outside the beam:
				// the ordinary loop does what the engine does
				return plain(e)
			}
			if called[name] {
				if bi, ok := b.v.(*Builtin); ok && bi.Lazy != nil {
					return plain(e) // its inputs are worked out by it, not before
				}
			}
			f.outer[i], f.born[i] = b, owner.born
		}
		return run(f), nil
	}
}

// lookupBinding finds the binding a name means here, as reading it would,
// and the scope holding it, and whether it's inside the beam (where a change
// would reach it). An object's fields and beams aren't bindings: nil.
func (e *Env) lookupBinding(name string) (*binding, *Env, bool) {
	inBeam := true
	for s := e; s != nil; s = s.parent {
		if b := s.find(name); b != nil {
			return b, s, inBeam
		}
		if s.obj != nil {
			if _, ok := s.obj.Class.index[name]; ok {
				return nil, nil, false
			}
			if _, ok := s.obj.Class.Methods[name]; ok {
				return nil, nil, false
			}
		}
		if s.isBeam {
			inBeam = false
		}
	}
	return nil, nil, false
}

func (c *loopCompiler) outerIndex(name string) int {
	if i, ok := c.outer[name]; ok {
		return i
	}
	i := len(c.names)
	c.outer[name] = i
	c.names = append(c.names, name)
	return i
}

func (c *loopCompiler) block(stmts []Stmt) (lstmt, bool) {
	parts := make([]lstmt, len(stmts))
	lines := make([]int, len(stmts))
	for i, st := range stmts {
		p, ok := c.stmt(st)
		if !ok {
			return nil, false
		}
		parts[i], lines[i] = p, st.Ln()
	}
	in := c.in
	if len(parts) == 1 {
		p, l := parts[0], lines[0]
		return func(f *lframe) ctrl {
			in.tick(l)
			return p(f)
		}, true
	}
	return func(f *lframe) ctrl {
		for i, p := range parts {
			in.tick(lines[i])
			if k := p(f); k != cNone {
				return k
			}
		}
		return cNone
	}, true
}

func (c *loopCompiler) each(s *EachStmt) (lstmt, bool) {
	if _, taken := c.slots[s.Var]; taken {
		return nil, false
	}
	if _, used := c.outer[s.Var]; used {
		return nil, false
	}
	in := c.in
	var over lexpr
	var from, to lexpr
	r, isRange := s.Over.(*Binary)
	isRange = isRange && r.Op == "to"
	var ok bool
	if isRange {
		if from, ok = c.expr(r.Left); !ok {
			return nil, false
		}
		if to, ok = c.expr(r.Right); !ok {
			return nil, false
		}
	} else if over, ok = c.expr(s.Over); !ok {
		return nil, false
	}
	slot := c.nslots
	c.slots[s.Var] = slot
	c.nslots++
	callsBefore := c.calls
	body, ok := c.block(s.Body)
	// a body that calls nothing can't change the list it goes through, so the
	// list needn't be copied first
	noCopy := c.calls == callsBefore
	delete(c.slots, s.Var) // the name is the loop's own only inside it
	if !ok {
		return nil, false
	}
	round := func(f *lframe) (stop bool, sent bool) {
		switch body(f) {
		case cStop:
			return true, false
		case cSend:
			return true, true
		}
		return false, false
	}
	// a counting loop that adds to an outside list once every round (the
	// usual way to build a list) makes room for all of it at the start,
	// instead of growing the list again and again
	grow := -1
	if isRange {
		for _, st := range s.Body {
			if es, ok := st.(*ExprStmt); ok {
				if call, ok := es.X.(*Call); ok && len(call.Args) == 1 {
					if g, ok := call.Callee.(*Get); ok && g.Name == "add" {
						if id, ok := g.Object.(*Ident); ok {
							if _, own := c.slots[id.Name]; !own {
								grow = c.outerIndex(id.Name)
							}
						}
					}
				}
			}
			break // only the first statement surely runs every round
		}
	}
	if isRange {
		return func(f *lframe) ctrl {
			a := in.wantInt(r.Line, "the start of a range", from(f))
			b := in.wantInt(r.Line, "the end of a range", to(f))
			if b-a > 50_000_000 {
				in.fail(r.Line, "this range would hold %d numbers, which is too many to make at once", b-a+1)
			}
			if grow >= 0 && b >= a && b-a < 1<<22 {
				if l, ok := f.outer[grow].v.(*List); ok && (in.parallel.Load() == 0 || in.writing.Load() || l.born >= in.regionStart.Load() && l.born&1 == 0) {
					l.Items = slices.Grow(l.Items, int(b-a+1))
				}
			}
			for i := a; i <= b; i++ {
				f.v[slot] = box(i)
				if stop, _ := round(f); stop {
					break
				}
			}
			return cNone
		}, true
	}
	return func(f *lframe) ctrl {
		var items []Value
		switch x := over(f).(type) {
		case *List:
			if noCopy {
				items = x.Items
			} else {
				items = append(items, x.Items...) // the list as it was when the loop began
			}
		case string:
			for _, r := range x {
				items = append(items, string(r))
			}
		case *Map:
			for _, k := range x.Keys {
				items = append(items, k)
			}
		default:
			in.fail(s.Line, "'each' needs a list to go through, but got %s", typeName(x))
		}
		for _, it := range items {
			f.v[slot] = it
			if stop, _ := round(f); stop {
				break
			}
		}
		return cNone
	}, true
}

func (c *loopCompiler) stmt(st Stmt) (lstmt, bool) {
	in := c.in
	switch s := st.(type) {
	case *EachStmt:
		return c.each(s)
	case *StopStmt:
		return func(*lframe) ctrl { return cStop }, true
	case *SkipStmt:
		return func(*lframe) ctrl { return cSkip }, true
	case *ShowStmt:
		v, ok := c.expr(s.Value)
		if !ok {
			return nil, false
		}
		return func(f *lframe) ctrl {
			in.print(Text(v(f)))
			return cNone
		}, true
	case *ExprStmt:
		v, ok := c.expr(s.X)
		if !ok {
			return nil, false
		}
		return func(f *lframe) ctrl {
			v(f)
			return cNone
		}, true
	case *WhenStmt:
		cond, ok := c.expr(s.Cond)
		if !ok {
			return nil, false
		}
		then, ok := c.block(s.Body)
		if !ok {
			return nil, false
		}
		var other lstmt
		if s.Otherwise != nil {
			if other, ok = c.block(s.Otherwise); !ok {
				return nil, false
			}
		}
		line := s.Cond.Ln()
		return func(f *lframe) ctrl {
			r := cond(f)
			b, isBool := r.(bool)
			if !isBool {
				in.fail(line, "'%s' needs a yes/no question, but this gave %s", "when", typeName(r))
			}
			if b {
				return then(f)
			}
			if other != nil {
				return other(f)
			}
			return cNone
		}, true
	case *AssignStmt:
		t, ok := s.Target.(*Ident)
		if !ok || s.Type != nil || s.Lock {
			return nil, false
		}
		v, ok := c.expr(s.Value)
		if !ok {
			return nil, false
		}
		if slot, own := c.slots[t.Name]; own {
			return func(f *lframe) ctrl {
				f.v[slot] = v(f)
				return cNone
			}, true
		}
		i := c.outerIndex(t.Name)
		c.written[t.Name] = true
		name, line := t.Name, s.Line
		return func(f *lframe) ctrl {
			val := v(f)
			b := f.outer[i]
			if b.locked {
				in.fail(line, "'%s' is locked, so it can't be changed", name)
			}
			if b.t != nil {
				val = in.mustConformName(line, val, b.t, name)
			}
			in.mayChangeName(f.born[i], line, name)
			b.v = val
			return cNone
		}, true
	}
	return nil, false
}

func (c *loopCompiler) expr(x Expr) (lexpr, bool) {
	in := c.in
	switch x := x.(type) {
	case *NumberLit:
		v := Value(x.Value)
		if n, ok := x.Value.(int64); ok {
			v = box(n)
		}
		return func(*lframe) Value { return v }, true
	case *TextLit:
		v := Value(x.Value)
		return func(*lframe) Value { return v }, true
	case *BoolLit:
		v := Value(x.Value)
		return func(*lframe) Value { return v }, true
	case *Ident:
		if slot, own := c.slots[x.Name]; own {
			return func(f *lframe) Value { return f.v[slot] }, true
		}
		i := c.outerIndex(x.Name)
		return func(f *lframe) Value { return f.outer[i].v }, true
	case *Get:
		obj, ok := c.expr(x.Object)
		if !ok {
			return nil, false
		}
		name, line := x.Name, x.Line
		var hint fieldHint
		return func(f *lframe) Value {
			o := obj(f)
			if inst, ok := o.(*Instance); ok {
				if i := hint.index(inst.Class, name); i >= 0 {
					return inst.Slots[i]
				}
			}
			return in.get(o, name, line)
		}, true
	case *Binary:
		switch x.Op {
		case "and", "or", "to", "then":
			return nil, false
		}
		l, ok1 := c.expr(x.Left)
		r, ok2 := c.expr(x.Right)
		if !ok1 || !ok2 {
			return nil, false
		}
		op := x.Op
		return func(f *lframe) Value {
			a, b := l(f), r(f)
			if p, ok := a.(int64); ok {
				if q, ok := b.(int64); ok {
					switch op {
					case "+", "-", "*":
						if v, ok := intOp(op, p, q); ok {
							return box(v)
						}
					case "%":
						if q != 0 {
							return box(p % q)
						}
					case "<":
						return p < q
					case ">":
						return p > q
					case "<=":
						return p <= q
					case ">=":
						return p >= q
					case "is":
						return p == q
					case "is not":
						return p != q
					}
				}
			}
			return in.binaryValues(x, a, b) // everything else, and every problem, as the engine does it
		}, true
	case *Call:
		if x.Tx || len(x.Args) > 4 {
			return nil, false
		}
		args := make([]lexpr, len(x.Args))
		for i, a := range x.Args {
			if a.Name != "" {
				return nil, false
			}
			e, ok := c.expr(a.Value)
			if !ok {
				return nil, false
			}
			args[i] = e
		}
		line := x.Line
		c.calls++
		// list.add(v), the commonest change to a list
		if g, ok := x.Callee.(*Get); ok && g.Name == "add" && len(args) == 1 {
			obj, ok := c.expr(g.Object)
			if !ok {
				return nil, false
			}
			arg := args[0]
			return func(f *lframe) Value {
				o := obj(f)
				if l, ok := o.(*List); ok {
					v := arg(f)
					in.mayChange(l.born, line, "a list")
					l.Items = append(l.Items, v)
					return l
				}
				return in.call(in.get(o, "add", g.Line), []Value{arg(f)}, nil, line, nil)
			}, true
		}
		id, ok := x.Callee.(*Ident)
		if !ok { // other beams of values (which might work out their own inputs): the ordinary way
			return nil, false
		}
		if _, own := c.slots[id.Name]; own {
			return nil, false
		}
		c.called[id.Name] = true
		callee, _ := c.expr(id)
		n := len(args)
		return func(f *lframe) Value {
			fn := callee(f)
			if cl, ok := fn.(*Class); ok && n == len(cl.Fields) { // every field given in order
				inst := in.newInstance(cl)
				for i, a := range args {
					inst.Slots[i] = a(f)
				}
				for i := range inst.Slots {
					inst.Slots[i] = in.fieldValue(line, cl, i, inst.Slots[i])
				}
				return inst
			}
			vals := make([]Value, n)
			for i, a := range args {
				vals[i] = a(f)
			}
			if b, ok := fn.(*Beam); ok && b.Self == nil && b.code != nil && n == len(b.Params) && (b.Sig == nil || len(b.Sig.TypeParams) == 0) {
				if code := in.compiled(b); code != nil {
					return in.fastCallValues(b, code, vals, line)
				}
			}
			return in.call(fn, vals, nil, line, nil)
		}, true
	}
	return nil, false
}
