package lang

import (
	"fmt"
	"strconv"
	"strings"
)

type parser struct {
	toks    []Token
	pos     int
	noTrail int // when > 0, a "{" after a call is a block, not a trailing beam
}

// Parse reads Laser Feet source code into a Program.
func Parse(src string) (prog *Program, err error) {
	toks, lerr := Lex(src, 1)
	if lerr != nil {
		return nil, lerr
	}
	p := &parser{toks: toks}
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(*SyntaxError); ok {
				prog, err = nil, se
				return
			}
			panic(r)
		}
	}()
	prog = &Program{}
	for {
		p.skipNewlines()
		if p.peek().Type == EOF {
			break
		}
		prog.Stmts = append(prog.Stmts, p.statement())
		p.endOfStatement()
	}
	return prog, nil
}

// ---- helpers ----

func (p *parser) peek() Token { return p.toks[p.pos] }
func (p *parser) next() Token {
	t := p.toks[p.pos]
	if t.Type != EOF {
		p.pos++
	}
	return t
}
func (p *parser) fail(line int, format string, args ...any) {
	panic(&SyntaxError{line, fmt.Sprintf(format, args...)})
}
func (p *parser) isKw(word string) bool {
	t := p.peek()
	return t.Type == KEYWORD && t.Text == word
}
func (p *parser) isSym(s string) bool {
	t := p.peek()
	return t.Type == SYMBOL && t.Text == s
}
func (p *parser) acceptKw(word string) bool {
	if p.isKw(word) {
		p.pos++
		return true
	}
	return false
}
func (p *parser) acceptSym(s string) bool {
	if p.isSym(s) {
		p.pos++
		return true
	}
	return false
}
func describe(t Token) string {
	switch t.Type {
	case NEWLINE:
		return "the end of the line"
	case EOF:
		return "the end of the file"
	case STRING:
		return "some text"
	}
	return "'" + t.Text + "'"
}
func (p *parser) expectSym(s string, what string) Token {
	t := p.peek()
	if !p.isSym(s) {
		p.fail(t.Line, "I expected '%s' %s, but found %s", s, what, describe(t))
	}
	return p.next()
}
func (p *parser) expectIdent(what string) Token {
	t := p.peek()
	if t.Type != IDENT {
		if t.Type == KEYWORD {
			p.fail(t.Line, "'%s' is a Laser Feet keyword, so it can't be used as %s", t.Text, what)
		}
		p.fail(t.Line, "I expected %s, but found %s", what, describe(t))
	}
	return p.next()
}
func (p *parser) skipNewlines() {
	for p.peek().Type == NEWLINE {
		p.pos++
	}
}

// peekPastNewlines reports whether the next real token (ignoring line breaks) is the keyword.
func (p *parser) keywordAfterNewlines(word string) bool {
	i := p.pos
	for p.toks[i].Type == NEWLINE {
		i++
	}
	t := p.toks[i]
	if t.Type == KEYWORD && t.Text == word {
		p.pos = i
		return true
	}
	return false
}

func (p *parser) endOfStatement() {
	t := p.peek()
	if t.Type == NEWLINE {
		p.pos++
		return
	}
	if t.Type == EOF || p.isSym("}") {
		return
	}
	p.fail(t.Line, "I didn't expect %s here. Put each instruction on its own line", describe(t))
}

func (p *parser) block(what string) []Stmt {
	open := p.peek()
	if !p.isSym("{") {
		p.fail(open.Line, "I expected '{' to start %s, but found %s", what, describe(open))
	}
	p.next()
	var body []Stmt
	for {
		p.skipNewlines()
		if p.isSym("}") {
			p.next()
			return body
		}
		if p.peek().Type == EOF {
			p.fail(open.Line, "the '{' for %s on line %d is never closed with '}'", what, open.Line)
		}
		body = append(body, p.statement())
		p.endOfStatement()
	}
}

// ---- statements ----

