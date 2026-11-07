// Laser Feet for VS Code: connects the editor to `lf lsp`, the Laser Feet
// language server, for live errors, autocomplete, hover, go to definition,
// references, rename, the outline and signature help. No third-party
// packages: it speaks the Language Server Protocol directly over stdin/stdout.
"use strict";

const vscode = require("vscode");
const { spawn } = require("child_process");

const LANG = "laserfeet";

class LanguageClient {
  constructor(command, output) {
    this.command = command;
    this.output = output;
    this.nextId = 1;
    this.pending = new Map();
    this.buffer = Buffer.alloc(0);
    this.handlers = new Map();
    this.ready = null;
  }

  start() {
    this.proc = spawn(this.command, ["lsp"], { stdio: ["pipe", "pipe", "pipe"], shell: false });
    this.proc.stdout.on("data", (chunk) => this.receive(chunk));
    this.proc.stderr.on("data", (d) => this.output.appendLine(String(d).trim()));
    const failed = new Promise((_, reject) => {
      this.proc.on("error", reject);
      this.proc.on("exit", (code) => {
        for (const { reject: r } of this.pending.values()) r(new Error("lf lsp stopped"));
        this.pending.clear();
        reject(new Error("lf lsp stopped (exit code " + code + ")"));
      });
    });
    this.ready = Promise.race([
      this.request("initialize", { processId: process.pid, rootUri: null, capabilities: {} }),
      failed,
    ]).then(() => this.notify("initialized", {}));
    this.ready.catch(() => {});
    return this.ready;
  }

  stop() {
    if (!this.proc) return;
    try {
      this.request("shutdown", null).finally(() => this.notify("exit", null));
    } catch (e) { /* already gone */ }
    setTimeout(() => this.proc && this.proc.kill(), 1000);
  }

  write(message) {
    if (!this.proc || !this.proc.stdin.writable) return;
    const body = Buffer.from(JSON.stringify(Object.assign({ jsonrpc: "2.0" }, message)), "utf8");
    this.proc.stdin.write("Content-Length: " + body.length + "\r\n\r\n");
    this.proc.stdin.write(body);
  }

  request(method, params) {
    const id = this.nextId++;
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject });
      this.write({ id, method, params });
    });
  }

  notify(method, params) {
    this.write({ method, params });
  }

  on(method, handler) {
    this.handlers.set(method, handler);
  }

  receive(chunk) {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    for (;;) {
      const headerEnd = this.buffer.indexOf("\r\n\r\n");
      if (headerEnd < 0) return;
      const header = this.buffer.slice(0, headerEnd).toString("ascii");
      const match = /Content-Length:\s*(\d+)/i.exec(header);
      if (!match) { this.buffer = Buffer.alloc(0); return; }
      const length = Number(match[1]);
      if (this.buffer.length < headerEnd + 4 + length) return;
      const body = this.buffer.slice(headerEnd + 4, headerEnd + 4 + length).toString("utf8");
      this.buffer = this.buffer.slice(headerEnd + 4 + length);
      let msg;
      try { msg = JSON.parse(body); } catch (e) { continue; }
      if (msg.id !== undefined && this.pending.has(msg.id)) {
        const { resolve, reject } = this.pending.get(msg.id);
        this.pending.delete(msg.id);
        if (msg.error) reject(new Error(msg.error.message)); else resolve(msg.result);
      } else if (msg.method && this.handlers.has(msg.method)) {
        this.handlers.get(msg.method)(msg.params);
      }
    }
  }
}

const COMPLETION_KINDS = { 2: "Method", 3: "Function", 5: "Field", 6: "Variable", 7: "Class", 9: "Module", 14: "Keyword", 15: "Snippet" };

