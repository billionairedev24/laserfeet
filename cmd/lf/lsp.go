package main

// lf lsp: a language server for editors (VS Code and any other editor that
// speaks the Language Server Protocol). It reports lf check problems as you
// type, completes names, fields and keywords, and shows types on hover.
// It reads code and never runs it.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"laserfeet/lang"
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type document struct {
	text string
	last *lang.Analysis // the last analysis of code that parsed, for completion while typing
}

type server struct {
	out  io.Writer
	mu   sync.Mutex
	docs map[string]*document
}

// maxMessage stops a broken or hostile client from making the server read without limit.
const maxMessage = 32 << 20

func runLSP(in io.Reader, out io.Writer) int {
	s := &server{out: out, docs: map[string]*document{}}
	r := bufio.NewReader(in)
	for {
		msg, err := readMessage(r)
		if err == io.EOF {
			return 0
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "lf lsp:", err)
			return 1
		}
		if msg.Method == "exit" {
			return 0
		}
		s.handle(msg)
	}
}

func readMessage(r *bufio.Reader) (*rpcMessage, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			length, err = strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("bad Content-Length")
			}
		}
	}
	if length < 0 || length > maxMessage {
		return nil, fmt.Errorf("message length missing or too large")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var m rpcMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("bad message: %v", err)
	}
	return &m, nil
}

func (s *server) send(m any) {
	b, _ := json.Marshal(m)
	s.mu.Lock()
	defer s.mu.Unlock()
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (s *server) reply(id json.RawMessage, result any) {
	if result == nil {
		result = json.RawMessage("null")
	}
	s.send(rpcMessage{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *server) notify(method string, params any) {
	b, _ := json.Marshal(params)
	s.send(rpcMessage{JSONRPC: "2.0", Method: method, Params: b})
}

func (s *server) handle(m *rpcMessage) {
	switch m.Method {
	case "initialize":
		s.reply(m.ID, map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync":           1, // the editor sends the whole file on each change
				"completionProvider":         map[string]any{"triggerCharacters": []string{"."}},
				"hoverProvider":              true,
				"documentFormattingProvider": true, // lf fmt, for format on save
			},
			"serverInfo": map[string]any{"name": "Laser Feet", "version": version},
		})
	case "shutdown":
		s.reply(m.ID, nil)
	case "textDocument/didOpen":
		var p struct {
			TextDocument struct {
				URI  string `json:"uri"`
				Text string `json:"text"`
			} `json:"textDocument"`
		}
		json.Unmarshal(m.Params, &p)
		s.update(p.TextDocument.URI, p.TextDocument.Text)
	case "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
			ContentChanges []struct {
				Text string `json:"text"`
			} `json:"contentChanges"`
		}
		json.Unmarshal(m.Params, &p)
		if n := len(p.ContentChanges); n > 0 {
			s.update(p.TextDocument.URI, p.ContentChanges[n-1].Text)
		}
	case "textDocument/didSave":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(m.Params, &p)
		if d := s.docs[p.TextDocument.URI]; d != nil {
			s.update(p.TextDocument.URI, d.text) // other files it uses may have changed
		}
	case "textDocument/didClose":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		json.Unmarshal(m.Params, &p)
		delete(s.docs, p.TextDocument.URI)
		s.notify("textDocument/publishDiagnostics", map[string]any{"uri": p.TextDocument.URI, "diagnostics": []any{}})
	case "textDocument/completion":
		uri, pos := docPosition(m.Params)
		s.reply(m.ID, s.complete(uri, pos))
	case "textDocument/hover":
		uri, pos := docPosition(m.Params)
		s.reply(m.ID, s.hover(uri, pos))
	case "textDocument/formatting":
		uri, _ := docPosition(m.Params)
		s.reply(m.ID, s.format(uri))
	default:
		if len(m.ID) > 0 && m.Method != "" {
			s.send(rpcMessage{JSONRPC: "2.0", ID: m.ID, Error: &rpcError{Code: -32601, Message: "not supported: " + m.Method}})
		}
	}
}

func docPosition(params json.RawMessage) (string, position) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position position `json:"position"`
	}
	json.Unmarshal(params, &p)
	return p.TextDocument.URI, p.Position
}

// fileReader lets 'use "file.lf"' read files next to the document being edited.
func fileReader(uri string) func(string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return nil
	}
	p := u.Path
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
	}
	dir := filepath.Dir(filepath.FromSlash(p))
	return func(name string) (string, error) {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		return string(b), err
	}
}

func (s *server) update(uri, text string) {
	d := s.docs[uri]
	if d == nil {
		d = &document{}
		s.docs[uri] = d
	}
	d.text = text
	read := fileReader(uri)
	a := lang.Analyze(text, read)
	if len(a.Facts) > 0 || len(a.Globals) > 0 {
		d.last = a
	} else if repaired, blanked := analyzeWhileTyping(text, a, read); repaired != nil {
		d.last = repaired
		// keep the syntax problem, and also show type problems on the other lines
		for _, p := range repaired.Problems {
			if !blanked[p.Line] {
				a.Problems = append(a.Problems, p)
			}
		}
	}
	lines := strings.Split(text, "\n")
	diags := []any{}
	for _, p := range a.Problems {
		line := p.Line - 1
		msg := p.Msg
		if p.File != "" { // a problem inside a file this one uses
			msg = p.File + ", line " + strconv.Itoa(p.Line) + ": " + msg
			line = 0
		}
		if line < 0 {
			line = 0
		}
		start, end := 0, 0
		if line < len(lines) {
			l := strings.TrimRight(lines[line], "\r")
			start = len(l) - len(strings.TrimLeft(l, " \t"))
			end = len([]rune(l))
		}
		diags = append(diags, map[string]any{
			"range":    lspRange{position{line, start}, position{line, end}},
			"severity": 1,
			"source":   "lf check",
			"message":  msg,
		})
	}
	s.notify("textDocument/publishDiagnostics", map[string]any{"uri": uri, "diagnostics": diags})
}

