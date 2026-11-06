package lang

import (
	"fmt"
	"sort"
	"strings"
)

// TypeExpr is a type as written in the code, like List[Item] or Text?.
type TypeExpr struct {
	Line     int
	Name     string
	Args     []*TypeExpr // List[T], Map[T]
	Params   []*TypeExpr // Beam(A, B) gives C
	Result   *TypeExpr
	Optional bool
}

func (t *TypeExpr) String() string {
	if t == nil {
		return "?"
	}
	var s string
	switch {
	case t.Name == "Beam":
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = p.String()
		}
		s = "Beam(" + strings.Join(parts, ", ") + ")"
		if t.Result != nil {
			s += " gives " + t.Result.String()
		}
	case len(t.Args) > 0:
		parts := make([]string, len(t.Args))
		for i, a := range t.Args {
			parts[i] = a.String()
		}
		s = t.Name + "[" + strings.Join(parts, ", ") + "]"
	default:
		s = t.Name
	}
	if t.Optional {
		s += "?"
	}
	return s
}

// Kind is the family a type belongs to.
type Kind int

const (
	KInt Kind = iota
	KFloat
	KDecimal
	KText
	KFlag
	KJson // any value, for data from outside such as JSON
	KNothing
	KList
	KMap
	KObject
	KRule
	KBeam
	KLibrary // Server, Response, Store, Request
	KChoice  // a value of a choice: Status
	KDate
	KTime
	KDuration
	KUUID
	// used only by lf check:
	KUnknown   // not known yet (an untyped one-line beam input); accepted anywhere
	KClassRef  // an object's name used as a value: Item(...) or Item.from(json)
	KModule    // a library or a file brought in with use
	KBuiltin   // a built-in beam, checked by its own rule
	KChoiceRef // a choice's name used as a value: Status.Pending
	KParam     // a generic's type name inside it: the T in first[T]
)

// Type is a resolved type.
type Type struct {
	Kind     Kind
	Elem     *Type // List and Map
	Class    *Class
	Rule     *RuleDef
	Choice   *Choice
	Args     []*Type  // a generic object or choice's types: Page[Product]
	TParams  []string // a generic beam's type names
	Params   []*Type
	Result   *Type
	Name     string // library types; built-in beam names
	Optional bool
	Lit      bool             // a decimal written in the code: also fits a Float
	PNames   []string         // beam input names, for named inputs
	Members  map[string]*Type // modules
	Sends    bool             // lf check: this beam type was inferred from a body that sends a value
}

var (
	tInt     = &Type{Kind: KInt}
	tFloat   = &Type{Kind: KFloat}
	tDecimal = &Type{Kind: KDecimal}
	tText    = &Type{Kind: KText}
	tFlag    = &Type{Kind: KFlag}
	tJson    = &Type{Kind: KJson}
	tNothing = &Type{Kind: KNothing}
)

var (
	tDate     = &Type{Kind: KDate}
	tTime     = &Type{Kind: KTime}
	tDuration = &Type{Kind: KDuration}
	tUUID     = &Type{Kind: KUUID}
)

var simpleTypes = map[string]*Type{
	"Int": tInt, "Float": tFloat, "Decimal": tDecimal, "Text": tText, "Flag": tFlag, "Json": tJson, "Nothing": tNothing,
	"Date": tDate, "Time": tTime, "Duration": tDuration, "UUID": tUUID,
}

var libraryTypes = map[string]bool{"Server": true, "Response": true, "Store": true, "Request": true, "Database": true, "Session": true, "Upload": true}

// Describe is how a type reads in messages.
func (t *Type) Describe() string {
	s := t.name()
	if t.Optional {
		return s + "? (or nothing)"
	}
	return s
}

func (t *Type) name() string {
	var s string
	switch t.Kind {
	case KInt:
		s = "Int"
	case KFloat:
		s = "Float"
	case KDecimal:
		s = "Decimal"
	case KText:
		s = "Text"
	case KFlag:
		s = "Flag"
	case KJson:
		s = "Json"
	case KNothing:
		s = "Nothing"
	case KDate:
		s = "Date"
	case KTime:
		s = "Time"
	case KDuration:
		s = "Duration"
	case KUUID:
		s = "UUID"
	case KList:
		s = "List[" + t.Elem.name() + "]"
	case KMap:
		s = "Map[" + t.Elem.name() + "]"
	case KObject:
		s = t.Class.Name
		if len(t.Args) > 0 {
			s += argNames(t.Args)
		}
	case KParam:
		s = t.Name
	case KRule:
		s = t.Rule.Name
	case KChoice:
		s = t.Choice.Name
		if len(t.Args) > 0 {
			s += argNames(t.Args)
		}
	case KChoiceRef:
		s = "the choice " + t.Choice.Name
	case KLibrary, KModule:
		s = t.Name
		if t.Name == "Job" && t.Elem != nil {
			s = "Job[" + t.Elem.name() + "]"
		}
	case KUnknown:
		s = "a value"
	case KClassRef:
		s = "the object " + t.Class.Name
	case KBuiltin:
		s = "the beam " + t.Name
	case KBeam:
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = p.name()
		}
		s = "Beam(" + strings.Join(parts, ", ") + ")"
		if t.Result != nil {
			s += " gives " + t.Result.name()
		}
	}
	if t.Optional && t.Kind != KNothing {
		s += "?"
	}
	return s
}

