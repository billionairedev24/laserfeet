package lang

import (
	"math/bits"
	"strings"
	"sync"
	"sync/atomic"
)

// The compiler turns a beam's body into a tree of Go closures the first time
// the beam runs. Each closure does one thing, with everything it can decide
// ahead of time already decided: which operator, which kind of statement,
// whether a loop needs a fresh name for each round. Running a closure skips the
// work the tree-walking engine repeats every time (looking at what kind of
// node it has, building argument lists, formatting labels), so beams run
// several times faster.
//
// The compiler keeps the engine's exact rules: names are found the same way,
// values have the same kinds, and problems have the same messages. Anything it
// doesn't compile simply runs on the tree-walking engine, so the two always
// agree; differential tests run every program on both and compare.

type cexpr func(e *Env) Value
type cblock func(e *Env) (ctrl, Value)

// beamCode is a beam body's compiled form, made once, the first time it runs.
type beamCode struct {
	once sync.Once
	body cblock
	expr cexpr
}

// codeFor finds the compiled form shared by every beam made from one
// declaration or one beam written in the code (compiled code belongs to one
// engine, since it reports problems and prints through it).
func (in *Interp) codeFor(node any) *beamCode {
	if c, ok := in.codes.Load(node); ok {
		return c.(*beamCode)
	}
	c, _ := in.codes.LoadOrStore(node, &beamCode{})
	return c.(*beamCode)
}

func (in *Interp) compiled(f *Beam) *beamCode {
	c := f.code
	if c == nil || in.NoCompile {
		return nil
	}
	c.once.Do(func() {
		if f.Expr != nil {
			c.expr = in.compileExpr(f.Expr)
		} else {
			c.body = in.compileBlock(f.Body)
		}
	})
	return c
}

// ---------- statements ----------

func (in *Interp) compileBlock(stmts []Stmt) cblock {
	parts := make([]cblock, len(stmts))
	lines := make([]int, len(stmts))
	for i, s := range stmts {
		parts[i] = in.compileStmt(s)
		lines[i] = s.Ln()
	}
	switch len(parts) {
	case 0:
		return func(e *Env) (ctrl, Value) { return cNone, nil }
	case 1:
		p, l := parts[0], lines[0]
		return func(e *Env) (ctrl, Value) {
			in.tick(l)
			return p(e)
		}
	}
	return func(e *Env) (ctrl, Value) {
		for i, p := range parts {
			in.tick(lines[i])
			if c, v := p(e); c != cNone {
				return c, v
			}
		}
		return cNone, nil
	}
}

func (in *Interp) compileStmt(s Stmt) cblock {
	switch s := s.(type) {
	case *ShowStmt:
		v := in.compileExpr(s.Value)
		return func(e *Env) (ctrl, Value) {
			in.print(Text(v(e)))
			return cNone, nil
		}
	case *ExprStmt:
		v := in.compileExpr(s.X)
		return func(e *Env) (ctrl, Value) {
			v(e)
			return cNone, nil
		}
	case *SendStmt:
		if s.Value == nil {
			return func(e *Env) (ctrl, Value) { return cSend, nil }
		}
		v := in.compileExpr(s.Value)
		return func(e *Env) (ctrl, Value) { return cSend, v(e) }
	case *StopStmt:
		return func(e *Env) (ctrl, Value) { return cStop, nil }
	case *SkipStmt:
		return func(e *Env) (ctrl, Value) { return cSkip, nil }
	case *AssignStmt:
		if t, ok := s.Target.(*Ident); ok && s.Type == nil && !s.Lock {
			return in.compileAssign(s, t.Name)
		}
	case *WhenStmt:
		cond := in.compileFlag(s.Cond, "when")
		body := in.compileBlock(s.Body)
		if s.Otherwise == nil {
			return func(e *Env) (ctrl, Value) {
				if cond(e) {
					return body(e)
				}
				return cNone, nil
			}
		}
		other := in.compileBlock(s.Otherwise)
		return func(e *Env) (ctrl, Value) {
			if cond(e) {
				return body(e)
			}
			return other(e)
		}
	case *EachStmt:
		return in.compileEach(s)
	case *LoopStmt:
		body := in.compileBlock(s.Body)
		if s.Times != nil {
			times := in.compileExpr(s.Times)
			return func(e *Env) (ctrl, Value) {
				n := in.wantInt(s.Line, "the number in 'loop ... times'", times(e))
				for i := int64(0); i < n; i++ {
					c, v := body(e)
					if c == cStop {
						break
					}
					if c == cSend {
						return c, v
					}
				}
				return cNone, nil
			}
		}
		cond := in.compileFlag(s.While, "loop while")
		return func(e *Env) (ctrl, Value) {
			for cond(e) {
				in.tick(s.Line)
				c, v := body(e)
				if c == cStop {
					break
				}
				if c == cSend {
					return c, v
				}
			}
			return cNone, nil
		}
	}
	return func(e *Env) (ctrl, Value) { return in.exec(s, e) } // the tree-walking engine
}

