package lang

import "fmt"

// Choice is a type whose values are exactly one of a few options, some of which
// carry data: choice Status { Pending, Shipped(on: Text), Lost }.
// Each option is a small Class (its data are its fields) that points back at the
// choice, so making and checking option values works like making objects.
type Choice struct {
	Name       string
	TypeParams []string
	Options    []*Class
	index      map[string]int
}

// option finds an option by name.
func (c *Choice) option(name string) *Class {
	if i, ok := c.index[name]; ok {
		return c.Options[i]
	}
	return nil
}

func (c *Choice) optionNames() []string {
	out := make([]string, len(c.Options))
	for i, o := range c.Options {
		out[i] = o.Name
	}
	return out
}

// newChoice builds a Choice from its declaration (used by the engine and lf check).
func newChoice(d *ChoiceDecl, env *Env) (*Choice, string) {
	ch := &Choice{Name: d.Name, index: map[string]int{}, TypeParams: d.TypeParams}
	for i, o := range d.Options {
		if _, dup := ch.index[o.Name]; dup {
			return nil, fmt.Sprintf("choice %s has two options called '%s'", d.Name, o.Name)
		}
		cl := &Class{Name: o.Name, Fields: o.Fields, Methods: map[string]*BeamDecl{}, Env: env, index: map[string]int{}, Choice: ch, TypeParams: d.TypeParams}
		for j, f := range o.Fields {
			if _, dup := cl.index[f.Name]; dup {
				return nil, fmt.Sprintf("option %s.%s has two values called '%s'", d.Name, o.Name, f.Name)
			}
			cl.index[f.Name] = j
		}
		if len(o.Fields) == 0 {
			cl.single = &Instance{Class: cl} // options without data are all the same value
		}
		ch.index[o.Name] = i
		ch.Options = append(ch.Options, cl)
	}
	return ch, ""
}

// choiceMember is Status.Pending (a value) or Status.Shipped (makes a value).
func (in *Interp) choiceMember(ch *Choice, name string, line int) Value {
	if o := ch.option(name); o != nil {
		if o.single != nil {
			return o.single
		}
		return o
	}
	if name == "from" {
		return builtin(ch.Name+".from", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, ch.Name+".from", a, 1)
			v, problem := in.convertDeep(a[0], &Type{Kind: KChoice, Choice: ch}, ch.Name)
			if problem != "" {
				panic(&RuntimeError{Line: line, Msg: problem, Status: 400})
			}
			return v
		})
	}
	in.fail(line, "choice %s has no option called '%s'%s", ch.Name, name, didYouMean(name, ch.optionNames()))
	return nil
}

// choiceFrom reads a choice from outside data: "Pending", or {"kind": "Shipped", "on": "..."}.
func (in *Interp) choiceFrom(ch *Choice, v Value, where string) (Value, string) {
	if inst, ok := v.(*Instance); ok && inst.Class.Choice == ch {
		return inst, ""
	}
	kind, data := "", NewMap()
	switch x := v.(type) {
	case string:
		kind = x
	case *Map:
		k, ok := x.M["kind"].(string)
		if !ok {
			return nil, fmt.Sprintf("%s must say which %s it is, like {\"kind\": \"%s\"}", where, ch.Name, ch.Options[0].Name)
		}
		kind = k
		for _, key := range x.Keys {
			if key != "kind" {
				data.Set(key, x.M[key])
			}
		}
	default:
		return nil, fmt.Sprintf("%s must be one of %s's options, but got %s", where, ch.Name, describeValue(v))
	}
	o := ch.option(kind)
	if o == nil {
		return nil, fmt.Sprintf("%s must be one of %s, but got \"%s\"", where, joinOr(ch.optionNames()), kind)
	}
	if o.single != nil {
		if len(data.Keys) > 0 {
			return nil, fmt.Sprintf("%s: %s has no data, but got %s", where, kind, data.Keys[0])
		}
		return o.single, ""
	}
	return in.objectFrom(o, data, where)
}

// joinOr writes a list of names as "A, B or C".
func joinOr(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	s := ""
	for i, n := range names[:len(names)-1] {
		if i > 0 {
			s += ", "
		}
		s += n
	}
	return s + " or " + names[len(names)-1]
}

// execFocus runs the first arm that matches the value.
func (in *Interp) execFocus(s *FocusStmt, env *Env) (ctrl, Value) {
	v := in.eval(s.Subject, env)
	inst, isOption := v.(*Instance)
	for _, arm := range s.Arms {
		switch {
		case arm.Otherwise:
			return in.execBlock(arm.Body, env)
		case arm.Option != "":
			if !isOption || inst.Class.Choice == nil || inst.Class.Name != arm.Option {
				continue
			}
			e := newEnv(env)
			for i, b := range arm.Binds {
				if i < len(inst.Slots) {
					e.define(b, inst.Slots[i], false)
				}
			}
			return in.execBlock(arm.Body, e)
		default:
			if equal(v, in.eval(arm.Value, env)) {
				return in.execBlock(arm.Body, env)
			}
		}
	}
	in.fail(s.Line, "focus has no arm for %s", toText(v, true))
	return cNone, nil
}
