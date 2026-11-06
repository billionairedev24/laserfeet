# Security

Security is a first-class part of Laser Feet. This page explains how releases are protected, how to check a download, and how to report a problem.

## Reporting a problem

Please don't open a public issue for a security problem. Use GitHub's **Report a vulnerability** button on the Security tab of this repository, so the report stays private until a fix is ready. You'll get a reply within 3 working days.

## Checking a download

Every release includes:

- **SHA256SUMS**, the SHA-256 checksum of every file.
- **Signed build provenance**: a GitHub attestation proving each file was built by this repository's release workflow from a specific commit.

Check both before running `lf`:

```
sha256sum --check --ignore-missing SHA256SUMS
gh attestation verify lf-linux-amd64 --repo OWNER/laserfeet
```

On a Mac, use `shasum -a 256 --check --ignore-missing SHA256SUMS`. On Windows, compare the output of `Get-FileHash lf-windows-amd64.exe` with the line in SHA256SUMS.

## How the build is protected

| Protection | What it stops |
|---|---|
| No third-party Go packages; only Go's standard library | Malicious or compromised dependencies |
| GitHub Actions pinned to exact commit IDs | A moved or hijacked action tag changing the build |
| Workflows get read-only access by default; only the release job can write | A compromised step pushing code or releases |
| Reproducible builds (`-trimpath`, fixed build ID, no cgo) | Hidden changes: anyone can rebuild a tag and get identical checksums |
| `govulncheck` on every push | Shipping with a known vulnerability in Go or the code |
| Dependabot updates for the pinned actions | Pins going stale |
| Tests with the race detector on every push | Concurrency bugs |

## How the website is protected

The playground page has the SHA-256 checksum of the Laser Feet engine (`laserfeet.wasm`) built into it. It downloads the engine, checks the checksum, and refuses to run it if the file was changed. Playground programs are also stopped after 5 million steps so a runaway loop can't freeze the browser.

## How Laser Feet web apps are protected by default

| Default | What it stops |
|---|---|
| Plain text replies are always sent as `text/plain` | Visitor input being injected into a page as HTML (XSS). Send HTML on purpose with `web.page(...)` and clean visitor input with `web.escape(...)` |
| `X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`, `Cross-Origin-Opener-Policy` headers | Content sniffing, clickjacking, leaking URLs to other sites |
| Request bodies limited to 1 MB, headers to 64 KB | Memory exhaustion |
| Read, write, header and idle timeouts | Slow-connection (Slowloris) attacks |
| Errors show visitors only "Something went wrong" | Leaking passwords, paths or internals in error messages. Details go to the server log |

## Data and files

| Default | What it stops |
|---|---|
| Store and `files.write` files are written to a temporary file and renamed into place | Half-written, corrupted data after a crash |
| Store files can only be read by their owner (`0600`) | Other users on the machine reading your app's data |
| `use "file.lf"` only accepts `.lf` files inside the program's folder, and refuses loops | Loading code from unexpected places |
| Built programs check a SHA-256 of the code inside them before running | Running a program that was changed or damaged after it was built |

## Packages

| Protection | What it stops |
|---|---|
| `sole.lock` pins each package to a commit and a SHA-256 of its code | Moved tags, rewritten branches, or changed code reaching your program |
| Packages are checked against the lock before every run, test and build | Code edited after installation |
| Installing never runs code; only `.lf` files are copied; symbolic links are refused | Install-time attacks |
| git is run HTTPS-only, without hooks or password prompts | Unexpected transports and credential prompts |
| A package can only use its own files and other packages | A package reading your project's code |

## Databases

| Default | What it stops |
|---|---|
| Values are sent separately from SQL, and one call runs one statement | SQL injection through values |
| Remote databases: TLS with certificate and hostname checks (`verify-full`); `prefer` is refused | Eavesdropping and fake servers |
| SCRAM-SHA-256 login with the server's proof checked; plain passwords only over TLS | Password theft and impersonation |
| 10 s connection timeout, 64 MB message limit | Hanging or flooding a program |

These are checked by `lang/security_test.go`, `lang/stdlib_test.go` and `cmd/lf/bundle_test.go` on every push.
