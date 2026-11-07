// Tests the extension against the real `lf lsp`, with a small stand-in for the
// VS Code API. Run: LF=/path/to/lf node test/extension.test.js
"use strict";
const Module = require("module");
const assert = require("assert");

const state = { diagnostics: new Map(), completion: null, hover: null, format: null, definition: null, references: null, rename: null, symbols: null, warnings: [], listeners: {} };
class Range { constructor(a, b, c, d) { this.start = { line: a, character: b }; this.end = { line: c, character: d }; } }
const fakeVscode = {
  Range,
  Diagnostic: class { constructor(range, message) { this.range = range; this.message = message; } },
  DiagnosticSeverity: { Error: 0 },
  CompletionItem: class { constructor(label, kind) { this.label = label; this.kind = kind; } },
  CompletionItemKind: { Method: 2, Function: 3, Field: 5, Variable: 6, Class: 7, Module: 9, Keyword: 14, Snippet: 15, Text: 1 },
  TextEdit: { replace: (range, newText) => ({ range, newText }) },
  Hover: class { constructor(md) { this.contents = md; } },
  MarkdownString: class { constructor(v) { this.value = v; } },
  Uri: { parse: (s) => ({ toString: () => s }) },
  Location: class { constructor(uri, range) { this.uri = uri; this.range = range; } },
  WorkspaceEdit: class { constructor() { this.edits = []; } replace(uri, range, text) { this.edits.push({ uri: uri.toString(), range, text }); } },
  SignatureHelp: class { constructor() { this.signatures = []; } },
  SignatureInformation: class { constructor(label) { this.label = label; this.parameters = []; } },
  ParameterInformation: class { constructor(label) { this.label = label; } },
  DocumentSymbol: class { constructor(name, detail, kind, range, sel) { Object.assign(this, { name, detail, kind, range, selectionRange: sel, children: [] }); } },
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
    registerDocumentFormattingEditProvider: (lang, p) => { state.format = p; return { dispose() {} }; },
    registerDefinitionProvider: (lang, p) => { state.definition = p; return { dispose() {} }; },
    registerReferenceProvider: (lang, p) => { state.references = p; return { dispose() {} }; },
    registerRenameProvider: (lang, p) => { state.rename = p; return { dispose() {} }; },
    registerDocumentSymbolProvider: (lang, p) => { state.symbols = p; return { dispose() {} }; },
    registerSignatureHelpProvider: (lang, p) => { state.signature = p; return { dispose() {} }; },
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

  const messy = doc("file:///tmp/t.lf", "launch {\nshow 1+2\n}\n");
  messy.version = 3;
  state.listeners.change({ document: messy });
  const edits = await state.format.provideDocumentFormattingEdits(messy);
  assert.ok(edits.length === 1 && edits[0].newText === "launch {\n    show 1 + 2\n}\n", "format document uses lf fmt, got " + JSON.stringify(edits));

  const nav = doc("file:///tmp/t.lf", "beam twice(n: Int) gives Int {\n    send n * 2\n}\n\nlaunch {\n    x = twice(4)\n    show twice(x)\n}\n");
  nav.version = 4;
  state.listeners.change({ document: nav });
  const def = await state.definition.provideDefinition(nav, { line: 5, character: 9 });
  assert.ok(def && def.range.start.line === 0 && def.range.start.character === 5, "go to definition finds beam twice, got " + JSON.stringify(def));
  const refs = await state.references.provideReferences(nav, { line: 0, character: 6 });
  assert.strictEqual(refs.length, 3, "twice appears 3 times");
  const ren = await state.rename.provideRenameEdits(nav, { line: 5, character: 4 }, "y");
  assert.strictEqual(ren.edits.length, 2, "renaming x changes 2 places");
  await assert.rejects(state.rename.provideRenameEdits(nav, { line: 5, character: 4 }, "show"), /can't be a name/);
  const sig = await state.signature.provideSignatureHelp(nav, { line: 6, character: 15 });
  assert.ok(sig && sig.signatures[0].label === "twice(n: Int) gives Int", "signature help shows twice's inputs, got " + JSON.stringify(sig));
  const syms = await state.symbols.provideDocumentSymbols(nav);
  assert.ok(syms.some((s) => s.name === "twice"), "the outline has twice");

  subs.forEach((s) => s.dispose && s.dispose());
  console.log("VS Code extension: live errors, completion, hover, formatting, definition, references, rename, outline and signature help all work");
  setTimeout(() => process.exit(0), 200);
})().catch((e) => { console.error(e.message); process.exit(1); });
