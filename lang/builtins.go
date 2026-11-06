package lang

import (
	"math"
	"strconv"
	"strings"
	"time"
)

func builtin(name string, fn func(in *Interp, line int, args []Value) Value) *Builtin {
	return &Builtin{Name: name, Fn: fn}
}

func wantArgs(in *Interp, line int, name string, args []Value, n int) {
	if len(args) != n {
		in.fail(line, "%s takes %d input(s), but got %d", name, n, len(args))
	}
}

func wantList(in *Interp, line int, name string, v Value) *List {
	l, ok := v.(*List)
	if !ok {
		in.fail(line, "%s needs a list, but got %s", name, typeName(v))
	}
	return l
}

// floatOf turns any number into a Float, for things like seconds to wait.
func floatOf(in *Interp, line int, name string, v Value) float64 {
	switch n := v.(type) {
	case int64:
		return float64(n)
	case float64:
		return n
	case *Decimal:
		return n.Float64()
	}
	in.fail(line, "%s needs a number, but got %s", name, typeName(v))
	return 0
}

func installBuiltins(in *Interp, g *Env) {
	def := func(b *Builtin) { g.define(b.Name, b, true) }
	in.clockBuiltins(def)

	def(builtin("text", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "text", a, 1)
		return Text(a[0])
	}))
	toDecimal := func(in *Interp, line int, name string, v Value) *Decimal {
		switch x := v.(type) {
		case int64:
			return decimalFromInt(x)
		case *Decimal:
			return x
		case float64:
			d, err := decimalFromFloat(x)
			if err != nil {
				in.fail(line, "%s: %v", name, err)
			}
			return d
		case string:
			d, err := ParseDecimal(x)
			if err != nil {
				in.fail(line, "%q is not a number", x)
			}
			return d
		}
		in.fail(line, "I can't turn %s into a number", typeName(v))
		return nil
	}
	def(builtin("decimal", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "decimal", a, 1)
		return toDecimal(in, line, "decimal", a[0])
	}))
	// number is the friendly name for reading a number from text: it gives a Decimal
	def(builtin("number", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "number", a, 1)
		return toDecimal(in, line, "number", a[0])
	}))
	def(builtin("float", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "float", a, 1)
		if t, ok := a[0].(string); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
			if err != nil {
				in.fail(line, "%q is not a number", t)
			}
			return f
		}
		return floatOf(in, line, "float", a[0])
	}))
	def(builtin("int", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "int", a, 1)
		switch x := a[0].(type) {
		case int64:
			return x
		case float64:
			if math.IsNaN(x) || x >= 9.2e18 || x <= -9.2e18 {
				in.fail(line, "this Float doesn't fit in an Int")
			}
			return box(int64(x))
		case *Decimal:
			i, err := x.Int64()
			if err != nil {
				in.fail(line, "this number doesn't fit in an Int")
			}
			return box(i)
		case string:
			i, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
			if err != nil {
				in.fail(line, "%q is not a whole number", x)
			}
			return box(i)
		}
		in.fail(line, "I can't turn %s into an Int", typeName(a[0]))
		return nil
	}))
	def(builtin("count", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "count", a, 1)
		switch x := a[0].(type) {
		case *List:
			return box(int64(len(x.Items)))
		case string:
			return box(int64(len([]rune(x))))
		case *Map:
			return box(int64(len(x.Keys)))
		}
		in.fail(line, "count needs a list or text, but got %s", typeName(a[0]))
		return nil
	}))
	def(builtin("sum", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "sum", a, 1)
		var total Value = box(0)
		for _, v := range wantList(in, line, "sum", a[0]).Items {
			total = in.arith(line, "+", total, v)
		}
		return total
	}))
	pick := func(name string, want int) *Builtin {
		return builtin(name, func(in *Interp, line int, a []Value) Value {
			items := a
			if len(a) == 1 {
				if l, ok := a[0].(*List); ok {
					items = l.Items
				}
			}
			if len(items) == 0 {
				in.fail(line, "%s needs at least one number", name)
			}
			best := items[0]
			for _, v := range items {
				c, ok := numberCmp(v, best)
				if !ok {
					in.fail(line, "%s needs numbers, but got %s", name, typeName(v))
				}
				if c == want {
					best = v
				}
			}
			return best
		})
	}
	def(pick("biggest", 1))
	def(pick("smallest", -1))
	def(builtin("round", func(in *Interp, line int, a []Value) Value {
		if len(a) < 1 || len(a) > 2 {
			in.fail(line, "round takes a number and an optional number of places, like round(price, 2)")
		}
		places := int64(0)
		if len(a) == 2 {
			places = in.wantInt(line, "the number of places", a[1])
		}
		switch x := a[0].(type) {
		case int64:
			return x
		case *Decimal:
			r := x.Round(int32(places))
			if len(a) == 1 {
				i, err := r.Int64()
				if err != nil {
					in.fail(line, "this number doesn't fit in an Int")
				}
				return box(i)
			}
			return r
		case float64:
			if len(a) == 1 {
				return box(int64(math.Round(x)))
			}
			p := math.Pow(10, float64(places))
			return math.Round(x*p) / p
		}
		in.fail(line, "round needs a number, but got %s", typeName(a[0]))
		return nil
	}))
	// fixed shows a number with exactly the given places, like money: fixed(12.5, 2) is "12.50"
	def(builtin("fixed", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "fixed", a, 2)
		places := in.wantInt(line, "the number of places", a[1])
		return toDecimal(in, line, "fixed", a[0]).Round(int32(places)).String()
	}))
	def(builtin("first", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "first", a, 1)
		l := wantList(in, line, "first", a[0])
		if len(l.Items) == 0 {
			return nil
		}
		return l.Items[0]
	}))
	def(builtin("last", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "last", a, 1)
		l := wantList(in, line, "last", a[0])
		if len(l.Items) == 0 {
			return nil
		}
		return l.Items[len(l.Items)-1]
	}))
	def(builtin("reverse", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "reverse", a, 1)
		l := wantList(in, line, "reverse", a[0])
		out := newList(make([]Value, len(l.Items)))
		for i, v := range l.Items {
			out.Items[len(l.Items)-1-i] = v
		}
		return out
	}))
	def(builtin("join", func(in *Interp, line int, a []Value) Value {
		if len(a) < 1 || len(a) > 2 {
			in.fail(line, "join takes a list and an optional separator")
		}
		sep := ", "
		if len(a) == 2 {
			sep = Text(a[1])
		}
		parts := []string{}
		for _, v := range wantList(in, line, "join", a[0]).Items {
			parts = append(parts, Text(v))
		}
		return strings.Join(parts, sep)
	}))
	def(builtin("pause", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "pause", a, 1)
		secs := floatOf(in, line, "pause", a[0])
		in.outside(func() {
			time.Sleep(time.Duration(secs * float64(time.Second)))
		})
		return nil
	}))
	def(envBuiltin())
	def(builtin("readFile", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "readFile", a, 1)
		data, err := readFile(Text(a[0]))
		if err != nil {
			in.fail(line, "could not read %s (%s)", Text(a[0]), err.Error())
		}
		return data
	}))

	// Chain helpers: the condition is worked out once for each item.
	// Inside it you can use the item's fields directly, or 'it' for the item itself.
	def(&Builtin{Name: "keepIf", Lazy: func(in *Interp, env *Env, line int, first Value, rest []Expr) Value {
		l, cond := lazyArgs(in, line, "keepIf", first, rest)
		out := newList(nil)
		for _, it := range l.Items {
			if in.perItemTruth(env, cond, it, "keepIf") {
				out.Items = append(out.Items, it)
			}
		}
		return out
	}})
	def(&Builtin{Name: "dropIf", Lazy: func(in *Interp, env *Env, line int, first Value, rest []Expr) Value {
		l, cond := lazyArgs(in, line, "dropIf", first, rest)
		out := newList(nil)
		for _, it := range l.Items {
			if !in.perItemTruth(env, cond, it, "dropIf") {
				out.Items = append(out.Items, it)
			}
		}
		return out
	}})
	def(&Builtin{Name: "turn", Lazy: func(in *Interp, env *Env, line int, first Value, rest []Expr) Value {
		l, x := lazyArgs(in, line, "turn", first, rest)
		out := newList(nil)
		for _, it := range l.Items {
			out.Items = append(out.Items, in.perItem(env, x, it))
		}
		return out
	}})
	def(&Builtin{Name: "sortBy", Lazy: func(in *Interp, env *Env, line int, first Value, rest []Expr) Value {
		l, x := lazyArgs(in, line, "sortBy", first, rest)
		items := append([]Value{}, l.Items...)
		keys := make([]Value, len(items))
		for i, it := range items {
			keys[i] = in.perItem(env, x, it)
		}
		sortValues(in, line, items, keys)
		return newList(items)
	}})
	def(&Builtin{Name: "findFirst", Lazy: func(in *Interp, env *Env, line int, first Value, rest []Expr) Value {
		l, cond := lazyArgs(in, line, "findFirst", first, rest)
		for _, it := range l.Items {
			if in.perItemTruth(env, cond, it, "findFirst") {
				return it
			}
		}
		return nil
	}})
	def(builtin("sort", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "sort", a, 1)
		items := append([]Value{}, wantList(in, line, "sort", a[0]).Items...)
		keys := append([]Value{}, items...)
		sortValues(in, line, items, keys)
		return newList(items)
	}))
}

