# Laser Feet

A programming language with plain English keywords, objects and functions side by side, and a web server built in.

The engine is an interpreter written in Go. The language is defined by [docs/spec.md](docs/spec.md), and the programs in `conformance/` check that the engine follows it. Until 1.0 the language may still change; when it does, `lf fix` updates your code.

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
| `use web` | Web servers and replies (`server.post("/x") as request { }`; `request.json` reads JSON bodies) |
| `use store` | A built-in database saved safely to a JSON file |
| `use json` | `json.read`, `json.write`, `json.pretty` |
| `use config` | `Settings.from(config.read("app.yaml", "prod.properties"))`: settings from YAML, `.properties` or JSON, typed and checked, with `${NAME:default}` from the environment |
| `use files` | `files.read`, `files.write`, `files.exists`, `files.list` |
| `use auth` | `auth.hash(password)`, `auth.check(password, hash)` (Argon2id, our own, RFC-tested), `auth.token()` |
| `use log` | `log.info(message, {details})`: JSON lines on standard error |
| `use time` | `time.now()` (a `Time`), `time.today()` (a `Date`), `time.at(text)`, `time.seconds()` |
| `use "file.lf"` | Your own beams and objects from another file |

`env("PORT", "8080")` reads a setting from the environment. See `examples/todo` for a complete web API that uses all of these, with tests.

## Web apps

```
use web
use auth

server = web.open(8080)
server.sessions(env("SESSION_SECRET", ""))   -- lf secret makes one
server.files("/static", "public")
server.limit("/login", 10, minutes(1))

server.get("/products/:id") as request {
    send web.render("views/product.html", {product: find(request.params.id)})
}
```

Routes for every method, `server.before` guards, signed session cookies, Argon2id passwords (`use auth`), templates that escape every value, cookies and headers on replies, form posts and uploads, CORS, rate limits, a `/health` check, JSON request logs and graceful shutdown. With sessions on, other sites can't act for a signed-in visitor. `examples/shipments` is a complete app (sign-up, sign-in, pages, a JSON API, PostgreSQL), tested end to end over real HTTP in CI.

## Databases

`use postgres` connects to PostgreSQL with Laser Feet's own driver (`pg/`, written with only Go's standard library):

```
db = postgres.open(env("DATABASE_URL"))
rows = db.query("select name, price from products where price < $1", [50])
transaction with db as tx {
    tx.run("update products set stock = stock - $1 where sku = $2", [1, "LAMP-1"])
}
```

Values are always sent separately from SQL; logins use SCRAM-SHA-256 with the server's proof checked; remote databases use TLS with certificate checks by default; statements stop after 30 seconds (`?timeout=`). `db.migrate("migrations")` or `lf migrate` runs new `.sql` files in order, each once, with checksums. See `examples/inventory` and `examples/shipments`.

## Formatting and updating code

```
lf fmt               # lay out every .lf file in this folder the standard way
lf fmt --check       # change nothing; fail if a file isn't laid out (for CI)
lf fix               # update code written for an older Laser Feet
```

`lf doc` writes documentation from the `--` comments above beams, objects and choices (Markdown, or a web page with `--html docs.html`); private ones are left out. `lf try` lets you type Laser Feet a line at a time and see what each piece gives.

`lf fmt` only changes spaces and blank lines, and checks that the code reads exactly the same afterwards. The VS Code extension formats on save. `lf fix` makes the changes a new version needs without changing what the code does: in 0.7 it names route requests (`as request`), writes transactions as `transaction with db as tx { }`, and adds brackets where `then` was unclear.

## Packages

```
lf get github.com/billionairedev24/laserfeet/packages/textkit@main
```

Packages are git repositories (or folders in them) with a `package.lf`. `sole.list` lists them and `sole.lock` pins each to a commit and a SHA-256 of its code, checked at install and before every run. Packages can need packages (their own `sole.list`): each is installed once, at the newest `vX.Y.Z` anything asks for, and locked like the rest. `lf get -u` updates packages within their major version, and `lf audit` checks them against known security problems (`advisories/advisories.list`). Installing never runs code. `packages/textkit` is an example package; `packages/greetings` uses it, and `packages/letters` uses both.

## Share a program

```
lf build app.lf      # makes ./app (app.exe on Windows), runs with nothing installed
```

The built file checks a SHA-256 of the program inside it and refuses to start if it was changed after building.

## Speed

Beams compile, the first time they run, into Go closures with fast paths for whole-number arithmetic, comparisons, fields, loops and calls; anything not compiled runs on the original tree-walking engine, and a differential test (plus a fuzzer) checks that both engines always give the same results. Timings on a 2-core machine, `lf run`, best of three:

Measured on one machine (2 cores), best of 15 runs, with `lf run`:

| Benchmark | 0.9 | 0.14 | 0.15 | 0.15 vs 0.9 |
|---|---|---|---|---|
| `bench/fib.lf` (calls, arithmetic) | 240 ms | 29 ms | 29 ms | 8.3× |
| `bench/loops.lf` (loops, lists, objects) | 300 ms | 82 ms | 55 ms | 5.4× |
| `bench/parallel.lf` (`fast each`, CPU work) | 1329 ms | 33 ms | 32 ms | 41× |
| `bench/jobs.lf` (four `fast` jobs, CPU work) | 434 ms | 153 ms | 57 ms | 7.6× |