func (p *parser) statement() Stmt {
	t := p.peek()
	if t.Type == KEYWORD {
		switch t.Text {
		case "launch":
			p.next()
			return &LaunchStmt{t.Line, p.block("the launch block")}
		case "show":
			p.next()
			return &ShowStmt{t.Line, p.expr()}
		case "beam":
			if p.toks[p.pos+1].Type == IDENT {
				return p.beamDecl()
			}
		case "send":
			p.next()
			if p.peek().Type == NEWLINE || p.isSym("}") || p.peek().Type == EOF {
				return &SendStmt{t.Line, nil}
			}
			return &SendStmt{t.Line, p.expr()}
		case "lock":
			p.next()
			name := p.expectIdent("a name after 'lock'")
			p.expectSym("=", "after the name of a locked value")
			return &AssignStmt{t.Line, &Ident{name.Line, name.Text}, p.expr(), true}
		case "object":
			return p.objectDecl()
		case "rule":
			return p.ruleDecl()
		case "when":
			return p.whenStmt()
		case "each":
			p.next()
			v := p.expectIdent("a name for each item, like 'each user in users'")
			if !p.acceptKw("in") {
				p.fail(v.Line, "I expected 'in' after 'each %s'", v.Text)
			}
			p.noTrail++
			over := p.expr()
			p.noTrail--
			return &EachStmt{t.Line, v.Text, over, p.block("the each loop")}
		case "loop":
			p.next()
			p.noTrail++
			defer func() { p.noTrail-- }()
			if p.acceptKw("while") {
				cond := p.expr()
				p.noTrail--
				body := p.block("the loop")
				p.noTrail++
				return &LoopStmt{Line: t.Line, While: cond, Body: body}
			}
			n := p.expr()
			if !p.acceptKw("times") {
				p.fail(t.Line, "write 'loop while <condition>' or 'loop <number> times'")
			}
			p.noTrail--
			body := p.block("the loop")
			p.noTrail++
			return &LoopStmt{Line: t.Line, Times: n, Body: body}
		case "stop":
			p.next()
			return &StopStmt{t.Line}
		case "skip":
			p.next()
			return &SkipStmt{t.Line}
		case "fast":
			p.next()
			if p.isSym("{") {
				return &FastStmt{t.Line, p.block("the fast block")}
			}
			x := p.expr()
			return &FastStmt{t.Line, []Stmt{&ExprStmt{x.Ln(), x}}}
		case "attempt":
			p.next()
			body := p.block("the attempt block")
			if !p.keywordAfterNewlines("fails") {
				p.fail(t.Line, "an 'attempt' block needs a 'fails' block after it")
			}
			p.next()
			name := ""
			if p.peek().Type == IDENT {
				name = p.next().Text
			}
			return &AttemptStmt{t.Line, body, name, p.block("the fails block")}
		case "fail":
			p.next()
			return &FailStmt{t.Line, p.expr()}
		case "use":
			p.next()
			if p.peek().Type == STRING {
				return &UseStmt{Line: t.Line, Path: p.next().Text}
			}
			return &UseStmt{Line: t.Line, Name: p.expectIdent("a library name or a \"file.lf\" after 'use'").Text}
		case "test":
			p.next()
			name := p.peek()
			if name.Type != STRING {
				p.fail(t.Line, "give the test a name in quotes, like test \"adds numbers\" { ... }")
			}
			p.next()
			return &TestStmt{t.Line, name.Text, p.block("the test")}
		case "expect":
			p.next()
			return &ExpectStmt{t.Line, p.expr()}
		case "otherwise":
			p.fail(t.Line, "'otherwise' must come right after a 'when' block")
		case "fails":
			p.fail(t.Line, "'fails' must come right after an 'attempt' block")
		}
	}
	x := p.expr()
	if p.isSym("=") {
		eq := p.next()
		switch x.(type) {
		case *Ident, *Get, *Index:
		default:
			p.fail(eq.Line, "the left side of '=' must be a name, like 'total = 5'")
		}
		return &AssignStmt{eq.Line, x, p.expr(), false}
	}
	return &ExprStmt{x.Ln(), x}
}

