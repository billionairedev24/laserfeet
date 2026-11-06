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

// withFiles lets 'use "file.lf"' read from files the page passes in.
func withFiles(in *lang.Interp, files map[string]string) {
	in.ReadSource = func(p string) (string, error) {
		if s, ok := files[p]; ok {
			return s, nil
		}
		return "", fmt.Errorf("the playground only has the files that come with this example")
	}
}

func filesArg(args []js.Value) map[string]string {
	files := map[string]string{}
	if len(args) > 1 && args[1].Type() == js.TypeObject {
		keys := js.Global().Get("Object").Call("keys", args[1])
		for i := 0; i < keys.Length(); i++ {
			k := keys.Index(i).String()
			files[k] = args[1].Get(k).String()
		}
	}
	return files
}

func run(src string, files map[string]string) string {
	var out bytes.Buffer
	in := lang.NewInterp(&out)
	withFiles(in, files)
	in.MaxSteps = 5_000_000
	if err := in.Run(src); err != nil {
		out.WriteString("\n⚠ " + err.Error() + "\n")
	}
	return out.String()
}

func test(src string, files map[string]string) string {
	var out bytes.Buffer
	in := lang.NewInterp(&out)
	withFiles(in, files)
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
		src, files := args[0].String(), filesArg(args)
		return promise(func() string { return run(src, files) })
	}))
	js.Global().Set("laserTest", js.FuncOf(func(this js.Value, args []js.Value) any {
		src, files := args[0].String(), filesArg(args)
		return promise(func() string { return test(src, files) })
	}))
	js.Global().Set("laserReady", true)
	if cb := js.Global().Get("onLaserReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	select {}
}
