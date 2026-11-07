package lang

import (
	"fmt"
	"strings"
)

// Format lays out Laser Feet code the standard way (lf fmt): four spaces per
// level, one space around operators and after commas, at most one blank line in
// a row, and no blank lines just inside braces. It only changes spaces and blank
// lines, never the code itself: the result is checked to read exactly the same.
func Format(src string) (string, error) {
	toks, err := Lex(src, 1)
	if err != nil {
		return "", err
	}
	rs := []rune(src)
	lines := splitLines(rs, toks)
	out := layout(rs, toks, lines)
	if err := sameCode(src, out); err != nil {
		return "", err
	}
	return out, nil
}

// fline is one line of output: code tokens, a whole-line comment, or a blank.
type fline struct {
	toks    []int  // indexes into the tokens
	comment string // a comment at the end of the line, or the whole line
	gap     int    // spaces written before an end-of-line comment
	blank   bool
}

func splitLines(rs []rune, toks []Token) []fline {
	var lines []fline
	cur := &fline{}
	flush := func() {
		if len(cur.toks) > 0 || cur.comment != "" {
			lines = append(lines, *cur)
		}
		cur = &fline{}
	}
	// handleGap reads the spaces, newlines and comments between two tokens.
	handleGap := func(gap []rune) {
		pieces := strings.Split(string(gap), "\n")
		for i, piece := range pieces {
			trimmed := strings.TrimSpace(piece)
			if i > 0 {
				flush()
				if trimmed == "" && i < len(pieces)-1 {
					lines = append(lines, fline{blank: true})
				}
			}
			if strings.HasPrefix(trimmed, "--") {
				cur.comment = strings.TrimRight(trimmed, " \t\r")
				lead := piece[:strings.Index(piece, "--")]
				cur.gap = len(strings.ReplaceAll(lead, "\t", "    "))
				if cur.gap < 1 {
					cur.gap = 1
				}
			}
		}
	}
	prev := 0
	for i, t := range toks {
		if t.Type == NEWLINE || t.Type == EOF {
			continue
		}
		handleGap(rs[prev:t.Start])
		cur.toks = append(cur.toks, i)
		prev = t.End
	}
	handleGap(rs[prev:])
	flush()
	return lines
}

func isOpener(t Token) bool {
	return t.Type == SYMBOL && (t.Text == "{" || t.Text == "(" || t.Text == "[")
}
func isCloser(t Token) bool {
	return t.Type == SYMBOL && (t.Text == "}" || t.Text == ")" || t.Text == "]")
}

func layout(rs []rune, toks []Token, lines []fline) string {
	// work out each line's indent: one level for every line that leaves
	// brackets open, however many it opens
	var levels []int // unclosed brackets held by each level
	type outLine struct {
		indent  int
		text    string
		blank   bool
		opens   bool // ends with an opening bracket
		closes  bool // starts with a closing bracket
		comment bool // a whole-line comment
	}
	var outs []outLine
	for _, l := range lines {
		if l.blank {
			outs = append(outs, outLine{blank: true})
			continue
		}
		if len(l.toks) == 0 { // a comment on its own line
			outs = append(outs, outLine{indent: len(levels), text: l.comment, comment: true})
			continue
		}
		closeOne := func() {
			if n := len(levels); n > 0 {
				levels[n-1]--
				if levels[n-1] == 0 {
					levels = levels[:n-1]
				}
			}
		}
		k := 0
		for k < len(l.toks) && isCloser(toks[l.toks[k]]) {
			closeOne()
			k++
		}
		indent := len(levels)
		open := 0
		for _, ti := range l.toks[k:] {
			switch t := toks[ti]; {
			case isOpener(t):
				open++
			case isCloser(t):
				if open > 0 {
					open--
				} else {
					closeOne()
				}
			}
		}
		if open > 0 {
			levels = append(levels, open)
		}
		var sb strings.Builder
		for j, ti := range l.toks {
			if j > 0 {
				prev := l.toks[j-1]
				if spaceBetween(rs, toks, prev, ti) {
					sb.WriteByte(' ')
				}
			}
			sb.WriteString(string(rs[toks[ti].Start:toks[ti].End]))
		}
		if l.comment != "" {
			sb.WriteString(strings.Repeat(" ", l.gap))
			sb.WriteString(l.comment)
		}
		last := toks[l.toks[len(l.toks)-1]]
		outs = append(outs, outLine{indent: indent, text: sb.String(), opens: isOpener(last) && l.comment == "", closes: k > 0})
	}
	// blank lines: none at the start or end, none just inside braces, never two in a row
	var sb strings.Builder
	wrote := false
	for i, o := range outs {
		if o.blank {
			if !wrote || outs[i-1].blank || outs[i-1].opens {
				continue
			}
			next := i + 1
			for next < len(outs) && outs[next].blank {
				next++
			}
			if next == len(outs) || outs[next].closes {
				continue
			}
			sb.WriteByte('\n')
			continue
		}
		sb.WriteString(strings.Repeat("    ", o.indent))
		sb.WriteString(o.text)
		sb.WriteByte('\n')
		wrote = true
	}
	return sb.String()
}