func (p *parser) params() []string {
	var ps []string
	if !p.acceptSym("(") {
		return ps
	}
	for !p.isSym(")") {
		ps = append(ps, p.expectIdent("a name for each input").Text)
		if p.acceptSym(":") { // optional type, ignored for now
			p.expectIdent("a type name")
		}
		if !p.acceptSym(",") {
			break
		}
	}
	p.expectSym(")", "to close the list of inputs")
	return ps
}

func (p *parser) beamDecl() *BeamDecl {
	t := p.next() // beam
	name := p.expectIdent("a name for the beam")
	ps := p.params()
	return &BeamDecl{t.Line, name.Text, ps, p.block("beam " + name.Text)}
}

func (p *parser) objectDecl() Stmt {
	t := p.next()
	name := p.expectIdent("a name for the object")
	d := &ObjectDecl{Line: t.Line, Name: name.Text}
	if p.acceptKw("follows") {
		for {
			d.Follows = append(d.Follows, p.expectIdent("a rule name after 'follows'").Text)
			if !p.acceptSym(",") {
				break
			}
		}
	}
	p.expectSym("{", "to start object "+name.Text)
	for {
		p.skipNewlines()
		if p.acceptSym("}") {
			return d
		}
		if p.isKw("beam") {
			d.Methods = append(d.Methods, p.beamDecl())
		} else {
			f := p.expectIdent("a field name or a beam inside the object")
			fd := FieldDecl{Name: f.Text}
			if p.acceptSym(":") {
				p.expectIdent("a type name")
			}
			if p.acceptSym("=") {
				fd.Default = p.expr()
			}
			d.Fields = append(d.Fields, fd)
		}
		p.endOfStatement()
	}
}

func (p *parser) ruleDecl() Stmt {
	t := p.next()
	name := p.expectIdent("a name for the rule")
	d := &RuleDecl{Line: t.Line, Name: name.Text}
	p.expectSym("{", "to start rule "+name.Text)
	for {
		p.skipNewlines()
		if p.acceptSym("}") {
			return d
		}
		if !p.acceptKw("beam") {
			p.fail(p.peek().Line, "a rule can only list beams, like 'beam greet()'")
		}
		d.Methods = append(d.Methods, p.expectIdent("a beam name").Text)
		p.params()
		p.endOfStatement()
	}
}

func (p *parser) whenStmt() Stmt {
	t := p.next()
	p.noTrail++
	cond := p.expr()
	p.noTrail--
	s := &WhenStmt{Line: t.Line, Cond: cond, Body: p.block("the when block")}
	if p.keywordAfterNewlines("otherwise") {
		p.next()
		if p.isKw("when") {
			s.Otherwise = []Stmt{p.whenStmt()}
		} else {
			s.Otherwise = p.block("the otherwise block")
		}
	}
	return s
}

// ---- expressions ----

func (p *parser) expr() Expr {
	left := p.or()
	for p.keywordAfterNewlines("then") {
		t := p.next()
		right := p.postfix()
		left = &Then{t.Line, left, right}
	}
	return left
}

func (p *parser) or() Expr {
	left := p.and()
	for p.isKw("or") {
		t := p.next()
		left = &Binary{t.Line, "or", left, p.and()}
	}
	return left
}

func (p *parser) and() Expr {
	left := p.not()
	for p.isKw("and") {
		t := p.next()
		left = &Binary{t.Line, "and", left, p.not()}
	}
	return left
}

func (p *parser) not() Expr {
	if p.isKw("not") {
		t := p.next()
		return &Unary{t.Line, "not", p.not()}
	}
	return p.equality()
}

