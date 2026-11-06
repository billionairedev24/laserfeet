package lang

import (
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RuntimeError is a problem that happens while the program runs.
type RuntimeError struct {
	Line   int
	Msg    string
	Value  Value // what was passed to `fail`, if anything
	Status int   // for bad input from outside (like Item.from), 400; web replies use it
}

func (e *RuntimeError) Error() string { return fmt.Sprintf("Problem on line %d: %s", e.Line, e.Msg) }

type testFailure struct {
	Line int
	Msg  string
}

type binding struct {
	v      Value
	locked bool
	t      *Type // set when the name was given a type, like total: Decimal = 0
}

// Env holds the names visible at one point in the program.
// Small scopes (most blocks and calls) keep their names in a short slice,
// which is much cheaper than a map; scopes that grow large switch to a map.
type Env struct {
	names  []string
	binds  []binding
	big    map[string]*binding
	parent *Env
	obj    *Instance // fields of this object are visible by name
	// the built-in names live here; your own names can reuse them
	isBuiltins bool
	// a beam call, launch, a test or a file: new names made in its blocks live here
	isScope bool
}

const smallScope = 8

func newEnv(parent *Env) *Env { return &Env{parent: parent} }

func newScope(parent *Env) *Env { return &Env{parent: parent, isScope: true} }

// scope returns the beam, launch, test or file scope that new names belong to.
func (e *Env) scope() *Env {
	for s := e; s != nil; s = s.parent {
		if s.isScope {
			return s
		}
	}
	return e
}

// find returns the binding for name in this scope only, or nil.
func (e *Env) find(name string) *binding {
	if e.big != nil {
		return e.big[name]
	}
	for i := range e.names {
		if e.names[i] == name {
			return &e.binds[i]
		}
	}
	return nil
}

func (e *Env) define(name string, v Value, locked bool) {
	if b := e.find(name); b != nil {
		b.v, b.locked = v, locked
		return
	}
	if e.big != nil {
		e.big[name] = &binding{v: v, locked: locked}
		return
	}
	if len(e.names) < smallScope {
		e.names = append(e.names, name)
		e.binds = append(e.binds, binding{v: v, locked: locked})
		return
	}
	e.big = make(map[string]*binding, 2*smallScope)
	for i, n := range e.names {
		b := e.binds[i]
		e.big[n] = &b
	}
	e.names, e.binds = nil, nil
	e.big[name] = &binding{v: v, locked: locked}
}

func (e *Env) lookup(name string) (Value, bool) {
	for s := e; s != nil; s = s.parent {
		if b := s.find(name); b != nil {
			return b.v, true
		}
		if s.obj != nil {
			if v, ok := s.obj.field(name); ok {
				return v, true
			}
			if m, ok := s.obj.Class.Methods[name]; ok {
				return &Beam{Name: m.Name, Params: m.Params, Sig: m.Sig, Body: m.Body, Closure: s.obj.Class.Env, Self: s.obj}, true
			}
		}
	}
	return nil, false
}

func (e *Env) names_() []string {
	var out []string
	for s := e; s != nil; s = s.parent {
		out = append(out, s.names...)
		for k := range s.big {
			out = append(out, k)
		}
		if s.obj != nil {
			for _, f := range s.obj.Class.Fields {
				out = append(out, f.Name)
			}
		}
	}
	return out
}

type ctrl int

const (
	cNone ctrl = iota
	cSend
	cStop
	cSkip
)

// Interp runs Laser Feet programs.
type Interp struct {
	Out      io.Writer
	MaxSteps int64 // 0 = unlimited (used by the browser playground)
	globals  *Env
	gil      sync.Mutex
	wg       sync.WaitGroup
	fastLive int32
	steps    int64
	launch   *LaunchStmt
	builtins *Env
	modules  map[string]*Module // loaded .lf files, by path
	loading  map[string]bool    // files being loaded right now, to catch loops
	useStack []UseContext       // where the file being loaded lives
	// ReadSource reads another .lf file for 'use "file.lf"'. The path is already
	// checked to stay inside the program's folder. Nil means files can't be used.
	ReadSource func(path string) (string, error)
	tests      []*TestStmt
	outMu      sync.Mutex
}

func NewInterp(out io.Writer) *Interp {
	builtins := newEnv(nil)
	builtins.isBuiltins = true
	in := &Interp{Out: out, globals: newScope(builtins), builtins: builtins}
	installBuiltins(in, builtins)
	return in
}

func (in *Interp) fail(line int, format string, args ...any) {
	panic(&RuntimeError{Line: line, Msg: fmt.Sprintf(format, args...)})
}

func (in *Interp) print(s string) {
	in.outMu.Lock()
	fmt.Fprintln(in.Out, s)
	in.outMu.Unlock()
}

// load declares everything at the top level and runs the plain top-level code.
func (in *Interp) load(prog *Program) { in.loadInto(prog, in.globals, true) }

// loadInto declares a file's rules, objects and beams in env, then runs its
// top-level code. Only the main file (main=true) may have launch and test blocks.
func (in *Interp) loadInto(prog *Program, env *Env, main bool) {
	// First pass: objects, rules and beams can be used before they appear.
	for _, s := range prog.Stmts {
		switch d := s.(type) {
		case *RuleDecl:
			env.define(d.Name, &RuleDef{Name: d.Name, Methods: d.Methods, Sigs: d.Sigs}, true)
		}
	}
	for _, s := range prog.Stmts {
		switch d := s.(type) {
		case *BeamDecl:
			env.define(d.Name, &Beam{Name: d.Name, Params: d.Params, Sig: d.Sig, Body: d.Body, Closure: env}, true)
		case *ObjectDecl:
			in.declareObject(d, env)
		case *ChoiceDecl:
			ch, problem := newChoice(d, env)
			if problem != "" {
				in.fail(d.Line, "%s", problem)
			}
			env.define(d.Name, ch, true)
		case *LaunchStmt:
			if !main {
				continue
			}
			if in.launch != nil {
				in.fail(d.Line, "a program can only have one launch block (the first is on line %d)", in.launch.Line)
			}
			in.launch = d
		case *TestStmt:
			if main {
				in.tests = append(in.tests, d)
			}
		}
	}
	for _, s := range prog.Stmts {
		switch s.(type) {
		case *RuleDecl, *BeamDecl, *ObjectDecl, *ChoiceDecl, *LaunchStmt, *TestStmt:
			continue
		}
		if c, _ := in.exec(s, env); c != cNone {
			in.fail(s.Ln(), "'send', 'stop' and 'skip' can't be used here")
		}
	}
}

func (in *Interp) declareObject(d *ObjectDecl, env *Env) {
	c := &Class{Name: d.Name, Fields: d.Fields, Methods: map[string]*BeamDecl{}, Follows: d.Follows, Env: env, index: map[string]int{}, TypeParams: d.TypeParams}
	for i, f := range d.Fields {
		if _, dup := c.index[f.Name]; dup {
			in.fail(d.Line, "object %s has two fields called '%s'", d.Name, f.Name)
		}
		c.index[f.Name] = i
	}
	for _, m := range d.Methods {
		c.Methods[m.Name] = m
	}
	for _, rn := range d.Follows {
		rv, ok := env.lookup(rn)
		r, isRule := rv.(*RuleDef)
		if !ok || !isRule {
			in.fail(d.Line, "object %s follows '%s', but there is no rule with that name", d.Name, rn)
		}
		for i, m := range r.Methods {
			got, ok := c.Methods[m]
			if !ok {
				in.fail(d.Line, "object %s follows the rule %s, so it must have a beam called '%s'", d.Name, r.Name, m)
			}
			if i < len(r.Sigs) && len(got.Params) != len(r.Sigs[i].Params) {
				in.fail(got.Line, "the rule %s says %s takes %d input(s), but %s's %s takes %d", r.Name, m, len(r.Sigs[i].Params), d.Name, m, len(got.Params))
			}
		}
	}
	env.define(d.Name, c, true)
}

func (in *Interp) catch(err *error) {
	if r := recover(); r != nil {
		switch e := r.(type) {
		case *RuntimeError:
			*err = e
		case *SyntaxError:
			*err = e
		case *testFailure:
			*err = &RuntimeError{Line: e.Line, Msg: e.Msg}
		default:
			panic(r)
		}
	}
}

// Run parses and runs a program.
func (in *Interp) Run(src string) (err error) {
	prog, perr := Parse(src)
	if perr != nil {
		return perr
	}
	in.gil.Lock()
	locked := true
	defer func() {
		if locked {
			in.gil.Unlock()
		}
	}()
	func() {
		defer in.catch(&err)
		in.load(prog)
		if in.launch != nil {
			in.execBlock(in.launch.Body, newScope(in.globals))
		}
	}()
	in.gil.Unlock()
	locked = false
	in.wg.Wait() // let fast work finish
	return err
}

// TestResult is the outcome of one test block.
type TestResult struct {
	Name   string
	Passed bool
	Msg    string
}

// RunTests runs every `test` block and reports each result.
func (in *Interp) RunTests(src string) (results []TestResult, err error) {
	prog, perr := Parse(src)
	if perr != nil {
		return nil, perr
	}
	in.gil.Lock()
	defer in.gil.Unlock()
	func() {
		defer in.catch(&err)
		in.load(prog)
	}()
	if err != nil {
		return nil, err
	}
	for _, t := range in.tests {
		var terr error
		func() {
			defer in.catch(&terr)
			in.execBlock(t.Body, newScope(in.globals))
		}()
		r := TestResult{Name: t.Name, Passed: terr == nil}
		if terr != nil {
			r.Msg = terr.Error()
		}
		results = append(results, r)
	}
	return results, nil
}

// ---- statements ----

func (in *Interp) tick(line int) {
	if in.MaxSteps > 0 {
		if atomic.AddInt64(&in.steps, 1) > in.MaxSteps {
			in.fail(line, "the program ran for too long and was stopped (is there a loop that never ends?)")
		}
	}
	if atomic.LoadInt32(&in.fastLive) > 0 {
		in.gil.Unlock()
		runtime.Gosched()
		in.gil.Lock()
	}
}

func (in *Interp) execBlock(stmts []Stmt, env *Env) (ctrl, Value) {
	for _, s := range stmts {
		in.tick(s.Ln())
		if c, v := in.exec(s, env); c != cNone {
			return c, v
		}
	}
	return cNone, nil
}

func (in *Interp) exec(s Stmt, env *Env) (ctrl, Value) {
	switch s := s.(type) {
	case *ShowStmt:
		in.print(Text(in.eval(s.Value, env)))
	case *ExprStmt:
		in.eval(s.X, env)
	case *AssignStmt:
		in.assign(s, env)
	case *SendStmt:
		if s.Value == nil {
			return cSend, nil
		}
		return cSend, in.eval(s.Value, env)
	case *FocusStmt:
		return in.execFocus(s, env)
	case *WhenStmt:
		if in.truth(s.Cond, env, "when") {
			return in.execBlock(s.Body, env)
		} else if s.Otherwise != nil {
			return in.execBlock(s.Otherwise, env)
		}
	case *EachStmt:
		over := in.eval(s.Over, env)
		var items []Value
		switch x := over.(type) {
		case *List:
			items = append(items, x.Items...)
		case string:
			for _, r := range x {
				items = append(items, string(r))
			}
		case *Map:
			for _, k := range x.Keys {
				items = append(items, k)
			}
		default:
			in.fail(s.Line, "'each' needs a list to go through, but got %s", typeName(over))
		}
		for _, it := range items {
			e := newEnv(env)
			e.define(s.Var, it, false)
			c, v := in.execBlock(s.Body, e)
			if c == cStop {
				break
			}
			if c == cSend {
				return c, v
			}
		}
	case *LoopStmt:
		if s.Times != nil {
			n := in.wantInt(s.Line, "the number in 'loop ... times'", in.eval(s.Times, env))
			for i := int64(0); i < n; i++ {
				c, v := in.execBlock(s.Body, env)
				if c == cStop {
					break
				}
				if c == cSend {
					return c, v
				}
			}
			return cNone, nil
		}
		for in.truth(s.While, env, "loop while") {
			in.tick(s.Line)
			c, v := in.execBlock(s.Body, env)
			if c == cStop {
				break
			}
			if c == cSend {
				return c, v
			}
		}
	case *StopStmt:
		return cStop, nil
	case *SkipStmt:
		return cSkip, nil
	case *FastStmt:
		atomic.AddInt32(&in.fastLive, 1)
		in.wg.Add(1)
		go func() {
			in.gil.Lock()
			defer func() {
				if r := recover(); r != nil {
					if re, ok := r.(*RuntimeError); ok {
						in.print("Problem in fast work: " + re.Error())
					} else {
						in.print(fmt.Sprint("Problem in fast work: ", r))
					}
				}
				atomic.AddInt32(&in.fastLive, -1)
				in.gil.Unlock()
				in.wg.Done()
			}()
			in.execBlock(s.Body, newEnv(env))
		}()
		in.tick(s.Line) // let the fast work get started
	case *AttemptStmt:
		return in.attempt(s, env)
	case *FailStmt:
		v := in.eval(s.Value, env)
		panic(&RuntimeError{Line: s.Line, Msg: Text(v), Value: v})
	case *UseStmt:
		if s.Path != "" {
			m := in.loadFile(s.Path, s.Line)
			env.define(m.Name, m, true)
			break
		}
		m := loadModule(in, s.Name)
		if m == nil {
			in.fail(s.Line, "there is no library called '%s' (available: web, json, files, time, store, postgres)", s.Name)
		}
		env.define(s.Name, m, true)
	case *ExpectStmt:
		in.expect(s, env)
	case *BeamDecl:
		env.define(s.Name, &Beam{Name: s.Name, Params: s.Params, Sig: s.Sig, Body: s.Body, Closure: env}, true)
	case *ObjectDecl, *RuleDecl, *ChoiceDecl:
		in.fail(s.Ln(), "objects and rules must be created at the top of the file, not inside a block")
	case *LaunchStmt:
		in.fail(s.Line, "'launch' must be at the top of the file, not inside a block")
	case *TestStmt:
		in.fail(s.Line, "tests must be at the top of the file, not inside a block")
	default:
		in.fail(s.Ln(), "I don't know how to run this")
	}
	return cNone, nil
}

func (in *Interp) attempt(s *AttemptStmt, env *Env) (c ctrl, v Value) {
	var caught *RuntimeError
	func() {
		defer func() {
			if r := recover(); r != nil {
				if re, ok := r.(*RuntimeError); ok {
					caught = re
					return
				}
				panic(r)
			}
		}()
		c, v = in.execBlock(s.Body, newEnv(env))
	}()
	if caught == nil {
		return c, v
	}
	var val Value = caught.Msg
	if caught.Value != nil {
		val = caught.Value
	}
	for _, h := range s.Handlers {
		if h.Type != nil {
			if _, ok := conform(caught.Value, in.resolveType(h.Type, env)); !ok || caught.Value == nil {
				continue // not this kind of problem
			}
		}
		e := newEnv(env)
		if h.Name != "" {
			e.define(h.Name, val, false)
		}
		return in.execBlock(h.Body, e)
	}
	panic(caught) // no 'fails' here is for this problem: it carries on outwards
}

func (in *Interp) expect(s *ExpectStmt, env *Env) {
	if b, ok := s.Value.(*Binary); ok && (b.Op == "is" || b.Op == "is not") {
		l, r := in.eval(b.Left, env), in.eval(b.Right, env)
		same := equal(l, r)
		if b.Op == "is" && !same {
			panic(&testFailure{s.Line, fmt.Sprintf("expected %s, but got %s", toText(r, true), toText(l, true))})
		}
		if b.Op == "is not" && same {
			panic(&testFailure{s.Line, fmt.Sprintf("expected something other than %s", toText(r, true))})
		}
		return
	}
	v := in.eval(s.Value, env)
	if v != true {
		panic(&testFailure{s.Line, fmt.Sprintf("expected yes, but got %s", toText(v, true))})
	}
}

func (in *Interp) assign(s *AssignStmt, env *Env) {
	v := in.eval(s.Value, env)
	switch t := s.Target.(type) {
	case *Ident:
		var declared *Type
		if s.Type != nil {
			declared = in.resolveType(s.Type, env)
			v = in.mustConform(s.Line, v, declared, "'"+t.Name+"'")
		}
		if s.Lock || s.Type != nil {
			sc := env.scope()
			if b := sc.find(t.Name); b != nil {
				if s.Lock {
					in.fail(s.Line, "'%s' already exists here, so it can't be locked again", t.Name)
				}
				in.fail(s.Line, "'%s' already exists here; give a name its type the first time you set it", t.Name)
			}
			sc.define(t.Name, v, s.Lock)
			sc.find(t.Name).t = declared
			return
		}
		for e := env; e != nil && !e.isBuiltins; e = e.parent {
			if b := e.find(t.Name); b != nil {
				if b.locked {
					in.fail(s.Line, "'%s' is locked, so it can't be changed", t.Name)
				}
				if b.t != nil {
					v = in.mustConform(s.Line, v, b.t, "'"+t.Name+"'")
				}
				b.v = v
				return
			}
			if e.obj != nil {
				if i, ok := e.obj.Class.index[t.Name]; ok {
					e.obj.Slots[i] = in.fieldValue(s.Line, e.obj.Class, i, v)
					return
				}
			}
		}
		env.scope().define(t.Name, v, false)
	case *Get:
		obj := in.eval(t.Object, env)
		switch o := obj.(type) {
		case *Instance:
			i, ok := o.Class.index[t.Name]
			if !ok {
				in.fail(s.Line, "%s has no field called '%s'", o.Class.Name, t.Name)
			}
			o.Slots[i] = in.fieldValue(s.Line, o.Class, i, v)
		case *Map:
			o.Set(t.Name, v)
		default:
			in.fail(s.Line, "I can't set '%s' on %s", t.Name, typeName(obj))
		}
	case *Index:
		obj := in.eval(t.Object, env)
		key := in.eval(t.Key, env)
		switch o := obj.(type) {
		case *List:
			i := in.listIndex(s.Line, o, key)
			o.Items[i] = v
		case *Map:
			o.Set(in.mapKey(s.Line, key), v)
		default:
			in.fail(s.Line, "I can't use [ ] on %s", typeName(obj))
		}
	}
}

func (in *Interp) truth(x Expr, env *Env, where string) bool {
	v := in.eval(x, env)
	b, ok := v.(bool)
	if !ok {
		in.fail(x.Ln(), "'%s' needs a yes/no question, but this gave %s", where, typeName(v))
	}
	return b
}

// ---- expressions ----

func (in *Interp) eval(x Expr, env *Env) Value {
	switch x := x.(type) {
	case *NumberLit:
		return x.Value
	case *TextLit:
		return x.Value
	case *BoolLit:
		return x.Value
	case *NothingLit:
		return nil
	case *InterpText:
		var sb strings.Builder
		for _, p := range x.Parts {
			sb.WriteString(Text(in.eval(p, env)))
		}
		return sb.String()
	case *MeExpr:
		for e := env; e != nil; e = e.parent {
			if e.obj != nil {
				return e.obj
			}
		}
		in.fail(x.Line, "'me' only works inside an object's beam")
	case *Ident:
		v, ok := env.lookup(x.Name)
		if !ok {
			msg := fmt.Sprintf("I don't know what '%s' is", x.Name)
			if s := suggest(x.Name, env.names_()); s != "" {
				msg += fmt.Sprintf(". Did you mean '%s'?", s)
			} else if x.Name == "web" {
				msg += ". Add 'use web' at the top of the file"
			}
			in.fail(x.Line, "%s", msg)
		}
		return v
	case *ListLit:
		l := &List{}
		for _, it := range x.Items {
			l.Items = append(l.Items, in.eval(it, env))
		}
		return l
	case *MapLit:
		m := NewMap()
		for i, k := range x.Keys {
			m.Set(k, in.eval(x.Values[i], env))
		}
		return m
	case *Unary:
		v := in.eval(x.Operand, env)
		if x.Op == "not" {
			b, ok := v.(bool)
			if !ok {
				in.fail(x.Line, "'not' needs yes or no, but got %s", typeName(v))
			}
			return !b
		}
		return in.negate(x.Line, v)
	case *Binary:
		return in.binary(x, env)
	case *Get:
		return in.get(in.eval(x.Object, env), x.Name, x.Line)
	case *Index:
		obj := in.eval(x.Object, env)
		key := in.eval(x.Key, env)
		switch o := obj.(type) {
		case *List:
			return o.Items[in.listIndex(x.Line, o, key)]
		case *Map:
			return o.M[in.mapKey(x.Line, key)]
		case string:
			rs := []rune(o)
			l := &List{Items: make([]Value, len(rs))}
			for i, r := range rs {
				l.Items[i] = string(r)
			}
			return l.Items[in.listIndex(x.Line, l, key)]
		}
		in.fail(x.Line, "I can't use [ ] on %s", typeName(obj))
	case *FastExpr:
		return in.startJob(x.Line, func() Value { return in.eval(x.X, env) })
	case *FastEach:
		return in.fastEach(x, env)
	case *WaitExpr:
		v := in.eval(x.X, env)
		j, ok := v.(*Job)
		if !ok {
			in.fail(x.Line, "'wait' needs a job started with fast, but got %s", typeName(v))
		}
		var limit time.Duration
		if x.Timeout != nil {
			secs := floatOf(in, x.Line, "the time limit", in.eval(x.Timeout, env))
			if secs <= 0 {
				in.fail(x.Line, "the time limit must be more than 0 seconds")
			}
			limit = time.Duration(secs * float64(time.Second))
		}
		return in.waitJob(x.Line, j, limit)
	case *Lambda:
		return &Beam{Params: x.Params, Sig: x.Sig, Body: x.Body, Expr: x.Expr, Closure: env}
	case *Call:
		return in.callExpr(x, env, nil, false)
	case *Then:
		left := in.eval(x.Left, env)
		switch r := x.Right.(type) {
		case *Call:
			return in.callExpr(r, env, left, true)
		default:
			fn := in.eval(r, env)
			return in.call(fn, []Value{left}, nil, x.Line, nil)
		}
	}
	in.fail(x.Ln(), "I don't know how to work this out")
	return nil
}

func (in *Interp) listIndex(line int, l *List, key Value) int {
	n := in.wantInt(line, "a list position", key)
	i := int(n)
	if i < 0 {
		i += len(l.Items)
	}
	if i < 0 || i >= len(l.Items) {
		in.fail(line, "position %d is outside the list (it has %d items, starting at 0)", int(n), len(l.Items))
	}
	return i
}

func (in *Interp) mapKey(line int, key Value) string {
	s, ok := key.(string)
	if !ok {
		in.fail(line, "map entries are named with text, but got %s", typeName(key))
	}
	return s
}

func (in *Interp) binary(x *Binary, env *Env) Value {
	if x.Op == "and" || x.Op == "or" {
		l := in.truth(x.Left, env, x.Op)
		if x.Op == "and" && !l {
			return false
		}
		if x.Op == "or" && l {
			return true
		}
		return in.truth(x.Right, env, x.Op)
	}
	l, r := in.eval(x.Left, env), in.eval(x.Right, env)
	switch x.Op {
	case "is":
		return equal(l, r)
	case "is not":
		return !equal(l, r)
	case "<":
		return compareValues(in, x.Line, l, r) < 0
	case ">":
		return compareValues(in, x.Line, l, r) > 0
	case "<=":
		return compareValues(in, x.Line, l, r) <= 0
	case ">=":
		return compareValues(in, x.Line, l, r) >= 0
	case "+":
		if isNumber(l) && isNumber(r) {
			return in.arith(x.Line, "+", l, r)
		}
		if a, ok := l.(*List); ok {
			if b, ok := r.(*List); ok {
				return &List{Items: append(append([]Value{}, a.Items...), b.Items...)}
			}
		}
		_, ls := l.(string)
		_, rs := r.(string)
		if ls || rs {
			return Text(l) + Text(r)
		}
		if v, ok := in.clockOp(x.Line, "+", l, r); ok {
			return v
		}
		in.fail(x.Line, "I can't add %s and %s", typeName(l), typeName(r))
	case "to":
		a := in.wantInt(x.Line, "the start of a range", l)
		b := in.wantInt(x.Line, "the end of a range", r)
		if b-a > 50_000_000 {
			in.fail(x.Line, "this range would hold %d numbers, which is too many to make at once", b-a+1)
		}
		list := &List{}
		if b >= a {
			list.Items = make([]Value, 0, b-a+1)
		}
		for i := a; i <= b; i++ {
			list.Items = append(list.Items, box(i))
		}
		return list
	}
	return in.arith(x.Line, x.Op, l, r)
}

func (in *Interp) callExpr(c *Call, env *Env, first Value, hasFirst bool) Value {
	fn := in.eval(c.Callee, env)
	if b, ok := fn.(*Builtin); ok && b.Lazy != nil {
		rest := []Expr{}
		args := c.Args
		if !hasFirst {
			if len(args) == 0 {
				in.fail(c.Line, "%s needs a list", b.Name)
			}
			first = in.eval(args[0].Value, env)
			args = args[1:]
		}
		for _, a := range args {
			rest = append(rest, a.Value)
		}
		return b.Lazy(in, env, c.Line, first, rest)
	}
	n := len(c.Args)
	if hasFirst {
		n++
	}
	args := make([]Value, 0, n)
	var names []string // stays nil unless some input is named
	if hasFirst {
		args = append(args, first)
	}
	for i, a := range c.Args {
		if a.Name != "" && names == nil {
			names = make([]string, n)
		}
		if names != nil {
			idx := i
			if hasFirst {
				idx++
			}
			names[idx] = a.Name
		}
		args = append(args, in.eval(a.Value, env))
	}
	return in.call(fn, args, names, c.Line, nil)
}

// call runs a beam, builtin, or creates an object.
func (in *Interp) call(fn Value, args []Value, names []string, line int, extra map[string]Value) Value {
	named := false
	for _, n := range names {
		if n != "" {
			named = true
		}
	}
	switch f := fn.(type) {
	case *Builtin:
		if named {
			in.fail(line, "%s doesn't take named inputs", f.Name)
		}
		if f.Fn == nil {
			in.fail(line, "%s can't be used this way", f.Name)
		}
		return f.Fn(in, line, args)
	case *Beam:
		label := "this beam"
		if f.Name != "" {
			label = "beam " + f.Name // cheap: no formatting
		}
		vals := in.matchArgs(label, f.Params, args, names, line)
		parent := f.Closure
		if f.Sig != nil && len(f.Sig.TypeParams) > 0 {
			parent = withTypeParams(parent, f.Sig.TypeParams) // so 'out: List[T] = []' can be read
		}
		if f.Self != nil {
			parent = &Env{names: []string{"me"}, binds: []binding{{v: f.Self, locked: true}}, parent: f.Closure, obj: f.Self}
		}
		ptypes, rtype := in.sigTypes(f)
		for i, pt := range ptypes {
			if pt != nil && i < len(vals) {
				out, ok := conform(vals[i], pt)
				if !ok { // the message is only built when something is wrong
					in.fail(line, "the input '%s' of %s must be %s, but got %s", f.Params[i], label, pt.Describe(), describeValue(vals[i]))
				}
				vals[i] = out
			}
		}
		e := &Env{parent: parent, isScope: true}
		if len(f.Params) <= smallScope {
			e.names = append(make([]string, 0, len(f.Params)+2), f.Params...)
			e.binds = make([]binding, len(f.Params), len(f.Params)+2)
			for i := range f.Params {
				e.binds[i].v = vals[i]
			}
		} else {
			for i, p := range f.Params {
				e.define(p, vals[i], false)
			}
		}
		for k, v := range extra {
			e.define(k, v, false)
		}
		if f.Expr != nil {
			return in.eval(f.Expr, e)
		}
		c, v := in.execBlock(f.Body, e)
		if c == cStop || c == cSkip {
			in.fail(line, "'stop' and 'skip' only work inside a loop")
		}
		if rtype != nil {
			out, ok := conform(v, rtype)
			if !ok {
				if v == nil {
					in.fail(line, "%s must send %s, but it finished without sending one", label, rtype.Describe())
				}
				in.fail(line, "%s must send %s, but it sent %s", label, rtype.Describe(), describeValue(v))
			}
			return out
		}
		return v
	case *Class:
		inst := &Instance{Class: f, Slots: make([]Value, len(f.Fields))}
		given := make([]bool, len(f.Fields))
		pos := 0
		for i, a := range args {
			if names == nil || names[i] == "" {
				if pos >= len(f.Fields) {
					in.fail(line, "%s only has %d fields, but got more values", f.Name, len(f.Fields))
				}
				inst.Slots[pos] = in.fieldValue(line, f, pos, a)
				given[pos] = true
				pos++
				continue
			}
			j, ok := f.index[names[i]]
			if !ok {
				in.fail(line, "%s has no field called '%s'", f.Name, names[i])
			}
			inst.Slots[j] = in.fieldValue(line, f, j, a)
			given[j] = true
		}
		for j, fd := range f.Fields {
			if given[j] {
				continue
			}
			if fd.Default != nil {
				inst.Slots[j] = in.fieldValue(line, f, j, in.eval(fd.Default, f.Env))
			} else {
				in.fail(line, "making %s needs a value for '%s'", aOrAn(f.Name), fd.Name)
			}
		}
		return inst
	}
	in.fail(line, "%s is not a beam, so it can't be called with ( )", typeName(fn))
	return nil
}

func (in *Interp) matchArgs(label string, params []string, args []Value, names []string, line int) []Value {
	if names == nil && len(args) == len(params) {
		return args // the common case: every input given in order
	}
	vals := make([]Value, len(params))
	set := make([]bool, len(params))
	pos := 0
	for i, a := range args {
		n := ""
		if i < len(names) {
			n = names[i]
		}
		if n == "" {
			if pos >= len(params) {
				in.fail(line, "%s takes %d input(s), but got %d", label, len(params), len(args))
			}
			vals[pos], set[pos] = a, true
			pos++
			continue
		}
		ok := false
		for j, p := range params {
			if p == n {
				vals[j], set[j], ok = a, true, true
			}
		}
		if !ok {
			in.fail(line, "%s has no input called '%s'", label, n)
		}
	}
	for j := range params {
		if !set[j] {
			in.fail(line, "%s needs a value for '%s'", label, params[j])
		}
	}
	return vals
}

func (in *Interp) get(obj Value, name string, line int) Value {
	switch o := obj.(type) {
	case *Instance:
		if v, ok := o.field(name); ok {
			return v
		}
		if m, ok := o.Class.Methods[name]; ok {
			return &Beam{Name: m.Name, Params: m.Params, Sig: m.Sig, Body: m.Body, Closure: withTypeParams(o.Class.Env, o.Class.TypeParams), Self: o}
		}
		in.fail(line, "%s has no field or beam called '%s'", o.Class.Name, name)
	case *Class:
		if name == "from" {
			return builtin(o.Name+".from", func(in *Interp, line int, a []Value) Value {
				wantArgs(in, line, o.Name+".from", a, 1)
				m, ok := a[0].(*Map)
				if !ok {
					panic(&RuntimeError{Line: line, Msg: fmt.Sprintf("expected %s as a JSON object, but got %s", o.Name, typeName(a[0])), Status: 400})
				}
				v, problem := in.objectFrom(o, m, "")
				if problem != "" {
					panic(&RuntimeError{Line: line, Msg: problem, Status: 400})
				}
				return v
			})
		}
		in.fail(line, "%s has nothing called '%s' (objects have .from(json) to build one from JSON)", o.Name, name)
	case *Choice:
		return in.choiceMember(o, name, line)
	case Date, Moment, Duration:
		if v, ok := in.clockMember(o, name); ok {
			return v
		}
		in.fail(line, "%s has nothing called '%s'", typeName(o), name)
	case *Module:
		if v, ok := o.Members[name]; ok {
			return v
		}
		in.fail(line, "the %s library has nothing called '%s'", o.Name, name)
	case *Map:
		if v, ok := o.M[name]; ok {
			return v
		}
		if b := mapMethod(o, name); b != nil {
			return b
		}
		return nil
	case *List:
		if b := listMethod(o, name); b != nil {
			return b
		}
	case string:
		if b := textMethod(o, name); b != nil {
			return b
		}
	case *Server:
		if b := serverMethod(in, o, name); b != nil {
			return b
		}
	case *Store:
		if b := storeMethod(in, o, name); b != nil {
			return b
		}
	case *Database:
		if b := databaseMethod(in, o, name); b != nil {
			return b
		}
	case *Response:
		switch name {
		case "status":
			return box(int64(o.Status))
		case "body":
			return o.Body
		case "json":
			v, err := fromJSON(o.Body)
			if err != nil {
				in.fail(line, "this response isn't JSON")
			}
			return v
		}
	}
	in.fail(line, "%s has nothing called '%s'", typeName(obj), name)
	return nil
}

// suggest finds a close spelling, for friendlier errors.
func suggest(name string, options []string) string {
	sort.Strings(options)
	best, bestD := "", 3
	for _, o := range options {
		if o == name {
			continue
		}
		if d := distance(strings.ToLower(name), strings.ToLower(o)); d < bestD {
			best, bestD = o, d
		}
	}
	if len(name) <= 2 && bestD > 1 {
		return ""
	}
	return best
}

func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// fieldValue checks a value against a field's type (if the object declares types)
// and converts it where that's safe, like an Int into a Decimal field.
func (in *Interp) fieldValue(line int, c *Class, slot int, v Value) Value {
	in.classTypes(c)
	if c.types[slot] == nil {
		return v
	}
	out, ok := conform(v, c.types[slot])
	if !ok {
		in.fail(line, "%s.%s must be %s, but got %s", c.Name, c.Fields[slot].Name, c.types[slot].Describe(), describeValue(v))
	}
	return out
}

func (in *Interp) mustConform(line int, v Value, t *Type, what string) Value {
	out, ok := conform(v, t)
	if !ok {
		in.fail(line, "%s must be %s, but got %s", what, t.Describe(), describeValue(v))
	}
	return out
}

// sigTypes resolves a beam's written types once and remembers them.
func (in *Interp) sigTypes(f *Beam) ([]*Type, *Type) {
	sig := f.Sig
	if sig == nil {
		return nil, nil
	}
	if !sig.resolved {
		env := withTypeParams(f.Closure, sig.TypeParams)
		ps := make([]*Type, len(sig.Params))
		for i, te := range sig.Params {
			ps[i] = in.resolveType(te, env)
		}
		sig.params, sig.result, sig.resolved = ps, in.resolveType(sig.Result, env), true
	}
	return sig.params, sig.result
}
