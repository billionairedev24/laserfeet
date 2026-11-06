//go:build !race

// The race detector adds its own memory to every value, so budgets are measured without it.

package lang

import (
	"io"
	"runtime"
	"testing"
)

// Memory budgets: if a change makes these values bigger, this test fails.
// Measured per item over 100,000 items, with some headroom for the Go runtime.
func TestMemoryBudgets(t *testing.T) {
	budgets := []struct {
		name   string
		src    string
		budget uint64 // bytes per item
	}{
		{"Int in a list", "nums: List[Int] = []\neach i in 1 to 100000 {\n    nums.add(i * 7)\n}\nkeep = nums", 40},
		{"Decimal in a list", "nums: List[Decimal] = []\neach i in 1 to 100000 {\n    nums.add(i * 1.5)\n}\nkeep = nums", 56},
		{"object with 2 Int fields", "object P {\n    x: Int\n    y: Int\n}\npts: List[P] = []\neach i in 1 to 100000 {\n    pts.add(P(i * 7, i * 9))\n}\nkeep = pts", 112},
		{"object with 2 Decimal fields", "object P {\n    x: Decimal\n    y: Decimal\n}\npts: List[P] = []\neach i in 1 to 100000 {\n    pts.add(P(i * 1.5, i * 2.5))\n}\nkeep = pts", 160},
		{"short text", "words: List[Text] = []\neach i in 1 to 100000 {\n    words.add(\"item {i}\")\n}\nkeep = words", 64},
	}
	for _, b := range budgets {
		in := NewInterp(io.Discard)
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if err := in.Run(b.src); err != nil {
			t.Fatalf("%s: %v", b.name, err)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		per := (after.HeapAlloc - before.HeapAlloc) / 100000
		t.Logf("%-30s %4d bytes each (budget %d)", b.name, per, b.budget)
		if per > b.budget {
			t.Errorf("%s uses %d bytes each, over its budget of %d", b.name, per, b.budget)
		}
		runtime.KeepAlive(in)
	}
}