func (p *parser) equality() Expr {
	left := p.comparison()
	for {
		t := p.peek()
		switch {
		case p.isKw("is"):
			p.next()
			op := "is"
			if p.acceptKw("not") {
				op = "is not"
			}
			left = &Binary{t.Line, op, left, p.comparison()}
		case p.isSym("=="):
			p.next()
			left = &Binary{t.Line, "is", left, p.comparison()}
		case p.isSym("!="):
			p.next()
			left = &Binary{t.Line, "is not", left, p.comparison()}
		default:
			return left
		}
	}
}

func (p *parser) comparison() Expr {
	left := p.rangeExpr()
	for p.isSym("<") || p.isSym(">") || p.isSym("<=") || p.isSym(">=") {
		t := p.next()
		left = &Binary{t.Line, t.Text, left, p.rangeExpr()}
	}
	return left
}

func (p *parser) rangeExpr() Expr {
	left := p.additive()
	if p.isKw("to") {
		t := p.next()
		left = &Binary{t.Line, "to", left, p.additive()}
	}
	return left
}

func (p *parser) additive() Expr {
	left := p.multiplicative()
	for p.isSym("+") || p.isSym("-") {
		t := p.next()
		left = &Binary{t.Line, t.Text, left, p.multiplicative()}
	}
	return left
}

func (p *parser) multiplicative() Expr {
	left := p.unary()
	for p.isSym("*") || p.isSym("/") || p.isSym("%") {
		t := p.next()
		left = &Binary{t.Line, t.Text, left, p.unary()}
	}
	return left
}

func (p *parser) unary() Expr {
	if p.isSym("-") {
		t := p.next()
		return &Unary{t.Line, "-", p.unary()}
	}
	return p.postfix()
}

func (p *parser) postfix() Expr {
	x := p.primary()
	for {
		t := p.peek()
		switch {
		case p.isSym("("):
			p.next()
			c := &Call{Line: t.Line, Callee: x}
			saved := p.noTrail
			p.noTrail = 0
			for !p.isSym(")") {
				arg := Arg{}
				if p.peek().Type == IDENT && p.toks[p.pos+1].Type == SYMBOL && p.toks[p.pos+1].Text == ":" {
					arg.Name = p.next().Text
					p.next()
				}
				arg.Value = p.expr()
				c.Args = append(c.Args, arg)
				if !p.acceptSym(",") {
					break
				}
			}
			p.noTrail = saved
			p.expectSym(")", "to close the call")
			if p.isSym("{") && p.noTrail == 0 {
				bt := p.peek()
				body := p.block("the beam block")
				c.Args = append(c.Args, Arg{Value: &Lambda{Line: bt.Line, Body: body}})
			}
			x = c
		case p.isSym("."):
			p.next()
			name := p.peek()
			if name.Type != IDENT && name.Type != KEYWORD {
				p.fail(name.Line, "I expected a name after '.'")
			}
			p.next()
			x = &Get{name.Line, x, name.Text}
		case p.isSym("["):
			p.next()
			k := p.expr()
			p.expectSym("]", "to close the [ ]")
			x = &Index{t.Line, x, k}
		default:
			return x
		}
	}
}

