package lang

import (
	"fmt"
	"sort"
	"strings"
)

// ---------------------------------------------------------------------------
// lf check: finds type mistakes before a program runs.
//
// It follows the same rules the engine enforces while running, so a program
// that passes lf check won't hit a type problem at run time, except for data
// from outside (Json), which is checked when it's converted with Item.from.
// ---------------------------------------------------------------------------

// Problem is one mistake found by lf check.
type Problem struct {
	File string
	Line int
	Msg  string
}

func (p Problem) Error() string {
	if p.File != "" {
		return fmt.Sprintf("%s, line %d: %s", p.File, p.Line, p.Msg)
	}
	return fmt.Sprintf("Problem on line %d: %s", p.Line, p.Msg)
}

type cvar struct {
	t        *Type
	locked   bool
	declared bool // given a type when made, or an input: it keeps that type
}

type cscope struct {
	vars   map[string]*cvar
	parent *cscope
	obj    *Class // inside an object's beam: its fields and beams are visible
	scope  bool   // a beam, launch, test or file: new names land here
	open   bool   // a rule about Json items: any field name may be used
	fast   bool   // the edge of fast work: outside values can be read here, never changed
}

// fromFastWork reports whether name lives outside the fast work that sc is inside.
func (s *cscope) fromFastWork(name string) bool {
	crossed := false
	for c := s; c != nil; c = c.parent {
		if _, ok := c.vars[name]; ok {
			return crossed
		}
		if c.fast {
			crossed = true
		}
	}
	return false
}

func (c *checker) noChange(line int, sc *cscope, name string) {
	if sc.fromFastWork(name) {
		c.report(line, "fast work can't change '%s', because other work may be using it at the same time. Send a result instead, and collect it with wait or fast each", name)
	}
}

func (s *cscope) find(name string) (*cvar, *cscope) {
	for c := s; c != nil; c = c.parent {
		if v, ok := c.vars[name]; ok {
			return v, c
		}
	}
	return nil, nil
}

func (s *cscope) home() *cscope {
	for c := s; c != nil; c = c.parent {
		if c.scope {
			return c
		}
	}
	return s
}

type beamCtx struct {
	name   string
	result *Type // nil: gives nothing
	infer  bool  // a one-line or anonymous beam without 'gives': work out what it sends
	sent   *Type
}

type checker struct {
	file      string
	problems  []Problem
	seen      map[string]bool
	classes   map[string]*Class
	choices   map[string]*Choice
	tparams   map[string]bool // type names of the generic being checked (T)
	rules     map[string]*RuleDef
	beam      *beamCtx
	read      func(string) (string, error)
	modules   map[string]*Type
	loading   map[string]bool
	inRoute   bool
	reqNamed  bool     // the route's block names its request (as request)
	routeOpen int      // where the route block's '{' is, for lf fix
	fixes     []Edit   // changes lf fix would make
	route     string   // the path of the route being checked, like /todos/:id
	params    []string // its :names, which are always there when the route runs
	info      *Analysis
	useStack  []UseContext
}

// note records the type of a name on a line, for hover and autocomplete in editors.
func (c *checker) note(line int, name string, t *Type) {
	if c.info == nil || c.file != "" || t == nil {
		return
	}
	m := c.info.Facts[line]
	if m == nil {
		m = map[string]*Type{}
		c.info.Facts[line] = m
	}
	if _, ok := m[name]; !ok || t.Kind != KUnknown {
		m[name] = t
	}
}

// Check finds type mistakes in a program without running it. read loads other
// .lf files used with 'use "file.lf"' (nil if there are none to load).
func Check(src string, read func(string) (string, error)) []Problem {
	prog, err := Parse(src)
	if err != nil {
		if se, ok := err.(*SyntaxError); ok {
			return []Problem{{Line: se.Line, Msg: se.Msg}}
		}
		return []Problem{{Msg: err.Error()}}
	}
	c := &checker{seen: map[string]bool{}, read: read, modules: map[string]*Type{}, loading: map[string]bool{}}
	c.checkProgram(prog, true)
	sort.SliceStable(c.problems, func(i, j int) bool {
		if c.problems[i].File != c.problems[j].File {
			return c.problems[i].File < c.problems[j].File
		}
		return c.problems[i].Line < c.problems[j].Line
	})
	return c.problems
}

func (c *checker) report(line int, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	key := fmt.Sprintf("%s:%d:%s", c.file, line, msg)
	if c.seen[key] {
		return
	}
	c.seen[key] = true
	c.problems = append(c.problems, Problem{File: c.file, Line: line, Msg: msg})
}

var tUnknown = &Type{Kind: KUnknown}

func opt(t *Type) *Type {
	if t.Optional || t.Kind == KJson || t.Kind == KUnknown || t.Kind == KNothing {
		return t
	}
	cp := *t
	cp.Optional = true
	return &cp
}

func nonOpt(t *Type) *Type {
	if !t.Optional {
		return t
	}
	cp := *t
	cp.Optional = false
	return &cp
}

func listOf(t *Type) *Type  { return &Type{Kind: KList, Elem: t} }
func mapOf_(t *Type) *Type  { return &Type{Kind: KMap, Elem: t} }
func lib(name string) *Type { return &Type{Kind: KLibrary, Name: name} }

// ---------- programs and declarations ----------

func (c *checker) checkProgram(prog *Program, main bool) *cscope {
	saved := struct {
		classes map[string]*Class
		rules   map[string]*RuleDef
		choices map[string]*Choice
	}{c.classes, c.rules, c.choices}
	c.classes, c.rules, c.choices = map[string]*Class{}, map[string]*RuleDef{}, map[string]*Choice{}
	defer func() {
		if !main {
			c.classes, c.rules, c.choices = saved.classes, saved.rules, saved.choices
		}
	}()
	top := &cscope{vars: map[string]*cvar{}, scope: true}

	// declarations first, so they can be used before they appear
	for _, s := range prog.Stmts {
		switch d := s.(type) {
		case *RuleDecl:
			c.rules[d.Name] = &RuleDef{Name: d.Name, Methods: d.Methods, Sigs: d.Sigs}
		case *ObjectDecl:
			cl := &Class{Name: d.Name, Fields: d.Fields, Methods: map[string]*BeamDecl{}, Follows: d.Follows, index: map[string]int{}, TypeParams: d.TypeParams}
			for i, f := range d.Fields {
				if _, dup := cl.index[f.Name]; dup {
					c.report(f.Line, "object %s has two fields called '%s'", d.Name, f.Name)
				}
				cl.index[f.Name] = i
			}
			for _, m := range d.Methods {
				cl.Methods[m.Name] = m
			}
			c.classes[d.Name] = cl
		case *ChoiceDecl:
			ch, problem := newChoice(d, nil)
			if problem != "" {
				c.report(d.Line, "%s", problem)
				continue
			}
			if c.classes[d.Name] != nil {
				c.report(d.Line, "'%s' is already the name of an object", d.Name)
			}
			c.choices[d.Name] = ch
		}
	}
	for _, s := range prog.Stmts {
		if u, ok := s.(*UseStmt); ok {
			c.use(u, top)
		}
	}
	for name, cl := range c.classes {
		cl.types = make([]*Type, len(cl.Fields))
		c.withTypeParams(cl.TypeParams, func() {
			for i, f := range cl.Fields {
				cl.types[i] = c.resolve(f.Type)
			}
		})
		top.vars[name] = &cvar{t: &Type{Kind: KClassRef, Class: cl}, locked: true}
	}
	for name, ch := range c.choices {
		for _, o := range ch.Options {
			o.types = make([]*Type, len(o.Fields))
			c.withTypeParams(ch.TypeParams, func() {
				for i, f := range o.Fields {
					o.types[i] = c.resolve(f.Type)
				}
			})
		}
		top.vars[name] = &cvar{t: &Type{Kind: KChoiceRef, Choice: ch}, locked: true}
	}
	for _, s := range prog.Stmts {
		if d, ok := s.(*BeamDecl); ok {
			top.vars[d.Name] = &cvar{t: c.sigType(d.Params, d.Sig), locked: true}
		}
	}
	// objects: rules they follow, default values, and their beams
	for _, s := range prog.Stmts {
		d, ok := s.(*ObjectDecl)
		if !ok {
			continue
		}
		cl := c.classes[d.Name]
		for _, rn := range d.Follows {
			r := c.rules[rn]
			if r == nil {
				c.report(d.Line, "object %s follows '%s', but there is no rule with that name", d.Name, rn)
				continue
			}
			for _, want := range r.Sigs {
				got := cl.Methods[want.Name]
				if got == nil {
					c.report(d.Line, "object %s follows the rule %s, so it must have a beam called '%s'", d.Name, r.Name, want.Name)
					continue
				}
				wt, gt := c.sigType(want.Params, want.Sig), c.sigType(got.Params, got.Sig)
				if !sameType(wt, gt) {
					c.report(got.Line, "the rule %s says %s is %s, but %s's %s is %s", r.Name, want.Name, wt.name(), d.Name, got.Name, gt.name())
				}
			}
		}
		for i, f := range d.Fields {
			if f.Default != nil {
				ft := c.expr(f.Default, top, cl.types[i])
				c.assignable(f.Line, ft, cl.types[i], fmt.Sprintf("the default for %s.%s", d.Name, f.Name))
			}
		}
		for _, m := range d.Methods {
			me := &Type{Kind: KObject, Class: cl}
			for _, tp := range cl.TypeParams {
				me.Args = append(me.Args, &Type{Kind: KParam, Name: tp})
			}
			inner := &cscope{vars: map[string]*cvar{"me": {t: me, locked: true}}, parent: top, obj: cl}
			c.withTypeParams(cl.TypeParams, func() {
				c.beamBody(m.Name, m.Params, m.Sig, m.Body, inner, m.Line)
			})
		}
	}
	for _, s := range prog.Stmts {
		switch d := s.(type) {
		case *BeamDecl:
			c.beamBody(d.Name, d.Params, d.Sig, d.Body, top, d.Line)
		case *RuleDecl, *ObjectDecl, *ChoiceDecl, *UseStmt:
		case *LaunchStmt:
		case *TestStmt:
		default:
			c.stmt(s, top)
		}
	}
	for _, s := range prog.Stmts {
		switch d := s.(type) {
		case *LaunchStmt:
			if main {
				c.block(d.Body, &cscope{vars: map[string]*cvar{}, parent: top, scope: true})
			}
		case *TestStmt:
			if main {
				c.block(d.Body, &cscope{vars: map[string]*cvar{}, parent: top, scope: true})
			}
		}
	}
	return top
}

