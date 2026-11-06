package lang

import (
	"fmt"
	"path"
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
	file     string
	problems []Problem
	seen     map[string]bool
	classes  map[string]*Class
	rules    map[string]*RuleDef
	beam     *beamCtx
	read     func(string) (string, error)
	modules  map[string]*Type
	loading  map[string]bool
	inRoute  bool
	route    string   // the path of the route being checked, like /todos/:id
	params   []string // its :names, which are always there when the route runs
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
	}{c.classes, c.rules}
	c.classes, c.rules = map[string]*Class{}, map[string]*RuleDef{}
	defer func() {
		if !main {
			c.classes, c.rules = saved.classes, saved.rules
		}
	}()
	top := &cscope{vars: map[string]*cvar{}, scope: true}

	// declarations first, so they can be used before they appear
	for _, s := range prog.Stmts {
		switch d := s.(type) {
		case *RuleDecl:
			c.rules[d.Name] = &RuleDef{Name: d.Name, Methods: d.Methods, Sigs: d.Sigs}
		case *ObjectDecl:
			cl := &Class{Name: d.Name, Fields: d.Fields, Methods: map[string]*BeamDecl{}, Follows: d.Follows, index: map[string]int{}}
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
		}
	}
	for _, s := range prog.Stmts {
		if u, ok := s.(*UseStmt); ok {
			c.use(u, top)
		}
	}
	for name, cl := range c.classes {
		cl.types = make([]*Type, len(cl.Fields))
		for i, f := range cl.Fields {
			cl.types[i] = c.resolve(f.Type)
		}
		top.vars[name] = &cvar{t: &Type{Kind: KClassRef, Class: cl}, locked: true}
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
			inner := &cscope{vars: map[string]*cvar{"me": {t: &Type{Kind: KObject, Class: cl}, locked: true}}, parent: top, obj: cl}
			c.beamBody(m.Name, m.Params, m.Sig, m.Body, inner, m.Line)
		}
	}
	for _, s := range prog.Stmts {
		switch d := s.(type) {
		case *BeamDecl:
			c.beamBody(d.Name, d.Params, d.Sig, d.Body, top, d.Line)
		case *RuleDecl, *ObjectDecl, *UseStmt:
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
		t = &Type{Kind: KObject, Class: c.classes[te.Name]}
	case c.rules[te.Name] != nil:
		t = &Type{Kind: KRule, Rule: c.rules[te.Name]}
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
			c.report(u.Line, "there is no library called '%s' (available: web, json, files, time, store)", u.Name)
			return
		}
		top.vars[u.Name] = &cvar{t: m, locked: true}
		return
	}
	clean := path.Clean(strings.ReplaceAll(u.Path, "\\", "/"))
	name := strings.TrimSuffix(path.Base(clean), ".lf")
	if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") {
		c.report(u.Line, "use \"%s\": files must be inside your program's folder", u.Path)
		return
	}
	if !strings.HasSuffix(clean, ".lf") {
		c.report(u.Line, "use \"%s\": only .lf files can be used", u.Path)
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
		c.report(u.Line, "use \"%s\": I couldn't read it (%v)", u.Path, err)
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
	scope := c.checkProgram(prog, false)
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
	bt := c.sigType(params, sig)
	sc := &cscope{vars: map[string]*cvar{}, parent: parent, scope: true}
	for i, p := range params {
		sc.vars[p] = &cvar{t: bt.Params[i], declared: true}
	}
	saved := c.beam
	c.beam = &beamCtx{name: "beam " + name, result: bt.Result}
	c.block(body, sc)
	if bt.Result != nil && !bt.Result.Optional && bt.Result.Kind != KNothing && !alwaysExits(body) {
		c.report(line, "beam %s gives %s, but it can finish without sending one. Make sure every path ends with send", name, bt.Result.name())
	}
	c.beam = saved
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
	case *SendStmt:
		c.send(s, sc)
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
		inner := &cscope{vars: map[string]*cvar{}, parent: sc}
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
	case *ObjectDecl, *RuleDecl:
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
		c.assignable(s.Line, vt, existing.t, "'"+t.Name+"'")
	case *Get:
		obj := c.expr(t.Object, sc, nil)
		ft := c.member(t.Line, obj, t.Name, true)
		vt := c.expr(s.Value, sc, ft)
		c.assignable(s.Line, vt, ft, t.Name)
	case *Index:
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
		return c.ident(x, sc)
	case *ListLit:
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
		return c.member(x.Line, obj, x.Name, false)
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
	if c.inRoute && x.Name == "request" {
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
	return a.Kind == b.Kind && (a.Kind != KObject || a.Class == b.Class)
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
		if i, ok := obj.Class.index[name]; ok {
			return obj.Class.types[i]
		}
		if m, ok := obj.Class.Methods[name]; ok && !forSet {
			return c.sigType(m.Params, m.Sig)
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
	if hasFirst {
		return c.callWith(x, sc, first)
	}
	fn := c.expr(x.Callee, sc, nil)
	var args []*Type
	var names []string
	var exprs []Expr
	for i, a := range x.Args {
		var want *Type
		if fn.Kind == KBeam && i < len(fn.Params) {
			want = fn.Params[i]
		}
		if lam, ok := a.Value.(*Lambda); ok && c.isRouteCall(x) {
			saved, savedRoute, savedParams := c.inRoute, c.route, c.params
			c.inRoute, c.route, c.params = true, "", nil
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
			var implicit map[string]*Type
			if len(lam.Params) == 0 {
				implicit = map[string]*Type{"request": lib("Request")}
			}
			args = append(args, c.lambda(lam, sc, nil, implicit))
			c.beam, c.inRoute, c.route, c.params = savedBeam, saved, savedRoute, savedParams
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
		c.assignable(line, a, cl.types[idx], cl.Name+"."+cl.Fields[idx].Name)
	}
	for i, f := range cl.Fields {
		if !given[i] && f.Default == nil && !cl.types[i].Optional {
			c.report(line, "making a %s needs a value for '%s'", cl.Name, f.Name)
		}
	}
	return &Type{Kind: KObject, Class: cl}
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
		return from.Kind == KObject && from.Class == to.Class, ""
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
		return a.Class.Name == b.Class.Name
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