func (p *parser) primary() Expr {
	t := p.peek()
	switch t.Type {
	case NUMBER:
		p.next()
		if strings.Contains(t.Text, ".") {
			d, err := ParseDecimal(t.Text)
			if err != nil {
				p.fail(t.Line, "'%s' is not a valid number", t.Text)
			}
			d.literal = true
			return &NumberLit{Line: t.Line, Text: t.Text, Value: d}
		}
		i, err := strconv.ParseInt(t.Text, 10, 64)
		if err != nil {
			p.fail(t.Line, "%s is too big for an Int. Write it with a decimal point to make an exact Decimal: %s.0", t.Text, t.Text)
		}
		return &NumberLit{Line: t.Line, Text: t.Text, Value: box(i)}
	case STRING:
		p.next()
		return p.text(t)
	case IDENT:
		p.next()
		if p.isSym("=>") {
			p.next()
			return &Lambda{Line: t.Line, Params: []string{t.Text}, Expr: p.or()}
		}
		return &Ident{t.Line, t.Text}
	case KEYWORD:
		switch t.Text {
		case "yes":
			p.next()
			return &BoolLit{t.Line, true}
		case "no":
			p.next()
			return &BoolLit{t.Line, false}
		case "nothing":
			p.next()
			return &NothingLit{t.Line}
		case "me":
			p.next()
			return &MeExpr{t.Line}
		case "beam":
			p.next()
			ps := p.params()
			saved := p.noTrail
			p.noTrail = 0
			body := p.block("the beam")
			p.noTrail = saved
			return &Lambda{Line: t.Line, Params: ps, Body: body}
		}
		p.fail(t.Line, "I didn't expect the keyword '%s' here", t.Text)
	case SYMBOL:
		switch t.Text {
		case "(":
			p.next()
			x := p.expr()
			p.expectSym(")", "to close the '('")
			return x
		case "[":
			p.next()
			l := &ListLit{Line: t.Line}
			for !p.isSym("]") {
				l.Items = append(l.Items, p.expr())
				if !p.acceptSym(",") {
					break
				}
			}
			p.expectSym("]", "to close the list")
			return l
		case "{":
			p.next()
			m := &MapLit{Line: t.Line}
			for {
				p.skipNewlines()
				if p.isSym("}") {
					break
				}
				k := p.next()
				if k.Type != STRING && k.Type != IDENT {
					p.fail(k.Line, "each entry in a { } map needs a name, like {name: \"Ada\"}")
				}
				p.expectSym(":", "after the entry name")
				m.Keys = append(m.Keys, k.Text)
				m.Values = append(m.Values, p.expr())
				p.skipNewlines()
				if !p.acceptSym(",") {
					break
				}
			}
			p.skipNewlines()
			p.expectSym("}", "to close the map")
			return m
		}
	}
	p.fail(t.Line, "I expected a value here, but found %s", describe(t))
	return nil
}

// text turns "Hi {name}" into an interpolated expression.
func (p *parser) text(t Token) Expr {
	s := t.Text
	if !strings.Contains(s, "{") {
		return &TextLit{t.Line, unescapeBraces(s)}
	}
	it := &InterpText{Line: t.Line}
	rs := []rune(s)
	var buf []rune
	for i := 0; i < len(rs); i++ {
		if rs[i] != '{' {
			buf = append(buf, rs[i])
			continue
		}
		j := i + 1
		depth := 1
		for j < len(rs) && depth > 0 {
			if rs[j] == '{' {
				depth++
			} else if rs[j] == '}' {
				depth--
			}
			j++
		}
		if depth != 0 {
			p.fail(t.Line, "a '{' inside this text is never closed with '}'")
		}
		inner := string(rs[i+1 : j-1])
		if e := parseInner(inner, t.Line); e != nil {
			if len(buf) > 0 {
				it.Parts = append(it.Parts, &TextLit{t.Line, unescapeBraces(string(buf))})
				buf = nil
			}
			it.Parts = append(it.Parts, e)
		} else {
			// not a Laser Feet expression (for example JSON), so keep it as plain text
			buf = append(buf, rs[i:j]...)
		}
		i = j - 1
	}
	if len(buf) > 0 {
		it.Parts = append(it.Parts, &TextLit{t.Line, unescapeBraces(string(buf))})
	}
	return it
}

func unescapeBraces(s string) string {
	return strings.NewReplacer(string(litOpen), "{", string(litClose), "}").Replace(s)
}

func parseInner(src string, line int) (e Expr) {
	toks, err := Lex(src, line)
	if err != nil {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			e = nil
		}
	}()
	sub := &parser{toks: toks}
	x := sub.expr()
	sub.skipNewlines()
	if sub.peek().Type != EOF {
		return nil
	}
	return x
}