func (c *checker) sigType(params []string, sig *Sig) *Type {
	t := &Type{Kind: KBeam, PNames: params}
	if sig == nil {
		for range params {
			t.Params = append(t.Params, tUnknown)
		}
		return t
	}
	if len(sig.TypeParams) > 0 {
		var out *Type
		tps := sig.TypeParams
		c.withTypeParams(tps, func() {
			sig.TypeParams = nil
			out = c.sigType(params, sig)
			sig.TypeParams = tps
		})
		out.TParams = tps
		return out
	}
	for _, te := range sig.Params {
		if te == nil {
			t.Params = append(t.Params, tUnknown)
		} else {
			t.Params = append(t.Params, c.resolve(te))
		}
	}
	if sig.Result != nil {
		t.Result = c.resolve(sig.Result)
	}
	return t
}

func (c *checker) resolve(te *TypeExpr) *Type {
	if te == nil {
		return nil
	}
	var t *Type
	switch {
	case c.tparams[te.Name]:
		if len(te.Args) > 0 {
			c.report(te.Line, "%s stands for a type, so it doesn't take anything in [ ]", te.Name)
		}
		t = &Type{Kind: KParam, Name: te.Name}
	case simpleTypes[te.Name] != nil:
		base := *simpleTypes[te.Name]
		t = &base
		if len(te.Args) > 0 {
			c.report(te.Line, "%s doesn't take anything in [ ]", te.Name)
		}
	case te.Name == "List" || te.Name == "Map":
		if len(te.Args) != 1 {
			c.report(te.Line, "say what goes in the %s, like %s[Text]", te.Name, te.Name)
			return tUnknown
		}
		t = &Type{Kind: KList, Elem: c.resolve(te.Args[0])}
		if te.Name == "Map" {
			t.Kind = KMap
		}
	case te.Name == "Beam":
		t = &Type{Kind: KBeam, Result: c.resolve(te.Result)}
		for _, p := range te.Params {
			t.Params = append(t.Params, c.resolve(p))
		}
	case libraryTypes[te.Name]:
		t = lib(te.Name)
	case c.classes[te.Name] != nil:
		cl := c.classes[te.Name]
		t = &Type{Kind: KObject, Class: cl, Args: c.typeArgs(te, cl.TypeParams)}
	case c.rules[te.Name] != nil:
		t = &Type{Kind: KRule, Rule: c.rules[te.Name]}
	case c.choices[te.Name] != nil:
		ch := c.choices[te.Name]
		t = &Type{Kind: KChoice, Choice: ch, Args: c.typeArgs(te, ch.TypeParams)}
	default:
		var names []string
		for k := range simpleTypes {
			names = append(names, k)
		}
		for k := range c.classes {
			names = append(names, k)
		}
		for k := range c.rules {
			names = append(names, k)
		}
		for k := range c.choices {
			names = append(names, k)
		}
		names = append(names, "List", "Map", "Beam", "Server", "Request", "Response", "Store")
		msg := fmt.Sprintf("there is no type called '%s'", te.Name)
		if s := suggest(te.Name, names); s != "" {
			msg += fmt.Sprintf(". Did you mean '%s'?", s)
		} else if te.Name == "String" || te.Name == "Bool" || te.Name == "Boolean" || te.Name == "Number" {
			msg += ". Laser Feet uses Text, Flag, Int, Float and Decimal"
		}
		c.report(te.Line, "%s", msg)
		return tUnknown
	}
	t.Optional = te.Optional
	return t
}

func (c *checker) use(u *UseStmt, top *cscope) {
	if u.Path == "" {
		m := libraryModule(u.Name)
		if m == nil {
			c.report(u.Line, "there is no library called '%s' (available: web, json, files, time, store, postgres)", u.Name)
			return
		}
		top.vars[u.Name] = &cvar{t: m, locked: true}
		return
	}
	ctx := UseContext{}
	if n := len(c.useStack); n > 0 {
		ctx = c.useStack[n-1]
	}
	clean, name, next, err := ResolveUse(ctx, u.Path)
	if err != nil {
		c.report(u.Line, "use \"%s\": %v", u.Path, err)
		return
	}
	if m := c.modules[clean]; m != nil {
		top.vars[name] = &cvar{t: m, locked: true}
		return
	}
	if c.loading[clean] {
		c.report(u.Line, "use \"%s\": these files use each other in a loop", u.Path)
		return
	}
	if c.read == nil {
		top.vars[name] = &cvar{t: &Type{Kind: KModule, Name: name}, locked: true}
		return
	}
	src, err := c.read(clean)
	if err != nil {
		if IsPackagePath(u.Path) {
			c.report(u.Line, "use \"%s\": this package isn't installed. Run: lf get %s", u.Path, u.Path)
		} else {
			c.report(u.Line, "use \"%s\": I couldn't read it (%v)", u.Path, err)
		}
		return
	}
	prog, perr := Parse(src)
	if perr != nil {
		if se, ok := perr.(*SyntaxError); ok {
			c.problems = append(c.problems, Problem{File: clean, Line: se.Line, Msg: se.Msg})
		}
		return
	}
	c.loading[clean] = true
	savedFile, savedBeam := c.file, c.beam
	c.file, c.beam = clean, nil
	c.useStack = append(c.useStack, next)
	scope := c.checkProgram(prog, false)
	c.useStack = c.useStack[:len(c.useStack)-1]
	c.file, c.beam = savedFile, savedBeam
	delete(c.loading, clean)
	m := &Type{Kind: KModule, Name: name, Members: map[string]*Type{}}
	for k, v := range scope.vars {
		m.Members[k] = v.t
	}
	c.modules[clean] = m
	top.vars[name] = &cvar{t: m, locked: true}
}

// ---------- beams ----------

