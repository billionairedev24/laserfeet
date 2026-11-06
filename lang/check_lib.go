package lang

// The types of Laser Feet's built-in beams and libraries, for lf check.

var lazyBuiltins = map[string]bool{"keepIf": true, "dropIf": true, "turn": true, "sortBy": true, "findFirst": true}

var builtinNames = map[string]bool{
	"text": true, "number": true, "decimal": true, "float": true, "int": true, "count": true, "sum": true,
	"biggest": true, "smallest": true, "round": true, "fixed": true, "first": true, "last": true,
	"reverse": true, "sort": true, "join": true, "pause": true, "readFile": true, "env": true,
	"keepIf": true, "dropIf": true, "turn": true, "sortBy": true, "findFirst": true,
}

func builtinType(name string) *Type {
	if builtinNames[name] {
		return &Type{Kind: KBuiltin, Name: name}
	}
	return nil
}

func libraryModule(name string) *Type {
	members := map[string][]string{
		"web":   {"open", "json", "status", "page", "escape"},
		"json":  {"read", "write", "pretty"},
		"files": {"read", "write", "exists", "list"},
		"time":  {"now", "today", "seconds"},
		"store": {"open", "memory"},
	}[name]
	if members == nil {
		return nil
	}
	m := &Type{Kind: KModule, Name: name, Members: map[string]*Type{}}
	for _, f := range members {
		m.Members[f] = &Type{Kind: KBuiltin, Name: name + "." + f}
	}
	return m
}

var libraryMembers = map[string]bool{
	"list.add": true, "list.remove": true, "list.size": true, "list.has": true,
	"text.upper": true, "text.lower": true, "text.trim": true, "text.size": true, "text.has": true,
	"text.startsWith": true, "text.split": true, "text.replace": true,
	"server.get": true, "server.post": true, "server.on": true, "server.start": true, "server.visit": true, "server.routes": true,
	"store.add": true, "store.all": true, "store.find": true, "store.update": true, "store.remove": true, "store.count": true,
}

// memberType is the type of x.name for lists, text and library values.
func memberType(name string, recv *Type) *Type {
	switch name {
	case "response.status":
		return tInt
	case "response.body":
		return tText
	case "response.json":
		return tJson
	case "request.method", "request.path", "request.body":
		return tText
	case "request.params":
		return lib("Params")
	case "request.query":
		return mapOf_(tText)
	case "request.json":
		return tJson
	}
	if libraryMembers[name] {
		return &Type{Kind: KBuiltin, Name: name, Elem: recv}
	}
	return nil
}

func (c *checker) argCount(line int, name string, args []*Type, min, max int) bool {
	if len(args) < min || len(args) > max {
		want := ""
		switch {
		case min == max:
			want = itoa(min)
		default:
			want = itoa(min) + " to " + itoa(max)
		}
		c.report(line, "%s takes %s input(s), but got %d", name, want, len(args))
		return false
	}
	return true
}

func itoa(i int) string {
	return string(rune('0' + i))
}

func (c *checker) need(line int, t, want *Type, what string) {
	if t != nil {
		c.assignable(line, t, want, what)
	}
}

func (c *checker) numberArg(line int, t *Type, what string) {
	if t != nil && !isNum(t) && t.Kind != KJson && t.Kind != KUnknown {
		c.report(line, "%s must be a number, but this is %s", what, t.name())
	}
}

func elemOf(t *Type) *Type {
	if t != nil && t.Kind == KList && t.Elem != nil {
		return t.Elem
	}
	return tJson
}

func (c *checker) listArg(line int, t *Type, name string) *Type {
	if t == nil || t.Kind == KJson || t.Kind == KUnknown {
		return listOf(tJson)
	}
	if t.Kind != KList {
		c.report(line, "%s needs a list, but this is %s", name, t.name())
		return listOf(tUnknown)
	}
	if t.Optional {
		c.report(line, "%s needs a list, but this might be nothing", name)
	}
	return t
}

// perItem checks a rule like keepIf(age >= 18): the item's fields and 'it' are visible.
func (c *checker) perItem(e Expr, item *Type, sc *cscope) *Type {
	inner := &cscope{vars: map[string]*cvar{"it": {t: item}}, parent: sc}
	if item.Kind == KObject && !item.Optional {
		inner.obj = item.Class
	}
	if item.Kind == KJson || item.Kind == KUnknown {
		inner.open = true
	}
	if lam, ok := e.(*Lambda); ok {
		t := c.lambda(lam, sc, &Type{Kind: KBeam, Params: []*Type{item}}, nil)
		return resultOf(t)
	}
	return c.expr(e, inner, nil)
}

