package lang

import (
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Many parallel items read the same values, call beams for the first time
// (filling the engine's caches), make and change their own values, use a
// store, and some fail. Run with -race: there must be no data races, and the
// results must be the same every time.
func TestParallelStress(t *testing.T) {
	src := `
use store
object Point {
    x: Int
    y: Int
    beam size() gives Int {
        send me.x * me.x + me.y * me.y
    }
}
choice Shape {
    Dot
    Box(w: Int, h: Int)
}
lock settings = {scale: 3, name: "grid"}
points: List[Point] = []
db = store.memory()

beam area(s: Shape) gives Int {
    focus s {
        Dot {
            send 0
        }
        Box(w, h) {
            send w * h
        }
    }
}

beam work(n: Int) gives Int {
    mine: List[Int] = []
    each p in points {
        mine.add(p.size() % (n + 1))
    }
    shape = Shape.Box(n, settings.scale)
    db.add("seen", {n: n})
    send sum(mine) + area(shape) + "{settings.name}".size()
}

launch {
    each i in 1 to 200 {
        points.add(Point(i, i + 1))
    }
    first = fast each n in 1 to 64 {
        send work(n)
    }
    second = fast each n in 1 to 64 {
        send work(n)
    }
    show first is second
    show sum(first)
    show count(db.all("seen"))
    attempt {
        fast each n in 1 to 32 {
            when n is 17 {
                fail "item {n} failed"
            }
            send n
        }
    } fails problem {
        show problem
    }
}
`
	for round := 0; round < 3; round++ {
		for _, compile := range []bool{true, false} {
			var out bytes.Buffer
			in := NewInterp(&out)
			in.NoCompile = !compile
			if err := in.Run(src); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			if !strings.HasPrefix(got, "yes\n") || !strings.Contains(got, "\n128\nitem 17 failed\n") {
				t.Fatalf("unexpected output (compiled %v):\n%s", compile, got)
			}
		}
	}
}

// Many web requests at once, on every core, while other code changes the
// program's values between them. Run with -race: requests must never see a
// half-made change, and each must get the right answer.
func TestParallelRequests(t *testing.T) {
	src := `
use web
use store

object Point {
    x: Int
    y: Int
}

hits = 0
seen: List[Int] = []
db = store.memory()
server = web.open(8080)

beam fib(n: Int) gives Int {
    when n < 2 {
        send n
    }
    send fib(n - 1) + fib(n - 2)
}

beam bump() {
    hits = hits + 1
    seen.add(hits)
}

beam sneak() {
    seen.add(0)
}

server.get("/fib/:n") as request {
    send fib(int(request.params.n))
}

server.get("/points/:n") as request {
    pts: List[Point] = []
    each i in 1 to int(request.params.n) {
        pts.add(Point(i, i * 2))
    }
    each p in pts {
        p.y = p.y + 1
    }
    send pts then turn(x + y) then sum()
}

server.get("/read") {
    send seen.size() >= 0 and hits >= 0
}

server.post("/notes") as request {
    db.add("notes", {text: request.body})
    send db.count("notes")
}

server.get("/sneak") {
    sneak()
    send "should not get here"
}

server.get("/nested") {
    send server.visit("/fib/10").body
}
`
	var log bytes.Buffer
	in := NewInterp(&log)
	if err := in.Run(src); err != nil {
		t.Fatal(err)
	}
	v, _ := in.globals.lookup("server")
	ts := httptest.NewServer(newHandler(in, v.(*Server)))
	defer ts.Close()
	bump, _ := in.globals.lookup("bump")
	v.(*Server).quiet = true

	stop := make(chan struct{})
	var bg sync.WaitGroup
	bg.Add(1)
	go func() { // other code takes whole turns between requests
		defer bg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			in.gil.Lock()
			in.call(bump, nil, nil, 0, nil)
			in.gil.Unlock()
		}
	}()

	want := map[string]string{
		"/fib/18":    "2584",
		"/points/50": "3875", // sum of i + 2i + 1 for 1..50
		"/read":      "true",
		"/nested":    "55",
	}
	var wg sync.WaitGroup
	errs := make(chan string, 1000)
	client := ts.Client()
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				for path, body := range want {
					r, err := client.Get(ts.URL + path)
					if err != nil {
						errs <- err.Error()
						return
					}
					b, _ := io.ReadAll(r.Body)
					r.Body.Close()
					if string(b) != body {
						errs <- path + ": " + string(b)
					}
				}
				r, err := client.Post(ts.URL+"/notes", "text/plain", strings.NewReader("hi"))
				if err == nil {
					r.Body.Close()
				}
				if w == 0 && i%5 == 0 {
					r, err := client.Get(ts.URL + "/sneak")
					if err == nil {
						if r.StatusCode != 500 {
							errs <- "a request changed a shared list"
						}
						r.Body.Close()
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	bg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	r, _ := client.Post(ts.URL+"/notes", "text/plain", strings.NewReader("last"))
	b, _ := io.ReadAll(r.Body)
	if string(b) != "201" { // 8 workers x 25 notes, then this one
		t.Errorf("every note must be kept once, got %s", b)
	}
	if !strings.Contains(log.String(), "a web request can't change a list made outside it") {
		t.Errorf("the refused change must be logged, got %q", log.String())
	}
}