func (c *checker) beamBody(name string, params []string, sig *Sig, body []Stmt, parent *cscope, line int) {
	if sig != nil && len(sig.TypeParams) > 0 {
		tps := sig.TypeParams
		c.withTypeParams(tps, func() {
			sig.TypeParams = nil
			c.beamBody(name, params, sig, body, parent, line)
			sig.TypeParams = tps
		})
		return
	}
	bt := c.sigType(params, sig)
	sc := &cscope{vars: map[string]*cvar{}, parent: parent, scope: true}
	for i, p := range params {
		sc.vars[p] = &cvar{t: bt.Params[i], declared: true}
		c.note(line, p, bt.Params[i])
	}
	c.note(line, name, bt)
	saved := c.beam
	c.beam = &beamCtx{name: "beam " + name, result: bt.Result}
	c.block(body, sc)
	if bt.Result != nil && !bt.Result.Optional && bt.Result.Kind != KNothing && !alwaysExits(body) && !endsWithIncompleteFocus(body) {
		c.report(line, "beam %s gives %s, but it can finish without sending one. Make sure every path ends with send", name, bt.Result.name())
	}
	c.beam = saved
}

// endsWithIncompleteFocus: the focus has already said which arms are missing.
func endsWithIncompleteFocus(body []Stmt) bool {
	if len(body) == 0 {
		return false
	}
	f, ok := body[len(body)-1].(*FocusStmt)
	return ok && !f.Complete
}

// alwaysExits reports whether a block always ends by sending, failing, or leaving a loop.
func alwaysExits(stmts []Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	switch s := stmts[len(stmts)-1].(type) {
	case *SendStmt, *FailStmt, *StopStmt, *SkipStmt:
		return true
	case *WhenStmt:
		return s.Otherwise != nil && alwaysExits(s.Body) && alwaysExits(s.Otherwise)
	case *AttemptStmt:
		return alwaysExits(s.Body) && alwaysExits(s.Handler)
	case *FocusStmt:
		if !s.Complete {
			return false
		}
		for _, a := range s.Arms {
			if !alwaysExits(a.Body) {
				return false
			}
		}
		return true
	}
	return false
}

// ---------- statements ----------

func (c *checker) block(stmts []Stmt, sc *cscope) {
	for i, s := range stmts {
		c.stmt(s, sc)
		// "when x is nothing { send ... }" means x is known to be there afterwards
		if w, ok := s.(*WhenStmt); ok && w.Otherwise == nil && alwaysExits(w.Body) {
			if name, isNothing := nothingTest(w.Cond); isNothing {
				c.narrow(name, sc)
			}
		}
		_ = i
	}
}

// nothingTest recognises "name is nothing" and "name is not nothing".
func nothingTest(e Expr) (string, bool) {
	b, ok := e.(*Binary)
	if !ok || (b.Op != "is" && b.Op != "is not") {
		return "", false
	}
	id, ok := b.Left.(*Ident)
	if _, isNothing := b.Right.(*NothingLit); !ok || !isNothing {
		return "", false
	}
	return id.Name, b.Op == "is"
}

func (c *checker) narrow(name string, sc *cscope) {
	if v, _ := sc.find(name); v != nil && v.t != nil && v.t.Optional {
		sc.vars[name] = &cvar{t: nonOpt(v.t), locked: v.locked, declared: v.declared}
	}
}

func (c *checker) stmt(s Stmt, sc *cscope) {
	switch s := s.(type) {
	case *ShowStmt:
		c.expr(s.Value, sc, nil)
	case *ExprStmt:
		c.expr(s.X, sc, nil)
	case *AssignStmt:
		c.assign(s, sc)
		if id, ok := s.Target.(*Ident); ok {
			if v, _ := sc.find(id.Name); v != nil {
				c.note(s.Line, id.Name, v.t)
			}
		}
	case *SendStmt:
		c.send(s, sc)
	case *FocusStmt:
		c.focus(s, sc)
	case *WhenStmt:
		c.flag(s.Cond, sc, "when")
		name, isNothing := nothingTest(s.Cond)
		then := &cscope{vars: map[string]*cvar{}, parent: sc}
		if name != "" && !isNothing {
			c.narrow(name, then)
		}
		c.block(s.Body, then)
		c.merge(then, sc)
		if s.Otherwise != nil {
			other := &cscope{vars: map[string]*cvar{}, parent: sc}
			if name != "" && isNothing {
				c.narrow(name, other)
			}
			c.block(s.Otherwise, other)
			c.merge(other, sc)
		}
	case *EachStmt:
		over := c.expr(s.Over, sc, nil)
		var item *Type
		switch {
		case over.Kind == KList:
			item = over.Elem
		case over.Kind == KText, over.Kind == KMap:
			item = tText
		case over.Kind == KJson || over.Kind == KUnknown:
			item = over
		default:
			c.report(s.Line, "'each' needs a list to go through, but this is %s", over.name())
			item = tUnknown
		}
		if over.Optional {
			c.report(s.Line, "this list might be nothing here. Check it first with: when ... is nothing { ... }")
		}
		inner := &cscope{vars: map[string]*cvar{s.Var: {t: item}}, parent: sc}
		c.note(s.Line, s.Var, item)
		c.block(s.Body, inner)
		c.merge(inner, sc)
	case *LoopStmt:
		if s.While != nil {
			c.flag(s.While, sc, "loop while")
		} else {
			c.wantInt(s.Times, sc, "'loop ... times'")
		}
		inner := &cscope{vars: map[string]*cvar{}, parent: sc}
		c.block(s.Body, inner)
		c.merge(inner, sc)
	case *StopStmt, *SkipStmt:
	case *FastStmt:
		inner := &cscope{vars: map[string]*cvar{}, parent: sc, fast: true, scope: true}
		c.block(s.Body, inner)
	case *AttemptStmt:
		inner := &cscope{vars: map[string]*cvar{}, parent: sc}
		c.block(s.Body, inner)
		c.merge(inner, sc)
		handler := &cscope{vars: map[string]*cvar{}, parent: sc}
		if s.ErrName != "" {
			handler.vars[s.ErrName] = &cvar{t: tJson}
		}
		c.block(s.Handler, handler)
		c.merge(handler, sc)
	case *FailStmt:
		c.expr(s.Value, sc, nil)
	case *UseStmt:
		c.use(s, sc.home())
	case *ExpectStmt:
		c.flag(s.Value, sc, "expect")
	case *BeamDecl:
		sc.vars[s.Name] = &cvar{t: c.sigType(s.Params, s.Sig), locked: true}
		c.beamBody(s.Name, s.Params, s.Sig, s.Body, sc, s.Line)
	case *ObjectDecl, *RuleDecl, *ChoiceDecl:
		c.report(s.Ln(), "objects and rules must be created at the top of the file, not inside a block")
	case *LaunchStmt:
		c.report(s.Line, "'launch' must be at the top of the file, not inside a block")
	case *TestStmt:
		c.report(s.Line, "tests must be at the top of the file, not inside a block")
	}
}

// merge moves names first made inside a block to the beam's scope, as the engine does.
func (c *checker) merge(inner, outer *cscope) {
	home := outer.home()
	for k, v := range inner.vars {
		if existing, _ := outer.find(k); existing == nil {
			home.vars[k] = v
		}
	}
}

