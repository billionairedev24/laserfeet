# Laser Feet

A programming language with plain English keywords, objects and functions side by side, and a web server built in.

Version 0.1 is a prototype: an interpreter written in Go.

## Install

Mac or Linux:

```
curl -fsSL https://billionairedev24.github.io/laserfeet/install.sh | sh
```

Windows (PowerShell):

```
irm https://billionairedev24.github.io/laserfeet/install.ps1 | iex
```

Both installers check the download's SHA-256 checksum against the release before installing. Then:

```
lf new my-app
cd my-app
lf run app.lf
```

## Build from source

You need Go 1.24 or newer. Releases are built with the newest supported Go (1.26).

```
go install ./cmd/lf             # puts lf in $(go env GOPATH)/bin
lf run examples/hello.lf
lf test examples/shop_test.lf
lf run examples/web.lf          # then open http://localhost:8080/hello
```

If `lf` isn't found, add Go's bin folder to your PATH. On Mac or Linux:

```
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc && source ~/.zshrc
```

On Windows the Go installer adds `%USERPROFILE%\go\bin` to PATH; open a new terminal.

## Run the automated tests

```
go test ./...
```

This runs every example and compares its output with the matching `.out` file, checks that each program in `examples/mistakes` fails with the expected friendly message, and runs every `*_test.lf` file's test blocks.

## Build everything

```
./scripts/build.sh
```

This builds `lf` for Linux, Mac and Windows, builds the website, and writes `dist/SHA256SUMS`. Builds are reproducible: the same source and Go version always give the same checksums. `dist/index.html` and `dist/laserfeet.wasm` together are the website.

## Website

The website source is `site/template.html`. `scripts/build.sh` turns it into `site/public/` (a complete page plus the engine), and the Website workflow publishes that with GitHub Pages on every push to `main`. One-time setup: Settings → Pages → Source: **GitHub Actions**.

## Releases and security

Pushing a tag such as `v0.1.0` runs the release workflow, which publishes the binaries with checksums and signed build provenance. See [SECURITY.md](SECURITY.md) for how to verify a download and how to report a problem.

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
| `scripts/build.sh` | Builds all binaries, the website and the checksums |
| `.github/workflows` | CI (tests, vulnerability scan, build) and signed releases |

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
