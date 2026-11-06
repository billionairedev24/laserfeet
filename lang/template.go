package lang

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"sync"
)

// Templates: web.render("views/products.html", {products: list}).
//
//	<h1>{title}</h1>                    the value, made safe for HTML
//	{each p in products} ... {end}      once per item
//	{when user is nothing} ... {otherwise} ... {end}
//	{use "views/header.html"}           another template, with the same values
//
// Every value is escaped, so text from visitors can't add HTML or scripts.
// web.trusted(html) marks HTML the program made itself. Braces that don't
// hold an expression (CSS, JSON) stay as they are; \{ is always a brace.

// Trusted is HTML that a template writes as it is: web.trusted("<b>hi</b>").
type Trusted string

type tnode interface{}

type ttext string

type texpr struct {
	line int
	x    Expr
}

type teach struct {
	line int
	name string
	list Expr
	body []tnode
}

type twhen struct {
	conds  []Expr // nil for the final otherwise
	bodies [][]tnode
}

type tuse struct {
	line int
	path string
}

// TemplateError is a mistake in a template, with the template's own line number.
type TemplateError struct {
	Path string
	Line int
	Msg  string
}

func (e *TemplateError) Error() string { return fmt.Sprintf("%s, line %d: %s", e.Path, e.Line, e.Msg) }

type tparser struct {
	path string
	src  string
	pos  int
	line int
}

// parseTemplate reads a template into its parts.
func parseTemplate(path, src string) (nodes []tnode, err error) {
	p := &tparser{path: path, src: src, line: 1}
	defer func() {
		if r := recover(); r != nil {
			if te, ok := r.(*TemplateError); ok {
				nodes, err = nil, te
				return
			}
			panic(r)
		}
	}()
	nodes, end := p.nodes()
	if end != "" {
		p.fail("'{%s}' here has nothing to finish", end)
	}
	return nodes, nil
}

func (p *tparser) fail(format string, a ...any) {
	panic(&TemplateError{p.path, p.line, fmt.Sprintf(format, a...)})
}

// nodes reads until the end of the template or a {end} / {otherwise...} tag,
// which it returns (without reading past it).
func (p *tparser) nodes() ([]tnode, string) {
	var out []tnode
	var text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			out = append(out, ttext(text.String()))
			text.Reset()
		}
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\\' && p.pos+1 < len(p.src) && (p.src[p.pos+1] == '{' || p.src[p.pos+1] == '}') {
			text.WriteByte(p.src[p.pos+1])
			p.pos += 2
			continue
		}
		if c != '{' {
			if c == '\n' {
				p.line++
			}
			text.WriteByte(c)
			p.pos++
			continue
		}
		end := matchingBrace(p.src, p.pos)
		if end < 0 {
			text.WriteByte(c)
			p.pos++
			continue
		}
		inner := strings.TrimSpace(p.src[p.pos+1 : end])
		word, rest, _ := strings.Cut(inner, " ")
		rest = strings.TrimSpace(rest)
		line := p.line
		if word == "each" || word == "when" || word == "otherwise" || word == "use" || inner == "end" {
			// a tag alone on its line leaves no blank line behind
			if t := text.String(); lineStartsAt(t) {
				trimmed := strings.TrimRight(t, " \t")
				text.Reset()
				text.WriteString(trimmed)
				if after := standaloneEnd(p.src, end+1); after > 0 {
					p.line += strings.Count(p.src[end+1:after], "\n")
					p.src = p.src[:end+1] + p.src[after:] // drop the rest of the line
				}
			}
		}
		advance := func() {
			p.line += strings.Count(p.src[p.pos:end+1], "\n")
			p.pos = end + 1
		}
		switch {
		case inner == "end" || inner == "otherwise" || word == "otherwise" && strings.HasPrefix(rest, "when "):
			flush()
			return out, inner // the caller reads past it
		case word == "each" && strings.Contains(rest, " in "):
			flush()
			advance()
			name, listSrc, _ := strings.Cut(rest, " in ")
			name = strings.TrimSpace(name)
			if !isName(name) {
				p.line = line
				p.fail("write {each item in list}, with a name for each item")
			}
			list := p.expr(listSrc, line)
			body, closer := p.nodes()
			if closer != "end" {
				p.line = line
				p.fail("{each %s in ...} needs an {end}", name)
			}
			p.skipTag()
			out = append(out, &teach{line, name, list, body})
		case word == "when" && rest != "":
			flush()
			advance()
			w := &twhen{conds: []Expr{p.expr(rest, line)}}
			for {
				body, closer := p.nodes()
				w.bodies = append(w.bodies, body)
				switch {
				case closer == "end":
					p.skipTag()
					out = append(out, w)
				case closer == "otherwise":
					if w.conds[len(w.conds)-1] == nil {
						p.fail("{otherwise} can only come once, at the end")
					}
					p.skipTag()
					w.conds = append(w.conds, nil)
					continue
				case strings.HasPrefix(closer, "otherwise when "):
					if w.conds[len(w.conds)-1] == nil {
						p.fail("{otherwise when ...} can't come after {otherwise}")
					}
					l := p.line
					p.skipTag()
					w.conds = append(w.conds, p.expr(strings.TrimPrefix(closer, "otherwise when "), l))
					continue
				default:
					p.line = line
					p.fail("{when ...} needs an {end}")
				}
				break
			}
		case word == "use" && strings.HasPrefix(rest, "\""):
			flush()
			advance()
			path := strings.Trim(rest, "\"")
			out = append(out, &tuse{line, path})
		default:
			x, ok := tryExpr(inner)
			if !ok { // CSS, JSON and other braces stay as written
				text.WriteByte('{')
				p.pos++
				continue
			}
			flush()
			advance()
			out = append(out, &texpr{line, x})
		}
	}
	flush()
	return out, ""
}