func lazyArgs(in *Interp, line int, name string, first Value, rest []Expr) (*List, Expr) {
	if len(rest) != 1 {
		in.fail(line, "%s takes one rule, like %s(age >= 18)", name, name)
	}
	return wantList(in, line, name, first), rest[0]
}

func (in *Interp) perItem(env *Env, x Expr, item Value) Value {
	if lam, ok := x.(*Lambda); ok {
		return in.call(in.eval(lam, env), []Value{item}, nil, x.Ln(), nil)
	}
	// one allocation per item: the item's scope with "it" in it
	fr := new(frame1)
	e := &fr.Env
	e.parent, e.names, e.binds, e.born = env, itName, fr.slots[:1], genNow.Load()
	fr.slots[0].v = item
	switch o := item.(type) {
	case *Instance:
		e.obj = o
	case *Map:
		for _, k := range o.Keys {
			e.define(k, o.M[k], false)
		}
	}
	if in.NoCompile {
		return in.eval(x, e)
	}
	return in.exprCode(x)(e)
}

func (in *Interp) perItemTruth(env *Env, x Expr, item Value, name string) bool {
	v := in.perItem(env, x, item)
	b, ok := v.(bool)
	if !ok {
		in.fail(x.Ln(), "%s needs a yes/no rule, but this gave %s", name, typeName(v))
	}
	return b
}