func (c *checker) assign(s *AssignStmt, sc *cscope) {
	switch t := s.Target.(type) {
	case *Ident:
		var declared *Type
		if s.Type != nil {
			declared = c.resolve(s.Type)
		}
		existing, _ := sc.find(t.Name)
		vt := c.expr(s.Value, sc, firstNonNil(declared, typeOf(existing)))
		if declared != nil {
			if existing != nil {
				c.report(s.Line, "'%s' already exists; give a name its type the first time you set it", t.Name)
			}
			c.assignable(s.Line, vt, declared, "'"+t.Name+"'")
			sc.home().vars[t.Name] = &cvar{t: declared, locked: s.Lock, declared: true}
			return
		}
		if s.Lock {
			if existing != nil {
				c.report(s.Line, "'%s' already exists here, so it can't be locked again", t.Name)
			}
			sc.home().vars[t.Name] = &cvar{t: c.settle(s.Line, vt, t.Name), locked: true}
			return
		}
		if existing == nil && sc.obj != nil {
			if i, ok := sc.obj.index[t.Name]; ok {
				c.assignable(s.Line, vt, sc.obj.types[i], sc.obj.Name+"."+t.Name)
				return
			}
		}
		if existing == nil {
			if vt.Kind == KNothing {
				c.report(s.Line, "'%s' is set to nothing, so I can't tell what it will hold. Give it a type: %s: Text? = nothing", t.Name, t.Name)
				vt = tUnknown
			}
			sc.home().vars[t.Name] = &cvar{t: c.settle(s.Line, vt, t.Name)}
			return
		}
		if existing.locked {
			c.report(s.Line, "'%s' is locked, so it can't be changed", t.Name)
			return
		}
		c.noChange(s.Line, sc, t.Name)
		c.assignable(s.Line, vt, existing.t, "'"+t.Name+"'")
	case *Get:
		if id, ok := t.Object.(*Ident); ok {
			c.noChange(s.Line, sc, id.Name)
		}
		obj := c.expr(t.Object, sc, nil)
		ft := c.member(t.Line, obj, t.Name, true)
		vt := c.expr(s.Value, sc, ft)
		c.assignable(s.Line, vt, ft, t.Name)
	case *Index:
		if id, ok := t.Object.(*Ident); ok {
			c.noChange(s.Line, sc, id.Name)
		}
		obj := c.expr(t.Object, sc, nil)
		key := c.expr(t.Key, sc, nil)
		vt := c.expr(s.Value, sc, nil)
		switch obj.Kind {
		case KList:
			c.intType(t.Line, key, "a list position")
			c.assignable(s.Line, vt, obj.Elem, "an item of this list")
		case KMap:
			c.assignable(t.Line, key, tText, "a map entry's name")
			c.assignable(s.Line, vt, obj.Elem, "an entry of this map")
		case KJson, KUnknown:
		default:
			c.report(t.Line, "I can't use [ ] on %s", obj.name())
		}
	}
}

func typeOf(v *cvar) *Type {
	if v == nil {
		return nil
	}
	return v.t
}

func firstNonNil(ts ...*Type) *Type {
	for _, t := range ts {
		if t != nil {
			return t
		}
	}
	return nil
}

// settle makes sure a new name's type is fully known.
func (c *checker) settle(line int, t *Type, name string) *Type {
	if (t.Kind == KList || t.Kind == KMap) && t.Elem != nil && t.Elem.Kind == KUnknown && !t.Elem.Optional {
		kind := "List"
		if t.Kind == KMap {
			kind = "Map"
		}
		c.report(line, "say what '%s' will hold, like: %s: %s[Text] = ...", name, name, kind)
	}
	if t.Lit {
		cp := *t
		cp.Lit = false
		return &cp
	}
	return t
}

func (c *checker) send(s *SendStmt, sc *cscope) {
	if c.beam == nil {
		if c.inRoute {
			if s.Value != nil {
				c.expr(s.Value, sc, nil)
			}
			return
		}
		c.report(s.Line, "'send' only works inside a beam")
		return
	}
	b := c.beam
	if s.Value == nil {
		if b.result != nil && !b.result.Optional && b.result.Kind != KNothing {
			c.report(s.Line, "%s gives %s, so 'send' needs a value here", b.name, b.result.name())
		}
		return
	}
	vt := c.expr(s.Value, sc, b.result)
	switch {
	case b.infer:
		if b.sent == nil || b.sent.Kind == KNothing {
			b.sent = vt
		}
	case b.result == nil:
		c.report(s.Line, "%s sends a value, so say what it gives, like: gives %s", b.name, suggestTypeName(vt))
	default:
		c.assignable(s.Line, vt, b.result, "what "+b.name+" sends")
	}
}

func suggestTypeName(t *Type) string {
	if t == nil || t.Kind == KUnknown || t.Kind == KNothing {
		return "Text"
	}
	return t.name()
}

// ---------- expressions ----------

func (c *checker) flag(e Expr, sc *cscope, where string) {
	t := c.expr(e, sc, tFlag)
	if t.Kind != KFlag && t.Kind != KJson && t.Kind != KUnknown {
		c.report(e.Ln(), "'%s' needs a yes/no question, but this is %s", where, t.name())
	} else if t.Optional {
		c.report(e.Ln(), "'%s' needs a yes/no answer, but this might be nothing", where)
	}
}

func (c *checker) wantInt(e Expr, sc *cscope, what string) {
	c.intType(e.Ln(), c.expr(e, sc, tInt), what)
}

func (c *checker) intType(line int, t *Type, what string) {
	if t.Kind != KInt && t.Kind != KJson && t.Kind != KUnknown {
		c.report(line, "%s must be an Int, but this is %s", what, t.name())
	}
}

func isNum(t *Type) bool {
	return t.Kind == KInt || t.Kind == KFloat || t.Kind == KDecimal
}

func (c *checker) expr(e Expr, sc *cscope, want *Type) *Type {
	t := c.expr1(e, sc, want)
	if t == nil {
		return tUnknown
	}
	return t
}

func (c *checker) expr1(e Expr, sc *cscope, want *Type) *Type {
	switch x := e.(type) {
	case *NumberLit:
		if _, ok := x.Value.(int64); ok {
			return tInt
		}
		return &Type{Kind: KDecimal, Lit: true}
	case *TextLit:
		return tText
	case *InterpText:
		for _, p := range x.Parts {
			c.expr(p, sc, nil)
		}
		return tText
	case *BoolLit:
		return tFlag
	case *NothingLit:
		return tNothing
	case *MeExpr:
		for s := sc; s != nil; s = s.parent {
			if s.obj != nil {
				return &Type{Kind: KObject, Class: s.obj}
			}
		}
		c.report(x.Line, "'me' only works inside an object's beam")
		return tUnknown
	case *Ident:
		t := c.ident(x, sc)
		c.note(x.Line, x.Name, t)
		return t
	case *ListLit:
		if want != nil && want.Kind == KJson {
			for _, it := range x.Items {
				c.expr(it, sc, tJson)
			}
			return tJson
		}
		if len(x.Items) == 0 {
			if want != nil && want.Kind == KList {
				return want
			}
			return listOf(tUnknown)
		}
		var elemWant *Type
		if want != nil && want.Kind == KList {
			elemWant = want.Elem
		}
		var elem *Type
		for _, it := range x.Items {
			t := c.expr(it, sc, elemWant)
			if elemWant != nil {
				c.assignable(it.Ln(), t, elemWant, "an item of this list")
				continue
			}
			elem = c.join(it.Ln(), elem, t, "the items of a list")
		}
		if elemWant != nil {
			return listOf(elemWant)
		}
		return listOf(c.settleLit(elem))
	case *MapLit:
		if want != nil && want.Kind == KMap {
			for _, v := range x.Values {
				c.assignable(v.Ln(), c.expr(v, sc, want.Elem), want.Elem, "an entry of this map")
			}
			return want
		}
		var elem *Type
		mixed := false
		for _, v := range x.Values {
			t := c.expr(v, sc, nil)
			if elem == nil {
				elem = t
			} else if !sameType(elem, t) {
				mixed = true
			}
		}
		if mixed || elem == nil || want == nil || want.Kind == KJson {
			// maps with mixed values are records, like JSON
			return tJson
		}
		return mapOf_(c.settleLit(elem))
	case *Unary:
		t := c.expr(x.Operand, sc, nil)
		if x.Op == "not" {
			if t.Kind != KFlag && t.Kind != KJson && t.Kind != KUnknown {
				c.report(x.Line, "'not' needs yes or no, but this is %s", t.name())
			}
			return tFlag
		}
		if !isNum(t) && t.Kind != KJson && t.Kind != KUnknown {
			c.report(x.Line, "'-' needs a number, but this is %s", t.name())
		}
		return t
	case *Binary:
		return c.binary(x, sc)
	case *Get:
		obj := c.expr(x.Object, sc, nil)
		t := c.member(x.Line, obj, x.Name, false)
		c.note(x.Line, x.Name, t)
		return t
	case *Index:
		obj := c.expr(x.Object, sc, nil)
		key := c.expr(x.Key, sc, nil)
		if obj.Optional {
			c.report(x.Line, "this might be nothing here. Check it first with: when ... is nothing { ... }")
		}
		switch obj.Kind {
		case KList:
			c.intType(x.Line, key, "a list position")
			return obj.Elem
		case KText:
			c.intType(x.Line, key, "a letter position")
			return tText
		case KMap:
			c.assignable(x.Line, key, tText, "a map entry's name")
			return opt(obj.Elem)
		case KJson, KUnknown:
			return tJson
		}
		c.report(x.Line, "I can't use [ ] on %s", obj.name())
		return tUnknown
	case *FastExpr:
		inner := &cscope{vars: map[string]*cvar{}, parent: sc, fast: true}
		t := c.expr(x.X, inner, nil)
		return &Type{Kind: KLibrary, Name: "Job", Elem: c.settleLit(t)}
	case *WaitExpr:
		j := c.expr(x.X, sc, nil)
		if x.Timeout != nil {
			c.numberArg(x.Line, c.expr(x.Timeout, sc, nil), "the time limit")
		}
		if j.Kind == KJson || j.Kind == KUnknown {
			return tJson
		}
		if j.Kind != KLibrary || j.Name != "Job" {
			c.report(x.Line, "'wait' needs a job started with fast, but this is %s", j.name())
			return tUnknown
		}
		if j.Elem == nil {
			return tNothing
		}
		return j.Elem
	case *FastEach:
		over := c.expr(x.Over, sc, nil)
		item := tUnknown
		switch {
		case over.Kind == KList:
			item = over.Elem
		case over.Kind == KJson || over.Kind == KUnknown:
			item = tJson
		default:
			c.report(x.Line, "'fast each' needs a list, but this is %s", over.name())
		}
		inner := &cscope{vars: map[string]*cvar{x.Var: {t: item}}, parent: sc, fast: true, scope: true}
		c.note(x.Line, x.Var, item)
		saved := c.beam
		ctx := &beamCtx{name: "fast each", infer: true}
		c.beam = ctx
		c.block(x.Body, inner)
		c.beam = saved
		if ctx.sent == nil {
			return listOf(tNothing)
		}
		if alwaysExits(x.Body) {
			return listOf(c.settleLit(ctx.sent))
		}
		// an item that doesn't send gives nothing in its place
		return listOf(opt(c.settleLit(ctx.sent)))
	case *Lambda:
		return c.lambda(x, sc, want, nil)
	case *Call:
		return c.call(x, sc, nil, false)
	case *Then:
		left := c.expr(x.Left, sc, nil)
		switch r := x.Right.(type) {
		case *Call:
			return c.callWith(r, sc, left)
		default:
			fn := c.expr(r, sc, nil)
			return c.apply(x.Line, fn, []*Type{left}, nil, []Expr{x.Left}, sc)
		}
	}
	return tUnknown
}

