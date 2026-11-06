"""Builds index.html for the Laser Feet website from template.html and the examples folder."""
import html, json, re, pathlib

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
    ("web.lf", "Product API", "A small web API with routes, path values and JSON replies.", False),
    ("shop_test.lf", "Shopping cart tests", "Tests for cart totals and discounts.", True),
    ("webapp_test.lf", "Web app tests", "Testing routes without opening a browser.", True),
]
examples = [{"file": f, "title": t, "about": a, "test": test, "src": (ex / f).read_text()} for f, t, a, test in meta]

page = (here / "template.html").read_text()
page = re.sub(r"<lf-code([^>]*)>(.*?)</lf-code>",
              lambda m: f'<pre class="lf"{m.group(1)}>{html.escape(m.group(2), quote=False)}</pre>',
              page, flags=re.S)
page = page.replace("/*EXAMPLES*/", json.dumps(examples).replace("</", "<\\/"))
page = page.replace("/*WASM_EXEC*/", (here / "wasm_exec.js").read_text())
(here / "index.html").write_text(page)
print("built index.html", len(page), "bytes")
