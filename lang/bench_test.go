package lang

import (
	"io"
	"os"
	"testing"
)

func benchFile(b *testing.B, path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < b.N; i++ {
		if err := NewInterp(io.Discard).Run(string(src)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFib(b *testing.B)   { benchFile(b, "../bench/fib.lf") }
func BenchmarkLoops(b *testing.B) { benchFile(b, "../bench/loops.lf") }
