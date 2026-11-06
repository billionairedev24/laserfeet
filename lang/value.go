package lang

import (
	"fmt"
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
	Sig     *Sig
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
	index   map[string]int // field name -> slot
	types   []*Type        // field types, by slot
}

type RuleDef struct {
	Name    string
	Methods []string
	Sigs    []*BeamDecl
}

// Instance is one object. Its fields live in fixed slots in the order the object
// declares them, which takes far less memory than a map per object.
type Instance struct {
	Class *Class
	Slots []Value
}

func (o *Instance) field(name string) (Value, bool) {
	if i, ok := o.Class.index[name]; ok {
		return o.Slots[i], true
	}
	return nil, false
}

func (o *Instance) hasField(name string) bool {
	_, ok := o.Class.index[name]
	return ok
}

type Module struct {
	Name    string
	Members map[string]Value
}

func typeName(v Value) string {
	switch x := v.(type) {
	case nil:
		return "nothing"
	case int64:
		return "an Int"
	case float64:
		return "a Float"
	case *Decimal:
		return "a Decimal"
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
	case *Store:
		return "a store"
	}
	return "a value"
}

// Text is how a value looks when shown.
func Text(v Value) string { return toText(v, false) }

func toText(v Value, quote bool) string {
	switch x := v.(type) {
	case nil:
		return "nothing"
	case int64:
		return strconv.FormatInt(x, 10)
	case float64:
		return formatFloat(x)
	case *Decimal:
		return x.String()
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
		for i, f := range x.Class.Fields {
			parts = append(parts, f.Name+": "+toText(x.Slots[i], true))
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
		return x.Body
	case *Server:
		return fmt.Sprintf("<web server on port %d>", x.Port)
	case *Store:
		if x.path == "" {
			return "<store in memory>"
		}
		return "<store " + x.path + ">"
	}
	return fmt.Sprint(v)
}

func equal(a, b Value) bool {
	if isNumber(a) && isNumber(b) {
		c, _ := numberCmp(a, b)
		return c == 0
	}
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
		for i := range x.Class.Fields {
			if !equal(x.Slots[i], y.Slots[i]) {
				return false
			}
		}
		return true
	}
	defer func() { recover() }() // uncomparable kinds are simply not equal
	return a == b
}

func compareValues(in *Interp, line int, a, b Value) int {
	if c, ok := numberCmp(a, b); ok {
		return c
	}
	switch x := a.(type) {
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