func (c *checker) builtin(line int, fn *Type, a []*Type, exprs []Expr, sc *cscope) *Type {
	n := fn.Name
	switch n {
	// ---- conversions ----
	case "text":
		c.argCount(line, n, a, 1, 1)
		return tText
	case "number", "decimal":
		if c.argCount(line, n, a, 1, 1) && a[0] != nil && a[0].Kind == KFloat {
			// allowed: converting a Float on purpose
		}
		return tDecimal
	case "float":
		c.argCount(line, n, a, 1, 1)
		return tFloat
	case "int":
		c.argCount(line, n, a, 1, 1)
		return tInt
	case "fixed":
		if c.argCount(line, n, a, 2, 2) {
			c.numberArg(line, a[0], "the number for fixed")
			c.need(line, a[1], tInt, "the number of places")
		}
		return tText
	case "round":
		if !c.argCount(line, n, a, 1, 2) {
			return tUnknown
		}
		c.numberArg(line, a[0], "the number to round")
		if len(a) == 2 {
			c.need(line, a[1], tInt, "the number of places")
			return c.settleLit(a[0])
		}
		return tInt
	// ---- lists ----
	case "count":
		if c.argCount(line, n, a, 1, 1) && a[0] != nil {
			switch a[0].Kind {
			case KList, KText, KMap, KJson, KUnknown:
			default:
				c.report(line, "count needs a list, text or map, but this is %s", a[0].name())
			}
		}
		return tInt
	case "sum", "biggest", "smallest":
		if !c.argCount(line, n, a, 1, 1) {
			return tUnknown
		}
		l := c.listArg(line, a[0], n)
		el := elemOf(l)
		if el.Kind != KJson && el.Kind != KUnknown && !isNum(el) {
			c.report(line, "%s needs a list of numbers, but this is %s", n, l.name())
		}
		if n == "sum" {
			return c.settleLit(el)
		}
		return opt(c.settleLit(el))
	case "first", "last":
		if !c.argCount(line, n, a, 1, 1) {
			return tUnknown
		}
		return opt(elemOf(c.listArg(line, a[0], n)))
	case "reverse", "sort":
		if !c.argCount(line, n, a, 1, 1) {
			return tUnknown
		}
		return c.listArg(line, a[0], n)
	case "join":
		if c.argCount(line, n, a, 1, 2) {
			c.listArg(line, a[0], n)
			if len(a) == 2 {
				c.need(line, a[1], tText, "the separator")
			}
		}
		return tText
	case "keepIf", "dropIf", "turn", "sortBy", "findFirst":
		if len(a) != 2 {
			c.report(line, "%s takes a list and one rule, like %s(age >= 18)", n, n)
			return tUnknown
		}
		l := c.listArg(line, a[0], n)
		item := elemOf(l)
		r := c.perItem(exprs[1], item, sc)
		switch n {
		case "keepIf", "dropIf", "findFirst":
			if r.Kind != KFlag && r.Kind != KJson && r.Kind != KUnknown {
				c.report(line, "%s needs a yes/no rule, but this rule gives %s", n, r.name())
			}
			if n == "findFirst" {
				return opt(item)
			}
			return l
		case "turn":
			return listOf(c.settleLit(r))
		case "sortBy":
			if !isNum(r) && r.Kind != KText && r.Kind != KJson && r.Kind != KUnknown {
				c.report(line, "sortBy needs a number or text to sort by, but this is %s", r.name())
			}
			return l
		}
	// ---- other ----
	case "pause":
		if c.argCount(line, n, a, 1, 1) {
			c.numberArg(line, a[0], "the seconds to pause")
		}
		return tNothing
	case "readFile":
		if c.argCount(line, n, a, 1, 1) {
			c.need(line, a[0], tText, "the file name")
		}
		return tText
	case "env":
		if !c.argCount(line, n, a, 1, 2) {
			return tUnknown
		}
		c.need(line, a[0], tText, "the setting's name")
		if len(a) == 2 {
			c.need(line, a[1], tText, "the default")
			return tText
		}
		return opt(tText)

	// ---- libraries ----
	case "web.open":
		if c.argCount(line, n, a, 0, 1) && len(a) == 1 {
			c.need(line, a[0], tInt, "the port")
		}
		return lib("Server")
	case "web.json", "web.page":
		c.argCount(line, n, a, 1, 1)
		if n == "web.page" && len(a) == 1 {
			c.need(line, a[0], tText, "the page")
		}
		return lib("Response")
	case "web.status":
		if c.argCount(line, n, a, 1, 2) {
			c.need(line, a[0], tInt, "the status code")
		}
		return lib("Response")
	case "web.escape":
		c.argCount(line, n, a, 1, 1)
		return tText
	case "json.read":
		if c.argCount(line, n, a, 1, 1) {
			c.need(line, a[0], tText, "the JSON text")
		}
		return tJson
	case "json.write", "json.pretty":
		c.argCount(line, n, a, 1, 1)
		return tText
	case "files.read":
		if c.argCount(line, n, a, 1, 1) {
			c.need(line, a[0], tText, "the file name")
		}
		return tText
	case "files.write":
		if c.argCount(line, n, a, 2, 2) {
			c.need(line, a[0], tText, "the file name")
			c.need(line, a[1], tText, "the text to save")
		}
		return tNothing
	case "files.exists":
		if c.argCount(line, n, a, 1, 1) {
			c.need(line, a[0], tText, "the file name")
		}
		return tFlag
	case "files.list":
		if c.argCount(line, n, a, 1, 1) {
			c.need(line, a[0], tText, "the folder")
		}
		return listOf(tText)
	case "time.now", "time.today":
		c.argCount(line, n, a, 0, 0)
		return tText
	case "time.seconds":
		c.argCount(line, n, a, 0, 0)
		return tFloat
	case "store.open":
		if c.argCount(line, n, a, 0, 1) && len(a) == 1 {
			c.need(line, a[0], tText, "the store's file name")
		}
		return lib("Store")
	case "store.memory":
		c.argCount(line, n, a, 0, 0)
		return lib("Store")

	// ---- methods ----
	case "list.add":
		for _, t := range a {
			c.need(line, t, elemOf(fn.Elem), "an item added to this list")
		}
		return fn.Elem
	case "list.remove":
		if c.argCount(line, "remove", a, 1, 1) {
			c.need(line, a[0], tInt, "the position to remove")
		}
		return elemOf(fn.Elem)
	case "list.size", "text.size":
		c.argCount(line, "size", a, 0, 0)
		return tInt
	case "list.has":
		if c.argCount(line, "has", a, 1, 1) {
			c.need(line, a[0], elemOf(fn.Elem), "what to look for")
		}
		return tFlag
	case "text.upper", "text.lower", "text.trim":
		c.argCount(line, n[5:], a, 0, 0)
		return tText
	case "text.has", "text.startsWith":
		if c.argCount(line, n[5:], a, 1, 1) {
			c.need(line, a[0], tText, "the text to look for")
		}
		return tFlag
	case "text.split":
		if c.argCount(line, "split", a, 0, 1) && len(a) == 1 {
			c.need(line, a[0], tText, "the separator")
		}
		return listOf(tText)
	case "text.replace":
		if c.argCount(line, "replace", a, 2, 2) {
			c.need(line, a[0], tText, "the text to replace")
			c.need(line, a[1], tText, "the new text")
		}
		return tText
	case "map.keys":
		c.argCount(line, "keys", a, 0, 0)
		return listOf(tText)
	case "map.has":
		if c.argCount(line, "has", a, 1, 1) {
			c.need(line, a[0], tText, "the entry name")
		}
		return tFlag
	case "server.get", "server.post", "server.on":
		if c.argCount(line, n[7:], a, 2, 2) {
			c.need(line, a[0], tText, "the path")
		}
		return tNothing
	case "server.start":
		c.argCount(line, "start", a, 0, 0)
		return tNothing
	case "server.visit":
		if c.argCount(line, "visit", a, 1, 3) {
			c.need(line, a[0], tText, "the path")
			if len(a) >= 2 {
				c.need(line, a[1], tText, "the method")
			}
		}
		return lib("Response")
	case "server.routes":
		return listOf(tText)
	case "store.add":
		if c.argCount(line, "add", a, 2, 2) {
			c.need(line, a[0], tText, "the collection name")
		}
		return tJson
	case "store.all":
		if c.argCount(line, "all", a, 1, 1) {
			c.need(line, a[0], tText, "the collection name")
		}
		return listOf(tJson)
	case "store.find", "store.update":
		max := 2
		if n == "store.update" {
			max = 3
		}
		if c.argCount(line, n[6:], a, max, max) {
			c.need(line, a[0], tText, "the collection name")
			c.need(line, a[1], tInt, "the id")
		}
		return opt(tJson)
	case "store.remove":
		if c.argCount(line, "remove", a, 2, 2) {
			c.need(line, a[0], tText, "the collection name")
			c.need(line, a[1], tInt, "the id")
		}
		return tFlag
	case "store.count":
		if c.argCount(line, "count", a, 1, 1) {
			c.need(line, a[0], tText, "the collection name")
		}
		return tInt
	}
	if fn.Class != nil { // Item.from(json)
		c.argCount(line, n, a, 1, 1)
		return &Type{Kind: KObject, Class: fn.Class}
	}
	return tUnknown
}
