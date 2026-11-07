package lang

import (
	"encoding/json"
	"html"
	"net/url"
	"path"
	"strings"
	"time"
)

// Server is a Laser Feet web server.
type Server struct {
	Port        int
	routes      []*route
	before      []Value        // beams run before every route (server.before)
	origins     []string       // other sites allowed to call this server (server.allow)
	statics     []staticFolder // folders served as files (server.files)
	limits      []*limiter     // requests allowed per visitor (server.limit)
	session     *sessionKeys   // signed session cookies (server.sessions)
	maxBody     int64          // largest request body, in bytes
	quiet       bool           // no request log lines
	metricsPath string         // where the metrics page is (server.metrics), "" for none
	stats       *metrics       // counts for the metrics page
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
	Headers     [][2]string // extra headers, in order (Set-Cookie may repeat)
	Route       string      // the route pattern that answered, for metrics
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
	case "auth":
		return authModule()
	case "log":
		return logModule()
	case "config":
		return configModule()
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
			var r *Response
			var err error
			in.outside(func() {
				r, err = fetchURL(method, Text(a[0]), body)
			})
			if err != nil {
				in.fail(line, "web.fetch %s: %v", Text(a[0]), err)
			}
			return r
		}),
		"urlEncode": builtin("urlEncode", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "web.urlEncode", a, 1)
			return url.QueryEscape(Text(a[0]))
		}),
		"escape": builtin("escape", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "web.escape", a, 1)
			return html.EscapeString(Text(a[0]))
		}),
		"reply": builtin("reply", webReply),
		"render": builtin("render", func(in *Interp, line int, a []Value) Value {
			if len(a) < 1 || len(a) > 3 {
				in.fail(line, "web.render takes a template, its values and optionally a status, like web.render(\"views/home.html\", {title: \"Shop\"})")
			}
			var values Value
			if len(a) >= 2 {
				values = a[1]
			}
			status := int64(200)
			if len(a) == 3 {
				status = in.wantInt(line, "the status code", a[2])
				if status < 100 || status > 599 {
					in.fail(line, "a status code must be between 100 and 599")
				}
			}
			return &Response{Status: int(status), Body: in.render(line, Text(a[0]), values, 0), ContentType: "text/html; charset=utf-8"}
		}),
		"trusted": builtin("trusted", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "web.trusted", a, 1)
			return Trusted(Text(a[0]))
		}),
		"redirect": builtin("redirect", webRedirect),
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
	case "put":
		return addRoute("PUT")
	case "patch":
		return addRoute("PATCH")
	case "delete":
		return addRoute("DELETE")
	case "before":
		return builtin("before", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "server.before", a, 1)
			s.before = append(s.before, a[0])
			return nil
		})
	case "allow":
		return builtin("allow", func(in *Interp, line int, a []Value) Value {
			if len(a) == 0 {
				in.fail(line, "server.allow needs the sites that may call this server, like server.allow(\"https://shop.example\")")
			}
			for _, o := range a {
				o := strings.TrimRight(Text(o), "/")
				if o != "*" && !strings.HasPrefix(o, "https://") && !strings.HasPrefix(o, "http://") {
					in.fail(line, "server.allow takes addresses like https://shop.example, but got %q", o)
				}
				s.origins = append(s.origins, o)
			}
			return nil
		})
	case "files":
		return builtin("files", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "server.files", a, 2)
			prefix, dir := Text(a[0]), strings.TrimRight(Text(a[1]), "/")
			if !strings.HasPrefix(prefix, "/") {
				in.fail(line, "the path for server.files must start with /, like \"/static\"")
			}
			if !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}
			s.statics = append(s.statics, staticFolder{prefix, path.Clean(dir)})
			return nil
		})
	case "limit":
		return builtin("limit", func(in *Interp, line int, a []Value) Value {
			prefix := "/"
			if len(a) == 3 {
				prefix, a = Text(a[0]), a[1:]
			}
			if len(a) != 2 {
				in.fail(line, "server.limit takes how many requests and in how long, like server.limit(100, minutes(1)), optionally after a path")
			}
			n := in.wantInt(line, "the number of requests", a[0])
			per, ok := a[1].(Duration)
			if !ok || n < 1 || per <= 0 {
				in.fail(line, "server.limit takes a positive number and a Duration, like server.limit(100, minutes(1))")
			}
			s.limits = append(s.limits, &limiter{prefix: prefix, n: int(n), per: time.Duration(per), seen: map[string]*window{}})
			return nil
		})
	case "sessions":
		return builtin("sessions", func(in *Interp, line int, a []Value) Value {
			if len(a) < 1 || len(a) > 2 {
				in.fail(line, "server.sessions takes a secret, and optionally how long a session lasts, like server.sessions(env(\"SESSION_SECRET\", \"\"), days(7))")
			}
			secret := Text(a[0])
			if len(secret) < 32 {
				in.fail(line, "the session secret must be at least 32 characters, so nobody can guess it. Make one with: lf secret")
			}
			age := 7 * 24 * time.Hour
			if len(a) == 2 {
				d, ok := a[1].(Duration)
				if !ok || d <= 0 {
					in.fail(line, "how long a session lasts must be a Duration, like days(7)")
				}
				age = time.Duration(d)
			}
			s.session = &sessionKeys{key: []byte(secret), age: age}
			return nil
		})
	case "bodyLimit":
		return builtin("bodyLimit", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "server.bodyLimit", a, 1)
			mb := in.wantInt(line, "the size in megabytes", a[0])
			if mb < 1 || mb > 1024 {
				in.fail(line, "server.bodyLimit takes megabytes, from 1 to 1024")
			}
			s.maxBody = mb << 20
			return nil
		})
	case "quiet":
		return builtin("quiet", func(in *Interp, line int, a []Value) Value {
			s.quiet = true
			return nil
		})
	case "metrics":
		return builtin("metrics", func(in *Interp, line int, a []Value) Value {
			path := "/metrics"
			if len(a) > 0 {
				p, ok := a[0].(string)
				if !ok {
					in.fail(line, "server.metrics takes the path as Text")
				}
				path = p
			}
			if !strings.HasPrefix(path, "/") {
				in.fail(line, "server.metrics takes a path starting with /, like \"/metrics\"")
			}
			s.metricsPath = path
			return nil
		})
	case "start":
		return builtin("start", func(in *Interp, line int, a []Value) Value {
			startServer(in, s, line)
			return nil
		})
	case "visit":
		// visit pretends to be a browser: handy for tests and the playground.
		return builtin("visit", func(in *Interp, line int, a []Value) Value {
			return s.inRequest(in, visitRequest(in, line, a), false) // as a real request runs
		})
	case "routes":
		return builtin("routes", func(in *Interp, line int, a []Value) Value {
			out := newList(nil)
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
			if f.Private {
				continue // private fields stay inside the program
			}
			field(n, f.Name, x.Slots[i])
			n++
		}
		b.WriteByte('}')
	case Date, Moment, Duration, UUID:
		writeJSON(b, x.(interface{ String() string }).String())
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
