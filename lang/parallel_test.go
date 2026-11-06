package lang

import (
	"bytes"
	"strings"
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
