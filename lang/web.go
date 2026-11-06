package lang

import (
	"encoding/json"
	"html"
	"net/url"
	"sort"
	"strings"
)

// Server is a Laser Feet web server.
type Server struct {
	Port   int
	routes []*route
}

type route struct {
	method  string
	pattern string
	parts   []string
	handler Value
}

// Response is what a web handler sends back.
type Response struct {
	Status      int
	Body        string
	ContentType string
}

func loadModule(in *Interp, name string) *Module {
	switch name {
	case "web":
		return webModule()
	case "json":
		return jsonModule()
	case "files":
		return filesModule()
	case "time":
		return timeModule()
	case "store":
		return storeModule()
	case "postgres":
		return postgresModule()
	}
	return nil
}

func webModule() *Module {
	return &Module{Name: "web", Members: map[string]Value{
		"open": builtin("open", func(in *Interp, line int, a []Value) Value {
			port := int64(8080)
			if len(a) == 1 {
				port = in.wantInt(line, "the port", a[0])
			}
			if port < 1 || port > 65535 {
				in.fail(line, "a port must be between 1 and 65535")
			}
			return &Server{Port: int(port)}
		}),
		"json": builtin("json", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "web.json", a, 1)
			return &Response{Status: 200, Body: toJSON(a[0]), ContentType: "application/json"}
		}),
		"status": builtin("status", func(in *Interp, line int, a []Value) Value {
			if len(a) < 1 || len(a) > 2 {
				in.fail(line, "web.status takes a code and an optional message, like web.status(404, \"Not found\")")
			}
			code := in.wantInt(line, "the status code", a[0])
			if code < 100 || code > 599 {
				in.fail(line, "a status code must be between 100 and 599")
			}
			r := &Response{Status: int(code), ContentType: "text/plain; charset=utf-8"}
			if len(a) == 2 {
				if t, ok := a[1].(string); ok {
					r.Body = t
				} else {
					r.Body, r.ContentType = toJSON(a[1]), "application/json"
				}
			}
			return r
		}),
		"fetch": builtin("fetch", func(in *Interp, line int, a []Value) Value {
			if len(a) < 1 || len(a) > 3 {
				in.fail(line, "web.fetch needs an address, and optionally a method and a body, like web.fetch(\"https://example.com\")")
			}
			method, body := "GET", ""
			if len(a) >= 2 {
				method = strings.ToUpper(Text(a[1]))
			}
			if len(a) == 3 {
				if t, ok := a[2].(string); ok {
					body = t
				} else {
					body = toJSON(a[2])
				}
			}
			// waiting for the network lets other fast work run meanwhile
			in.gil.Unlock()
			r, err := fetchURL(method, Text(a[0]), body)
			in.gil.Lock()
			if err != nil {
				in.fail(line, "web.fetch %s: %v", Text(a[0]), err)
			}
			return r
		}),
		"escape": builtin("escape", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "web.escape", a, 1)
			return html.EscapeString(Text(a[0]))
		}),
		"page": builtin("page", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "web.page", a, 1)
			return &Response{Status: 200, Body: Text(a[0]), ContentType: "text/html; charset=utf-8"}
		}),
	}}
}