// compileAssign is name = value, with the engine's rule: change the nearest
// name of that name (in this beam, an outer one, or an object's field), or
// make a new one in this beam.
func (in *Interp) compileAssign(s *AssignStmt, name string) cblock {
	val := in.compileExpr(s.Value)
	return func(e *Env) (ctrl, Value) {
		v := val(e)
		for env := e; env != nil && !env.isBuiltins; env = env.parent {
			if b := env.find(name); b != nil {
				if b.locked {
					in.fail(s.Line, "'%s' is locked, so it can't be changed", name)
				}
				if b.t != nil {
					v = in.mustConform(s.Line, v, b.t, "'"+name+"'")
				}
				b.v = v
				return cNone, nil
			}
			if env.obj != nil {
				if i, ok := env.obj.Class.index[name]; ok {
					env.obj.Slots[i] = in.fieldValue(s.Line, env.obj.Class, i, v)
					return cNone, nil
				}
			}
		}
		e.scope().define(name, v, false)
		return cNone, nil
	}
}

// capturesNames reports whether code can keep hold of the names around it
// (a beam written inline, or fast work), so a loop must give each round its
// own name for the item.
func capturesNames(stmts []Stmt) bool {
	found := false
	var visit func(n any)
	visitExpr := func(x Expr) {
		if x != nil {
			visit(x)
		}
	}
	visit = func(n any) {
		if found || n == nil {
			return
		}
		switch x := n.(type) {
		case *Lambda, *FastExpr, *FastEach, *BeamDecl:
			found = true
		case *ShowStmt:
			visitExpr(x.Value)
		case *ExprStmt:
			visitExpr(x.X)
		case *AssignStmt:
			visitExpr(x.Target)
			visitExpr(x.Value)
		case *SendStmt:
			visitExpr(x.Value)
		case *WhenStmt:
			visitExpr(x.Cond)
			for _, s := range x.Body {
				visit(s)
			}
			for _, s := range x.Otherwise {
				visit(s)
			}
		case *EachStmt:
			visitExpr(x.Over)
			for _, s := range x.Body {
				visit(s)
			}
		case *LoopStmt:
			visitExpr(x.Times)
			visitExpr(x.While)
			for _, s := range x.Body {
				visit(s)
			}
		case *Binary:
			visitExpr(x.Left)
			visitExpr(x.Right)
		case *Unary:
			visitExpr(x.Operand)
		case *Get:
			visitExpr(x.Object)
		case *Index:
			visitExpr(x.Object)
			visitExpr(x.Key)
		case *Call:
			visitExpr(x.Callee)
			for _, a := range x.Args {
				visitExpr(a.Value)
			}
		case *Then:
			visitExpr(x.Left)
			visitExpr(x.Right)
		case *ListLit:
			for _, it := range x.Items {
				visitExpr(it)
			}
		case *MapLit:
			for _, v := range x.Values {
				visitExpr(v)
			}
		case *InterpText:
			for _, p := range x.Parts {
				visitExpr(p)
			}
		case *WaitExpr:
			visitExpr(x.X)
			visitExpr(x.Timeout)
		case *NumberLit, *TextLit, *BoolLit, *NothingLit, *Ident, *MeExpr, *StopStmt, *SkipStmt:
		default:
			found = true // something this check doesn't know: be safe
		}
	}
	for _, s := range stmts {
		visit(s)
	}
	return found
}