// skipTag moves past the {end} or {otherwise ...} that nodes stopped at.
func (p *tparser) skipTag() {
	end := matchingBrace(p.src, p.pos)
	p.line += strings.Count(p.src[p.pos:end+1], "\n")
	p.pos = end + 1
}

func (p *tparser) expr(src string, line int) Expr {
	x, ok := tryExpr(strings.TrimSpace(src))
	if !ok {
		p.line = line
		p.fail("I can't read '%s' as a Laser Feet expression", strings.TrimSpace(src))
	}
	return x
}

// matchingBrace finds the } that closes the { at i, skipping text in quotes.
func matchingBrace(s string, i int) int {
	depth, inQuote := 0, false
	for j := i; j < len(s); j++ {
		switch c := s[j]; {
		case inQuote && c == '\\':
			j++
		case c == '"':
			inQuote = !inQuote
		case inQuote:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return j
			}
		case c == '\n' && depth == 1 && j-i > 200:
			return -1 // a long block over several lines is CSS or code, not a value
		}
	}
	return -1
}

var nameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func isName(s string) bool { return nameRe.MatchString(s) && !keywords[s] }

// tryExpr reads text as one Laser Feet expression.
func tryExpr(src string) (x Expr, ok bool) {
	if src == "" {
		return nil, false
	}
	toks, err := Lex(src, 1)
	if err != nil {
		return nil, false
	}
	defer func() {
		if r := recover(); r != nil {
			x, ok = nil, false
		}
	}()
	sub := &parser{toks: toks, src: []rune(src), grouped: map[Expr]bool{}}
	x = sub.expr()
	sub.skipNewlines()
	if sub.peek().Type != EOF {
		return nil, false
	}
	return x, true
}

// templateCache keeps compiled templates, keyed by their text.
var templateCache sync.Map

func (in *Interp) loadTemplate(line int, path string) []tnode {
	src, err := in.readAsset(path)
	if err != nil {
		in.fail(line, "I couldn't read the template %s", path)
	}
	if n, ok := templateCache.Load(path + "\x00" + src); ok {
		return n.([]tnode)
	}
	nodes, err := parseTemplate(path, src)
	if err != nil {
		in.fail(line, "%v", err)
	}
	templateCache.Store(path+"\x00"+src, nodes)
	return nodes
}