func (c *checker) settleLit(t *Type) *Type {
	if t == nil {
		return tUnknown
	}
	if t.Lit {
		cp := *t
		cp.Lit = false
		return &cp
	}
	return t
}

// join works out one type for values that must agree, like the items of a list.
func (c *checker) join(line int, a, b *Type, what string) *Type {
	if a == nil {
		return b
	}
	if b.Kind == KNothing {
		return opt(a)
	}
	if a.Kind == KNothing {
		return opt(b)
	}
	if a.Kind == KUnknown || a.Kind == KJson {
		return a
	}
	if b.Kind == KUnknown || b.Kind == KJson {
		return b
	}
	if isNum(a) && isNum(b) {
		r, ok := numJoin(a, b)
		if ok {
			return r
		}
	}
	if sameType(nonOpt(a), nonOpt(b)) {
		if b.Optional {
			return opt(a)
		}
		return a
	}
	c.report(line, "%s must all be the same type, but I found %s and %s", what, a.name(), b.name())
	return a
}

func numJoin(a, b *Type) (*Type, bool) {
	switch {
	case a.Kind == b.Kind:
		return &Type{Kind: a.Kind, Lit: a.Lit && b.Lit}, true
	case a.Kind == KInt:
		return b, true
	case b.Kind == KInt:
		return a, true
	case a.Kind == KFloat && b.Lit, b.Kind == KFloat && a.Lit:
		return tFloat, true
	}
	return nil, false
}

func (c *checker) ident(x *Ident, sc *cscope) *Type {
	if v, _ := sc.find(x.Name); v != nil {
		return v.t
	}
	for s := sc; s != nil; s = s.parent {
		if s.obj != nil {
			if i, ok := s.obj.index[x.Name]; ok {
				return s.obj.types[i]
			}
			if m, ok := s.obj.Methods[x.Name]; ok {
				return c.sigType(m.Params, m.Sig)
			}
		}
	}
	if c.inRoute && !c.reqNamed && x.Name == "request" {
		c.report(x.Line, "name the request to use it: write 'as request' before the route's '{', like server.get(\"/hello\") as request { ... } (lf fix can do this for you)")
		c.fix(c.routeOpen, "as request ")
		c.reqNamed = true // one message per route is enough
		return lib("Request")
	}
	for s := sc; s != nil; s = s.parent {
		if s.open {
			return tJson
		}
	}
	if b := builtinType(x.Name); b != nil {
		return b
	}
	var names []string
	for s := sc; s != nil; s = s.parent {
		for k := range s.vars {
			names = append(names, k)
		}
		if s.obj != nil {
			for _, f := range s.obj.Fields {
				names = append(names, f.Name)
			}
		}
	}
	msg := fmt.Sprintf("I don't know what '%s' is", x.Name)
	if s := suggest(x.Name, names); s != "" {
		msg += fmt.Sprintf(". Did you mean '%s'?", s)
	} else if libraryModule(x.Name) != nil {
		msg += fmt.Sprintf(". Add 'use %s' at the top of the file", x.Name)
	}
	c.report(x.Line, "%s", msg)
	return tUnknown
}

func (c *checker) binary(x *Binary, sc *cscope) *Type {
	switch x.Op {
	case "and", "or":
		c.flag(x.Left, sc, x.Op)
		c.flag(x.Right, sc, x.Op)
		return tFlag
	case "is", "is not":
		l := c.expr(x.Left, sc, nil)
		r := c.expr(x.Right, sc, nil)
		if l.Kind != KNothing && r.Kind != KNothing && !comparable(l, r) {
			c.report(x.Line, "'%s' compares %s with %s, which can never be the same", x.Op, l.name(), r.name())
		}
		return tFlag
	}
	l := c.expr(x.Left, sc, nil)
	r := c.expr(x.Right, sc, nil)
	dyn := l.Kind == KJson || l.Kind == KUnknown || r.Kind == KJson || r.Kind == KUnknown
	for _, t := range []*Type{l, r} {
		if t.Optional && t.Kind != KJson && t.Kind != KUnknown {
			c.report(x.Line, "'%s' can't use a value that might be nothing. Check it first with: when ... is nothing { ... }", x.Op)
			return tUnknown
		}
	}
	switch x.Op {
	case "<", ">", "<=", ">=":
		if !dyn && !(isNum(l) && isNum(r)) && !(l.Kind == KText && r.Kind == KText) {
			c.report(x.Line, "'%s' compares numbers or text, but got %s and %s", x.Op, l.name(), r.name())
		}
		if !dyn && isNum(l) && isNum(r) {
			if _, ok := numJoin(l, r); !ok {
				c.report(x.Line, "'%s' can't compare a Float with an exact Decimal. Convert one first: float(x) or decimal(x)", x.Op)
			}
		}
		return tFlag
	case "to":
		c.intType(x.Line, l, "the start of a range")
		c.intType(x.Line, r, "the end of a range")
		return listOf(tInt)
	case "+":
		if l.Kind == KText || r.Kind == KText {
			return tText
		}
		if l.Kind == KList && r.Kind == KList {
			return listOf(c.join(x.Line, l.Elem, r.Elem, "the items of both lists"))
		}
	}
	if dyn {
		if isNum(l) {
			return l
		}
		if isNum(r) {
			return r
		}
		return tJson
	}
	if !isNum(l) || !isNum(r) {
		c.report(x.Line, "'%s' needs two numbers, but got %s and %s", x.Op, l.name(), r.name())
		return tUnknown
	}
	res, ok := numJoin(l, r)
	if !ok {
		c.report(x.Line, "'%s' can't mix a Float with an exact Decimal. Convert one first: float(x) or decimal(x)", x.Op)
		return tUnknown
	}
	if x.Op == "/" && res.Kind == KInt {
		return tDecimal // dividing Ints gives an exact Decimal: 7 / 2 is 3.5
	}
	return res
}

