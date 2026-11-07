package main

// lf doc: documentation made from code. The comment lines just above a beam,
// object, choice or rule describe it; the comment at the top of the file
// describes the file. Private beams and fields are left out: they aren't
// part of what the file shares.

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"

	"laserfeet/lang"
)

type docItem struct {
	Kind, Name, Sig, About string
	Parts                  []docItem // an object's fields and beams, a choice's options
}

type docFile struct {
	Path, About string
	Items       []docItem
}

// commentAbove is the run of -- lines right above line (1-based).
func commentAbove(lines []string, line int) string {
	var got []string
	for i := line - 2; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(t, "--") {
			break
		}
		got = append([]string{strings.TrimSpace(strings.TrimPrefix(t, "--"))}, got...)
	}
	return strings.Join(got, " ")
}

// headerComment is the file's opening comment, up to its first blank line.
func headerComment(lines []string) string {
	var got []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, "--") {
			break
		}
		got = append(got, strings.TrimSpace(strings.TrimPrefix(t, "--")))
	}
	return strings.Join(got, "\n")
}

func beamSig(b *lang.BeamDecl) string {
	var ps []string
	for i, p := range b.Params {
		if b.Sig != nil && i < len(b.Sig.Params) && b.Sig.Params[i] != nil {
			p += ": " + b.Sig.Params[i].String()
		}
		ps = append(ps, p)
	}
	tp := ""
	if b.Sig != nil && len(b.Sig.TypeParams) > 0 {
		tp = "[" + strings.Join(b.Sig.TypeParams, ", ") + "]"
	}
	s := "beam " + b.Name + tp + "(" + strings.Join(ps, ", ") + ")"
	if b.Sig != nil && b.Sig.Result != nil {
		s += " gives " + b.Sig.Result.String()
	}
	return s
}

func fieldSig(f lang.FieldDecl) string {
	s := f.Name
	if f.Type != nil {
		s += ": " + f.Type.String()
	}
	if f.Default != nil {
		s += " (has a default)"
	}
	return s
}

func describeFile(path string) (docFile, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return docFile{}, err
	}
	prog, err := lang.Parse(string(src))
	if err != nil {
		return docFile{}, err
	}
	lines := strings.Split(string(src), "\n")
	df := docFile{Path: path, About: headerComment(lines)}
	for _, st := range prog.Stmts {
		switch d := st.(type) {
		case *lang.BeamDecl:
			if !d.Private {
				df.Items = append(df.Items, docItem{Kind: "beam", Name: d.Name, Sig: beamSig(d), About: commentAbove(lines, d.Line)})
			}
		case *lang.ObjectDecl:
			it := docItem{Kind: "object", Name: d.Name, About: commentAbove(lines, d.Line)}
			it.Sig = "object " + d.Name
			if len(d.TypeParams) > 0 {
				it.Sig += "[" + strings.Join(d.TypeParams, ", ") + "]"
			}
			if len(d.Follows) > 0 {
				it.Sig += " follows " + strings.Join(d.Follows, ", ")
			}
			for _, f := range d.Fields {
				if !f.Private {
					it.Parts = append(it.Parts, docItem{Kind: "field", Name: f.Name, Sig: fieldSig(f), About: commentAbove(lines, f.Line)})
				}
			}
			for _, m := range d.Methods {
				if !m.Private {
					it.Parts = append(it.Parts, docItem{Kind: "beam", Name: m.Name, Sig: beamSig(m), About: commentAbove(lines, m.Line)})
				}
			}
			df.Items = append(df.Items, it)
		case *lang.ChoiceDecl:
			it := docItem{Kind: "choice", Name: d.Name, Sig: "choice " + d.Name, About: commentAbove(lines, d.Line)}
			for _, o := range d.Options {
				sig := o.Name
				if len(o.Fields) > 0 {
					var fs []string
					for _, f := range o.Fields {
						fs = append(fs, fieldSig(f))
					}
					sig += "(" + strings.Join(fs, ", ") + ")"
				}
				it.Parts = append(it.Parts, docItem{Kind: "option", Name: o.Name, Sig: sig, About: commentAbove(lines, o.Line)})
			}
			df.Items = append(df.Items, it)
		case *lang.RuleDecl:
			it := docItem{Kind: "rule", Name: d.Name, Sig: "rule " + d.Name, About: commentAbove(lines, d.Line)}
			for _, m := range d.Sigs {
				it.Parts = append(it.Parts, docItem{Kind: "beam", Name: m.Name, Sig: beamSig(m), About: commentAbove(lines, m.Line)})
			}
			df.Items = append(df.Items, it)
		}
	}
	return df, nil
}

