"""Builds index.html for the Laser Feet website from template.html and the examples folder."""
import base64, hashlib, html, json, re, pathlib

here = pathlib.Path(__file__).parent
ex = here.parent / "examples"
meta = [
    ("hello.lf", "Hello, world", "The smallest Laser Feet program.", False),
    ("values.lf", "Values", "Names, locked values, text with {braces}, lists and maps.", False),
    ("beams.lf", "Beams", "Functions with send, named inputs, recursion and one-line beams.", False),
    ("objects.lf", "Objects and rules", "A User object that follows the Greeter rule.", False),
    ("decisions.lf", "Decisions and loops", "when, otherwise, each, skip, stop, loop while and loop times.", False),
    ("chaining.lf", "Chaining with then", "Filter, sort and reshape a list of users in one line.", False),
    ("errors.lf", "Handling problems", "attempt and fails keep a program running when something breaks.", False),
    ("fast.lf", "Fast work", "Two jobs running at the same time.", False),
    ("jobs.lf", "Jobs and fast each", "Start work with fast, collect it with wait and fast each, with errors and time limits.", False),
    ("web.lf", "Product API", "A small web API with routes, path values and JSON replies.", False),
    ("shop_test.lf", "Shopping cart tests", "Tests for cart totals and discounts.", True),
    ("todo/app.lf", "To-do API", "A real web API: saves data, reads JSON, checks input with rules from checks.lf, and tests every route.", True),
    ("webapp_test.lf", "Web app tests", "Testing routes without opening a browser.", True),
]
def extra_files(f):
    # other .lf files in the same folder, for examples that 'use' them
    folder = (ex / f).parent
    if folder == ex:
        return {}
    return {p.name: p.read_text() for p in folder.glob("*.lf") if p.name != pathlib.Path(f).name}

examples = [{"file": f, "title": t, "about": a, "test": test, "src": (ex / f).read_text(), "files": extra_files(f)}
            for f, t, a, test in meta]

page = (here / "template.html").read_text()
page = re.sub(r"<lf-code([^>]*)>(.*?)</lf-code>",
              lambda m: f'<pre class="lf"{m.group(1)}>{html.escape(m.group(2), quote=False)}</pre>',
              page, flags=re.S)
page = page.replace("/*EXAMPLES*/", json.dumps(examples).replace("</", "<\\/"))
# The page refuses to run the engine unless its SHA-256 matches this build.
wasm_hash = base64.b64encode(hashlib.sha256((here / "laserfeet.wasm").read_bytes()).digest()).decode()
page = page.replace("/*WASM_SHA256*/", json.dumps(wasm_hash))
page = page.replace("/*WASM_EXEC*/", (here / "wasm_exec.js").read_text())
# index.html is the page body used for the Claude artifact (it adds its own <head>).
(here / "index.html").write_text(page)
# public/index.html is a complete standalone page for GitHub Pages or any web host.
standalone = ('<!doctype html>\n<html lang="en">\n<head>\n<meta charset="utf-8">\n'
              '<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">\n'
              '<style>:root{padding-top:env(safe-area-inset-top,0px);padding-bottom:env(safe-area-inset-bottom,0px)}'
              'body{margin:0}img{max-width:100%}[hidden]{display:none!important}</style>\n'
              '</head>\n<body>\n' + page + '\n</body>\n</html>\n')
(here / "public").mkdir(exist_ok=True)
(here / "public" / "index.html").write_text(standalone)
print("built index.html and public/index.html", len(page), "bytes")
