package lang

import (
	"encoding/json"
	"fmt"
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
	if name != "web" {
		return nil
	}
	return &Module{Name: "web", Members: map[string]Value{
		"open": builtin("open", func(in *Interp, line int, a []Value) Value {
			port := 8080.0
			if len(a) == 1 {
				port = wantNumber(in, line, "web.open", a[0])
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
			r := &Response{Status: int(wantNumber(in, line, "web.status", a[0])), ContentType: "text/plain; charset=utf-8"}
			if len(a) == 2 {
				r.Body = Text(a[1])
			}
			return r
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
			if len(a) < 1 || len(a) > 2 {
				in.fail(line, "server.visit needs a path, like server.visit(\"/hello\")")
			}
			method := "GET"
			if len(a) == 2 {
				method = strings.ToUpper(Text(a[1]))
			}
			r := s.handle(in, method, Text(a[0]), "")
			if r.Status != 200 {
				return &Response{Status: r.Status, Body: r.Body}
			}
			return r.Body
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
		return s.run(in, r, req)
	}
	return &Response{Status: 404, Body: "Not found: " + u.Path, ContentType: "text/plain; charset=utf-8"}
}

func (s *Server) run(in *Interp, r *route, req *Map) (resp *Response) {
	defer func() {
		if x := recover(); x != nil {
			if re, ok := x.(*RuntimeError); ok {
				in.print("Problem while handling " + r.pattern + ": " + re.Error())
				resp = &Response{Status: 500, Body: "Something went wrong: " + re.Msg, ContentType: "text/plain; charset=utf-8"}
				return
			}
			panic(x)
		}
	}()
	var out Value
	if b, ok := r.handler.(*Beam); ok && len(b.Params) == 0 {
		out = in.call(b, nil, nil, 0, map[string]Value{"request": req})
	} else {
		out = in.call(r.handler, []Value{req}, nil, 0, nil)
	}
	switch v := out.(type) {
	case *Response:
		return v
	case string:
		ct := "text/plain; charset=utf-8"
		if strings.HasPrefix(strings.TrimSpace(v), "<") {
			ct = "text/html; charset=utf-8"
		}
		return &Response{Status: 200, Body: v, ContentType: ct}
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
		for k, it := range x.Fields {
			out[k] = toPlain(it)
		}
		return out
	case float64, string, bool, nil:
		return x
	}
	return Text(v)
}

func toJSON(v Value) string {
	b, err := json.Marshal(toPlain(v))
	if err != nil {
		return fmt.Sprintf("%q", Text(v))
	}
	return string(b)
}
