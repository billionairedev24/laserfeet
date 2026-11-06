package main

// Moving around code in an editor: go to definition, find references, rename,
// and the outline of a file. All of it comes from lf check, which records
// where each name is made and where it is used, so a name means exactly what
// it means to the language (a beam's input called x is not the x outside it).

import (
	"encoding/json"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"laserfeet/lang"
)

// wordAt finds the name under the cursor: its text and where it starts and ends.
func wordAt(text string, pos position) (string, int, int) {
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return "", 0, 0
	}
	rs := []rune(strings.TrimRight(lines[pos.Line], "\r"))
	isWord := func(r rune) bool {
		return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	a, b := pos.Character, pos.Character
	if a > len(rs) {
		return "", 0, 0
	}
	for a > 0 && isWord(rs[a-1]) {
		a--
	}
	for b < len(rs) && isWord(rs[b]) {
		b++
	}
	if a >= b || rs[a] >= '0' && rs[a] <= '9' {
		return "", 0, 0
	}
	return string(rs[a:b]), a, b
}

// nameColumns finds where a name appears as a name (not inside text or a
// comment) on a line of source: the start of each appearance, in characters.
// member picks the appearances written after a dot (p.x), or the others.
func nameColumns(src string, line int, name string, member bool) []int {
	toks, err := lang.Lex(src, 1)
	if err != nil {
		return nil
	}
	// where each line starts, counted in characters, like the tokens
	starts := []int{0}
	n := 0
	for _, r := range src {
		n++
		if r == '\n' {
			starts = append(starts, n)
		}
	}
	var out []int
	for i, t := range toks {
		dotted := i > 0 && toks[i-1].Type == lang.SYMBOL && toks[i-1].Text == "."
		if t.Line == line && t.Type == lang.IDENT && t.Text == name && dotted == member && line-1 < len(starts) {
			out = append(out, t.Start-starts[line-1])
		}
	}
	return out
}

// definitionOf finds what the name under the cursor means.
func (s *server) definitionOf(uri string, pos position) (lang.Place, string, bool) {
	d := s.docs[uri]
	if d == nil || d.last == nil {
		return lang.Place{}, "", false
	}
	word, a, _ := wordAt(d.text, pos)
	if word == "" {
		return lang.Place{}, "", false
	}
	dotted := false
	if line := []rune(strings.Split(d.text, "\n")[pos.Line]); a > 0 && line[a-1] == '.' {
		dotted = true
	}
	for _, r := range d.last.Refs {
		if r.Line == pos.Line+1 && r.Name == word && r.Member == dotted {
			return r.Def, word, true
		}
	}
	return lang.Place{}, word, false
}

// uriFor turns a place into the address of its file.
func uriFor(docURI string, p lang.Place) (string, string, bool) {
	if p.File == "" {
		return docURI, "", true
	}
	u, err := url.Parse(docURI)
	if err != nil || u.Scheme != "file" {
		return "", "", false
	}
	full := path.Join(path.Dir(u.Path), p.File)
	src, err := os.ReadFile(filepath.FromSlash(fromURIPath(full)))
	if err != nil {
		return "", "", false
	}
	return (&url.URL{Scheme: "file", Path: full}).String(), string(src), true
}

func fromURIPath(p string) string {
	if len(p) > 2 && p[0] == '/' && p[2] == ':' { // /C:/... on Windows
		return p[1:]
	}
	return p
}

func (s *server) definition(uri string, pos position) any {
	def, word, ok := s.definitionOf(uri, pos)
	if !ok {
		return nil
	}
	target, src, ok := uriFor(uri, def)
	if !ok {
		return nil
	}
	if def.File == "" {
		src = s.docs[uri].text
	}
	col, end := 0, 0
	if cols := nameColumns(src, def.Line, word, false); len(cols) > 0 {
		col, end = cols[0], cols[0]+len([]rune(word))
	}
	return map[string]any{
		"uri":   target,
		"range": lspRange{position{def.Line - 1, col}, position{def.Line - 1, end}},
	}
}

// occurrences lists every place in this file where the name under the cursor
// means the same thing.
func (s *server) occurrences(uri string, pos position) ([]lspRange, lang.Place, bool) {
	def, word, ok := s.definitionOf(uri, pos)
	if !ok {
		return nil, def, false
	}
	d := s.docs[uri]
	type key struct {
		line   int
		member bool
	}
	seen := map[key]bool{}
	var out []lspRange
	for _, r := range d.last.Refs {
		k := key{r.Line, r.Member}
		if r.Name != word || r.Def != def || seen[k] {
			continue
		}
		seen[k] = true
		for _, col := range nameColumns(d.text, r.Line, word, r.Member) {
			out = append(out, lspRange{position{r.Line - 1, col}, position{r.Line - 1, col + len([]rune(word))}})
		}
	}
	return out, def, true
}

