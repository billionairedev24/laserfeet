package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

type client struct {
	t   *testing.T
	in  *io.PipeWriter
	out *bufio.Reader
	id  int
}

func startServer(t *testing.T) *client {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	go runLSP(inR, outW)
	return &client{t: t, in: inW, out: bufio.NewReader(outR)}
}

func (c *client) write(method string, id int, params any) {
	m := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	if id > 0 {
		m["id"] = id
	}
	b, _ := json.Marshal(m)
	fmt.Fprintf(c.in, "Content-Length: %d\r\n\r\n%s", len(b), b)
}

func (c *client) read() map[string]any {
	done := make(chan *rpcMessage, 1)
	go func() {
		m, err := readMessage(c.out)
		if err != nil {
			c.t.Error(err)
		}
		done <- m
	}()
	select {
	case m := <-done:
		b, _ := json.Marshal(m)
		var out map[string]any
		json.Unmarshal(b, &out)
		return out
	case <-time.After(5 * time.Second):
		c.t.Fatal("the language server didn't answer")
	}
	return nil
}

const lspDoc = `object User {
    name: Text
    age: Int
}

launch {
    ada = User("Ada", 36)
    show ada.nmae
    show ada.
}
`

func TestLanguageServer(t *testing.T) {
	c := startServer(t)
	c.write("initialize", 1, map[string]any{})
	init := c.read()
	if !strings.Contains(fmt.Sprint(init["result"]), "completionProvider") {
		t.Fatalf("initialize: %v", init)
	}
	uri := "file:///tmp/app.lf"
	c.write("textDocument/didOpen", 0, map[string]any{"textDocument": map[string]any{"uri": uri, "text": lspDoc}})
	diag := c.read()
	if diag["method"] != "textDocument/publishDiagnostics" || !strings.Contains(fmt.Sprint(diag["params"]), "Did you mean 'name'?") {
		t.Errorf("expected a live error for ada.nmae, got %v", diag)
	}

	// completion right after "ada." lists the fields of User
	c.write("textDocument/completion", 2, map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 8, "character": 13}})
	comp := fmt.Sprint(c.read()["result"])
	for _, want := range []string{"label:name", "label:age"} {
		if !strings.Contains(comp, want) {
			t.Errorf("completion is missing %s: %s", want, comp)
		}
	}

	// hover over "ada" shows its type
	c.write("textDocument/hover", 3, map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 6, "character": 5}})
	hover := fmt.Sprint(c.read()["result"])
	if !strings.Contains(hover, "ada: User") {
		t.Errorf("hover should show ada: User, got %s", hover)
	}

	// fixing the mistake clears the error
	fixed := strings.Replace(strings.Replace(lspDoc, "ada.nmae", "ada.name", 1), "    show ada.\n", "", 1)
	c.write("textDocument/didChange", 0, map[string]any{"textDocument": map[string]any{"uri": uri}, "contentChanges": []any{map[string]any{"text": fixed}}})
	if d := c.read(); !strings.Contains(fmt.Sprint(d["params"]), "diagnostics:[]") {
		t.Errorf("expected no problems after the fix, got %v", d)
	}
	// format (format on save) gives the lf fmt layout
	c.write("textDocument/didChange", 0, map[string]any{"textDocument": map[string]any{"uri": uri}, "contentChanges": []any{map[string]any{"text": "launch {\nshow 1+2\n}\n"}}})
	c.read()
	c.write("textDocument/formatting", 5, map[string]any{"textDocument": map[string]any{"uri": uri}})
	if f := fmt.Sprint(c.read()["result"]); !strings.Contains(f, "show 1 + 2") {
		t.Errorf("formatting should lay out the code, got %s", f)
	}
	c.write("shutdown", 4, nil)
	c.read()
	c.write("exit", 0, nil)
}

func TestLanguageServerRefusesHugeMessages(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("Content-Length: 999999999999\r\n\r\n"))
	if _, err := readMessage(r); err == nil {
		t.Error("a message claiming to be enormous must be refused")
	}
}

const navDoc = `use "helpers.lf"

object Point {
    x: Int
    y: Int
}

beam total(p: Point, x: Int) gives Int {
    x = p.x + p.y + x
    send x
}

x = 10

server.get("/hi") {
    send "hi"
}

launch {
    p = Point(1, 2)
    show total(p, 1) + x
    show helpers.twice(x)
}
`

