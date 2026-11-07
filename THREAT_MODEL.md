# Threat model

What Laser Feet protects, from whom, and how. It covers the `lf` program, the
apps people write with it, packages, and the release process. Each threat
names the defence and the test that checks it. Problems found outside this
model are reported as the [security policy](SECURITY.md) describes.

## What is being protected

| Asset | Why it matters |
|---|---|
| The data a Laser Feet app keeps (its database, store files, sessions) | Customers' records, passwords and money |
| The machine an app or `lf` runs on | Running other people's code there means losing it |
| The code a project runs: its own, its packages, the built program | A changed line can do anything the app can |
| Releases of `lf` itself | Everyone who installs Laser Feet runs them |

## Who might attack

- **Visitors to a web app**, including automated ones: they control every
  byte of a request and can send many at once.
- **A database or service the app talks to** that has been taken over or
  impersonated: it controls every reply.
- **A package author, or someone who takes over a package's repository**: they
  control the package's code and its tags.
- **Someone between a developer and GitHub**, or with access to a release
  workflow: they could swap downloads.
- **Other users on the same machine**: they can read files that aren't private.

Out of scope: the developer's own code doing harm on purpose, a machine
whose administrator is the attacker, and bugs in Go itself (tracked through
`govulncheck` and Go's own releases).

## Trust boundaries

1. **Network → app**: requests are untrusted until a route has checked them.
2. **App → database and other services**: replies are untrusted input too.
3. **Package → project**: a package is someone else's code, pinned and checked,
   but it runs with the app's rights once used.
4. **Parallel work → shared values**: fast jobs, `fast each` items and web
   requests run at the same time and must not corrupt each other.
5. **GitHub → developer**: releases and the installer are checked before use.

## Threats and defences

### Web apps

| Threat | Defence | Checked by |
|---|---|---|
| Visitor input shown as HTML (cross-site scripting) | Text replies are `text/plain`; templates escape every value; `web.escape` | `TestTextIsNeverServedAsHTML`, `TestEscapeInPages`, `FuzzTemplate` |
| Another site making a visitor's browser post a form (CSRF) | With sessions on, posts from another site are refused (`Sec-Fetch-Site` and `Origin` checks); session cookies are `SameSite=Lax`, `HttpOnly`, and `Secure` off localhost | `TestShipmentsEndToEnd` |
| Stolen or forged sessions | Sessions are signed with HMAC-SHA-256 under a secret of at least 32 characters, and expire | `conformance/12-web/sessions` |
| Password theft from a leaked database | Argon2id (RFC 9106 vectors), with a per-password salt | `TestArgon2idRFCVector`, `conformance/12-web/passwords` |
| Guessing passwords or flooding | `server.limit` per visitor; body and header limits; read, write and slow-header (Slowloris) timeouts | `TestStaticFilesAndRateLimits`, `TestLargeBodiesAreRefused` |
| Clickjacking, content sniffing, leaking URLs | `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`, `Cross-Origin-Opener-Policy` on every reply | `TestShipmentsEndToEnd` |
| Error messages revealing secrets or paths | Visitors see "Something went wrong"; details go to the log | `TestErrorsDoNotLeakDetails` |
| Reading files outside a static folder (path traversal) | Static paths are cleaned and must stay inside the folder; hidden files are refused | `TestStaticFilesAndRateLimits` |
| Requests changing each other's data (races) | Parallel work can't change values made outside it, checked by `lf check` and the engine | `TestParallelRequests` with the race detector |
| Watching what an app does | The metrics page is for the machine itself, or for whoever has `METRICS_TOKEN` | `TestTracesAndMetrics` |
| Malformed requests crashing the server | The HTTP handler and the router are fuzzed | `FuzzWebRequests` |

### Databases

| Threat | Defence | Checked by |
|---|---|---|
| SQL injection | Values are always sent apart from the SQL; one call runs one statement | `TestQueriesAgainstARealServer` |
| Eavesdropping or a fake server | TLS with certificate and host checks (`verify-full`) off localhost; `prefer` is refused; SCRAM-SHA-256 with the server's proof checked | `TestTLS`, `TestSCRAMProofsMatchTheStandard`, `FuzzLogin` |
| A hostile server stalling or flooding the app | 10 s connection timeout, statement timeouts, 64 MB message limit | `FuzzServerReplies` |

### Packages

| Threat | Defence | Checked by |
|---|---|---|
| A moved tag or rewritten branch delivering new code | `sole.lock` pins each package to a commit and a SHA-256 of its code (and its own `sole.list`) | `TestPackagesThatNeedPackages` |
| Code changed after installing | Every run, test and build checks installed packages against the lock | `TestPackagesThatNeedPackages` |
| Code running while installing | Installing copies `.lf` files and `sole.list` only; no scripts, no symbolic links | `install` in `cmd/lf/sole.go` (no test of its own yet) |
| A package reading the project's files | A package can use only its own files and other packages | `lang/resolve.go` (no test of its own yet) |
| A known-bad version staying in use | `lf audit` and `lf get` check `advisories/advisories.list` | `TestPackageUpdatesAndAdvisories` |
| Credential prompts or other transports | git runs HTTPS-only, without hooks or prompts | `sole.go` |

### Releases and installing

| Threat | Defence | Checked by |
|---|---|---|
| A swapped download | `SHA256SUMS`, signed build provenance, and the installers checking the checksum | release workflow, `install.sh` |
| A compromised build dependency | No third-party Go packages; GitHub Actions pinned to commits | CI |
| Hidden changes in a release | Reproducible builds: the same source gives the same checksums | `scripts/build.sh` |
| Not knowing what a release contains | An SPDX software bill of materials with every release | `scripts/sbom` |
| A changed built program | `lf build` programs check a SHA-256 of the code inside them before running | `TestBundleTamperingIsRefused` |
| A changed playground engine | The website checks the engine's SHA-256 before running it; programs stop after 5 million steps | site build |

### Files and the machine

| Threat | Defence | Checked by |
|---|---|---|
| Other users reading app data | Store files are `0600` | `TestStoreSavesSafely` |
| Half-written data after a crash | Writes go to a temporary file renamed into place | `TestStoreSavesSafely` |
| `use` loading unexpected code | Only `.lf` files inside the program's folder, no loops | `TestUseFiles` |
| A runaway program | The playground's step limit; the engine's limits on ranges, text and numbers (overflow is a problem, never a wrap) | `FuzzEngines` |

## Known gaps

- **No outside review yet.** This model and the code have been reviewed by
  the people who wrote them. An independent security review is planned before
  1.0 and its findings will be published here.
- **Packages run with the app's rights.** Pinning and checksums stop changed
  code, not a package that was harmful from its first version. Read what you
  install.
- **Forwarded addresses aren't trusted.** Behind a proxy, rate limits see the
  proxy's address unless the program reads the forwarding header itself.
- **`fast` work can still use too much memory or time.** There are no limits
  per job yet; a server should run under its platform's memory and CPU limits.