func (s *server) references(uri string, pos position) any {
	ranges, _, ok := s.occurrences(uri, pos)
	out := []any{}
	if !ok {
		return out
	}
	for _, r := range ranges {
		out = append(out, map[string]any{"uri": uri, "range": r})
	}
	return out
}

// rename changes a name everywhere it means the same thing. Names made in
// another file are renamed there, so this refuses them.
func (s *server) rename(id json.RawMessage, uri string, pos position, newName string) {
	ranges, def, ok := s.occurrences(uri, pos)
	switch {
	case !ok:
		s.fail(id, "there's no name here to rename")
	case def.File != "":
		s.fail(id, "this name is made in "+def.File+": rename it there")
	case !lang.IsName(newName):
		s.fail(id, "'"+newName+"' can't be a name: names start with a letter, have only letters, digits and _, and aren't keywords")
	default:
		edits := []any{}
		for _, r := range ranges {
			edits = append(edits, map[string]any{"range": r, "newText": newName})
		}
		s.reply(id, map[string]any{"changes": map[string]any{uri: edits}})
	}
}

func (s *server) fail(id json.RawMessage, msg string) {
	s.send(rpcMessage{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32803, Message: msg}})
}

const (
	symClass    = 5
	symMethod   = 6
	symField    = 8
	symEnum     = 10
	symFunction = 12
	symVariable = 13
	symEnumItem = 22
)

// symbols is the outline of a file: its objects (with their fields and
// beams), choices, rules, beams, routes and top-level names.
func (s *server) symbols(uri string) any {
	out := []any{}
	d := s.docs[uri]
	if d == nil {
		return out
	}
	prog, err := lang.Parse(d.text)
	if err != nil {
		return out
	}
	lines := strings.Split(d.text, "\n")
	span := func(line int) lspRange {
		l := line - 1
		if l < 0 || l >= len(lines) {
			return lspRange{}
		}
		return lspRange{position{l, 0}, position{l, len([]rune(strings.TrimRight(lines[l], "\r")))}}
	}
	sym := func(name string, kind, line int, detail string, children []any) map[string]any {
		r := span(line)
		sel := r
		if cols := nameColumns(d.text, line, name, false); len(cols) > 0 {
			sel = lspRange{position{line - 1, cols[0]}, position{line - 1, cols[0] + len([]rune(name))}}
		}
		m := map[string]any{"name": name, "kind": kind, "range": r, "selectionRange": sel}
		if detail != "" {
			m["detail"] = detail
		}
		if len(children) > 0 {
			m["children"] = children
		}
		return m
	}
	seen := map[string]bool{}
	for _, st := range prog.Stmts {
		switch x := st.(type) {
		case *lang.ObjectDecl:
			var kids []any
			for _, f := range x.Fields {
				kids = append(kids, sym(f.Name, symField, f.Line, "", nil))
			}
			for _, m := range x.Methods {
				kids = append(kids, sym(m.Name, symMethod, m.Line, "beam", nil))
			}
			out = append(out, sym(x.Name, symClass, x.Line, "object", kids))
		case *lang.ChoiceDecl:
			var kids []any
			for _, o := range x.Options {
				kids = append(kids, sym(o.Name, symEnumItem, o.Line, "", nil))
			}
			out = append(out, sym(x.Name, symEnum, x.Line, "choice", kids))
		case *lang.RuleDecl:
			out = append(out, sym(x.Name, symClass, x.Line, "rule", nil))
		case *lang.BeamDecl:
			out = append(out, sym(x.Name, symFunction, x.Line, "beam", nil))
		case *lang.AssignStmt:
			if id, ok := x.Target.(*lang.Ident); ok && !seen[id.Name] {
				seen[id.Name] = true
				out = append(out, sym(id.Name, symVariable, x.Line, "", nil))
			}
		case *lang.ExprStmt:
			if method, path, line, ok := routeOf(x); ok {
				m := sym(method+" "+path, symFunction, line, "route", nil)
				m["selectionRange"] = m["range"]
				out = append(out, m)
			}
		}
	}
	return out
}

// routeOf recognises server.get("/path") { ... } and the like.
func routeOf(x *lang.ExprStmt) (string, string, int, bool) {
	c, ok := x.X.(*lang.Call)
	if !ok || len(c.Args) == 0 {
		return "", "", 0, false
	}
	g, ok := c.Callee.(*lang.Get)
	if !ok {
		return "", "", 0, false
	}
	switch g.Name {
	case "get", "post", "put", "patch", "delete", "on":
	default:
		return "", "", 0, false
	}
	p, ok := c.Args[0].Value.(*lang.TextLit)
	if !ok {
		return "", "", 0, false
	}
	method := strings.ToUpper(g.Name)
	if method == "ON" {
		method = "ANY"
	}
	return method, p.Value, c.Line, true
}
