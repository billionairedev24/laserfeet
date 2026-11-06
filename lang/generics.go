package lang

import "strings"

// Generics: beam first[T](items: List[T]) gives T?, object Page[T] { ... } and
// choice Result[T] { ... }. lf check works out what each T is from the values
// given; the engine itself doesn't need to know.

// subst replaces each type name (T) with what it stands for. Names not worked
// out yet become "a value", which fits anything.
func subst(t *Type, binds map[string]*Type) *Type {
	if t == nil || len(binds) == 0 && !hasParams(t) {
		return t
	}
	switch t.Kind {
	case KParam:
		b, ok := binds[t.Name]
		if !ok || b == nil {
			b = tUnknown
		}
		if t.Optional && !b.Optional {
			cp := *b
			cp.Optional = true
			return &cp
		}
		return b
	case KList, KMap:
		cp := *t
		cp.Elem = subst(t.Elem, binds)
		return &cp
	case KBeam:
		cp := *t
		cp.Params = make([]*Type, len(t.Params))
		for i, p := range t.Params {
			cp.Params[i] = subst(p, binds)
		}
		cp.Result = subst(t.Result, binds)
		cp.TParams = nil
		return &cp
	case KObject, KChoice:
		if len(t.Args) == 0 {
			return t
		}
		cp := *t
		cp.Args = make([]*Type, len(t.Args))
		for i, a := range t.Args {
			cp.Args[i] = subst(a, binds)
		}
		return &cp
	}
	return t
}

func hasParams(t *Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case KParam:
		return true
	case KList, KMap:
		return hasParams(t.Elem)
	case KBeam:
		for _, p := range t.Params {
			if hasParams(p) {
				return true
			}
		}
		return hasParams(t.Result)
	case KObject, KChoice:
		for _, a := range t.Args {
			if hasParams(a) {
				return true
			}
		}
	}
	return false
}

// unify works out type names by matching a declared type (List[T]) against the
// type of a value given for it (List[Text]), recording T = Text.
func unify(want, got *Type, binds map[string]*Type) {
	if want == nil || got == nil || got.Kind == KUnknown {
		return
	}
	switch want.Kind {
	case KParam:
		if got.Kind == KNothing {
			return // nothing says nothing about T
		}
		if _, done := binds[want.Name]; done {
			return
		}
		g := settle(got)
		if want.Optional && g.Optional {
			g = nonOpt(g)
		}
		binds[want.Name] = g
	case KList, KMap:
		if got.Kind == want.Kind {
			unify(want.Elem, got.Elem, binds)
		}
	case KBeam:
		if got.Kind == KBeam {
			for i := range want.Params {
				if i < len(got.Params) {
					unify(want.Params[i], got.Params[i], binds)
				}
			}
			unify(want.Result, got.Result, binds)
		}
	case KObject, KChoice:
		if got.Kind == want.Kind {
			for i := range want.Args {
				if i < len(got.Args) {
					unify(want.Args[i], got.Args[i], binds)
				}
			}
		}
	}
}

// settle turns a decimal written in the code into a plain Decimal, so T = Decimal.
func settle(t *Type) *Type {
	if t.Lit {
		cp := *t
		cp.Lit = false
		return &cp
	}
	return t
}

// paramBinds pairs a generic's type names with the types given for them.
func paramBinds(names []string, args []*Type) map[string]*Type {
	if len(names) == 0 {
		return nil
	}
	b := map[string]*Type{}
	for i, n := range names {
		if i < len(args) {
			b[n] = args[i]
		}
	}
	return b
}

// argsFrom lists the types worked out for a generic's type names, in order.
func argsFrom(names []string, binds map[string]*Type) []*Type {
	if len(names) == 0 {
		return nil
	}
	out := make([]*Type, len(names))
	for i, n := range names {
		if b, ok := binds[n]; ok && b != nil {
			out[i] = b
		} else {
			out[i] = tUnknown
		}
	}
	return out
}

func argNames(args []*Type) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = a.name()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// genericCall checks a call to a generic beam: it works out the type names from
// the values given (values first, then beams, so x => x * 2 knows what x is),
// then checks the call as if the beam had been written with those types.
func (c *checker) genericCall(line int, fn *Type, first *Type, hasFirst bool, xargs []Arg, sc *cscope) *Type {
	binds := map[string]*Type{}
	off := 0
	if hasFirst {
		off = 1
	}
	n := len(xargs) + off
	types, names, exprs := make([]*Type, n), make([]string, n), make([]Expr, n)
	index := make([]int, n) // which input each value is for
	pos := 0
	if hasFirst {
		types[0], index[0] = first, 0
		pos = 1
	}
	for i, a := range xargs {
		k := i + off
		names[k], exprs[k] = a.Name, a.Value
		index[k] = -1
		if a.Name == "" {
			index[k] = pos
			pos++
		} else {
			for j, pn := range fn.PNames {
				if pn == a.Name {
					index[k] = j
				}
			}
		}
	}
	param := func(k int) *Type {
		if index[k] >= 0 && index[k] < len(fn.Params) {
			return fn.Params[index[k]]
		}
		return nil
	}
	if hasFirst {
		unify(param(0), first, binds)
	}
	for i, a := range xargs {
		k := i + off
		if _, isBeam := a.Value.(*Lambda); isBeam {
			continue
		}
		var want *Type
		if p := param(k); p != nil && !hasParams(p) {
			want = p
		}
		types[k] = c.expr(a.Value, sc, want)
		unify(param(k), types[k], binds)
	}
	for i, a := range xargs {
		k := i + off
		if _, isBeam := a.Value.(*Lambda); !isBeam {
			continue
		}
		types[k] = c.expr(a.Value, sc, subst(param(k), binds))
		unify(param(k), types[k], binds)
	}
	return c.apply(line, subst(fn, binds), types, names, exprs, sc)
}

// withTypeParams makes a generic's type names known while its types are read.
func (c *checker) withTypeParams(names []string, f func()) {
	if len(names) == 0 {
		f()
		return
	}
	saved := c.tparams
	c.tparams = map[string]bool{}
	for k := range saved {
		c.tparams[k] = true
	}
	for _, n := range names {
		c.tparams[n] = true
	}
	f()
	c.tparams = saved
}