func comparable(a, b *Type) bool {
	if a.Kind == KJson || b.Kind == KJson || a.Kind == KUnknown || b.Kind == KUnknown {
		return true
	}
	if isNum(a) && isNum(b) {
		return true
	}
	if a.Kind == KRule || b.Kind == KRule {
		return a.Kind == KObject || b.Kind == KObject || a.Kind == b.Kind
	}
	return a.Kind == b.Kind && (a.Kind != KObject || a.Class == b.Class) && (a.Kind != KChoice || a.Choice == b.Choice)
}

// member is the type of x.name.
func (c *checker) member(line int, obj *Type, name string, forSet bool) *Type {
	if obj.Kind == KJson || obj.Kind == KUnknown {
		return tJson
	}
	if obj.Optional {
		c.report(line, "this might be nothing here, so it has no '%s'. Check it first with: when ... is nothing { ... }", name)
		return tUnknown
	}
	switch obj.Kind {
	case KObject:
		binds := paramBinds(obj.Class.TypeParams, obj.Args)
		if i, ok := obj.Class.index[name]; ok {
			return subst(obj.Class.types[i], binds)
		}
		if m, ok := obj.Class.Methods[name]; ok && !forSet {
			var st *Type
			c.withTypeParams(obj.Class.TypeParams, func() { st = c.sigType(m.Params, m.Sig) })
			return subst(st, binds)
		}
		c.report(line, "%s has no field or beam called '%s'%s", obj.Class.Name, name, didYouMean(name, fieldNames(obj.Class)))
		return tUnknown
	case KRule:
		for _, s := range obj.Rule.Sigs {
			if s.Name == name {
				return c.sigType(s.Params, s.Sig)
			}
		}
		c.report(line, "the rule %s has no beam called '%s'", obj.Rule.Name, name)
		return tUnknown
	case KClassRef:
		if name == "from" {
			return &Type{Kind: KBuiltin, Name: obj.Class.Name + ".from", Class: obj.Class}
		}
	case KChoiceRef:
		if o := obj.Choice.option(name); o != nil {
			if o.single != nil {
				return &Type{Kind: KChoice, Choice: obj.Choice, Args: argsFrom(obj.Choice.TypeParams, nil)}
			}
			return &Type{Kind: KClassRef, Class: o} // Status.Shipped(...) makes a Status
		}
		if name == "from" {
			return &Type{Kind: KBuiltin, Name: obj.Choice.Name + ".from", Choice: obj.Choice}
		}
		c.report(line, "choice %s has no option called '%s'%s", obj.Choice.Name, name, didYouMean(name, obj.Choice.optionNames()))
		return tUnknown
	case KChoice:
		hint := obj.Choice.Options[0].Name + " { ... }"
		for _, o := range obj.Choice.Options {
			if o.single == nil {
				hint = o.Name + "(" + strings.Join(fieldNames(o), ", ") + ") { ... }"
				break
			}
		}
		c.report(line, "a %s may be any of its options, so it has no '%s'. Use focus to look inside it: focus x { %s }", obj.Choice.Name, name, hint)
		return tUnknown
	case KModule:
		if obj.Members == nil {
			return tUnknown
		}
		if t, ok := obj.Members[name]; ok {
			return t
		}
		c.report(line, "%s has nothing called '%s'%s", obj.Name, name, didYouMean(name, keys(obj.Members)))
		return tUnknown
	case KMap:
		if name == "keys" || name == "has" {
			return &Type{Kind: KBuiltin, Name: "map." + name, Elem: obj}
		}
		return opt(obj.Elem)
	case KList, KText, KLibrary:
		if obj.Kind == KLibrary && obj.Name == "Params" {
			for _, p := range c.params {
				if p == name {
					return tText
				}
			}
			if c.route != "" {
				c.report(line, "the path %s has no :%s, so request.params.%s is never set%s", c.route, name, name, didYouMean(name, c.params))
				return tText
			}
			return opt(tText)
		}
		prefix := map[Kind]string{KList: "list.", KText: "text."}[obj.Kind]
		if obj.Kind == KLibrary {
			prefix = strings.ToLower(obj.Name) + "."
		}
		if t := memberType(prefix+name, obj); t != nil {
			return t
		}
	}
	c.report(line, "%s has nothing called '%s'", obj.name(), name)
	return tUnknown
}

func fieldNames(cl *Class) []string {
	var out []string
	for _, f := range cl.Fields {
		out = append(out, f.Name)
	}
	for m := range cl.Methods {
		out = append(out, m)
	}
	return out
}

func keys(m map[string]*Type) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func didYouMean(name string, options []string) string {
	if s := suggest(name, options); s != "" {
		return fmt.Sprintf(". Did you mean '%s'?", s)
	}
	return ""
}

// ---------- calls ----------

func (c *checker) lambda(x *Lambda, sc *cscope, want *Type, implicit map[string]*Type) *Type {
	inner := &cscope{vars: map[string]*cvar{}, parent: sc, scope: true}
	for k, v := range implicit {
		inner.vars[k] = &cvar{t: v}
	}
	t := &Type{Kind: KBeam, PNames: x.Params}
	for i, p := range x.Params {
		pt := tUnknown
		if x.Sig != nil && i < len(x.Sig.Params) && x.Sig.Params[i] != nil {
			pt = c.resolve(x.Sig.Params[i])
		} else if want != nil && want.Kind == KBeam && i < len(want.Params) && want.Params[i] != nil {
			pt = want.Params[i]
		}
		t.Params = append(t.Params, pt)
		inner.vars[p] = &cvar{t: pt, declared: true}
	}
	if x.Expr != nil {
		t.Result = c.settleLit(c.expr(x.Expr, inner, nil))
		return t
	}
	saved := c.beam
	ctx := &beamCtx{name: "this beam", infer: true}
	if x.Sig != nil && x.Sig.Result != nil {
		ctx.result, ctx.infer = c.resolve(x.Sig.Result), false
	}
	c.beam = ctx
	c.block(x.Body, inner)
	c.beam = saved
	if ctx.infer {
		t.Result = ctx.sent
	} else {
		t.Result = ctx.result
	}
	return t
}

func (c *checker) call(x *Call, sc *cscope, first *Type, hasFirst bool) *Type {
	if g, ok := x.Callee.(*Get); ok && (g.Name == "add" || g.Name == "remove") {
		if id, ok := g.Object.(*Ident); ok {
			c.noChange(x.Line, sc, id.Name)
		}
	}
	if hasFirst {
		return c.callWith(x, sc, first)
	}
	fn := c.expr(x.Callee, sc, nil)
	if fn.Kind == KBeam && len(fn.TParams) > 0 {
		return c.genericCall(x.Line, fn, nil, false, x.Args, sc)
	}
	var args []*Type
	var names []string
	var exprs []Expr
	for i, a := range x.Args {
		var want *Type
		if fn.Kind == KBeam && i < len(fn.Params) {
			want = fn.Params[i]
		} else if fn.Kind == KBuiltin {
			want = builtinWant(fn.Name, i)
		}
		if lam, ok := a.Value.(*Lambda); ok && isTransactionCall(x.Callee) {
			// transaction with db as tx { ... }: the block's input is the transaction
			var implicit map[string]*Type
			if len(lam.Params) == 0 {
				c.report(x.Line, "name the transaction: write 'transaction with %s as tx { ... }' (lf fix can do this for you)", shortText(x.Callee.(*Get).Object))
				c.fix(lam.Open, "as tx ")
				implicit = map[string]*Type{"tx": lib("Database")}
			}
			args = append(args, c.lambda(lam, sc, &Type{Kind: KBeam, Params: []*Type{lib("Database")}}, implicit))
		} else if lam, ok := a.Value.(*Lambda); ok && c.isRouteCall(x) {
			saved, savedRoute, savedParams, savedNamed, savedOpen := c.inRoute, c.route, c.params, c.reqNamed, c.routeOpen
			c.inRoute, c.route, c.params, c.reqNamed, c.routeOpen = true, "", nil, len(lam.Params) > 0, lam.Open
			if len(x.Args) > 0 {
				if p, ok := x.Args[0].Value.(*TextLit); ok {
					c.route = p.Value
					for _, part := range strings.Split(p.Value, "/") {
						if strings.HasPrefix(part, ":") {
							c.params = append(c.params, part[1:])
						}
					}
				}
			}
			savedBeam := c.beam
			c.beam = nil
			args = append(args, c.lambda(lam, sc, &Type{Kind: KBeam, Params: []*Type{lib("Request")}}, nil))
			c.beam, c.inRoute, c.route, c.params, c.reqNamed, c.routeOpen = savedBeam, saved, savedRoute, savedParams, savedNamed, savedOpen
		} else if fn.Kind == KBuiltin && lazyBuiltins[fn.Name] && i > 0 {
			args = append(args, nil) // checked by the built-in itself, once per item
		} else {
			args = append(args, c.expr(a.Value, sc, want))
		}
		names = append(names, a.Name)
		exprs = append(exprs, a.Value)
	}
	return c.apply(x.Line, fn, args, names, exprs, sc)
}

