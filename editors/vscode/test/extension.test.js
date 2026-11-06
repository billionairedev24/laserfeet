// Tests the extension against the real `lf lsp`, with a small stand-in for the
// VS Code API. Run: LF=/path/to/lf node test/extension.test.js
"use strict";
const Module = require("module");
const assert = require("assert");

const state = { diagnostics: new Map(), completion: null, hover: null, warnings: [], listeners: {} };
class Range { constructor(a, b, c, d) { this.start = { line: a, character: b }; this.end = { line: c, character: d }; } }
const fakeVscode = {
  Range,
  Diagnostic: class { constructor(range, message) { this.range = range; this.message = message; } },
  DiagnosticSeverity: { Error: 0 },
  CompletionItem: class { constructor(label, kind) { this.label = label; this.kind = kind; } },
  CompletionItemKind: { Method: 2, Function: 3, Field: 5, Variable: 6, Class: 7, Module: 9, Keyword: 14, Snippet: 15, Text: 1 },
  Hover: class { constructor(md) { this.contents = md; } },
  MarkdownString: class { constructor(v) { this.value = v; } },
  Uri: { parse: (s) => ({ toString: () => s }) },
  window: {
    createOutputChannel: () => ({ appendLine() {}, dispose() {} }),
    showWarningMessage: (m) => state.warnings.push(m),
  },
  workspace: {
    getConfiguration: () => ({ get: () => process.env.LF || "lf" }),
    textDocuments: [],
    onDidOpenTextDocument: (f) => { state.listeners.open = f; return { dispose() {} }; },
    onDidChangeTextDocument: (f) => { state.listeners.change = f; return { dispose() {} }; },
    onDidSaveTextDocument: () => ({ dispose() {} }),
    onDidCloseTextDocument: () => ({ dispose() {} }),
  },
  languages: {
    createDiagnosticCollection: () => ({ set: (uri, d) => state.diagnostics.set(uri.toString(), d), delete() {}, dispose() {} }),
    registerCompletionItemProvider: (lang, p) => { state.completion = p; return { dispose() {} }; },
    registerHoverProvider: (lang, p) => { state.hover = p; return { dispose() {} }; },
  },
};
const realLoad = Module._load;
Module._load = function (request, ...rest) { return request === "vscode" ? fakeVscode : realLoad.call(this, request, ...rest); };

const ext = require("../extension.js");

function doc(uri, text) {
  return { uri: { toString: () => uri }, languageId: "laserfeet", version: 1, getText: () => text };
}
const wait = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const subs = [];
  ext.activate({ subscriptions: subs });
  await wait(500);
  assert.deepStrictEqual(state.warnings, [], "lf lsp should start: " + state.warnings.join());

  const text = 'object User {\n    name: Text\n    age: Int\n}\n\nlaunch {\n    ada = User("Ada", 36)\n    show ada.nmae\n}\n';
  const d = doc("file:///tmp/t.lf", text);
  state.listeners.open(d);
  await wait(400);
  const diags = state.diagnostics.get("file:///tmp/t.lf") || [];
  assert.ok(diags.some((x) => /Did you mean 'name'/.test(x.message)), "live error expected, got " + JSON.stringify(diags.map((x) => x.message)));
  assert.strictEqual(diags[0].range.start.line, 7, "the error is on line 8");

  const items = await state.completion.provideCompletionItems(d, { line: 7, character: 9 });
  const labels = items.map((i) => i.label);
  assert.ok(labels.includes("ada") && labels.includes("launch"), "names and keywords expected, got " + labels.slice(0, 10));

  const typing = doc("file:///tmp/t.lf", text.replace("show ada.nmae", "show ada."));
  typing.version = 2;
  state.listeners.change({ document: typing });
  const fields = (await state.completion.provideCompletionItems(typing, { line: 7, character: 13 })).map((i) => i.label);
  assert.deepStrictEqual(fields.sort(), ["age", "name"], "fields of User after the dot");

  const hover = await state.hover.provideHover(typing, { line: 6, character: 5 });
  assert.ok(hover && /ada: User/.test(hover.contents.value), "hover shows the type");

  subs.forEach((s) => s.dispose && s.dispose());
  console.log("VS Code extension: live errors, completion and hover all work");
  setTimeout(() => process.exit(0), 200);
})().catch((e) => { console.error(e.message); process.exit(1); });
