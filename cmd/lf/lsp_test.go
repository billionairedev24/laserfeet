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
