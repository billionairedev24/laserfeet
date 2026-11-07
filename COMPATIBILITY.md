# Compatibility

From 1.0, code that works with one Laser Feet 1.x release keeps working with
every later 1.x release. This page says exactly what that covers, and what
can still change.

## What stays the same in 1.x

- **The language**, as written in [the spec](docs/spec.md): keywords, syntax,
  types, the rules for names, and what each statement and expression does. A
  program that `lf check` accepts keeps being accepted and keeps giving the
  same answers.
- **The built-in libraries** (`web`, `store`, `json`, `files`, `time`, `log`,
  `auth`, `postgres`): their names, what they take and what they give.
- **The `lf` commands** and the options they take, the layout `lf fmt` writes,
  and the format of `sole.list` and `sole.lock`.
- **What a web app sends**: security headers, cookie settings and request
  limits only get stricter when a security problem calls for it (see below).

## What may still change

- **Problem messages.** Their words may improve; their meaning won't. Tests
  should check that a problem happens, not its exact words.
- **Speed, memory and how work is spread across cores.** Programs get faster;
  the order in which parallel work finishes was never promised.
- **New things.** New keywords, beams, library members and commands may be
  added. A new keyword never takes a name already in use in a library, and
  `lf fix` renames a name of yours that a new keyword needs.
- **Security fixes.** When the only way to fix a security problem is to
  refuse something that used to be allowed, it will be refused. The release
  notes say so, and `lf fix` updates code where it can.
- **Things the spec calls out as unsettled**, and anything not in the spec.

## Before 1.0

Releases before 1.0 may change the language. Every such change is listed in
the release notes, and `lf fix` rewrites code to match wherever the change can
be made without changing what the code does.

## Packages

A package's own versions follow the same idea: a new `vX.Y.Z` with the same
`X` is meant to keep working for code written for the older one. `lf get -u`
relies on this; a new `X` is only taken when asked for by name.