func serverMethod(in *Interp, s *Server, name string) *Builtin {
	addRoute := func(method string) *Builtin {
		return builtin(name, func(in *Interp, line int, a []Value) Value {
			if len(a) != 2 {
				in.fail(line, "server.%s needs a path and a beam, like server.%s(\"/hello\") { send \"Hi\" }", name, name)
			}
			path := Text(a[0])
			switch a[1].(type) {
			case *Beam, *Builtin:
			default:
				in.fail(line, "server.%s needs a beam to run for %s", name, path)
			}
			s.routes = append(s.routes, &route{method, path, splitPath(path), a[1]})
			return nil
		})
	}
	switch name {
	case "on":
		return addRoute("ANY")
	case "get":
		return addRoute("GET")
	case "post":
		return addRoute("POST")
	case "start":
		return builtin("start", func(in *Interp, line int, a []Value) Value {
			startServer(in, s, line)
			return nil
		})
	case "visit":
		// visit pretends to be a browser: handy for tests and the playground.
		return builtin("visit", func(in *Interp, line int, a []Value) Value {
			if len(a) < 1 || len(a) > 3 {
				in.fail(line, "server.visit needs a path, and optionally a method and a body, like server.visit(\"/todos\", \"POST\", {title: \"Milk\"})")
			}
			method := "GET"
			if len(a) >= 2 {
				method = strings.ToUpper(Text(a[1]))
			}
			body := ""
			if len(a) == 3 {
				if t, ok := a[2].(string); ok {
					body = t
				} else {
					body = toJSON(a[2])
				}
			}
			return s.handle(in, method, Text(a[0]), body)
		})
	case "routes":
		return builtin("routes", func(in *Interp, line int, a []Value) Value {
			out := &List{}
			for _, r := range s.routes {
				out.Items = append(out.Items, r.pattern)
			}
			return out
		})
	}
	return nil
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

// handle finds the route for a request and runs it. The caller must hold the interpreter lock.
func (s *Server) handle(in *Interp, method, rawPath, body string) (resp *Response) {
	u, err := url.Parse(rawPath)
	if err != nil {
		return &Response{Status: 400, Body: "Bad request"}
	}
	got := splitPath(u.Path)
	for _, r := range s.routes {
		if r.method != "ANY" && r.method != method {
			continue
		}
		if len(r.parts) != len(got) {
			continue
		}
		params := NewMap()
		ok := true
		for i, part := range r.parts {
			if strings.HasPrefix(part, ":") {
				params.Set(part[1:], got[i])
			} else if part != got[i] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		query := NewMap()
		keys := make([]string, 0, len(u.Query()))
		for k := range u.Query() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			query.Set(k, u.Query().Get(k))
		}
		req := NewMap()
		req.Set("method", method)
		req.Set("path", u.Path)
		req.Set("params", params)
		req.Set("query", query)
		req.Set("body", body)
		var parsed Value
		if strings.TrimSpace(body) != "" {
			if v, err := fromJSON(body); err == nil {
				parsed = v
			}
		}
		req.Set("json", parsed)
		return s.run(in, r, req)
	}
	return &Response{Status: 404, Body: "Not found: " + u.Path, ContentType: "text/plain; charset=utf-8"}
}

func (s *Server) run(in *Interp, r *route, req *Map) (resp *Response) {
	defer func() {
		if x := recover(); x != nil {
			if re, ok := x.(*RuntimeError); ok && re.Status == 400 {
				// the visitor sent something that doesn't fit: tell them what (it's about their input)
				resp = &Response{Status: 400, Body: toJSON(mapOf("error", re.Msg)), ContentType: "application/json"}
				return
			}
			if re, ok := x.(*RuntimeError); ok {
				in.print("Problem while handling " + r.pattern + ": " + re.Error())
				// details go to the server log only, never to the visitor
				resp = &Response{Status: 500, Body: "Something went wrong", ContentType: "text/plain; charset=utf-8"}
				return
			}
			panic(x)
		}
	}()
	var out Value
	if b, ok := r.handler.(*Beam); ok && len(b.Params) == 0 {
		out = in.call(b, nil, nil, 0, nil) // a route that doesn't name its request
	} else {
		out = in.call(r.handler, []Value{req}, nil, 0, nil)
	}
	switch v := out.(type) {
	case *Response:
		return v
	case string:
		// plain text is never treated as a web page, so visitor input can't inject HTML;
		// use web.page(...) to send HTML on purpose.
		return &Response{Status: 200, Body: v, ContentType: "text/plain; charset=utf-8"}
	case nil:
		return &Response{Status: 204}
	default:
		return &Response{Status: 200, Body: toJSON(v), ContentType: "application/json"}
	}
}

func toPlain(v Value) any {
	switch x := v.(type) {
	case *List:
		out := make([]any, len(x.Items))
		for i, it := range x.Items {
			out[i] = toPlain(it)
		}
		return out
	case *Map:
		out := map[string]any{}
		for k, it := range x.M {
			out[k] = toPlain(it)
		}
		return out
	case *Instance:
		out := map[string]any{}
		for i, f := range x.Class.Fields {
			out[f.Name] = toPlain(x.Slots[i])
		}
		return out
	case int64, float64, string, bool, nil:
		return x
	case *Decimal:
		return json.Number(x.String())
	}
	return Text(v)
}

// toJSON writes a value as JSON, keeping fields in the order they were written.
func toJSON(v Value) string {
	var b strings.Builder
	writeJSON(&b, v)
	return b.String()
}

func writeJSON(b *strings.Builder, v Value) {
	field := func(i int, k string, val Value) {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		writeJSON(b, val)
	}
	switch x := v.(type) {
	case *List:
		b.WriteByte('[')
		for i, it := range x.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, it)
		}
		b.WriteByte(']')
	case *Map:
		b.WriteByte('{')
		for i, k := range x.Keys {
			field(i, k, x.M[k])
		}
		b.WriteByte('}')
	case *Instance:
		if x.Class.single != nil { // an option without data: just its name
			writeJSON(b, x.Class.Name)
			return
		}
		b.WriteByte('{')
		n := 0
		if x.Class.Choice != nil { // an option with data says which one it is
			field(0, "kind", x.Class.Name)
			n = 1
		}
		for i, f := range x.Class.Fields {
			field(i+n, f.Name, x.Slots[i])
		}
		b.WriteByte('}')
	case *Decimal:
		b.WriteString(x.String()) // exact, never rounded through a Float
	case int64, float64, string, bool, nil:
		out, err := json.Marshal(x)
		if err != nil { // NaN or infinity have no JSON form
			b.WriteString("null")
			return
		}
		b.Write(out)
	default:
		out, _ := json.Marshal(Text(v))
		b.Write(out)
	}
}

func mapOf(k string, v Value) *Map {
	m := NewMap()
	m.Set(k, v)
	return m
}