function activate(context) {
  const output = vscode.window.createOutputChannel("Laser Feet");
  const diagnostics = vscode.languages.createDiagnosticCollection(LANG);
  // The setting can only come from the user's own settings (its scope is
  // "machine"), so a downloaded project can't point it at another program.
  const command = vscode.workspace.getConfiguration("laserfeet").get("path") || "lf";
  const client = new LanguageClient(command, output);

  client.on("textDocument/publishDiagnostics", (p) => {
    const uri = vscode.Uri.parse(p.uri);
    diagnostics.set(uri, p.diagnostics.map((d) => {
      const range = new vscode.Range(d.range.start.line, d.range.start.character, d.range.end.line, d.range.end.character);
      const diag = new vscode.Diagnostic(range, d.message, vscode.DiagnosticSeverity.Error);
      diag.source = "Laser Feet";
      return diag;
    }));
  });

  client.start().then(() => {
    vscode.workspace.textDocuments.forEach(open);
  }, (err) => {
    vscode.window.showWarningMessage(
      "Laser Feet: couldn't start '" + command + " lsp' (" + err.message + "). Colors still work; for live errors and autocomplete, install lf or set laserfeet.path in your user settings.");
  });

  const timers = new Map();
  function open(doc) {
    if (doc.languageId !== LANG) return;
    client.notify("textDocument/didOpen", { textDocument: { uri: doc.uri.toString(), languageId: LANG, version: doc.version, text: doc.getText() } });
  }
  function change(e) {
    const doc = e.document;
    if (doc.languageId !== LANG) return;
    const key = doc.uri.toString();
    clearTimeout(timers.get(key));
    // wait for a short pause in typing before checking
    timers.set(key, setTimeout(() => {
      client.notify("textDocument/didChange", { textDocument: { uri: key, version: doc.version }, contentChanges: [{ text: doc.getText() }] });
    }, 150));
  }

  context.subscriptions.push(
    output, diagnostics,
    vscode.workspace.onDidOpenTextDocument(open),
    vscode.workspace.onDidChangeTextDocument(change),
    vscode.workspace.onDidSaveTextDocument((doc) => {
      if (doc.languageId === LANG) client.notify("textDocument/didSave", { textDocument: { uri: doc.uri.toString() } });
    }),
    vscode.workspace.onDidCloseTextDocument((doc) => {
      if (doc.languageId !== LANG) return;
      client.notify("textDocument/didClose", { textDocument: { uri: doc.uri.toString() } });
      diagnostics.delete(doc.uri);
    }),
    vscode.languages.registerCompletionItemProvider(LANG, {
      async provideCompletionItems(doc, pos) {
        flush(doc);
        const items = await client.request("textDocument/completion", { textDocument: { uri: doc.uri.toString() }, position: { line: pos.line, character: pos.character } }).catch(() => []);
        return (items || []).map((it) => {
          const item = new vscode.CompletionItem(it.label, vscode.CompletionItemKind[COMPLETION_KINDS[it.kind] || "Text"]);
          if (it.detail) item.detail = it.detail;
          return item;
        });
      },
    }, "."),
    vscode.languages.registerHoverProvider(LANG, {
      async provideHover(doc, pos) {
        const h = await client.request("textDocument/hover", { textDocument: { uri: doc.uri.toString() }, position: { line: pos.line, character: pos.character } }).catch(() => null);
        if (!h || !h.contents) return null;
        return new vscode.Hover(new vscode.MarkdownString(h.contents.value));
      },
    }),
    vscode.languages.registerDocumentFormattingEditProvider(LANG, {
      // lf fmt: works with "Format Document" and with editor.formatOnSave
      async provideDocumentFormattingEdits(doc) {
        flush(doc);
        const edits = await client.request("textDocument/formatting", { textDocument: { uri: doc.uri.toString() }, options: { tabSize: 4, insertSpaces: true } }).catch(() => []);
        return (edits || []).map((e) => vscode.TextEdit.replace(
          new vscode.Range(e.range.start.line, e.range.start.character, e.range.end.line, e.range.end.character), e.newText));
      },
    }),
    vscode.languages.registerDefinitionProvider(LANG, {
      async provideDefinition(doc, pos) {
        flush(doc);
        const loc = await client.request("textDocument/definition", at(doc, pos)).catch(() => null);
        return loc ? location(loc) : null;
      },
    }),
    vscode.languages.registerReferenceProvider(LANG, {
      async provideReferences(doc, pos) {
        flush(doc);
        const locs = await client.request("textDocument/references", at(doc, pos)).catch(() => []);
        return (locs || []).map(location);
      },
    }),
    vscode.languages.registerRenameProvider(LANG, {
      async provideRenameEdits(doc, pos, newName) {
        flush(doc);
        // a refusal (a keyword, or a name made in another file) is shown to the person
        const r = await client.request("textDocument/rename", Object.assign(at(doc, pos), { newName }));
        const edit = new vscode.WorkspaceEdit();
        for (const [uri, edits] of Object.entries((r && r.changes) || {})) {
          for (const e of edits) edit.replace(vscode.Uri.parse(uri), range(e.range), e.newText);
        }
        return edit;
      },
    }),
    vscode.languages.registerSignatureHelpProvider(LANG, {
      async provideSignatureHelp(doc, pos) {
        flush(doc);
        const h = await client.request("textDocument/signatureHelp", at(doc, pos)).catch(() => null);
        if (!h || !h.signatures || !h.signatures.length) return null;
        const help = new vscode.SignatureHelp();
        help.signatures = h.signatures.map((sig) => {
          const info = new vscode.SignatureInformation(sig.label);
          info.parameters = (sig.parameters || []).map((p) => new vscode.ParameterInformation(p.label));
          return info;
        });
        help.activeSignature = h.activeSignature || 0;
        help.activeParameter = h.activeParameter || 0;
        return help;
      },
    }, "(", ","),
    vscode.languages.registerDocumentSymbolProvider(LANG, {
      async provideDocumentSymbols(doc) {
        flush(doc);
        const syms = await client.request("textDocument/documentSymbol", { textDocument: { uri: doc.uri.toString() } }).catch(() => []);
        const make = (s) => {
          const ds = new vscode.DocumentSymbol(s.name, s.detail || "", (s.kind || 13) - 1, range(s.range), range(s.selectionRange));
          ds.children = (s.children || []).map(make);
          return ds;
        };
        return (syms || []).map(make);
      },
    }),
    { dispose: () => client.stop() }
  );

  function at(doc, pos) {
    return { textDocument: { uri: doc.uri.toString() }, position: { line: pos.line, character: pos.character } };
  }
  function range(r) {
    return new vscode.Range(r.start.line, r.start.character, r.end.line, r.end.character);
  }
  function location(l) {
    return new vscode.Location(vscode.Uri.parse(l.uri), range(l.range));
  }

  // send any change still waiting, so completion sees the latest text
  function flush(doc) {
    const key = doc.uri.toString();
    if (timers.has(key)) {
      clearTimeout(timers.get(key));
      timers.delete(key);
      client.notify("textDocument/didChange", { textDocument: { uri: key, version: doc.version }, contentChanges: [{ text: doc.getText() }] });
    }
  }
}

function deactivate() {}

module.exports = { activate, deactivate, LanguageClient };