// spaceBetween decides whether two tokens on the same line get a space between them.
func spaceBetween(rs []rune, toks []Token, ai, bi int) bool {
	a, b := toks[ai], toks[bi]
	sym := func(t Token, s ...string) bool {
		if t.Type != SYMBOL {
			return false
		}
		for _, x := range s {
			if t.Text == x {
				return true
			}
		}
		return false
	}
	wordLike := func(t Token) bool {
		return t.Type == IDENT || t.Type == STRING || t.Type == NUMBER || sym(t, ")", "]", "?") ||
			t.Type == KEYWORD && (t.Text == "me" || t.Text == "nothing" || t.Text == "yes" || t.Text == "no")
	}
	switch {
	case sym(b, ",", ".", ")", "]", ":", "?"):
		// "?" only follows a type name: Text?
		return false
	case sym(a, ".", "(", "["):
		return false
	case sym(b, "("):
		// a call (f(x), beam(x: Int)) touches its name; a group after a word doesn't
		return !(a.Type == IDENT || sym(a, ")", "]") || a.Type == KEYWORD && a.Text == "beam")
	case sym(b, "["):
		// indexing (list[0]) and types (List[Int]) touch; a new list doesn't
		return !wordLike(a) || sym(a, "?")
	case sym(a, "{"):
		// {sku: "A"} on one line stays tight if it was written tight
		return a.End < b.Start
	case sym(b, "}"):
		return a.End < b.Start
	case sym(a, "-") && unaryMinus(toks, ai):
		return false
	}
	return true
}

// unaryMinus reports whether the '-' at toks[i] makes a number negative (-5)
// rather than subtracting (a - 5).
func unaryMinus(toks []Token, i int) bool {
	for j := i - 1; j >= 0; j-- {
		p := toks[j]
		if p.Type == NEWLINE {
			return true
		}
		if p.Type == IDENT || p.Type == NUMBER || p.Type == STRING {
			return false
		}
		if p.Type == SYMBOL && (p.Text == ")" || p.Text == "]" || p.Text == "}" || p.Text == "?") {
			return false
		}
		if p.Type == KEYWORD && (p.Text == "me" || p.Text == "nothing" || p.Text == "yes" || p.Text == "no") {
			return false
		}
		return true
	}
	return true
}

// sameCode checks that formatting changed nothing but layout.
func sameCode(before, after string) error {
	if !strings.HasSuffix(before, "\n") {
		before += "\n" // a missing last newline is layout, not meaning
	}
	a, err1 := Lex(before, 1)
	b, err2 := Lex(after, 1)
	if err1 != nil || err2 != nil || len(a) != len(b) {
		return fmt.Errorf("lf fmt would have changed the meaning of this code, so it left it alone (please report this)")
	}
	for i := range a {
		if a[i].Type != b[i].Type || a[i].Text != b[i].Text {
			return fmt.Errorf("lf fmt would have changed the meaning of this code near line %d, so it left it alone (please report this)", a[i].Line)
		}
	}
	return nil
}
