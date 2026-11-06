//go:build js

// Command wasm runs Laser Feet inside a web browser for the playground.
package main

import (
	"bytes"
	"fmt"
	"syscall/js"

	"laserfeet/lang"
)

func promise(work func() string) js.Value {
	handler := js.FuncOf(func(this js.Value, args []js.Value) any {
		resolve := args[0]
		go func() { resolve.Invoke(work()) }()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}

func run(src string) string {
	var out bytes.Buffer
	in := lang.NewInterp(&out)
	in.MaxSteps = 5_000_000
	if err := in.Run(src); err != nil {
		out.WriteString("\n⚠ " + err.Error() + "\n")
	}
	return out.String()
}

func test(src string) string {
	var out bytes.Buffer
	in := lang.NewInterp(&out)
	in.MaxSteps = 5_000_000
	results, err := in.RunTests(src)
	if err != nil {
		return out.String() + "\n⚠ " + err.Error() + "\n"
	}
	if len(results) == 0 {
		return "No test blocks found. Add one like:\n\ntest \"adds numbers\" {\n    expect 2 + 2 is 4\n}\n"
	}
	failed := 0
	for _, r := range results {
		if r.Passed {
			fmt.Fprintf(&out, "✓ %s\n", r.Name)
		} else {
			failed++
			fmt.Fprintf(&out, "✗ %s\n    %s\n", r.Name, r.Msg)
		}
	}
	fmt.Fprintf(&out, "\n%d passed, %d failed\n", len(results)-failed, failed)
	return out.String()
}

func main() {
	js.Global().Set("laserRun", js.FuncOf(func(this js.Value, args []js.Value) any {
		src := args[0].String()
		return promise(func() string { return run(src) })
	}))
	js.Global().Set("laserTest", js.FuncOf(func(this js.Value, args []js.Value) any {
		src := args[0].String()
		return promise(func() string { return test(src) })
	}))
	js.Global().Set("laserReady", true)
	if cb := js.Global().Get("onLaserReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	select {}
}