func (c *checker) isRouteCall(x *Call) bool {
	g, ok := x.Callee.(*Get)
	return ok && (g.Name == "get" || g.Name == "post" || g.Name == "on")
}

func (c *checker) callWith(x *Call, sc *cscope, first *Type) *Type {
	fn := c.expr(x.Callee, sc, nil)
	if fn.Kind == KBeam && len(fn.TParams) > 0 {
		return c.genericCall(x.Line, fn, first, true, x.Args, sc)
	}
	args := []*Type{first}
	names := []string{""}
	exprs := []Expr{nil}
	for i, a := range x.Args {
		if fn.Kind == KBuiltin && lazyBuiltins[fn.Name] {
			args = append(args, nil)
		} else {
			var want *Type
			if fn.Kind == KBeam && i+1 < len(fn.Params) {
				want = fn.Params[i+1]
			} else if fn.Kind == KBuiltin {
				want = builtinWant(fn.Name, i+1)
			}
			args = append(args, c.expr(a.Value, sc, want))
		}
		names = append(names, a.Name)
		exprs = append(exprs, a.Value)
	}
	return c.apply(x.Line, fn, args, names, exprs, sc)
}

func (c *checker) apply(line int, fn *Type, args []*Type, names []string, exprs []Expr, sc *cscope) *Type {
	switch fn.Kind {
	case KJson, KUnknown:
		return tJson
	case KClassRef:
		return c.construct(line, fn.Class, args, names)
	case KBuiltin:
		return c.builtin(line, fn, args, exprs, sc)
	case KBeam:
		label := "this beam"
		if fn.Optional {
			c.report(line, "this beam might be nothing here")
		}
		ordered := make([]*Type, len(fn.Params))
		filled := make([]bool, len(fn.Params))
		pos := 0
		for i, a := range args {
			n := ""
			if i < len(names) {
				n = names[i]
			}
			idx := pos
			if n != "" {
				idx = -1
				for j, pn := range fn.PNames {
					if pn == n {
						idx = j
					}
				}
				if idx < 0 {
					c.report(line, "%s has no input called '%s'", label, n)
					continue
				}
			} else {
				pos++
			}
			if idx >= len(fn.Params) {
				c.report(line, "%s takes %d input(s), but got %d", label, len(fn.Params), len(args))
				return resultOf(fn)
			}
			ordered[idx], filled[idx] = a, true
		}
		for j, ok := range filled {
			if !ok {
				name := fmt.Sprintf("input %d", j+1)
				if j < len(fn.PNames) {
					name = "'" + fn.PNames[j] + "'"
				}
				c.report(line, "%s needs a value for %s", label, name)
				continue
			}
			pname := fmt.Sprintf("input %d", j+1)
			if j < len(fn.PNames) {
				pname = "the input '" + fn.PNames[j] + "'"
			}
			c.assignable(line, ordered[j], fn.Params[j], pname)
		}
		return resultOf(fn)
	}
	c.report(line, "%s is not a beam, so it can't be called with ( )", fn.name())
	return tUnknown
}

func resultOf(fn *Type) *Type {
	if fn.Result == nil {
		return tNothing
	}
	return fn.Result
}

func (c *checker) construct(line int, cl *Class, args []*Type, names []string) *Type {
	var binds map[string]*Type
	fieldType := func(i int) *Type { return cl.types[i] }
	if len(cl.TypeParams) > 0 {
		// work out the type names from the values, then check against them
		binds = map[string]*Type{}
		pos := 0
		for i, a := range args {
			idx := pos
			if i < len(names) && names[i] != "" {
				j, ok := cl.index[names[i]]
				if !ok {
					continue
				}
				idx = j
			} else {
				pos++
			}
			if idx < len(cl.types) {
				unify(cl.types[idx], a, binds)
			}
		}
		fieldType = func(i int) *Type { return subst(cl.types[i], binds) }
	}
	given := make([]bool, len(cl.Fields))
	pos := 0
	for i, a := range args {
		idx := pos
		if i < len(names) && names[i] != "" {
			j, ok := cl.index[names[i]]
			if !ok {
				c.report(line, "%s has no field called '%s'%s", cl.Name, names[i], didYouMean(names[i], fieldNames(cl)))
				continue
			}
			idx = j
		} else {
			pos++
		}
		if idx >= len(cl.Fields) {
			c.report(line, "%s only has %d fields, but got more values", cl.Name, len(cl.Fields))
			break
		}
		given[idx] = true
		c.assignable(line, a, fieldType(idx), cl.Name+"."+cl.Fields[idx].Name)
	}
	for i, f := range cl.Fields {
		if !given[i] && f.Default == nil && !cl.types[i].Optional {
			c.report(line, "making %s needs a value for '%s'", aOrAn(cl.Name), f.Name)
		}
	}
	if cl.Choice != nil {
		return &Type{Kind: KChoice, Choice: cl.Choice, Args: argsFrom(cl.TypeParams, binds)}
	}
	return &Type{Kind: KObject, Class: cl, Args: argsFrom(cl.TypeParams, binds)}
}

// ---------- assignability ----------

// assignable reports a problem if a value of type from can't be used where to is needed.
func (c *checker) assignable(line int, from, to *Type, what string) {
	if ok, why := fits(from, to); !ok {
		c.report(line, "%s must be %s, but this is %s%s", what, to.name(), from.name(), why)
	}
}

func fits(from, to *Type) (bool, string) {
	if from == nil || to == nil || to.Kind == KJson || to.Kind == KUnknown || from.Kind == KJson || from.Kind == KUnknown {
		return true, ""
	}
	if from.Kind == KNothing {
		if to.Optional || to.Kind == KNothing {
			return true, ""
		}
		return false, ". Use " + to.name() + "? where nothing is allowed"
	}
	if from.Optional && !to.Optional {
		return false, " (it might be nothing). Check it first with: when ... is nothing { ... }"
	}
	switch to.Kind {
	case KInt:
		return from.Kind == KInt, ""
	case KFloat:
		if from.Kind == KDecimal && !from.Lit {
			return false, ". Convert it with float(...)"
		}
		return from.Kind == KFloat || from.Kind == KInt || from.Kind == KDecimal, ""
	case KDecimal:
		if from.Kind == KFloat {
			return false, ". Convert it with decimal(...)"
		}
		return from.Kind == KDecimal || from.Kind == KInt, ""
	case KList, KMap:
		if from.Kind != to.Kind {
			return false, ""
		}
		if from.Elem == nil || from.Elem.Kind == KUnknown || to.Elem == nil {
			return true, ""
		}
		if sameType(from.Elem, to.Elem) {
			return true, ""
		}
		return false, ""
	case KObject:
		return from.Kind == KObject && from.Class == to.Class && sameArgs(from.Args, to.Args), ""
	case KChoice:
		return from.Kind == KChoice && from.Choice == to.Choice && sameArgs(from.Args, to.Args), ""
	case KParam:
		return from.Kind == KParam && from.Name == to.Name, ""
	case KRule:
		if from.Kind == KRule {
			return from.Rule == to.Rule, ""
		}
		if from.Kind != KObject {
			return false, ""
		}
		for _, r := range from.Class.Follows {
			if r == to.Rule.Name {
				return true, ""
			}
		}
		return false, ". " + from.Class.Name + " doesn't follow " + to.Rule.Name
	case KBeam:
		if from.Kind == KBuiltin {
			return true, ""
		}
		if from.Kind != KBeam || len(from.Params) != len(to.Params) {
			return false, ""
		}
		return true, ""
	case KLibrary:
		return (from.Kind == KLibrary && from.Name == to.Name) || (to.Name == "Request" && from.Kind == KMap), ""
	case KNothing:
		return false, ""
	}
	return from.Kind == to.Kind, ""
}