// resolveType turns a written type into a Type, looking up object and rule names in env.
func (in *Interp) resolveType(te *TypeExpr, env *Env) *Type {
	if te == nil {
		return nil
	}
	var t *Type
	switch {
	case simpleTypes[te.Name] != nil:
		if len(te.Args) > 0 {
			in.fail(te.Line, "%s doesn't take anything in [ ]", te.Name)
		}
		base := *simpleTypes[te.Name]
		t = &base
	case te.Name == "List" || te.Name == "Map":
		if len(te.Args) != 1 {
			in.fail(te.Line, "say what goes in the %s, like %s[Text]", te.Name, te.Name)
		}
		k := KList
		if te.Name == "Map" {
			k = KMap
		}
		t = &Type{Kind: k, Elem: in.resolveType(te.Args[0], env)}
	case te.Name == "Beam":
		t = &Type{Kind: KBeam, Result: in.resolveType(te.Result, env)}
		for _, p := range te.Params {
			t.Params = append(t.Params, in.resolveType(p, env))
		}
	case libraryTypes[te.Name]:
		t = &Type{Kind: KLibrary, Name: te.Name}
	default:
		v, ok := env.lookup(te.Name)
		switch x := v.(type) {
		case *Class:
			t = &Type{Kind: KObject, Class: x}
		case *RuleDef:
			t = &Type{Kind: KRule, Rule: x}
		case *Choice:
			t = &Type{Kind: KChoice, Choice: x}
		case typeParam:
			t = &Type{Kind: KJson} // lf check has made sure the values fit; nothing to check here
		default:
			msg := fmt.Sprintf("there is no type called '%s'", te.Name)
			if !ok {
				if s := suggest(te.Name, typeNames(env)); s != "" {
					msg += fmt.Sprintf(". Did you mean '%s'?", s)
				}
			}
			in.fail(te.Line, "%s", msg)
		}
	}
	t.Optional = te.Optional
	return t
}

