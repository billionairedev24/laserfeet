package lang

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime"
	"mime/multipart"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// incoming is one request, from the network or from server.visit.
type incoming struct {
	method string
	uri    string
	body   string
	ip     string
	header map[string]string // names in lower case
}

func (r *incoming) get(name string) string { return r.header[strings.ToLower(name)] }

// staticFolder is a folder served as files: server.files("/static", "public").
type staticFolder struct {
	prefix string
	dir    string
}

// limiter allows each visitor (by address) n requests per period on paths
// under prefix: server.limit(100, minutes(1)).
type limiter struct {
	prefix string
	n      int
	per    time.Duration
	mu     sync.Mutex
	seen   map[string]*window
}

type window struct {
	start time.Time
	count int
}

func (l *limiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.seen) > 50000 { // forget old visitors so memory stays small
		for k, w := range l.seen {
			if now.Sub(w.start) > l.per {
				delete(l.seen, k)
			}
		}
	}
	w := l.seen[ip]
	if w == nil || now.Sub(w.start) >= l.per {
		w = &window{start: now}
		l.seen[ip] = w
	}
	w.count++
	return w.count <= l.n
}

func unsafeMethod(m string) bool {
	return m != "GET" && m != "HEAD" && m != "OPTIONS"
}

// handle answers one request. The caller must hold the interpreter lock.
// serve answers a real request. Requests run at the same time as each other,
// on every core: each holds the engine's lock shared, inside a parallel
// region, so like 'fast each' items they can't change values made before
// they started (shared data lives in a store or a database). Code outside
// requests (launch, fast work) takes the lock whole, so it runs between them.
func (s *Server) serve(in *Interp, req *incoming) *Response {
	in.gil.RLock()
	defer in.gil.RUnlock()
	return s.inRequest(in, req, true)
}

// inRequest runs one request inside a parallel region.
func (s *Server) inRequest(in *Interp, req *incoming, wait bool) *Response {
	in.serving.Add(1)
	defer in.serving.Add(-1)
	if in.writing.Load() { // server.visit from the main code: run it as parallel work would
		in.unlockWhole()
		in.gil.RLock()
		defer func() {
			in.gil.RUnlock()
			in.lockWhole()
		}()
	}
	started := in.enterRegion()
	defer in.leaveRegion(wait || started)
	return s.handle(in, req)
}

func (s *Server) handle(in *Interp, req *incoming) *Response {
	u, err := url.Parse(req.uri)
	if err != nil {
		return textReply(400, "Bad request")
	}
	now := time.Now()
	for _, l := range s.limits {
		if strings.HasPrefix(u.Path, l.prefix) && !l.allow(req.ip, now) {
			r := textReply(429, "Too many requests; please wait a little and try again")
			r.Headers = append(r.Headers, [2]string{"Retry-After", fmt.Sprint(int(l.per.Seconds()))})
			return r
		}
	}
	origin := req.get("Origin")
	allowed := origin != "" && s.allowsOrigin(origin)
	if req.method == "OPTIONS" && origin != "" && req.get("Access-Control-Request-Method") != "" {
		if !allowed {
			return textReply(403, "This site doesn't accept requests from "+origin)
		}
		r := &Response{Status: 204}
		s.corsHeaders(r, origin)
		r.Headers = append(r.Headers,
			[2]string{"Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE"},
			[2]string{"Access-Control-Allow-Headers", "Content-Type, Authorization"},
			[2]string{"Access-Control-Max-Age", "600"})
		return r
	}
	// with sessions on, a browser on another site must not be able to act as
	// the signed-in visitor (cross-site request forgery)
	if s.session != nil && unsafeMethod(req.method) && !allowed && crossSite(req) {
		return textReply(403, "This request came from another site, so it was refused")
	}
	resp := s.route(in, req, u)
	if allowed {
		s.corsHeaders(resp, origin)
	}
	return resp
}

// crossSite reports whether a browser says the request came from another site.
func crossSite(req *incoming) bool {
	if req.get("Sec-Fetch-Site") == "cross-site" {
		return true
	}
	origin := req.get("Origin")
	if origin == "" || req.get("Host") == "" {
		return false
	}
	o, err := url.Parse(origin)
	return err != nil || o.Host != req.get("Host")
}

func (s *Server) allowsOrigin(origin string) bool {
	for _, o := range s.origins {
		if o == "*" || strings.EqualFold(o, origin) {
			return true
		}
	}
	return false
}

func (s *Server) corsHeaders(r *Response, origin string) {
	r.Headers = append(r.Headers, [2]string{"Access-Control-Allow-Origin", origin}, [2]string{"Vary", "Origin"})
}