func sameType(a, b *Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind == KUnknown || b.Kind == KUnknown || a.Kind == KJson || b.Kind == KJson {
		return true
	}
	if a.Kind != b.Kind || a.Optional != b.Optional {
		return false
	}
	switch a.Kind {
	case KList, KMap:
		return sameType(a.Elem, b.Elem)
	case KObject:
		return a.Class.Name == b.Class.Name && sameArgs(a.Args, b.Args)
	case KChoice:
		return a.Choice == b.Choice && sameArgs(a.Args, b.Args)
	case KParam:
		return a.Name == b.Name
	case KRule:
		return a.Rule.Name == b.Rule.Name
	case KLibrary:
		return a.Name == b.Name
	case KBeam:
		if len(a.Params) != len(b.Params) {
			return false
		}
		for i := range a.Params {
			if !sameType(a.Params[i], b.Params[i]) {
				return false
			}
		}
		return sameType(a.Result, b.Result)
	}
	return true
}

// fix records a change that lf fix makes to update older code.
func (c *checker) fix(at int, text string) {
	if c.file != "" { // only the file being fixed, not files it uses
		return
	}
	for _, e := range c.fixes {
		if e.At == at && e.Text == text {
			return
		}
	}
	c.fixes = append(c.fixes, Edit{at, text})
}

// FixEdits lists the changes that bring code written for an older Laser Feet up
// to date (lf fix). It never changes what the code does.
func FixEdits(src string, read func(string) (string, error)) ([]Edit, error) {
	prog, edits, err := ParseForFix(src)
	if err != nil {
		return nil, err
	}
	c := &checker{seen: map[string]bool{}, read: read, modules: map[string]*Type{}, loading: map[string]bool{}}
	c.checkProgram(prog, true)
	return append(edits, c.fixes...), nil
}

// shortText writes a simple expression back as code, for messages (db, app.db).
func shortText(e Expr) string {
	switch x := e.(type) {
	case *Ident:
		return x.Name
	case *Get:
		return shortText(x.Object) + "." + x.Name
	}
	return "db"
}

// aOrAn puts "a" or "an" before a name: a Product, an Item.
func aOrAn(name string) string {
	if name != "" && strings.ContainsRune("AEIOUaeiou", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}

// focus checks a focus: each arm must fit the value, an option's data get their
// types, and every option of a choice needs an arm (or there's an otherwise).
func (c *checker) focus(s *FocusStmt, sc *cscope) {
	subject := c.expr(s.Subject, sc, nil)
	ch := subject.Choice
	if subject.Kind != KChoice {
		ch = nil
	}
	seen := map[string]bool{}
	sawNothing, sawYes, sawNo, badOption := false, false, false, false
	for i, arm := range s.Arms {
		armSc := &cscope{vars: map[string]*cvar{}, parent: sc}
		switch {
		case arm.Otherwise:
			if i != len(s.Arms)-1 {
				c.report(arm.Line, "'otherwise' must be the last arm of a focus, because the arms after it would never run")
			}
			s.Complete = true
		case arm.Option != "":
			if ch == nil {
				if subject.Kind != KUnknown && subject.Kind != KJson {
					c.report(arm.Line, "'%s' is an option of a choice, but this focus is on %s", arm.Option, subject.name())
				}
				break
			}
			o := ch.option(arm.Option)
			if o == nil {
				c.report(arm.Line, "choice %s has no option called '%s'%s", ch.Name, arm.Option, didYouMean(arm.Option, ch.optionNames()))
				for _, b := range arm.Binds {
					armSc.vars[b] = &cvar{t: tUnknown, declared: true}
				}
				badOption = true
				break
			}
			if seen[o.Name] {
				c.report(arm.Line, "%s already has an arm in this focus", o.Name)
			}
			seen[o.Name] = true
			if len(arm.Binds) > 0 && len(arm.Binds) != len(o.Fields) {
				c.report(arm.Line, "%s has %d value%s (%s), so name all of them or none", o.Name, len(o.Fields), plural(len(o.Fields)), strings.Join(fieldNames(o), ", "))
			}
			binds := paramBinds(ch.TypeParams, subject.Args)
			for j, b := range arm.Binds {
				if j < len(o.types) {
					bt := subst(o.types[j], binds)
					armSc.vars[b] = &cvar{t: bt, declared: true}
					c.note(arm.Line, b, bt)
				}
			}
		default:
			vt := c.expr(arm.Value, sc, nonOpt(subject))
			switch v := arm.Value.(type) {
			case *NothingLit:
				sawNothing = true
				if !subject.Optional && subject.Kind != KJson && subject.Kind != KUnknown {
					c.report(arm.Line, "this value can't be nothing, so the nothing arm would never run")
				}
			case *BoolLit:
				if v.Value {
					sawYes = true
				} else {
					sawNo = true
				}
			}
			if _, isNothing := arm.Value.(*NothingLit); !isNothing && !comparable(nonOpt(subject), vt) {
				c.report(arm.Line, "this arm is %s, but the focus is on %s, so it would never match", vt.name(), subject.name())
			}
		}
		c.block(arm.Body, armSc)
		for _, b := range arm.Binds {
			delete(armSc.vars, b) // an option's data belong to its arm only
		}
		c.merge(armSc, sc)
	}
	if s.Complete || badOption {
		return
	}
	nothingDone := !subject.Optional || sawNothing
	switch {
	case ch != nil:
		var missing []string
		for _, o := range ch.Options {
			if !seen[o.Name] {
				missing = append(missing, o.Name)
			}
		}
		if !nothingDone {
			missing = append(missing, "nothing")
		}
		if len(missing) > 0 {
			c.report(s.Line, "this focus has no arm for %s. Add %s, or an otherwise { } arm", joinOr(missing), armHint(missing))
			return
		}
		s.Complete = true
	case subject.Kind == KFlag && sawYes && sawNo && nothingDone:
		s.Complete = true
	case subject.Kind == KJson || subject.Kind == KUnknown:
	default:
		c.report(s.Line, "a focus on %s can't list every possible value, so it needs an otherwise { } arm at the end", subject.name())
	}
}

func armHint(missing []string) string {
	if len(missing) == 1 {
		return missing[0] + " { }"
	}
	return "arms for them"
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// typeArgs reads the types given to a generic object or choice: Page[Product].
func (c *checker) typeArgs(te *TypeExpr, params []string) []*Type {
	if len(params) == 0 {
		if len(te.Args) > 0 {
			c.report(te.Line, "%s doesn't take types in [ ]", te.Name)
		}
		return nil
	}
	if len(te.Args) != len(params) {
		c.report(te.Line, "say what %s holds, like %s[%s]", te.Name, te.Name, strings.Repeat("Text, ", len(params)-1)+"Text")
		return argsFrom(params, nil)
	}
	out := make([]*Type, len(te.Args))
	for i, a := range te.Args {
		out[i] = c.resolve(a)
	}
	return out
}

// sameArgs compares the types of two generic values; one not yet known matches anything.
func sameArgs(a, b []*Type) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for i := range a {
		if i < len(b) && !sameType(a[i], b[i]) {
			return false
		}
	}
	return true
}