Loops whose bodies only use names that already exist find each name once, when the loop starts, instead of every round, and a counting loop that builds a list makes room for it at once. Fast jobs run in parallel on every core. Beams that take and give `Int` and only work with whole numbers inside run on a whole-number tier: plain machine integers, no boxing and no name lookups. It has no side effects, so on anything unusual (a number too big, dividing by zero) it hands the call back to the ordinary engine, which gives the usual answer or message. CI measures the benchmarks and the reference app's requests per second on every push (see the run's summary).

Web requests run in parallel on every core, so the last row grows with the number of cores. Requests can't change values made outside them (the checker and the engine both refuse it), so they can't race; shared data lives in a store or a database.

`go test ./lang -bench .` runs them.

## VS Code

Download `laserfeet.vsix` from the [latest release](https://github.com/billionairedev24/laserfeet/releases/latest) and run:

```
code --install-extension laserfeet.vsix
```

You get live errors as you type, autocomplete (fields after a dot, names, keywords, types), types on hover, go to definition (into other files too), find references, rename, the outline of each file, the inputs of the beam you're calling as you type, syntax colors and snippets. Definitions, references and rename come from `lf check` itself, so a name means exactly what it means to the language: a beam's own `x` is not the `x` outside it. Live features come from `lf lsp`, a standard language server, so other editors that speak LSP can use it too. The extension (in `editors/vscode`) uses no third-party packages.

## Doing things at the same time

```
tea = fast brew("Tea")                 -- start work, get a job
show wait tea                          -- its result (or its problem)
squares = fast each n in 1 to 8 {      -- one run per item, on a worker pool
    send slowSquare(n)
}
```

Fast work may read outside values but never change them; `lf check` enforces this, and while `fast each` items run, changing anything made before they started is refused, so there are no data races and no locks to write. `fast each` runs its items in parallel on every core. A single `fast` job and web requests take turns on the engine, switching whenever one waits on a database, the network or `pause`. `wait job for 5 seconds` sets a time limit. `web.fetch(url)` calls other services (30-second limit, 10 MB maximum).

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

This runs the conformance suite (`conformance/`, one folder per section of the spec), runs every example and compares its output with the matching `.out` file, checks that each program in `examples/mistakes` fails with the expected friendly message, and runs every `*_test.lf` file's test blocks.

## Build everything

```
./scripts/build.sh
```

This builds `lf` for Linux, Mac and Windows, builds the website, and writes `dist/SHA256SUMS`. Builds are reproducible: the same source and Go version always give the same checksums. `dist/index.html` and `dist/laserfeet.wasm` together are the website.

## Website

The website source is `site/template.html`. `scripts/build.sh` turns it into `site/public/` (a complete page plus the engine), and the Website workflow publishes that with GitHub Pages on every push to `main`. One-time setup: Settings → Pages → Source: **GitHub Actions**.

## Releases and security

Changing the `VERSION` file on `main` runs the release workflow, which tags the version and publishes the binaries with checksums and signed build provenance. Every release also carries a software bill of materials (`laserfeet.spdx.json`). See [SECURITY.md](SECURITY.md) for how to verify a download and how to report a problem, [THREAT_MODEL.md](THREAT_MODEL.md) for what Laser Feet protects against, and [COMPATIBILITY.md](COMPATIBILITY.md) for what 1.x promises.

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
| `cmd/lf/lsp.go`, `lsp_nav.go` | The language server: live errors, completion, hover, definitions, references, rename, outline |
| `docker` | The official image |
| `pg` | The PostgreSQL driver |
| `auth` | Argon2id and BLAKE2b, checked against the RFC test vectors |
| `examples/shipments` | The reference web app |
| `docs/spec.md` | The language specification |
| `conformance` | Programs that check the engine follows the spec |
| `lang/format.go` | `lf fmt` |
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
| `Date`, `Time`, `Duration`, `UUID` | `date("2026-11-06")`, `time.now()`, `hours(2)`, `uuid()`; `time + hours(2)` and `later - earlier` work |
| `choice Status { Pending, Shipped(on: Date) }` | A value that is exactly one option; `focus` runs the matching arm and checks every option is handled |
| `Page[T]`, `beam first[T](...)` | Generic objects, choices and beams |
| `List[T]`, `Map[T]`, `T?` | Lists, named entries, and values that may be `nothing` (which must be checked before use) |
| `Beam(A) gives B`, rule names | Beams as values; any object that follows a rule |

`Item.from(json)` builds a checked object from JSON; in web routes a mismatch becomes a 400 reply naming the field. `db.query(Product, sql, values)` does the same for database rows. `private` keeps a field or beam inside its object (and out of JSON), and `fails p: PayProblem { }` catches only one kind of problem.

## Memory

Memory budgets in `lang/memory_test.go` fail the build if values grow: an object with two Int fields uses about 106 bytes (394 in 0.3), an exact Decimal about 42.
