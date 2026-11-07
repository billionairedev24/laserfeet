package lang

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// seed gives each fuzzer every example and conformance program to start from.
func seed(f *testing.F) {
	for _, pattern := range []string{"../examples/*.lf", "../examples/*/*.lf", "../conformance/*/*.lf"} {
		files, _ := filepath.Glob(pattern)
		for _, file := range files {
			b, _ := os.ReadFile(file)
			f.Add(string(b))
		}
	}
}

// Reading any text as code gives a program or a SyntaxError, never a crash.
func FuzzParse(f *testing.F) {
	seed(f)
	f.Fuzz(func(t *testing.T, src string) {
		if _, err := Parse(src); err != nil {
			if _, ok := err.(*SyntaxError); !ok {
				t.Fatalf("unexpected error type %T: %v", err, err)
			}
		}
		ParseForFix(src)
	})
}

// The checker finds problems in any program without crashing.
func FuzzCheck(f *testing.F) {
	seed(f)
	f.Fuzz(func(t *testing.T, src string) {
		Check(src, nil)
		FixEdits(src, nil)
	})
}

// lf fmt never changes what code means, and formatting twice changes nothing more.
func FuzzFormat(f *testing.F) {
	seed(f)
	f.Fuzz(func(t *testing.T, src string) {
		out, err := Format(src)
		if err != nil {
			return
		}
		if err := sameCode(src, out); err != nil {
			t.Fatal(err)
		}
		again, err := Format(out)
		if err != nil || again != out {
			t.Fatalf("formatting twice changed it:\n%q\n%q", out, again)
		}
	})
}

// JSON from outside gives a value or an error, never a crash.
func FuzzJSON(f *testing.F) {
	for _, s := range []string{`{"a": [1, 2.5, "x", null, true]}`, `[]`, `"é"`, `1e400`, `{"a":{"b":{"c":[]}}}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if v, err := fromJSON(s); err == nil {
			toJSON(v)
		}
	})
}

const fuzzApp = `
use web
object Todo {
    title: Text
    done: Flag = no
}
server = web.open(8080)
server.get("/todos/:id") as request {
    send {id: int(request.params.id)}
}
server.post("/todos") as request {
    send Todo.from(request.json)
}
server.on("/echo") as request {
    send {method: request.method, size: request.body.size(), q: request.query}
}
`

// Any request a visitor sends gets a reply: never a crash, and never a 500
// that leaks how the program works (bad input is the visitor's 400).
func FuzzWebRequests(f *testing.F) {
	in := NewInterp(io.Discard)
	if err := in.Run(fuzzApp); err != nil {
		f.Fatal(err)
	}
	v, _ := in.globals.lookup("server")
	h := newHandler(in, v.(*Server))
	f.Add("GET", "/todos/12", "")
	f.Add("POST", "/todos", `{"title": "x"}`)
	f.Add("POST", "/todos", `{"title": 5}`)
	f.Add("PUT", "/echo?a=1&a=2", "body")
	f.Fuzz(func(t *testing.T, method, path, body string) {
		req, err := http.NewRequest(method, "http://lf.test/", strings.NewReader(body))
		if err != nil {
			return
		}
		req.URL.Path = "/" + strings.TrimPrefix(path, "/")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code == 0 {
			t.Fatal("no reply")
		}
		if w.Code == 500 && strings.Contains(w.Body.String(), "line") {
			t.Fatalf("a 500 reply must not describe the program: %s", w.Body.String())
		}
	})
}

// Templates give HTML or a TemplateError, never a crash.
func FuzzTemplate(f *testing.F) {
	for _, s := range []string{"<h1>{title}</h1>", "{each p in ps}{p.name}{end}", "{when x}a{otherwise when y}b{otherwise}c{end}", "<style>a { b: c }</style>", `{use "x.html"}`, `\{x\}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		parseTemplate("t.html", src)
	})
}

// Any program gives the same output and the same problem on the compiled
// engine and the tree-walking engine.
func FuzzEngines(f *testing.F) {
	seed(f)
	f.Fuzz(func(t *testing.T, src string) {
		for _, risky := range []string{"start(", "pause", "fetch", "files.", "store.", "postgres", "uuid(", "now()", "auth.", "fast", "wait"} {
			if strings.Contains(src, risky) {
				return
			}
		}
		if _, err := Parse(src); err != nil {
			return
		}
		run := func(compile bool) (string, string) {
			var out bytes.Buffer
			in := NewInterp(&out)
			in.NoCompile = !compile
			in.MaxSteps = 20000
			err := in.Run(src)
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			return out.String(), msg
		}
		o1, e1 := run(true)
		o2, e2 := run(false)
		if o1 != o2 || e1 != e2 {
			t.Fatalf("the engines disagree on\n%s\ncompiled: %q %q\ntree:     %q %q", src, o1, e1, o2, e2)
		}
	})
}

// A value is worked out once even when using it fails: x[k] and not x used to
// work x out a second time on the compiled engine to word the problem.
func TestFailingUsesWorkOutOnce(t *testing.T) {
	for _, use := range []string{"x = f()[0]", "y = not f()"} {
		src := "beam f() gives Json {\n    show \"f ran\"\n    send 3\n}\nlaunch {\n    " + use + "\n}\n"
		for _, compile := range []bool{true, false} {
			var out bytes.Buffer
			in := NewInterp(&out)
			in.NoCompile = !compile
			if err := in.Run(src); err == nil {
				t.Fatalf("%s: expected a problem", use)
			}
			if got := strings.Count(out.String(), "f ran"); got != 1 {
				t.Fatalf("%s, compile=%v: f ran %d times, want once", use, compile, got)
			}
		}
	}
}
