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

## Libraries

| use | What it gives you |
|---|---|
| `use web` | Web servers and replies (`request.json` reads JSON bodies) |
| `use store` | A built-in database saved safely to a JSON file |
| `use json` | `json.read`, `json.write`, `json.pretty` |
| `use files` | `files.read`, `files.write`, `files.exists`, `files.list` |
| `use time` | `time.now`, `time.today`, `time.seconds` |
| `use "file.lf"` | Your own beams and objects from another file |

`env("PORT", "8080")` reads a setting from the environment. See `examples/todo` for a complete web API that uses all of these, with tests.

## Databases

`use postgres` connects to PostgreSQL with Laser Feet's own driver (`pg/`, written with only Go's standard library):

```
db = postgres.open(env("DATABASE_URL"))
rows = db.query("select name, price from products where price < $1", [50])
db.transaction() {
    tx.run("update products set stock = stock - $1 where sku = $2", [1, "LAMP-1"])
}
```

Values are always sent separately from SQL; logins use SCRAM-SHA-256 with the server's proof checked; remote databases use TLS with certificate checks by default. See `examples/inventory`.

## Packages

```
lf get github.com/billionairedev24/laserfeet/packages/textkit@main
```

Packages are git repositories (or folders in them) with a `package.lf`. `sole.list` lists them and `sole.lock` pins each to a commit and a SHA-256 of its code, checked at install and before every run. Installing never runs code. `packages/textkit` is an example package.

## Share a program

```
lf build app.lf      # makes ./app (app.exe on Windows), runs with nothing installed
```

The built file checks a SHA-256 of the program inside it and refuses to start if it was changed after building.

## Speed

`go test ./lang -bench .` runs the benchmarks in `bench/`. Compared with 0.1, the engine is now about 2.4x faster on function calls (`bench/fib.lf`: 438 ms → 179 ms) and 1.7x faster on loops, lists and objects (`bench/loops.lf`: 466 ms → 280 ms), mainly by cutting memory allocations.

## VS Code

Download `laserfeet.vsix` from the [latest release](https://github.com/billionairedev24/laserfeet/releases/latest) and run:

```
code --install-extension laserfeet.vsix
```

You get live errors as you type, autocomplete (fields after a dot, names, keywords, types), types on hover, syntax colors and snippets. Live features come from `lf lsp`, a standard language server, so other editors that speak LSP can use it too. The extension (in `editors/vscode`) uses no third-party packages.

## Doing things at the same time

```
tea = fast brew("Tea")                 -- start work, get a job
show wait tea                          -- its result (or its problem)
squares = fast each n in 1 to 8 {      -- one run per item, on a worker pool
    send slowSquare(n)
}
```

Fast work may read outside values but never change them; `lf check` enforces this, so there are no data races and no locks. `wait job for 5 seconds` sets a time limit. `web.fetch(url)` calls other services (30-second limit, 10 MB maximum). Today fast work takes turns on the engine and switches while waiting on the network, files or `pause`; running on several cores at once is planned.

## Docker

Every `lf new` project has a Dockerfile that tests and builds the app with the official image and ships only the built program on an empty image, as an unprivileged user:

```
docker build -t my-app .
docker run --rm -p 8080:8080 my-app
```

The official image `ghcr.io/billionairedev24/laserfeet` is published for amd64 and arm64 with every release, with signed build provenance.

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

Changing the `VERSION` file on `main` runs the release workflow, which tags the version and publishes the binaries with checksums and signed build provenance. See [SECURITY.md](SECURITY.md) for how to verify a download and how to report a problem.

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
| `bench` | Speed benchmarks |
| `examples` | Example programs, expected outputs, and test files |
| `site` | The website |
| `scripts/build.sh` | Builds all binaries, the website and the checksums |
| `editors/vscode` | The VS Code extension (and its test) |
| `cmd/lf/lsp.go` | The language server behind live errors and autocomplete |
| `docker` | The official image |
| `pg` | The PostgreSQL driver |
| `cmd/lf/sole.go` | The package manager |
| `packages/textkit` | An example package |
| `install.sh`, `install.ps1` | One-line installers that verify checksums |
| `.github/workflows` | CI (tests, vulnerability scan, build) and signed releases |

## A taste

```
object Item {
    name: Text
    price: Decimal
    quantity: Int = 1
}

beam total(items: List[Item]) gives Decimal {
    sum: Decimal = 0
    each item in items {
        sum = sum + item.price * item.quantity
    }
    send sum
}

launch {
    show total([Item("Lamp", 19.99, 3), Item("Bulb", 0.10)])   -- exactly 60.07
}
```

## Types

Beam inputs, results (`gives`) and object fields always say their type; values inside a beam take the type of what's put in them. `lf run`, `lf test` and `lf build` check every type first and run nothing if there's a problem; `lf check app.lf` does just the check.

| Type | Holds |
|---|---|
| `Int` | Whole numbers; overflow is an error, never a wrap-around |
| `Decimal` | Exact decimals, for money (`0.1 + 0.2` is exactly `0.3`); `19.99` written in code is a Decimal |
| `Float` | Fast approximate decimals; Float and Decimal never mix without `float()` / `decimal()` |
| `Text`, `Flag`, `Json` | Text, yes/no, data from outside |
| `List[T]`, `Map[T]`, `T?` | Lists, named entries, and values that may be `nothing` (which must be checked before use) |
| `Beam(A) gives B`, rule names | Beams as values; any object that follows a rule |

`Item.from(json)` builds a checked object from JSON; in web routes a mismatch becomes a 400 reply naming the field.

## Memory

Memory budgets in `lang/memory_test.go` fail the build if values grow: an object with two Int fields uses about 106 bytes (394 in 0.3), an exact Decimal about 42.