func listMethod(l *List, name string) *Builtin {
	switch name {
	case "add":
		return builtin("add", func(in *Interp, line int, a []Value) Value {
			in.mayChange(l.born, line, "a list")
			l.Items = append(l.Items, a...)
			return l
		})
	case "remove":
		return builtin("remove", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "remove", a, 1)
			in.mayChange(l.born, line, "a list")
			i := in.listIndex(line, l, a[0])
			v := l.Items[i]
			l.Items = append(l.Items[:i], l.Items[i+1:]...)
			return v
		})
	case "size":
		return builtin("size", func(in *Interp, line int, a []Value) Value { return box(int64(len(l.Items))) })
	case "has":
		return builtin("has", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "has", a, 1)
			for _, v := range l.Items {
				if equal(v, a[0]) {
					return true
				}
			}
			return false
		})
	}
	return nil
}

func textMethod(s string, name string) *Builtin {
	simple := func(f func(string) Value) *Builtin {
		return builtin(name, func(in *Interp, line int, a []Value) Value { return f(s) })
	}
	switch name {
	case "upper":
		return simple(func(s string) Value { return strings.ToUpper(s) })
	case "lower":
		return simple(func(s string) Value { return strings.ToLower(s) })
	case "trim":
		return simple(func(s string) Value { return strings.TrimSpace(s) })
	case "size":
		return simple(func(s string) Value { return box(int64(len([]rune(s)))) })
	case "has":
		return builtin("has", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "has", a, 1)
			return strings.Contains(s, Text(a[0]))
		})
	case "startsWith":
		return builtin("startsWith", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "startsWith", a, 1)
			return strings.HasPrefix(s, Text(a[0]))
		})
	case "split":
		return builtin("split", func(in *Interp, line int, a []Value) Value {
			sep := " "
			if len(a) == 1 {
				sep = Text(a[0])
			}
			out := newList(nil)
			for _, p := range strings.Split(s, sep) {
				out.Items = append(out.Items, p)
			}
			return out
		})
	case "replace":
		return builtin("replace", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "replace", a, 2)
			return strings.ReplaceAll(s, Text(a[0]), Text(a[1]))
		})
	}
	return nil
}

func mapMethod(m *Map, name string) *Builtin {
	switch name {
	case "keys":
		return builtin("keys", func(in *Interp, line int, a []Value) Value {
			out := newList(nil)
			for _, k := range m.Keys {
				out.Items = append(out.Items, k)
			}
			return out
		})
	case "has":
		return builtin("has", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "has", a, 1)
			_, ok := m.M[Text(a[0])]
			return ok
		})
	}
	return nil
}

var itName = []string{"it"}[:1:1] // shared; a scope that adds names copies it first

// exprCode is the compiled form of an expression used for every item of a list
// (keepIf(age > 18)), made once.
func (in *Interp) exprCode(x Expr) cexpr {
	if c, ok := in.exprCodes.Load(x); ok {
		return c.(cexpr)
	}
	c, _ := in.exprCodes.LoadOrStore(x, in.compileExpr(x))
	return c.(cexpr)
}
