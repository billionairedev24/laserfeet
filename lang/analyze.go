package lang

import (
	"sort"
	"strings"
)

// Analysis is what editors need about a program: its problems, and the type of
// each name on each line (for hover and autocomplete).
type Analysis struct {
	Problems []Problem
	Facts    map[int]map[string]*Type // line -> name -> type
	Starts   []int                    // lines where top-level statements start
	Globals  map[string]*Type         // names usable everywhere in the file
	Refs     []Ref                    // each use of a name, and where it was made
}

// Place is where something was made: a line of this file (File "") or of a
// file it uses (File is its path, as written for use).
type Place struct {
	File string
	Line int
}

// Ref is a name on a line, and the place it means.
type Ref struct {
	Line   int
	Name   string
	Def    Place
	Member bool // written after a dot, like the x of p.x
}

// Analyze checks a program and records what editors need. It never runs the program.
func Analyze(src string, read func(string) (string, error)) *Analysis {
	a := &Analysis{Facts: map[int]map[string]*Type{}, Globals: map[string]*Type{}}
	prog, err := Parse(src)
	if err != nil {
		if se, ok := err.(*SyntaxError); ok {
			a.Problems = []Problem{{Line: se.Line, Msg: se.Msg}}
		}
		return a
	}
	c := &checker{seen: map[string]bool{}, read: read, modules: map[string]*Type{}, loading: map[string]bool{}, info: a, declAt: map[any]Place{}}
	top := c.checkProgram(prog, true)
	for k, v := range top.vars {
		a.Globals[k] = v.t
	}
	for _, s := range prog.Stmts {
		a.Starts = append(a.Starts, s.Ln())
	}
	sort.SliceStable(c.problems, func(i, j int) bool { return c.problems[i].Line < c.problems[j].Line })
	a.Problems = c.problems
	return a
}

// TypeOf finds the type of a name as seen on a line, or on the nearest line above it
// within the same top-level block.
func (a *Analysis) TypeOf(name string, line int) *Type {
	if a == nil {
		return nil
	}
	start := a.blockStart(line)
	for l := line; l >= start && l > 0; l-- {
		if t, ok := a.Facts[l][name]; ok {
			return t
		}
	}
	if t, ok := a.Globals[name]; ok {
		return t
	}
	// a name used further down the same block (while the user is still typing)
	for l, m := range a.Facts {
		if l >= start {
			if t, ok := m[name]; ok {
				return t
			}
		}
	}
	return nil
}

func (a *Analysis) blockStart(line int) int {
	start := 1
	for _, s := range a.Starts {
		if s <= line {
			start = s
		}
	}
	return start
}

// NamesAt lists the names visible on a line, with their types.
func (a *Analysis) NamesAt(line int) map[string]*Type {
	out := map[string]*Type{}
	if a == nil {
		return out
	}
	for k, v := range a.Globals {
		out[k] = v
	}
	start := a.blockStart(line)
	for l, m := range a.Facts {
		if l >= start && l <= line {
			for k, v := range m {
				if _, ok := out[k]; !ok && v.Kind != KModule {
					out[k] = v
				}
			}
		}
	}
	return out
}

// Member describes one thing that can follow a dot.
type Member struct {
	Name   string
	Detail string
	Beam   bool
}

// MembersOf lists what can follow "x." for a value of type t.
func MembersOf(t *Type) []Member {
	if t == nil {
		return nil
	}
	var out []Member
	add := func(name, detail string, beam bool) { out = append(out, Member{name, detail, beam}) }
	switch t.Kind {
	case KObject:
		for i, f := range t.Class.Fields {
			d := ""
			if t.Class.types != nil && t.Class.types[i] != nil {
				d = t.Class.types[i].name()
			}
			add(f.Name, d, false)
		}
		for name, m := range t.Class.Methods {
			add(name, describeSig(m.Params, m.Sig), true)
		}
	case KRule:
		for _, s := range t.Rule.Sigs {
			add(s.Name, describeSig(s.Params, s.Sig), true)
		}
	case KClassRef:
		add("from", "build a "+t.Class.Name+" from JSON", true)
	case KChoiceRef:
		for _, o := range t.Choice.Options {
			if o.single != nil {
				add(o.Name, t.Choice.Name, false)
			} else {
				add(o.Name, "("+strings.Join(fieldNames(o), ", ")+") makes a "+t.Choice.Name, true)
			}
		}
		add("from", "read a "+t.Choice.Name+" from JSON", true)
	case KModule:
		for k, v := range t.Members {
			add(k, v.name(), v.Kind == KBuiltin || v.Kind == KBeam)
		}
	case KDate:
		for _, n := range []string{"year", "month", "day"} {
			add(n, "Int", false)
		}
		add("weekday", "Text, like Friday", false)
		add("addDays", "(n: Int) gives Date", true)
		add("addMonths", "(n: Int) gives Date", true)
	case KTime:
		add("date", "Date", false)
		for _, n := range []string{"hour", "minute", "second"} {
			add(n, "Int", false)
		}
	case KDuration:
		add("inSeconds", "Int", false)
		for _, n := range []string{"inMinutes", "inHours", "inDays"} {
			add(n, "Decimal", false)
		}
	case KList:
		add("add", "add items", true)
		add("remove", "remove the item at a position", true)
		add("size", "Int", true)
		add("has", "Flag", true)
	case KText:
		for _, n := range []string{"upper", "lower", "trim", "size", "has", "startsWith", "split", "replace"} {
			add(n, "", true)
		}
	case KMap:
		add("keys", "List[Text]", true)
		add("has", "Flag", true)
	case KLibrary:
		switch t.Name {
		case "Server":
			for _, n := range []string{"get", "post", "on", "start", "visit", "routes"} {
				add(n, "", true)
			}
		case "Store":
			for _, n := range []string{"add", "all", "find", "update", "remove", "count"} {
				add(n, "", true)
			}
		case "Database":
			add("query", "(sql: Text, values: List) gives List[Json]", true)
			add("first", "(sql: Text, values: List) gives Json?", true)
			add("run", "(sql: Text, values: List) gives Int", true)
			add("transaction", "() as tx { ... } (or: transaction with db as tx { ... })", true)
		case "Response":
			add("status", "Int", false)
			add("body", "Text", false)
			add("json", "Json", false)
		case "Request":
			for _, n := range []string{"method", "path", "body"} {
				add(n, "Text", false)
			}
			add("params", "the route's :values", false)
			add("query", "Map[Text]", false)
			add("json", "Json", false)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func describeSig(params []string, sig *Sig) string {
	parts := make([]string, len(params))
	for i, p := range params {
		parts[i] = p
		if sig != nil && i < len(sig.Params) && sig.Params[i] != nil {
			parts[i] += ": " + sig.Params[i].String()
		}
	}
	s := "(" + strings.Join(parts, ", ") + ")"
	if sig != nil && sig.Result != nil {
		s += " gives " + sig.Result.String()
	}
	return s
}

// Label is how a type reads in hover text.
func (t *Type) Label() string {
	if t == nil {
		return ""
	}
	return t.name()
}

// Keywords are offered by autocomplete.
func Keywords() []string {
	var out []string
	for k := range keywords {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BuiltinNames are the built-in beams, offered by autocomplete.
func BuiltinNames() []string {
	var out []string
	for k := range builtinNames {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