func TestLanguageServerNavigation(t *testing.T) {
	dir := t.TempDir()
	write(t, dir+"/helpers.lf", "-- helpers\n\nbeam twice(n: Int) gives Int {\n    send n * 2\n}\n")
	c := startServer(t)
	c.write("initialize", 1, map[string]any{})
	c.read()
	uri := "file://" + dir + "/app.lf"
	c.write("textDocument/didOpen", 0, map[string]any{"textDocument": map[string]any{"uri": uri, "text": navDoc}})
	c.read() // problems (server isn't defined; that's fine here)
	at := func(line, char int) map[string]any {
		return map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char}}
	}
	ask := func(id int, method string, params map[string]any) any {
		c.write(method, id, params)
		r := c.read()
		if r["error"] != nil {
			return r["error"]
		}
		return r["result"]
	}

	// total(p) on line 21 goes to beam total on line 8
	def := ask(2, "textDocument/definition", at(20, 10)).(map[string]any)
	if fmt.Sprint(def["range"]) != "map[end:map[character:10 line:7] start:map[character:5 line:7]]" {
		t.Errorf("definition of total: %v", def)
	}
	// p.x goes to the field x of Point, not the name x
	def = ask(3, "textDocument/definition", at(8, 11)).(map[string]any)
	if fmt.Sprint(def["range"]) != "map[end:map[character:5 line:3] start:map[character:4 line:3]]" {
		t.Errorf("definition of p.x: %v", def)
	}
	// helpers.twice goes into helpers.lf
	def = ask(4, "textDocument/definition", at(21, 18)).(map[string]any)
	if !strings.HasSuffix(fmt.Sprint(def["uri"]), "/helpers.lf") || !strings.Contains(fmt.Sprint(def["range"]), "line:2") {
		t.Errorf("definition of helpers.twice: %v", def)
	}
	// total's input x is its own: four appearances there, none outside
	refs := ask(5, "textDocument/references", at(9, 9)).([]any)
	if len(refs) != 4 {
		t.Errorf("x inside total has 4 appearances, got %v", refs)
	}
	// the top-level x: made on line 13, used twice in launch
	refs = ask(6, "textDocument/references", at(12, 0)).([]any)
	if len(refs) != 3 {
		t.Errorf("the top-level x has 3 appearances, got %v", refs)
	}
	// renaming the top-level x leaves total's x alone
	ren := ask(7, "textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 12, "character": 0}, "newName": "start"}).(map[string]any)
	edits := ren["changes"].(map[string]any)[uri].([]any)
	if len(edits) != 3 {
		t.Errorf("rename should change 3 places, got %v", edits)
	}
	if e := ask(8, "textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 12, "character": 0}, "newName": "launch"}); !strings.Contains(fmt.Sprint(e), "can't be a name") {
		t.Errorf("a keyword can't be a new name: %v", e)
	}
	if e := ask(9, "textDocument/rename", map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": 21, "character": 18}, "newName": "double"}); !strings.Contains(fmt.Sprint(e), "rename it there") {
		t.Errorf("a name from another file is renamed there: %v", e)
	}
	// the outline
	syms := fmt.Sprint(ask(10, "textDocument/documentSymbol", map[string]any{"textDocument": map[string]any{"uri": uri}}))
	for _, want := range []string{"name:Point", "name:x", "name:total", "name:GET /hi"} {
		if !strings.Contains(syms, want) {
			t.Errorf("the outline is missing %s: %s", want, syms)
		}
	}
}

func TestLanguageServerSignatureHelp(t *testing.T) {
	dir := t.TempDir()
	write(t, dir+"/helpers.lf", "beam pad(text: Text, width: Int) gives Text {\n    send text\n}\n")
	c := startServer(t)
	c.write("initialize", 1, map[string]any{})
	c.read()
	uri := "file://" + dir + "/app.lf"
	doc := "use \"helpers.lf\"\n\nbeam area(w: Int, h: Int) gives Int {\n    send w * h\n}\n\nlaunch {\n    show area(2, 3)\n    show helpers.pad(\"x\", 4)\n    show area(\n}\n"
	c.write("textDocument/didOpen", 0, map[string]any{"textDocument": map[string]any{"uri": uri, "text": doc}})
	c.read()
	help := func(id, line, char int) string {
		c.write("textDocument/signatureHelp", id, map[string]any{"textDocument": map[string]any{"uri": uri}, "position": map[string]any{"line": line, "character": char}})
		r := c.read()
		if r["result"] == nil {
			t.Logf("reply: %v", r)
		}
		return fmt.Sprint(r["result"])
	}
	if got := help(2, 7, 16); !strings.Contains(got, "area(w: Int, h: Int) gives Int") || !strings.Contains(got, "activeParameter:1") {
		t.Errorf("inside area(2, 3), after the comma: %s", got)
	}
	if got := help(3, 8, 22); !strings.Contains(got, "pad(text: Text, width: Int) gives Text") || !strings.Contains(got, "activeParameter:0") {
		t.Errorf("inside helpers.pad: %s", got)
	}
	if got := help(4, 9, 14); !strings.Contains(got, "area(w: Int, h: Int)") {
		t.Errorf("a call still being typed: %s", got)
	}
}