func textReply(status int, body string) *Response {
	return &Response{Status: status, Body: body, ContentType: "text/plain; charset=utf-8"}
}

// route finds what answers the request: a route, a file, or the health check.
func (s *Server) route(in *Interp, req *incoming, u *url.URL) *Response {
	got := splitPath(u.Path)
	for _, r := range s.routes {
		if r.method != "ANY" && r.method != req.method && !(r.method == "GET" && req.method == "HEAD") {
			continue
		}
		if len(r.parts) != len(got) {
			continue
		}
		params := NewMap()
		ok := true
		for i, part := range r.parts {
			if strings.HasPrefix(part, ":") {
				v, err := url.PathUnescape(got[i])
				if err != nil {
					v = got[i]
				}
				params.Set(part[1:], v)
			} else if part != got[i] {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		request, sess := s.request(in, req, u, params)
		resp := s.run(in, r.pattern, request, r.handler)
		if sess != nil && sess.changed {
			resp.Headers = append(resp.Headers, [2]string{"Set-Cookie", s.session.cookie(sess)})
		}
		return resp
	}
	if req.method == "GET" || req.method == "HEAD" {
		for _, st := range s.statics {
			if r := serveStatic(in, st, u.Path); r != nil {
				return r
			}
		}
		if u.Path == "/health" {
			return &Response{Status: 200, Body: `{"ok":true}`, ContentType: "application/json"}
		}
	}
	return textReply(404, "Not found: "+u.Path)
}

// serveStatic answers with a file from a static folder, never from outside it.
func serveStatic(in *Interp, st staticFolder, p string) *Response {
	if !strings.HasPrefix(p, st.prefix) {
		return nil
	}
	rel := strings.TrimPrefix(p, st.prefix)
	if rel == "" || strings.HasSuffix(rel, "/") {
		rel += "index.html"
	}
	clean := path.Clean("/" + rel)
	for _, part := range strings.Split(clean, "/") {
		if strings.HasPrefix(part, ".") { // no .env, .git, or ..
			return nil
		}
	}
	data, err := in.readAsset(st.dir + clean)
	if err != nil {
		return nil
	}
	ctype := mime.TypeByExtension(path.Ext(clean))
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	return &Response{Status: 200, Body: data, ContentType: ctype, Headers: [][2]string{{"Cache-Control", "public, max-age=300"}}}
}

// request builds the request value a route sees.
func (s *Server) request(in *Interp, req *incoming, u *url.URL, params *Map) (*Map, *Session) {
	query := NewMap()
	keys := make([]string, 0, len(u.Query()))
	for k := range u.Query() {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		query.Set(k, u.Query().Get(k))
	}
	r := NewMap()
	r.Set("method", req.method)
	r.Set("path", u.Path)
	r.Set("params", params)
	r.Set("query", query)
	r.Set("body", req.body)
	r.Set("ip", req.ip)
	var parsed Value
	form, files := NewMap(), newList(nil)
	ctype, ctParams, _ := mime.ParseMediaType(req.get("Content-Type"))
	switch {
	case ctype == "application/x-www-form-urlencoded":
		if vals, err := url.ParseQuery(req.body); err == nil {
			fk := make([]string, 0, len(vals))
			for k := range vals {
				fk = append(fk, k)
			}
			sort.Strings(fk)
			for _, k := range fk {
				form.Set(k, vals.Get(k))
			}
		}
	case ctype == "multipart/form-data":
		readMultipart(req.body, ctParams["boundary"], form, files)
	case strings.TrimSpace(req.body) != "":
		if v, err := fromJSON(req.body); err == nil {
			parsed = v
		}
	}
	r.Set("json", parsed)
	r.Set("form", form)
	r.Set("files", files)
	r.Set("header", builtin("header", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "request.header", a, 1)
		if v := req.get(Text(a[0])); v != "" {
			return v
		}
		return nil
	}))
	cookies := parseCookies(req.get("Cookie"))
	r.Set("cookie", builtin("cookie", func(in *Interp, line int, a []Value) Value {
		wantArgs(in, line, "request.cookie", a, 1)
		if v, ok := cookies[Text(a[0])]; ok {
			return v
		}
		return nil
	}))
	var sess *Session
	if s.session != nil {
		sess = s.session.read(cookies[sessionCookie], time.Now())
		r.Set("session", sess)
	}
	return r, sess
}

func parseCookies(h string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(h, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || name == "" {
			continue
		}
		if v, err := url.QueryUnescape(value); err == nil {
			value = v
		}
		if _, dup := out[name]; !dup {
			out[name] = value
		}
	}
	return out
}

// Upload is a file sent in a form.
type Upload struct {
	Field, Name, Type string
	Data              string
}