func (in *Interp) compileEach(s *EachStmt) cblock {
	body := in.compileBlock(s.Body)
	fresh := capturesNames(s.Body) // each round needs its own name only if something keeps it
	run := func(e *Env, items []Value) (ctrl, Value) {
		var round *Env
		for _, it := range items {
			if fresh || round == nil {
				round = &Env{parent: e, names: []string{s.Var}, binds: []binding{{v: it}}}
			} else {
				round.binds[0].v = it
				if len(round.names) > 1 { // a name made in the round's own scope (rare): start clean
					round.names, round.binds = round.names[:1], round.binds[:1]
				}
			}
			c, v := body(round)
			if c == cStop {
				break
			}
			if c == cSend {
				return c, v
			}
		}
		return cNone, nil
	}
	// each n in a to b counts without making the list
	if r, ok := s.Over.(*Binary); ok && r.Op == "to" {
		from, to := in.compileExpr(r.Left), in.compileExpr(r.Right)
		return func(e *Env) (ctrl, Value) {
			a := in.wantInt(r.Line, "the start of a range", from(e))
			b := in.wantInt(r.Line, "the end of a range", to(e))
			if b-a > 50_000_000 {
				in.fail(r.Line, "this range would hold %d numbers, which is too many to make at once", b-a+1)
			}
			var round *Env
			for i := a; i <= b; i++ {
				if fresh || round == nil {
					round = &Env{parent: e, names: []string{s.Var}, binds: []binding{{v: box(i)}}}
				} else {
					round.binds[0].v = box(i)
					if len(round.names) > 1 {
						round.names, round.binds = round.names[:1], round.binds[:1]
					}
				}
				c, v := body(round)
				if c == cStop {
					break
				}
				if c == cSend {
					return c, v
				}
			}
			return cNone, nil
		}
	}
	over := in.compileExpr(s.Over)
	return func(e *Env) (ctrl, Value) {
		var items []Value
		switch x := over(e).(type) {
		case *List:
			items = append(items, x.Items...) // the list as it was when the loop began
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
		return run(e, items)
	}
}

// ---------- expressions ----------

func (in *Interp) compileFlag(x Expr, what string) func(*Env) bool {
	v := in.compileExpr(x)
	line := x.Ln()
	return func(e *Env) bool {
		r := v(e)
		b, ok := r.(bool)
		if !ok {
			in.fail(line, "'%s' needs a yes/no question, but this gave %s", what, typeName(r))
		}
		return b
	}
}

func (in *Interp) compileExpr(x Expr) cexpr {
	switch x := x.(type) {
	case *NumberLit:
		v := x.Value
		return func(e *Env) Value { return v }
	case *TextLit:
		v := x.Value
		return func(e *Env) Value { return v }
	case *BoolLit:
		v := x.Value
		return func(e *Env) Value { return v }
	case *NothingLit:
		return func(e *Env) Value { return nil }
	case *Ident:
		return in.compileIdent(x)
	case *InterpText:
		parts := make([]cexpr, len(x.Parts))
		for i, p := range x.Parts {
			parts[i] = in.compileExpr(p)
		}
		return func(e *Env) Value {
			var sb strings.Builder
			for _, p := range parts {
				sb.WriteString(Text(p(e)))
			}
			return sb.String()
		}
	case *Unary:
		if x.Op == "not" {
			v := in.compileExpr(x.Operand)
			return func(e *Env) Value {
				b, ok := v(e).(bool)
				if !ok {
					return in.eval(x, e) // same message as the engine
				}
				return !b
			}
		}
	case *Binary:
		return in.compileBinary(x)
	case *Get:
		obj := in.compileExpr(x.Object)
		name, line := x.Name, x.Line
		return func(e *Env) Value {
			o := obj(e)
			if inst, ok := o.(*Instance); ok {
				if i, ok := inst.Class.index[name]; ok {
					return inst.Slots[i]
				}
			}
			return in.get(o, name, line)
		}
	case *Index:
		obj, key := in.compileExpr(x.Object), in.compileExpr(x.Key)
		return func(e *Env) Value {
			o, k := obj(e), key(e)
			if l, ok := o.(*List); ok {
				return l.Items[in.listIndex(x.Line, l, k)]
			}
			if m, ok := o.(*Map); ok {
				return m.M[in.mapKey(x.Line, k)]
			}
			return in.eval(x, e) // text, and the messages for everything else
		}
	case *ListLit:
		items := make([]cexpr, len(x.Items))
		for i, it := range x.Items {
			items[i] = in.compileExpr(it)
		}
		return func(e *Env) Value {
			l := &List{Items: make([]Value, len(items))}
			for i, it := range items {
				l.Items[i] = it(e)
			}
			return l
		}
	case *Call:
		return in.compileCall(x, nil)
	case *Then:
		left := in.compileExpr(x.Left)
		if c, ok := x.Right.(*Call); ok {
			return in.compileCall(c, left)
		}
		right := in.compileExpr(x.Right)
		return func(e *Env) Value {
			l := left(e)
			return in.call(right(e), []Value{l}, nil, x.Line, nil)
		}
	case *Lambda:
		code := in.codeFor(x)
		return func(e *Env) Value {
			return &Beam{Params: x.Params, Sig: x.Sig, Body: x.Body, Expr: x.Expr, Closure: e, code: code}
		}
	}
	return func(e *Env) Value { return in.eval(x, e) } // the tree-walking engine
}

func (in *Interp) compileBinary(x *Binary) cexpr {
	if x.Op == "and" || x.Op == "or" {
		l, r := in.compileFlag(x.Left, x.Op), in.compileFlag(x.Right, x.Op)
		if x.Op == "and" {
			return func(e *Env) Value { return l(e) && r(e) }
		}
		return func(e *Env) Value { return l(e) || r(e) }
	}
	left, right := in.compileExpr(x.Left), in.compileExpr(x.Right)
	line := x.Line
	switch x.Op {
	case "is":
		return func(e *Env) Value {
			l, r := left(e), right(e)
			if a, ok := l.(int64); ok {
				if b, ok := r.(int64); ok {
					return a == b
				}
			}
			return equal(l, r)
		}
	case "is not":
		return func(e *Env) Value { return !equal(left(e), right(e)) }
	case "<", ">", "<=", ">=":
		op := x.Op
		return func(e *Env) Value {
			l, r := left(e), right(e)
			var c int
			a, aok := l.(int64)
			b, bok := r.(int64)
			if aok && bok {
				switch {
				case a < b:
					c = -1
				case a > b:
					c = 1
				}
			} else {
				c = compareValues(in, line, l, r)
			}
			switch op {
			case "<":
				return c < 0
			case ">":
				return c > 0
			case "<=":
				return c <= 0
			}
			return c >= 0
		}
	case "+", "-", "*":
		op := x.Op
		return func(e *Env) Value {
			l, r := left(e), right(e)
			if a, ok := l.(int64); ok {
				if b, ok := r.(int64); ok {
					if v, ok := intOp(op, a, b); ok {
						return box(v)
					}
				}
			}
			return in.binaryValues(x, l, r)
		}
	}
	return func(e *Env) Value { return in.binaryValues(x, left(e), right(e)) }
}

// intOp does whole-number +, - and *, reporting whether it fit in an Int (the
// engine reports the overflow with its usual message).
func intOp(op string, a, b int64) (int64, bool) {
	switch op {
	case "+":
		s := a + b
		return s, !((a >= 0) == (b >= 0) && (s >= 0) != (a >= 0))
	case "-":
		s := a - b
		return s, !((a >= 0) != (b >= 0) && (s >= 0) != (a >= 0))
	case "*":
		if a == 0 || b == 0 {
			return 0, true
		}
		hi, lo := bits.Mul64(uint64(abs64(a)), uint64(abs64(b)))
		if hi != 0 || lo > 1<<63-1 || a == -1<<63 || b == -1<<63 {
			return 0, false
		}
		p := int64(lo)
		if (a < 0) != (b < 0) {
			p = -p
		}
		return p, true
	}
	return 0, false
}

func abs64(a int64) int64 {
	if a < 0 {
		return -a
	}
	return a
}

// compileCall compiles a call. A beam with compiled code and simple inputs
// gets a fast path: its frame is filled straight from the inputs, with no
// argument list in between.
func (in *Interp) compileCall(c *Call, first cexpr) cexpr {
	callee := in.compileExpr(c.Callee)
	args := make([]cexpr, 0, len(c.Args))
	lazy := false // named inputs go through the engine, which matches names
	for _, a := range c.Args {
		if a.Name != "" {
			lazy = true
		}
		args = append(args, in.compileExpr(a.Value))
	}
	line := c.Line
	hasFirst := first != nil
	n := len(args)
	if hasFirst {
		n++
	}
	return func(e *Env) Value {
		var firstVal Value
		if hasFirst { // x then f(...): x is worked out first, as in the engine
			firstVal = first(e)
		}
		fn := callee(e)
		switch f := fn.(type) {
		case *Builtin:
			if f.Lazy != nil || lazy {
				return in.callWithValues(c, e, fn, firstVal, hasFirst)
			}
			vals := make([]Value, 0, n)
			if hasFirst {
				vals = append(vals, firstVal)
			}
			for _, a := range args {
				vals = append(vals, a(e))
			}
			if f.Fn == nil {
				in.fail(line, "%s can't be used this way", f.Name)
			}
			return f.Fn(in, line, vals)
		case *Beam:
			if !lazy && f.Self == nil && f.code != nil && n == len(f.Params) && (f.Sig == nil || len(f.Sig.TypeParams) == 0) {
				if code := in.compiled(f); code != nil {
					return in.fastCall(f, code, firstVal, hasFirst, args, e, line)
				}
			}
		}
		if lazy {
			return in.callWithValues(c, e, fn, firstVal, hasFirst)
		}
		vals := make([]Value, 0, n)
		if hasFirst {
			vals = append(vals, firstVal)
		}
		for _, a := range args {
			vals = append(vals, a(e))
		}
		return in.call(fn, vals, nil, line, nil)
	}
}

// callWithValues runs a call through the engine with the callee already worked out.
func (in *Interp) callWithValues(c *Call, env *Env, fn Value, first Value, hasFirst bool) Value {
	return in.callExprWith(c, env, fn, first, hasFirst)
}

// frame1 and frame3 hold a beam call's names together with the call's
// scope, so a call needs one allocation.
type frame1 struct {
	Env
	slots [1]binding
}

type frame3 struct {
	Env
	slots [3]binding
}

// fastCall runs a compiled beam: inputs go straight into its frame.
func (in *Interp) fastCall(f *Beam, code *beamCode, firstVal Value, hasFirst bool, args []cexpr, caller *Env, line int) Value {
	n := len(args)
	if hasFirst {
		n++
	}
	var e *Env
	switch {
	case n <= 1:
		fr := new(frame1)
		e = &fr.Env
		e.binds = fr.slots[:n]
	case n <= 3:
		fr := new(frame3)
		e = &fr.Env
		e.binds = fr.slots[:n]
	default:
		e = new(Env)
		e.binds = make([]binding, n)
	}
	e.parent, e.isScope, e.names = f.Closure, true, f.Params[:n:n]
	k := 0
	if hasFirst {
		e.binds[0].v = firstVal
		k = 1
	}
	for i, a := range args {
		e.binds[k+i].v = a(caller)
	}
	ptypes, rtype := in.sigTypes(f)
	for i, pt := range ptypes {
		if pt != nil && i < n {
			out, ok := conform(e.binds[i].v, pt)
			if !ok {
				label := "this beam"
				if f.Name != "" {
					label = "beam " + f.Name
				}
				in.fail(line, "the input '%s' of %s must be %s, but got %s", f.Params[i], label, pt.Describe(), describeValue(e.binds[i].v))
			}
			e.binds[i].v = out
		}
	}
	var v Value
	if code.expr != nil {
		v = code.expr(e)
	} else {
		var c ctrl
		c, v = code.body(e)
		if c == cStop || c == cSkip {
			in.fail(line, "'stop' and 'skip' only work inside a loop")
		}
	}
	if rtype != nil {
		out, ok := conform(v, rtype)
		if !ok {
			label := "this beam"
			if f.Name != "" {
				label = "beam " + f.Name
			}
			if v == nil {
				in.fail(line, "%s must send %s, but it finished without sending one", label, rtype.Describe())
			}
			in.fail(line, "%s must send %s, but it sent %s", label, rtype.Describe(), describeValue(v))
		}
		return out
	}
	return v
}

// compileIdent finds a name the engine's way. Beams, objects and other locked
// names at the top of a program are remembered once found, so calls to them
// skip the search of the program's names (the scopes in between are still
// checked, so a name there still comes first).
func (in *Interp) compileIdent(x *Ident) cexpr {
	name := x.Name
	type place struct {
		env *Env
		b   *binding
	}
	var cache atomic.Pointer[place]
	return func(e *Env) Value {
		if p := cache.Load(); p != nil {
			for s := e; s != nil; s = s.parent {
				if s == p.env {
					return p.b.v
				}
				if s.obj != nil || s.find(name) != nil {
					break // something closer has the name: do the full search
				}
			}
		}
		for s := e; s != nil; s = s.parent {
			if b := s.find(name); b != nil {
				if b.locked && s.big != nil { // a stable place: remember it
					cache.Store(&place{s, b})
				}
				return b.v
			}
			if s.obj != nil {
				break
			}
		}
		if v, ok := e.lookup(name); ok {
			return v
		}
		return in.eval(x, e) // the engine reports the unknown name
	}
}