func typeNames(env *Env) []string {
	var out []string
	for k := range simpleTypes {
		out = append(out, k)
	}
	for k := range libraryTypes {
		out = append(out, k)
	}
	out = append(out, "List", "Map", "Beam")
	for _, n := range env.names_() {
		if v, _ := env.lookup(n); v != nil {
			switch v.(type) {
			case *Class, *RuleDef, *Choice:
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// conform checks that v fits t, converting where nothing can be lost
// (Int into Float or Decimal, a written decimal like 2.5 into Float).
// Lists and maps are checked as containers; what's inside them is checked by lf check.
func conform(v Value, t *Type) (Value, bool) {
	if t == nil || t.Kind == KJson {
		return v, true
	}
	if v == nil {
		return nil, t.Optional || t.Kind == KNothing
	}
	switch t.Kind {
	case KInt:
		_, ok := v.(int64)
		return v, ok
	case KFloat:
		switch x := v.(type) {
		case float64:
			return x, true
		case int64:
			return float64(x), true
		case *Decimal:
			if x.literal {
				return x.Float64(), true
			}
		}
		return v, false
	case KDecimal:
		switch x := v.(type) {
		case *Decimal:
			return x, true
		case int64:
			return decimalFromInt(x), true
		}
		return v, false
	case KText:
		_, ok := v.(string)
		return v, ok
	case KFlag:
		_, ok := v.(bool)
		return v, ok
	case KList:
		_, ok := v.(*List)
		return v, ok
	case KMap:
		_, ok := v.(*Map)
		return v, ok
	case KObject:
		o, ok := v.(*Instance)
		return v, ok && o.Class == t.Class
	case KChoice:
		o, ok := v.(*Instance)
		return v, ok && o.Class.Choice == t.Choice
	case KDate:
		_, ok := v.(Date)
		return v, ok
	case KTime:
		_, ok := v.(Moment)
		return v, ok
	case KDuration:
		_, ok := v.(Duration)
		return v, ok
	case KUUID:
		_, ok := v.(UUID)
		return v, ok
	case KRule:
		o, ok := v.(*Instance)
		if !ok {
			return v, false
		}
		for _, r := range o.Class.Follows {
			if r == t.Rule.Name {
				return v, true
			}
		}
		return v, false
	case KBeam:
		switch v.(type) {
		case *Beam, *Builtin:
			return v, true
		}
		return v, false
	case KLibrary:
		switch t.Name {
		case "Server":
			_, ok := v.(*Server)
			return v, ok
		case "Response":
			_, ok := v.(*Response)
			return v, ok
		case "Store":
			_, ok := v.(*Store)
			return v, ok
		case "Request":
			_, ok := v.(*Map)
			return v, ok
		case "Database":
			_, ok := v.(*Database)
			return v, ok
		case "Session":
			_, ok := v.(*Session)
			return v, ok
		case "Upload":
			_, ok := v.(*Upload)
			return v, ok
		}
	case KNothing:
		return v, false
	}
	return v, false
}

// fromJSON-style deep conversion, used by Item.from(json): turns maps and lists
// that came from outside into real typed values, or explains what's wrong.
func (in *Interp) convertDeep(v Value, t *Type, where string) (Value, string) {
	if t == nil || t.Kind == KJson {
		return v, ""
	}
	if v == nil {
		if t.Optional {
			return nil, ""
		}
		return nil, where + " is missing"
	}
	switch t.Kind {
	case KList:
		l, ok := v.(*List)
		if !ok {
			return nil, fmt.Sprintf("%s must be a list, but got %s", where, typeName(v))
		}
		out := newList(make([]Value, len(l.Items)))
		for i, it := range l.Items {
			c, problem := in.convertDeep(it, t.Elem, fmt.Sprintf("%s[%d]", where, i))
			if problem != "" {
				return nil, problem
			}
			out.Items[i] = c
		}
		return out, ""
	case KMap:
		m, ok := v.(*Map)
		if !ok {
			return nil, fmt.Sprintf("%s must be a map, but got %s", where, typeName(v))
		}
		out := NewMap()
		for _, k := range m.Keys {
			c, problem := in.convertDeep(m.M[k], t.Elem, where+"."+k)
			if problem != "" {
				return nil, problem
			}
			out.Set(k, c)
		}
		return out, ""
	case KChoice:
		return in.choiceFrom(t.Choice, v, where)
	case KDate, KTime, KDuration, KUUID:
		c, err := clockFrom(t.Kind, v)
		if err != nil {
			return nil, fmt.Sprintf("%s must be %s, but got %s", where, t.name(), describeValue(v))
		}
		return c, ""
	case KObject:
		if o, ok := v.(*Instance); ok && o.Class == t.Class {
			return o, ""
		}
		m, ok := v.(*Map)
		if !ok {
			return nil, fmt.Sprintf("%s must be an object with %s's fields, but got %s", where, t.Class.Name, typeName(v))
		}
		return in.objectFrom(t.Class, m, where)
	}
	c, ok := conform(v, t)
	if !ok {
		return nil, fmt.Sprintf("%s must be %s, but got %s", where, t.name(), describeValue(v))
	}
	return c, ""
}

// objectFrom builds an object from a map (usually JSON), checking every field.
func (in *Interp) objectFrom(c *Class, m *Map, where string) (Value, string) {
	in.classTypes(c)
	inst := in.newInstance(c)
	for i, f := range c.Fields {
		name := f.Name
		if where != "" {
			name = where + "." + f.Name
		}
		raw, present := m.M[f.Name]
		if f.Private { // never filled from outside data
			if f.Default == nil {
				return nil, fmt.Sprintf("%s.%s is private, so it can't come from outside data; give it a default value", c.Name, f.Name)
			}
			present = false
		}
		if !present && f.Default != nil {
			inst.Slots[i] = in.eval(f.Default, c.Env)
			continue
		}
		if !present && !c.types[i].Optional {
			return nil, name + " is missing"
		}
		v, problem := in.convertDeep(raw, c.types[i], name)
		if problem != "" {
			return nil, problem
		}
		inst.Slots[i] = v
	}
	for _, k := range m.Keys {
		if i, ok := c.index[k]; !ok || c.Fields[i].Private {
			field := k
			if where != "" {
				field = where + "." + k
			}
			return nil, fmt.Sprintf("%s isn't a field of %s", field, c.Name)
		}
	}
	return inst, ""
}

// classTypes resolves an object's field types the first time they're needed.
func (in *Interp) classTypes(c *Class) {
	if c.typesReady.Load() {
		return
	}
	in.typesMu.Lock()
	defer in.typesMu.Unlock()
	if c.typesReady.Load() {
		return
	}
	types := make([]*Type, len(c.Fields))
	env := withTypeParams(c.Env, c.TypeParams)
	for i, f := range c.Fields {
		types[i] = in.resolveType(f.Type, env)
	}
	c.types = types
	c.typesReady.Store(true)
}

// typeParam stands for a generic's type (the T in Page[T]) while the engine
// reads the types of its fields and inputs.
type typeParam struct{}

func withTypeParams(env *Env, names []string) *Env {
	if len(names) == 0 {
		return env
	}
	e := newEnv(env)
	for _, n := range names {
		e.define(n, typeParam{}, true)
	}
	return e
}