func readMultipart(body, boundary string, form *Map, files *List) {
	if boundary == "" {
		return
	}
	mr := multipart.NewReader(strings.NewReader(body), boundary)
	for {
		part, err := mr.NextPart()
		if err != nil {
			return
		}
		var buf bytes.Buffer
		buf.ReadFrom(part)
		if part.FileName() == "" {
			form.Set(part.FormName(), buf.String())
			continue
		}
		files.Items = append(files.Items, &Upload{
			Field: part.FormName(),
			Name:  path.Base(strings.ReplaceAll(part.FileName(), "\\", "/")), // never a folder path
			Type:  part.Header.Get("Content-Type"),
			Data:  buf.String(),
		})
	}
}

func (in *Interp) uploadMember(u *Upload, name string, line int) Value {
	switch name {
	case "field":
		return u.Field
	case "name":
		return u.Name
	case "type":
		return u.Type
	case "size":
		return int64(len(u.Data))
	case "text":
		return u.Data
	case "saveAs":
		return builtin("saveAs", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "saveAs", a, 1)
			if err := writeFileAtomic(Text(a[0]), []byte(u.Data), 0o644); err != nil {
				in.fail(line, "I couldn't save the upload to %s: %v", Text(a[0]), err)
			}
			return nil
		})
	}
	in.fail(line, "an upload has no '%s'", name)
	return nil
}

// run calls a route's beam and turns what it sends into a reply.
func (s *Server) run(in *Interp, pattern string, req *Map, handler Value) (resp *Response) {
	defer func() {
		if x := recover(); x != nil {
			if re, ok := x.(*RuntimeError); ok && re.Status == 400 {
				// the visitor sent something that doesn't fit: tell them what (it's about their input)
				resp = &Response{Status: 400, Body: toJSON(mapOf("error", re.Msg)), ContentType: "application/json"}
				return
			}
			if re, ok := x.(*RuntimeError); ok {
				in.print("Problem while handling " + pattern + ": " + re.Describe())
				// details go to the server log only, never to the visitor
				resp = textReply(500, "Something went wrong")
				return
			}
			panic(x)
		}
	}()
	for _, b := range s.before {
		if out := in.call(b, []Value{req}, nil, 0, nil); out != nil {
			return toResponse(out)
		}
	}
	var out Value
	if b, ok := handler.(*Beam); ok && len(b.Params) == 0 {
		out = in.call(b, nil, nil, 0, nil) // a route that doesn't name its request
	} else {
		out = in.call(handler, []Value{req}, nil, 0, nil)
	}
	return toResponse(out)
}

// toResponse turns anything a route sends into a reply.
func toResponse(v Value) *Response {
	switch x := v.(type) {
	case *Response:
		return x
	case string:
		// plain text is never treated as a web page, so visitor input can't inject HTML;
		// use web.page(...) to send HTML on purpose.
		return textReply(200, x)
	case nil:
		return &Response{Status: 204}
	}
	return &Response{Status: 200, Body: toJSON(v), ContentType: "application/json"}
}

// secureHost says whether cookies should be marked Secure (sent only over
// https). Browsers treat localhost as secure, but some don't keep Secure
// cookies there, so local development is left plain.
func secureHost(req *incoming) bool {
	h := req.get("Host")
	host := h
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.HasSuffix(h, "]") {
		host = h[:i]
	}
	return h != "" && host != "localhost" && host != "127.0.0.1" && host != "[::1]"
}