func docMarkdown(files []docFile) string {
	var b strings.Builder
	for i, f := range files {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "# %s\n\n", f.Path)
		if f.About != "" {
			b.WriteString(f.About + "\n\n")
		}
		for _, it := range f.Items {
			fmt.Fprintf(&b, "## %s\n\n```\n%s\n```\n\n", it.Name, it.Sig)
			if it.About != "" {
				b.WriteString(it.About + "\n\n")
			}
			for _, p := range it.Parts {
				fmt.Fprintf(&b, "- `%s`", p.Sig)
				if p.About != "" {
					b.WriteString(": " + p.About)
				}
				b.WriteString("\n")
			}
			if len(it.Parts) > 0 {
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func docHTML(files []docFile) string {
	e := html.EscapeString
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Documentation</title><style>
:root{--bg:#fbfaf7;--fg:#1d1c1a;--muted:#6b6862;--line:#e4e1da;--code:#f1eee7;--accent:#c2410c}
@media (prefers-color-scheme:dark){:root{--bg:#161513;--fg:#ece9e2;--muted:#a19d95;--line:#2e2c28;--code:#211f1c;--accent:#fb923c}}
body{background:var(--bg);color:var(--fg);font:16px/1.6 system-ui,sans-serif;max-width:46rem;margin:0 auto;padding:2rem 1rem}
h1{font-size:1.6rem;border-bottom:1px solid var(--line);padding-bottom:.4rem}h2{font-size:1.15rem;margin-top:2rem;color:var(--accent)}
pre,code{font-family:ui-monospace,Menlo,monospace;font-size:.92em}pre{background:var(--code);padding:.7rem .9rem;border-radius:6px;overflow-x:auto}
code{background:var(--code);padding:.1em .3em;border-radius:4px}li{margin:.3rem 0}.about{color:var(--muted)}
</style></head><body>`)
	for _, f := range files {
		fmt.Fprintf(&b, "<h1>%s</h1>", e(f.Path))
		if f.About != "" {
			fmt.Fprintf(&b, "<p class=about>%s</p>", strings.ReplaceAll(e(f.About), "\n", "<br>"))
		}
		for _, it := range f.Items {
			fmt.Fprintf(&b, "<h2 id=\"%s\">%s</h2><pre>%s</pre>", e(it.Name), e(it.Name), e(it.Sig))
			if it.About != "" {
				fmt.Fprintf(&b, "<p>%s</p>", e(it.About))
			}
			if len(it.Parts) > 0 {
				b.WriteString("<ul>")
				for _, p := range it.Parts {
					fmt.Fprintf(&b, "<li><code>%s</code>", e(p.Sig))
					if p.About != "" {
						fmt.Fprintf(&b, ": %s", e(p.About))
					}
					b.WriteString("</li>")
				}
				b.WriteString("</ul>")
			}
		}
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

// docCommand is lf doc [paths] [--html file]: files, or every .lf file in
// folders (installed packages and tests left out); a package's folder by
// default when there's a package.lf, else the current folder.
func docCommand(args []string) int {
	htmlOut := ""
	var paths []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--html" && i+1 < len(args) {
			htmlOut = args[i+1]
			i++
			continue
		}
		paths = append(paths, args[i])
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var files []docFile
	for _, p := range paths {
		var targets []string
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			entries, _ := os.ReadDir(p)
			for _, en := range entries {
				n := en.Name()
				if !en.IsDir() && strings.HasSuffix(n, ".lf") && !strings.HasSuffix(n, "_test.lf") {
					targets = append(targets, filepath.Join(p, n))
				}
			}
		} else {
			targets = []string{p}
		}
		for _, t := range targets {
			df, err := describeFile(t)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", t, err)
				return 1
			}
			files = append(files, df)
		}
	}
	// package.lf first: it's what users of a package reach
	for i, f := range files {
		if filepath.Base(f.Path) == "package.lf" && i > 0 {
			files[0], files[i] = files[i], files[0]
		}
	}
	if htmlOut != "" {
		if err := os.WriteFile(htmlOut, []byte(docHTML(files)), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("Wrote %s\n", htmlOut)
		return 0
	}
	fmt.Print(docMarkdown(files))
	return 0
}