// render fills in a template with values.
func (in *Interp) render(line int, path string, values Value, depth int) string {
	if depth > 20 {
		in.fail(line, "templates use each other in a loop (at %s)", path)
	}
	env := newEnv(in.globals)
	env.isScope = true
	switch v := values.(type) {
	case *Map:
		for _, k := range v.Keys {
			env.define(k, v.M[k], false)
		}
	case *Instance:
		for i, f := range v.Class.Fields {
			if !f.Private {
				env.define(f.Name, v.Slots[i], false)
			}
		}
	case nil:
	default:
		in.fail(line, "a template's values must be a map, like {title: \"Shop\"}, or an object")
	}
	var b strings.Builder
	in.renderNodes(&b, in.loadTemplate(line, path), env, path, values, depth)
	return b.String()
}

func (in *Interp) renderNodes(b *strings.Builder, nodes []tnode, env *Env, path string, values Value, depth int) {
	evalAt := func(line int, x Expr) (v Value) {
		defer func() {
			if r := recover(); r != nil {
				if re, ok := r.(*RuntimeError); ok {
					panic(&RuntimeError{Line: re.Line, Msg: fmt.Sprintf("%s, line %d: %s", path, line, re.Msg)})
				}
				panic(r)
			}
		}()
		return in.eval(x, env)
	}
	for _, n := range nodes {
		switch x := n.(type) {
		case ttext:
			b.WriteString(string(x))
		case *texpr:
			v := evalAt(x.line, x.x)
			if t, ok := v.(Trusted); ok {
				b.WriteString(string(t))
				continue
			}
			s := toText(v, false)
			if inLinkAttribute(b.String()) && unsafeLink(s) {
				s = "#unsafe-link"
			}
			b.WriteString(html.EscapeString(s))
		case *teach:
			list := evalAt(x.line, x.list)
			var items []Value
			switch l := list.(type) {
			case *List:
				items = l.Items
			case nil:
			default:
				panic(&RuntimeError{Line: 0, Msg: fmt.Sprintf("%s, line %d: {each} needs a list, but got %s", path, x.line, typeName(list))})
			}
			for _, item := range items {
				e := newEnv(env)
				e.define(x.name, item, false)
				in.renderNodes(b, x.body, e, path, values, depth)
			}
		case *twhen:
			for i, c := range x.conds {
				if c != nil {
					v := evalAt(0, c)
					f, ok := v.(bool)
					if !ok {
						panic(&RuntimeError{Msg: fmt.Sprintf("%s: {when} needs yes or no, but got %s", path, typeName(v))})
					}
					if !f {
						continue
					}
				}
				in.renderNodes(b, x.bodies[i], env, path, values, depth)
				break
			}
		case *tuse:
			b.WriteString(in.render(x.line, x.path, values, depth+1))
		}
	}
}

var linkAttr = regexp.MustCompile(`(?i)\b(href|src|action|formaction|poster|xlink:href)\s*=\s*["']?\s*$`)

// inLinkAttribute reports whether the next value goes inside an attribute that
// holds a link, where javascript: links could run code.
func inLinkAttribute(written string) bool {
	if len(written) > 64 {
		written = written[len(written)-64:]
	}
	return linkAttr.MatchString(written)
}

func unsafeLink(s string) bool {
	l := strings.ToLower(strings.Map(func(r rune) rune {
		if r <= ' ' {
			return -1 // browsers ignore spaces and control characters here
		}
		return r
	}, s))
	return strings.HasPrefix(l, "javascript:") || strings.HasPrefix(l, "vbscript:") || strings.HasPrefix(l, "data:text/html")
}

// lineStartsAt reports whether only spaces follow the last line break of t
// (or t has no line break and is only spaces): a tag here starts its line.
func lineStartsAt(t string) bool {
	tail := t[strings.LastIndex(t, "\n")+1:]
	return strings.TrimLeft(tail, " \t") == ""
}

// standaloneEnd returns where the next line starts if only spaces follow i on
// its line, or 0.
func standaloneEnd(s string, i int) int {
	j := i
	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\r') {
		j++
	}
	if j == len(s) {
		return j
	}
	if s[j] == '\n' {
		return j + 1
	}
	return 0
}