var wordBefore = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\.([A-Za-z_][A-Za-z0-9_]*)?$`)

const (
	kindMethod   = 2
	kindFunction = 3
	kindField    = 5
	kindVariable = 6
	kindClass    = 7
	kindModule   = 9
	kindKeyword  = 14
	kindSnippet  = 15
)

func (s *server) complete(uri string, pos position) any {
	d := s.docs[uri]
	items := []any{}
	if d == nil {
		return items
	}
	lines := strings.Split(d.text, "\n")
	if pos.Line >= len(lines) {
		return items
	}
	before := []rune(lines[pos.Line])
	if pos.Character < len(before) {
		before = before[:pos.Character]
	}
	line := pos.Line + 1
	if m := wordBefore.FindStringSubmatch(string(before)); m != nil {
		t := d.last.TypeOf(m[1], line)
		if m[1] == "request" && t == nil {
			t = &lang.Type{Kind: lang.KLibrary, Name: "Request"}
		}
		for _, mem := range lang.MembersOf(t) {
			kind := kindField
			if mem.Beam {
				kind = kindMethod
			}
			items = append(items, map[string]any{"label": mem.Name, "kind": kind, "detail": mem.Detail})
		}
		return items
	}
	for name, t := range d.last.NamesAt(line) {
		kind := kindVariable
		switch t.Kind {
		case lang.KBeam:
			kind = kindFunction
		case lang.KClassRef:
			kind = kindClass
		case lang.KModule:
			kind = kindModule
		}
		items = append(items, map[string]any{"label": name, "kind": kind, "detail": t.Label()})
	}
	for _, k := range lang.Keywords() {
		items = append(items, map[string]any{"label": k, "kind": kindKeyword})
	}
	for _, b := range lang.BuiltinNames() {
		items = append(items, map[string]any{"label": b, "kind": kindFunction, "detail": "built in"})
	}
	for _, t := range []string{"Int", "Float", "Decimal", "Text", "Flag", "Json", "List", "Map", "Beam"} {
		items = append(items, map[string]any{"label": t, "kind": kindClass, "detail": "type"})
	}
	return items
}

func (s *server) hover(uri string, pos position) any {
	d := s.docs[uri]
	if d == nil || d.last == nil {
		return nil
	}
	lines := strings.Split(d.text, "\n")
	if pos.Line >= len(lines) {
		return nil
	}
	rs := []rune(lines[pos.Line])
	isWord := func(r rune) bool {
		return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
	}
	a, b := pos.Character, pos.Character
	for a > 0 && a <= len(rs) && isWord(rs[a-1]) {
		a--
	}
	for b < len(rs) && isWord(rs[b]) {
		b++
	}
	if a >= b {
		return nil
	}
	word := string(rs[a:b])
	t := d.last.TypeOf(word, pos.Line+1)
	if t == nil {
		return nil
	}
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": "```laserfeet\n" + word + ": " + t.Label() + "\n```"},
		"range":    lspRange{position{pos.Line, a}, position{pos.Line, b}},
	}
}

// analyzeWhileTyping handles code that doesn't parse yet, like a line that ends in
// "ada." while you're typing: it sets the broken lines aside and analyzes the rest,
// so hover and completion keep working. The real problems are still reported.
func analyzeWhileTyping(text string, a *lang.Analysis, read func(string) (string, error)) (*lang.Analysis, map[int]bool) {
	lines := strings.Split(text, "\n")
	blanked := map[int]bool{}
	for tries := 0; tries < 5 && len(a.Problems) > 0; tries++ {
		bad := a.Problems[0].Line - 1
		if bad < 0 || bad >= len(lines) {
			return nil, nil
		}
		lines[bad] = ""
		blanked[bad+1] = true
		a = lang.Analyze(strings.Join(lines, "\n"), read)
		if len(a.Facts) > 0 || len(a.Globals) > 0 {
			return a, blanked
		}
	}
	return nil, nil
}

// format replaces the whole document with its lf fmt layout. Code that doesn't
// read (a missing quote, say) is left as it is.
func (s *server) format(uri string) any {
	d := s.docs[uri]
	if d == nil {
		return []any{}
	}
	out, err := lang.Format(d.text)
	if err != nil || out == d.text {
		return []any{}
	}
	lines := strings.Count(d.text, "\n") + 1
	return []any{map[string]any{
		"range": map[string]any{
			"start": map[string]int{"line": 0, "character": 0},
			"end":   map[string]int{"line": lines, "character": 0},
		},
		"newText": out,
	}}
}
