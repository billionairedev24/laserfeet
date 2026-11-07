# The Laser Feet Language Specification

Version 0.13. This document says exactly what Laser Feet code means. Where the
website explains, this specification decides. Every section has matching
programs in `conformance/<section>/`: each one prints exactly its `.out` file,
or is refused with exactly the message in its `.err` file. `go test ./lang -run
TestConformance` runs them all, and CI runs them on every change.

Until 1.0, a release may change the language. When it does, `lf fix` updates
existing code without changing what it does.

## Contents

1. [Lexical structure](#1-lexical-structure)
2. [Types and values](#2-types-and-values)
3. [Names](#3-names)
4. [Operators](#4-operators)
5. [Text](#5-text)
6. [Control flow](#6-control-flow)
7. [Beams](#7-beams)
8. [Objects, rules and choices](#8-objects-rules-and-choices)
9. [Problems and tests](#9-problems-and-tests)
10. [Collections](#10-collections)
11. [Doing things at the same time](#11-doing-things-at-the-same-time)
12. [Web](#12-web)
13. [Databases](#13-databases)
14. [Programs and files](#14-programs-and-files)
15. [Grammar](#15-grammar)
16. [Layout (lf fmt)](#16-layout-lf-fmt)

## 1. Lexical structure

Source files are UTF-8 text with the `.lf` extension.

- **Comments** start with `--` and run to the end of the line.
- **Line breaks** end statements. Inside `( )` and `[ ]` they are ignored, so
  a call or list can spread over several lines.
- **Names** start with a letter or `_` and continue with letters, digits or `_`.
  Case matters.
- **Numbers**: digits, optionally with `_` between them for readability
  (`1_000_000`). A number with a decimal point (`19.99`) is an exact Decimal.
- **Text** is written in double quotes. Escapes: `\n`, `\t`, `\"`, `\\`, `\{`
  and `\}`. Text in triple quotes (`"""`) may span lines.
- **Symbols**: `{ } ( ) [ ] , . : = + - * / % < > <= >= == != => ?`

**Keywords** can't be used as names:

```
launch show beam send lock object rule follows when otherwise each in loop
while times then fast attempt fails fail use test expect is not and or yes no
nothing me stop skip to gives wait choice focus private
```

Keywords may be used as entry names in a map (`{to: "x"}`) and after a dot.

The words `transaction`, `with` and `as` are ordinary names, with a special
meaning only in `transaction with ... as ... { }` and `f(...) as name { }`.

## 2. Types and values

| Type | Values |
|---|---|
| `Int` | Whole numbers from −9,223,372,036,854,775,808 to 9,223,372,036,854,775,807. A calculation that goes past either end is a problem, never a wrap-around. |
| `Decimal` | Exact decimal numbers. `0.1 + 0.2 is 0.3` is `yes`. A division that can't be exact keeps 28 digits after the point. |
| `Float` | IEEE 754 64-bit numbers, for measurements where speed matters more than exactness. |
| `Text` | Unicode text. `size()` counts characters, not bytes. |
| `Flag` | `yes` or `no`. |
| `Date` | A day on the calendar, with no time of day or time zone: `2026-11-06`. |
| `Time` | A moment, always kept in UTC: `2026-11-06T10:00:00Z`. |
| `Duration` | A length of time: `1h30m`, `45s`, `2d6h` (a `d` is 24 hours). |
| `UUID` | A 128-bit identifier: `0b7f3c1e-1234-4abc-8def-0123456789ab`. |
| `Json` | Data from outside the program (requests, files, databases), checked when it's used. |
| `List[T]` | An ordered list of values of type `T`. |
| `Map[T]` | Named entries, each holding a value of type `T`. |
| `T?` | A value of type `T`, or `nothing`. |
| `Beam(A, B) gives R` | A beam (function) taking `A` and `B` and giving `R`. |
| `Page[T]` | A generic object or choice, with the types it holds (sections 7 and 8). |
| object names | Objects (section 8). |
| rule names | Any object that follows the rule. |
| choice names | Exactly one of the choice's options (section 8). |

**Numbers mix this way:** Int mixes with Decimal (giving Decimal) and with Float
(giving Float). Float and Decimal never mix: convert one with `float(x)` or
`decimal(x)`. A decimal written in the code (`2.5`) can also be used where a
Float is expected. Dividing two Ints with `/` gives an exact Decimal
(`10 / 4` is `2.5`).

**Dates and times.** `date("2026-11-06")` or `date(2026, 11, 6)` makes a
Date; `seconds(n)`, `minutes(n)`, `hours(n)`, `days(n)` and
`duration("1h30m")` make Durations; `uuid()` makes a random UUID and
`uuid(text)` reads one. With `use time`: `time.now()` (a Time),
`time.today()` (a Date, where the program runs) and `time.at(text)`.

| Operation | Gives |
|---|---|
| Time ± Duration, Duration + Time | Time |
| Date ± Duration | Date (the Duration must be whole days) |
| Time − Time, Date − Date | Duration |
| Duration ± Duration, Duration × Int, Duration ÷ Int | Duration |
| `<` `>` `<=` `>=` between two of the same kind | Flag |

Members: a Date has `year`, `month`, `day`, `weekday`, `addDays(n)` and
`addMonths(n)`; a Time has `date`, `hour`, `minute`, `second`; a Duration has
`inSeconds` (Int), `inMinutes`, `inHours` and `inDays` (Decimal). Text is never
silently a Date: `start: Date = "2026-11-06"` is refused, and the message shows
`date("2026-11-06")`. In JSON all four are text; `Item.from(json)` reads them.
PostgreSQL `date`, `timestamp`, `timestamptz` and `uuid` columns arrive as Date,
Time and UUID, and these values can be sent as `$1` values.

**Where types are written:** beam inputs, beam results (`gives`) and object
fields always state their type. Values inside a beam take the type of what is
put in them; a type may be written anyway (`total: Decimal = 0`).

**Nothing-safety:** a value of type `T?` must be checked before it is used as a
`T`. Inside `when x is not nothing { ... }`, after `when x is nothing { send ...
}`, and on the right of `x is not nothing and ...` or `x is nothing or ...`,
`x` is a `T`. Checks combine: after `when a is nothing or b is nothing { send
... }` both are known, and so are both inside `when a is not nothing and b is
not nothing { ... }`.

**Checking:** `lf run`, `lf test` and `lf build` check every type before
running anything. If there is a problem, nothing runs.

## 3. Names

- `name = value` makes a name, or changes it if it exists in this beam.
  A beam (including one written inline) changes only its own names and the
  fields of its object, never a name made outside it: `lf check` refuses
  `name = value` in a beam when the name is made outside, so a second name
  never quietly hides the first. A beam's inputs are its own names, even when
  they share a name with something outside. `launch` and tests may change
  names made at the top of the file.
- `name: Type = value` makes a name with a stated type.
- `lock name = value` makes a name that can never change.
- A name made inside a `when`, `otherwise`, `each` or `loop` block belongs to the
  whole beam (or `launch` block), so it can be used after the block.
- Beams, objects and rules declared at the top of a file can be used anywhere
  in that file, before or after their declaration.
- A name of your own may hide a built-in beam (section 10): after `count = 3`,
  `count` is 3 in that beam.

## 4. Operators

From loosest to tightest:

| Level | Operators | Meaning |
|---|---|---|
| 1 | `then` | passes the value on its left into the beam on its right |
| 2 | `or` | either is `yes` (the right side runs only if needed) |
| 3 | `and` | both are `yes` (the right side runs only if needed) |
| 4 | `not` | the opposite |
| 5 | `is`, `is not`, `==`, `!=` | the same, not the same |
| 6 | `<` `>` `<=` `>=` | comparisons |
| 7 | `to` | a range of Ints, both ends included: `1 to 4` |
| 8 | `+` `-` | add, subtract; `+` also joins two Texts |
| 9 | `*` `/` `%` | multiply, divide, remainder |
| 10 | `-` (before a value), `fast`, `wait` | negative, start work, wait for work |
| 11 | `f(...)` `.name` `[i]` | call, field or beam of a value, item of a list |

Operators at the same level work left to right: `10 - 4 - 3` is `3`.

**`then`:** `x then f(a)` means `f(x, a)`, and `x then f` means `f(x)`. A chain
reads left to right: `users then keepIf(age >= 18) then sortBy(name)`.

The left side of `then` may not be an unbracketed calculation, because it would
be unclear how much of it `then` takes. `"n: " + names then count` is refused.
Write `("n: " + names) then count` to pass all of it, or
`"n: " + (names then count)` to pass part of it. A range is clear enough:
`1 to 10 then sum` is allowed. `lf fix` adds the brackets that keep what older
code did.

## 5. Text

- `{expression}` inside text is replaced by the expression's value, written as
  text: `"Total: {price * 2}"`.
- If what's inside the braces isn't a Laser Feet expression (JSON, for
  example), it is kept as written.
- `\{` and `\}` are always kept as plain braces.
- Methods: `size()`, `upper()`, `lower()`, `trim()`, `has(t)`,
  `startsWith(t)`, `split(t)`, `replace(old, new)`. `text[i]` is the
  character at position `i`, counting from 0.

## 6. Control flow

- `when condition { ... }` runs the block if the condition is `yes`. The
  condition must be a Flag. It may be followed by `otherwise { ... }` or
  `otherwise when condition { ... }`.
- `each item in list { ... }` runs the block once per item, in order.
  `each n in 1 to 10` counts.
- `loop while condition { ... }` repeats while the condition is `yes`.
- `loop n times { ... }` repeats `n` times.
- `stop` leaves the innermost loop; `skip` goes on to its next round.
- `focus value { arm { ... } ... }` runs the first arm that matches the value.
  An arm is an option of a choice (section 8), a value to compare with
  (`200`, `"admin"`, `yes`, `nothing`), or `otherwise`, which matches anything
  and must come last. A focus on a choice must have an arm for every option
  (and for `nothing`, if the value may be nothing), or an `otherwise`. A focus
  on other values needs an `otherwise`, except a Flag with both `yes` and `no`.
  A focus that covers every value and whose arms all `send` counts as sending
  on every path.
- `launch { ... }` is where a program starts. It runs after everything else at
  the top of the file.

## 7. Beams

A beam is a function.

```
beam total(items: List[Item]) gives Decimal {
    ...
    send sum
}
```

- Every input has a type. `gives Type` states the result; without it, the beam
  gives nothing.
- `send value` ends the beam with its result. A beam that `gives` something
  must `send` on every path.
- Inputs can be given by position or by name: `greet(name: "Ada")`.
- Beams are values: they can be stored, passed and returned, and they keep the
  names around them (closures).
- Anonymous beams: `beam(x: Int) gives Int { send x * 2 }`, or `x => x * 2`
  for one input and one expression.
- In `keepIf`, `dropIf`, `turn`, `sortBy` and `findFirst`, the fields of the
  item can be used directly (`keepIf(age >= 18)`), and `it` is the item itself.
- **Generic beams** name the types they work with in `[ ]` after their name.
  Each call works out what the names stand for from the values given:

```
beam firstOr[T](items: List[T], fallback: T) gives T { ... }
firstOr([3, 4], 0)          -- T is Int, so this gives an Int
firstOr(["a"], 0)           -- refused: T can't be Text and Int at once
```

  Inside the beam, a value of type `T` could be anything, so it can be stored,
  passed, sent and compared with `is`, but not used as a number or text.
  Values are worked out before beams, so in `mapAll(xs, x => x * 2)` the
  checker already knows what `x` is.
- **A block after a call** is passed as a beam: `server.get("/") { ... }`.
  `f(...) as name { ... }` gives that beam one input, called `name`.

## 8. Objects, rules and choices

```
object Item {
    name: Text
    price: Decimal
    quantity: Int = 1
    beam total() gives Decimal {
        send me.price * me.quantity
    }
}
```

- Fields have types and may have default values. `Item("Lamp", 19.99)` fills
  fields in order; `Item(name: "Lamp", price: 19.99)` by name. Every field
  without a default needs a value.
- `me` is the object a beam belongs to.
- `private` before a field or beam keeps it inside the object: only the
  object's own beams can use it. Private fields are never written as JSON, and
  `Item.from(json)` never fills them (they need a default value). `private`
  before a beam at the top of a file keeps it out of what `use` shares.
- `Item.from(json)` builds an Item from outside data, checking every field.
  A mismatch is a problem that names the field; in a web route it becomes a
  400 reply.
- A **rule** lists beams with their types. `object Square follows Shape`
  promises those beams, and the checker holds the object to it. A rule name can
  be used as a type.

**Generic objects and choices** name their types in `[ ]`:
`object Page[T] { items: List[T], total: Int }`,
`choice Result[T] { Ok(value: T), Failed(reason: Text) }`. Making one works out
the types from the values (`Page(products, 3)` is a `Page[Product]`). Written as
a type, a generic must say what it holds: `Page[Product]`, never just `Page`.
An option that doesn't mention a type name (`Result.Failed("empty")`) fits
any `Result`.

**Choices.** A choice is a type whose value is exactly one of its options.
Options may carry data, written like an object's fields:

```
choice Status {
    Pending
    Shipped(on: Text, carrier: Text)
    Lost
}
```

- Options are separated by new lines or commas: `choice Size { Small, Medium, Large }`.
- `Status.Pending` is a value. `Status.Shipped("2026-11-06", "DHL")` (or with
  names, `Status.Shipped(on: ..., carrier: ...)`) makes one.
- A choice value has no fields of its own; `focus` looks inside it, naming the
  option's data in order (all of them, or none):

```
focus status {
    Pending { ... }
    Shipped(on, carrier) { ... }   -- on and carrier exist only in this arm
    Lost { ... }
}
```

- `is` compares choice values, data included.
- As JSON, an option without data is its name (`"Pending"`), and an option
  with data is an object that says which option it is
  (`{"kind": "Shipped", "on": "2026-11-06", "carrier": "DHL"}`).
  `Status.from(json)`, and fields of type `Status` in `Item.from(json)`, read
  both forms and refuse anything else.

## 9. Problems and tests

- `fail value` stops with a problem.
- `attempt { ... } fails problem { ... }` runs the first block; if it fails,
  the second block runs with the problem in `problem`: the value given to
  `fail`, or the message for problems the engine finds.
- `fails p: Type { ... }` catches only problems whose value is that type (a
  choice is the usual kind), with `p` of that type. An attempt may have several
  `fails`; they are tried in order, and a `fails` without a type must come last.
  A problem that no `fails` matches carries on outwards.
- A problem that nothing catches ends the program, reported as
  `file, line N: message`, where the file is the one the problem happened in
  (a file the program uses, or a package), followed by the calls that led
  there, innermost first: `in safeShare, called from app.lf, line 5`. The
  same call repeated (recursion) is shown once with a count. Failed tests and
  problems in web requests (in the server's log) show the same calls.
- `test "name" { ... }` declares a test, run by `lf test` (a file, or every
  `.lf` file with tests under a folder, the current one by default; installed
  packages are left out; `--cover` reports the statements that never ran). Inside it,
  `expect condition` fails the test if the condition is `no`. `lf run` doesn't
  run tests.

## 10. Collections

**Lists:** `[1, 2, 3]`. All items have the same type. `list[i]` counts from 0;
a position outside the list is a problem. Methods: `add(x)`, `remove(x)`,
`size()`, `has(x)`.

**Maps:** `{name: "Ada", city: "Lagos"}`. `m.name` reads an entry. Methods:
`keys()`, `has(name)`.

**Built-in beams:**

| Beam | Gives |
|---|---|
| `count(list)` | how many items |
| `sum(list)`, `biggest(list)`, `smallest(list)` | total, largest, smallest |
| `first(list)`, `last(list)` | an item, or `nothing` for an empty list |
| `sort(list)`, `reverse(list)` | a new list |
| `join(list, between)` | text |
| `keepIf(list, test)`, `dropIf(list, test)` | the items that pass, or fail, the test |
| `turn(list, change)` | a new list with every item changed |
| `sortBy(list, key)` | a new list, sorted by the key |
| `findFirst(list, test)` | the first item that passes, or `nothing` |
| `text(x)`, `int(x)`, `decimal(x)`, `float(x)`, `number(x)` | conversions |
| `date(x)`, `duration(x)`, `uuid(x)`, `uuid()` | dates, durations and identifiers (section 2) |
| `seconds(n)`, `minutes(n)`, `hours(n)`, `days(n)` | Durations |
| `round(x)`, `fixed(x, places)` | rounding, and text with a fixed number of decimal places |
| `env(name, default)` | a setting from the environment |
| `pause(seconds)` | waits |

## 11. Doing things at the same time

- `fast work()` starts `work()` and gives a job at once.
- `wait job` gives the job's result, or its problem. `wait job for 5 seconds`
  is a problem if the job takes longer.
- `fast each item in list { ... send x }` runs the block for every item at the
  same time and gives the list of what each sent, in the list's order.
- `fast { ... }` runs a block in the background.
- **Fast work may read the names around it but never change them.** The
  checker refuses any change, so fast work can't race. Results come back with
  `send`.
- **`fast each` uses every core.** Its items run truly in parallel, one per
  core, while the code that started it waits. While they run, nothing made
  before they started can change: changing an older list, map, object or name,
  even from inside a beam an item calls, is a problem that says so. Items make
  and change their own values freely. `fast` work started inside an item runs
  in parallel too, and finishes before the `fast each` does.
- **Web requests use every core too.** Requests run at the same time as each
  other, truly in parallel, under the same rule as `fast each` items: a
  request can't change a list, map, object or name made before it started
  (the checker refuses it in a route block, and the engine refuses it through
  a beam the route calls, with a 500 reply and the reason in the log). A
  request makes and changes its own values freely. Data that requests share
  lives in a store or a database. `server.visit` runs a route the same way,
  so tests catch the mistake too.
- Other code (`launch`, a single `fast` job) runs between requests, never
  during one, and takes turns with other jobs; each lets others run while it
  waits for a database, the network, a timer or `wait`.
- `fast` work started inside parallel work (a `fast each` item or a web
  request) is done on the spot, before the next line runs, so it can never
  run beside the code that started it.

## 12. Web

`use web` brings in the web library.

```
server = web.open(8080)
server.get("/products/:id") as request {
    send request.params.id
}
```

**Routes.** `server.get`, `server.post`, `server.put`, `server.patch`,
`server.delete` and `server.on` (any method) add routes. A GET route also
answers HEAD. Parts of the path written `:name` arrive in
`request.params.name`; the checker refuses a name the path doesn't have. A
route that uses its request **names it** with `as request` (or any name); one
that doesn't need it leaves the name out.

**Requests** have `method`, `path`, `params`, `query` (the `?a=b` values),
`body` (the raw text), `json` (the body read as JSON), `form` (the fields of
a form post, as a `Map[Text]`), `files` (uploaded files), `ip`,
`header(name)` and `cookie(name)` (both `Text?`), and `session` (below).

**Replies.** What a route sends becomes the reply: Text as plain text (never
as HTML), `web.page(html)` and `web.render(...)` as web pages,
`web.status(code, body)` with a status, `nothing` as 204, anything else as
JSON. `web.reply(value)` makes any value a reply, so `.header(name, value)`,
`.cookie(name, value, days(30))` and `.forget(name)` can be added; each gives
the reply back, so they chain. `web.redirect(path)` sends the browser to
another page of the same site; any other address is refused, so a link can't
send visitors to another site.

**Cookies** are always `HttpOnly` and `SameSite=Lax`, and `Secure` unless the
server is on localhost.

**Before every route.** `server.before() as request { ... }` runs before each
route. If it sends anything, that is the reply and the route doesn't run.

**Sessions.** `server.sessions(secret)` (at least 32 characters; `lf secret`
makes one), optionally with how long they last (`days(7)` by default), keeps a
small signed cookie per visitor. `request.session` has `get(name)` (`Text?`),
`set(name, value)`, `has(name)`, `remove(name)` and `clear()`. A session that
was changed or made up is treated as empty. With sessions on, a request that
changes something (POST, PUT, PATCH, DELETE) is refused with 403 when the
browser says it came from another site, so other sites can't act for a
signed-in visitor.

**Other sites calling the API.** `server.allow("https://shop.example")` lets
that site's pages call this server from a browser (CORS), including with
sessions.

**Files and limits.**

- `server.files("/static", "public")` serves the files of a folder next to the
  program. Files whose names start with a dot are never served, and nothing
  outside the folder can be reached.
- `server.limit(100, minutes(1))` allows each visitor (by address) that many
  requests in that time, and replies 429 after that.
  `server.limit("/login", 10, minutes(1))` limits only paths starting there.
- Request bodies are limited to 1 MB; `server.bodyLimit(megabytes)` changes
  this.
- `GET /health` replies `{"ok":true}` unless a route uses that path.

**Templates.** `web.render(file, values)` (and optionally a status) fills in an
HTML template from a folder next to the program:

```
<h1>{title}</h1>
{each p in products}
  <li>{p.name}: {fixed(p.price, 2)}</li>
{end}
{when user is nothing}<a href="/login">Sign in</a>{otherwise}Hi {user}{end}
{use "views/footer.html"}
```

- `{expression}` is any Laser Feet expression over the values (a map or an
  object). Its value is always escaped for HTML; in an attribute that holds a
  link (`href`, `src`, `action`), `javascript:` links are replaced.
  `web.trusted(html)` marks HTML that the program made itself.
- `{each x in list} ... {end}`, `{when ...} ... {otherwise when ...} ...
  {otherwise} ... {end}` and `{use "other.html"}` (another template, with the
  same values).
- Braces that don't hold an expression (CSS, JSON) stay as written; `\{` is
  always a brace. A tag alone on its line leaves no blank line.
- `lf check` reads every template named in the code and reports its mistakes,
  with the template's line number.

**Running.** `server.start()` serves; each request is logged as one line of
JSON on standard error (`server.quiet()` turns this off). Ctrl-C or a stop
signal lets requests in progress finish (for up to 10 seconds) before the
program ends. `server.visit(path, method, body, headers)` calls the server
without a network, for tests; everything above applies to visits too.

## 13. Databases

`use postgres` connects to PostgreSQL with Laser Feet's own driver.

```
db = postgres.open(env("DATABASE_URL"))
rows = db.query("select name from products where price < $1", [50])
transaction with db as tx {
    tx.run("update products set stock = stock - 1 where sku = $1", ["LAMP-1"])
}
```

- `db.query(sql, values)` gives a list of rows, `db.first(sql, values)` one row
  or `nothing`, and `db.run(sql, values)` the number of rows changed.
- With an object first, rows become checked objects:
  `db.query(Product, sql, values)` gives a `List[Product]` and
  `db.first(Product, sql, values)` a `Product?`. Column names match field names
  without regard to case or underscores (`created_at` fills `createdAt`). A
  missing field, a value of the wrong type, or a column with no field is a
  problem that names it.
- Values are always sent separately from the SQL, never pasted into it.
- `transaction with db as tx { ... }`: everything the block does through `tx`
  happens, or, if anything fails, none of it does. `db.transaction() as tx { }`
  means the same.
- Every statement stops after 30 seconds; `?timeout=10s` in the address
  changes this.
- **Migrations.** `db.migrate("migrations")` (or `lf migrate`) runs each `.sql`
  file of the folder that hasn't run yet, in name order, each in its own
  transaction, and gives the names it ran. A table called `lf_migrations`
  records them with a checksum; a file that changed after it ran is refused.
  Two copies of a program can't migrate at the same time.

## 14. Programs and files

- `use name` brings in a library: `web`, `store`, `json`, `files`, `time`,
  `postgres`, `auth` and `log`.
- `auth.hash(password)` hashes a password with Argon2id (19 MiB, 2 passes) into
  text to store; `auth.check(password, hash)` gives `yes` when they match,
  comparing in constant time; `auth.token()` gives 32 random bytes as URL-safe
  text.
- `log.info(message, details)`, `log.warn` and `log.error` write one line of
  JSON to standard error.
- `lf run`, `lf test` and `lf build` read settings from a `.env` file next to the
  program (`NAME=value` lines); settings already in the environment win. `time` has `now()`, `today()`, `at(text)` and `seconds()` (seconds
  since 1970, for measuring).
- `use "file.lf"` brings in the beams, objects and rules of another file, under
  the file's name (`use "tools.lf"` gives `tools.slug(...)`).
- `use "host/owner/repo"` brings in a package installed with `lf get`, checked
  against `sole.lock` before every run.
- A package may have its own `sole.list` for the packages it needs; `lf get`
  installs those too. A project has each package once: when two ask for
  different `vX.Y.Z` versions of a package, the newer is used, and any other
  difference (a branch, a commit) is a conflict `lf get` explains.
  `sole.lock` pins every package installed, including the ones packages
  need, and a package's `sole.list` is part of the code its SHA-256 covers.

## 15. Grammar

In this EBNF, `{ x }` means zero or more, `[ x ]` means optional, and quoted
words are written as shown. `NL` is a line break.

```ebnf
program     = { statement NL } ;
block       = "{" { statement NL } "}" ;

statement   = "launch" block
            | "show" expr
            | "send" [ expr ]
            | "lock" NAME [ ":" type ] "=" expr
            | NAME ":" type "=" expr
            | target "=" expr
            | [ "private" ] beamDecl | objectDecl | ruleDecl | choiceDecl
            | "focus" expr "{" { arm NL } "}"
            | "when" expr block { "otherwise" "when" expr block } [ "otherwise" block ]
            | "each" NAME "in" expr block
            | "loop" "while" expr block
            | "loop" expr "times" block
            | "stop" | "skip"
            | "fast" block
            | "attempt" block "fails" [ NAME [ ":" type ] ] block { "fails" [ NAME [ ":" type ] ] block }
            | "fail" expr
            | "use" ( NAME | TEXT )
            | "test" TEXT block
            | "expect" expr
            | "transaction" "with" expr "as" NAME block
            | expr ;
target      = NAME | postfix "." NAME | postfix "[" expr "]" ;

beamDecl    = "beam" NAME [ typeParams ] "(" [ param { "," param } ] ")" [ "gives" type ] block ;
typeParams  = "[" NAME { "," NAME } "]" ;      (* names start with a capital letter *)
param       = NAME ":" type ;
objectDecl  = "object" NAME [ typeParams ] [ "follows" NAME { "," NAME } ] "{" { member NL } "}" ;
member      = [ "private" ] ( NAME ":" type [ "=" expr ] | beamDecl ) ;
choiceDecl  = "choice" NAME [ typeParams ] "{" option { ( "," | NL ) option } "}" ;
option      = NAME [ "(" param { "," param } ")" ] ;
arm         = ( NAME [ "(" NAME { "," NAME } ")" ] | or | "otherwise" ) block ;
ruleDecl    = "rule" NAME "{" { "beam" NAME "(" [ param { "," param } ] ")" [ "gives" type ] NL } "}" ;

type        = NAME [ "[" type { "," type } "]" ] [ "?" ]
            | "Beam" "(" [ type { "," type } ] ")" [ "gives" type ] [ "?" ] ;

expr        = or { "then" postfix } ;          (* see section 4 for the bracket rule *)
or          = and { "or" and } ;
and         = not { "and" not } ;
not         = "not" not | equality ;
equality    = comparison { ( "is" [ "not" ] | "==" | "!=" ) comparison } ;
comparison  = range { ( "<" | ">" | "<=" | ">=" ) range } ;
range       = additive [ "to" additive ] ;
additive    = term { ( "+" | "-" ) term } ;
term        = unary { ( "*" | "/" | "%" ) unary } ;
unary       = "-" unary
            | "fast" "each" NAME "in" or block
            | "fast" unary
            | "wait" postfix [ "for" additive ( "seconds" | "second" ) ]
            | postfix ;
postfix     = primary { call | "." NAME | "[" expr "]" } ;
call        = "(" [ arg { "," arg } ] ")" [ [ "as" NAME ] block ] ;
arg         = [ NAME ":" ] expr ;
primary     = NUMBER | TEXT | "yes" | "no" | "nothing" | "me"
            | NAME [ "=>" or ]
            | "beam" [ "(" [ NAME [ ":" type ] { "," NAME [ ":" type ] } ] ")" ] [ "gives" type ] block
            | "(" expr ")"
            | "[" [ expr { "," expr } ] "]"
            | "{" [ key ":" expr { "," key ":" expr } ] "}" ;
key         = NAME | TEXT | keyword ;
```

A block directly after a call's `)` is passed to the call as a beam, except
where a block is already expected: after the condition of `when`, `each`,
`loop` and `fast each`, after the value of `focus` and its arms, and after the
database of `transaction with`. In a focus arm, a name followed by `{` or `(`
is an option.

## 16. Layout (lf fmt)

`lf fmt` is the standard layout. It changes only spaces and blank lines, and
checks that the code reads exactly the same afterwards.

- Four spaces per level. A line that leaves brackets open indents the lines
  after it by one level, however many brackets it opens.
- One space around operators and after commas; none inside `( )` and `[ ]`,
  before `,` `.` `:` `?`, or between a name and its `(` or `[`.
- A minus that makes a value negative touches it: `-5`.
- At most one blank line in a row; none at the start or end of a file, after an
  opening `{` or before a closing `}`.
- Comments stay where they are. Spaces before an end-of-line comment are kept,
  so comments can line up.
- Text is never changed.

`lf fmt --check` changes nothing and fails if a file isn't laid out this way.
The examples, packages and conformance suite are all checked in CI.
