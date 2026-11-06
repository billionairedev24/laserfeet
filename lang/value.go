package lang

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Value is any Laser Feet value: float64, string, bool, nil (nothing),
// *List, *Map, *Beam, *Builtin, *Class, *RuleDef, *Instance, *Module, *Response.
type Value any

type List struct{ Items []Value }

type Map struct {
	Keys []string
	M    map[string]Value
}

func NewMap() *Map { return &Map{M: map[string]Value{}} }
func (m *Map) Set(k string, v Value) {
	if _, ok := m.M[k]; !ok {
		m.Keys = append(m.Keys, k)
	}
	m.M[k] = v
}

type Beam struct {
	Name    string
	Params  []string
	Body    []Stmt
	Expr    Expr
	Closure *Env
	Self    *Instance
}

type Builtin struct {
	Name string
	Fn   func(in *Interp, line int, args []Value) Value
	// Lazy builtins (keepIf, turn, sortBy, findFirst) receive their extra
	// arguments unevaluated so they can be worked out once per item.
	Lazy func(in *Interp, env *Env, line int, first Value, rest []Expr) Value
}

type Class struct {
	Name    string
	Fields  []FieldDecl
	Methods map[string]*BeamDecl
	Follows []string
	Env     *Env
}

type RuleDef struct {
	Name    string
	Methods []string
}

type Instance struct {
	Class  *Class
	Fields map[string]Value
}

type Module struct {
	Name    string
	Members map[string]Value
}

func typeName(v Value) string {
	switch x := v.(type) {
	case nil:
		return "nothing"
	case float64:
		return "a number"
	case string:
		return "text"
	case bool:
		return "yes/no"
	case *List:
		return "a list"
	case *Map:
		return "a map"
	case *Beam, *Builtin:
		return "a beam"
	case *Class:
		return "the object " + x.Name
	case *RuleDef:
		return "the rule " + x.Name
	case *Instance:
		return "a " + x.Class.Name
	case *Module:
		return "the " + x.Name + " library"
	case *Response:
		return "a web response"
	case *Server:
		return "a web server"
	}
	return "a value"
}

func formatNumber(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// Text is how a value looks when shown.
func Text(v Value) string { return toText(v, false) }

func toText(v Value, quote bool) string {
	switch x := v.(type) {
	case nil:
		return "nothing"
	case float64:
		return formatNumber(x)
	case string:
		if quote {
			return strconv.Quote(x)
		}
		return x
	case bool:
		if x {
			return "yes"
		}
		return "no"
	case *List:
		parts := make([]string, len(x.Items))
		for i, it := range x.Items {
			parts[i] = toText(it, true)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Map:
		parts := make([]string, len(x.Keys))
		for i, k := range x.Keys {
			parts[i] = k + ": " + toText(x.M[k], true)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case *Instance:
		parts := []string{}
		for _, f := range x.Class.Fields {
			parts = append(parts, f.Name+": "+toText(x.Fields[f.Name], true))
		}
		return x.Class.Name + "(" + strings.Join(parts, ", ") + ")"
	case *Beam:
		if x.Name == "" {
			return "<beam>"
		}
		return "<beam " + x.Name + ">"
	case *Builtin:
		return "<beam " + x.Name + ">"
	case *Class:
		return "<object " + x.Name + ">"
	case *RuleDef:
		return "<rule " + x.Name + ">"
	case *Module:
		return "<library " + x.Name + ">"
	case *Response:
		return fmt.Sprintf("<response %d>", x.Status)
	case *Server:
		return fmt.Sprintf("<web server on port %d>", x.Port)
	}
	return fmt.Sprint(v)
}

func equal(a, b Value) bool {
	switch x := a.(type) {
	case *List:
		y, ok := b.(*List)
		if !ok || len(x.Items) != len(y.Items) {
			return false
		}
		for i := range x.Items {
			if !equal(x.Items[i], y.Items[i]) {
				return false
			}
		}
		return true
	case *Map:
		y, ok := b.(*Map)
		if !ok || len(x.Keys) != len(y.Keys) {
			return false
		}
		for k, v := range x.M {
			if !equal(v, y.M[k]) {
				return false
			}
		}
		return true
	case *Instance:
		y, ok := b.(*Instance)
		if !ok || x.Class != y.Class {
			return false
		}
		if x == y {
			return true
		}
		for _, f := range x.Class.Fields {
			if !equal(x.Fields[f.Name], y.Fields[f.Name]) {
				return false
			}
		}
		return true
	}
	defer func() { recover() }() // uncomparable kinds are simply not equal
	return a == b
}

func compareValues(in *Interp, line int, a, b Value) int {
	switch x := a.(type) {
	case float64:
		if y, ok := b.(float64); ok {
			switch {
			case x < y:
				return -1
			case x > y:
				return 1
			}
			return 0
		}
	case string:
		if y, ok := b.(string); ok {
			return strings.Compare(x, y)
		}
	}
	in.fail(line, "I can't compare %s with %s", typeName(a), typeName(b))
	return 0
}

func sortValues(in *Interp, line int, items []Value, keys []Value) {
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return compareValues(in, line, keys[idx[i]], keys[idx[j]]) < 0 })
	sorted := make([]Value, len(items))
	for i, k := range idx {
		sorted[i] = items[k]
	}
	copy(items, sorted)
}
