# Laser Feet

A programming language with plain English keywords, objects and functions side by side, and a web server built in.

Version 0.1 is a prototype: an interpreter written in Go.

## Build and run

You need Go 1.22 or newer.

```
go build -o lf ./cmd/lf
./lf run examples/hello.lf
./lf test examples/shop_test.lf
./lf run examples/web.lf        # then open http://localhost:8080/hello
```

## Run the automated tests

```
go test ./...
```

This runs every example and compares its output with the matching `.out` file, checks that each program in `examples/mistakes` fails with the expected friendly message, and runs every `*_test.lf` file's test blocks.

## Rebuild the website and playground

```
GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o site/laserfeet.wasm ./cmd/wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" site/
python3 site/build.py
```

`site/index.html` and `site/laserfeet.wasm` together are the website.

## Layout

| Folder | What's in it |
|---|---|
| `lang/lexer.go` | Splits source text into words and symbols |
| `lang/parser.go`, `lang/ast.go` | Turns those into a tree that describes the program |
| `lang/interp.go` | Runs the tree |
| `lang/builtins.go` | Built-in beams such as `count`, `keepIf`, `sortBy` |
| `lang/web.go` | The `web` library |
| `cmd/lf` | The `lf` command |
| `cmd/wasm` | The browser version used by the playground |
| `examples` | Example programs, expected outputs, and test files |
| `site` | The website |

## A taste

```
object User {
    name
    age
}

launch {
    users = [User("Ada", 36), User("Tobi", 12)]
    adults = users then keepIf(age >= 18) then turn(name)
    show adults
}
```