func validCookieName(n string) bool {
	if n == "" || len(n) > 64 {
		return false
	}
	for _, c := range n {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

// cookieHeader writes a Set-Cookie value with safe settings: scripts can't read
// it and other sites can't send it with forms. The server adds Secure (https
// only) when it isn't running on localhost.
func cookieHeader(name, value string, maxAge int, secure bool) string {
	c := name + "=" + url.QueryEscape(value) + "; Path=/; HttpOnly; SameSite=Lax"
	if maxAge != 0 {
		c += fmt.Sprintf("; Max-Age=%d", maxAge)
	}
	if secure {
		c += "; Secure"
	}
	return c
}

// responseMember is reply.status, reply.header(...), reply.cookie(...) and the like.
func (in *Interp) responseMember(r *Response, name string, line int) Value {
	switch name {
	case "status":
		return box(int64(r.Status))
	case "body":
		return r.Body
	case "json":
		v, err := fromJSON(r.Body)
		if err != nil {
			in.fail(line, "this response isn't JSON")
		}
		return v
	case "header":
		return builtin("header", func(in *Interp, line int, a []Value) Value {
			if len(a) < 1 || len(a) > 2 {
				in.fail(line, "header takes a name (to read it) or a name and a value (to set it)")
			}
			n := Text(a[0])
			if len(a) == 1 { // read a header (in tests)
				for _, h := range r.Headers {
					if strings.EqualFold(h[0], n) {
						return h[1]
					}
				}
				if strings.EqualFold(n, "Content-Type") && r.ContentType != "" {
					return r.ContentType
				}
				return nil
			}
			v := Text(a[1])
			if strings.ContainsAny(n+v, "\r\n") || !validCookieName(n) {
				in.fail(line, "a header name must be letters, digits and dashes, and neither part may contain a line break")
			}
			if strings.EqualFold(n, "Content-Type") {
				r.ContentType = v
			} else {
				r.Headers = append(r.Headers, [2]string{n, v})
			}
			return r
		})
	case "cookie":
		return builtin("cookie", func(in *Interp, line int, a []Value) Value {
			if len(a) < 2 || len(a) > 3 {
				in.fail(line, "cookie takes a name, a value and optionally how long to keep it, like .cookie(\"theme\", \"dark\", days(30))")
			}
			n := Text(a[0])
			if !validCookieName(n) {
				in.fail(line, "a cookie name must be letters, digits, _ and -")
			}
			age := 0
			if len(a) == 3 {
				d, ok := a[2].(Duration)
				if !ok {
					in.fail(line, "how long to keep a cookie must be a Duration, like days(30)")
				}
				age = int(time.Duration(d).Seconds())
			}
			r.Headers = append(r.Headers, [2]string{"Set-Cookie", cookieHeader(n, Text(a[1]), age, false)})
			return r
		})
	case "forget":
		return builtin("forget", func(in *Interp, line int, a []Value) Value {
			wantArgs(in, line, "forget", a, 1)
			r.Headers = append(r.Headers, [2]string{"Set-Cookie", cookieHeader(Text(a[0]), "", -1, false)})
			return r
		})
	case "cookies":
		out := newList(nil)
		for _, h := range r.Headers {
			if h[0] == "Set-Cookie" {
				out.Items = append(out.Items, h[1])
			}
		}
		return out
	}
	in.fail(line, "a response has nothing called '%s'", name)
	return nil
}

// webReply is web.reply(value): any value as a reply, so headers and cookies can be added.
func webReply(in *Interp, line int, a []Value) Value {
	wantArgs(in, line, "web.reply", a, 1)
	r := toResponse(a[0])
	cp := *r // never change a reply that something else holds
	return &cp
}

// webRedirect is web.redirect("/login"): send the browser to another page of this site.
func webRedirect(in *Interp, line int, a []Value) Value {
	wantArgs(in, line, "web.redirect", a, 1)
	to := Text(a[0])
	// only paths on this site, so a link like ?next=https://evil.example can't send visitors away
	if !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") || strings.HasPrefix(to, "/\\") || strings.ContainsAny(to, "\r\n") {
		in.fail(line, "web.redirect only goes to a page on this site, like \"/login\", but got %q", to)
	}
	return &Response{Status: 303, ContentType: "text/plain; charset=utf-8", Body: "See " + to, Headers: [][2]string{{"Location", to}}}
}

// visitArgs builds a request for server.visit(path, method, body, headers).
func visitRequest(in *Interp, line int, a []Value) *incoming {
	if len(a) < 1 || len(a) > 4 {
		in.fail(line, "server.visit needs a path, and optionally a method, a body and headers, like server.visit(\"/todos\", \"POST\", {title: \"Milk\"})")
	}
	req := &incoming{method: "GET", uri: Text(a[0]), ip: "127.0.0.1", header: map[string]string{}}
	if len(a) >= 2 {
		req.method = strings.ToUpper(Text(a[1]))
	}
	if len(a) >= 3 && a[2] != nil {
		if t, ok := a[2].(string); ok {
			req.body = t
		} else {
			req.body = toJSON(a[2])
			req.header["content-type"] = "application/json"
		}
	}
	if len(a) == 4 {
		m, ok := a[3].(*Map)
		if !ok {
			in.fail(line, "the headers for server.visit must be a map, like {Cookie: \"session=abc\"}")
		}
		for _, k := range m.Keys {
			req.header[strings.ToLower(k)] = Text(m.M[k])
		}
	}
	return req
}

// readAsset reads a file named in the program (a template, a static file),
// found next to the program itself. p uses / between folders.
func (in *Interp) readAsset(p string) (string, error) {
	if in.ReadAsset != nil {
		return in.ReadAsset(p)
	}
	full := filepath.FromSlash(p)
	if in.Dir != "" && !filepath.IsAbs(full) {
		full = filepath.Join(in.Dir, full)
	}
	return readFile(full)
}

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
