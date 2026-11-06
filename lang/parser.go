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
	src     []rune
	grouped map[Expr]bool // expressions written inside ( )
	fixing  bool          // lf fix: record Edits for old code instead of failing
	edits   []Edit
}

// Edit is a change lf fix makes to bring older code up to date: Text is put at
// position At (counted in characters).
type Edit struct {
	At   int
	Text string
}

// Parse reads Laser Feet source code into a Program.
func Parse(src string) (prog *Program, err error) {
	prog, _, err = parse(src, false)
	return prog, err
}

// ParseForFix is Parse for lf fix: code written for older versions is accepted,
// and the edits that update it are returned.
func ParseForFix(src string) (*Program, []Edit, error) {
	return parse(src, true)
}

func parse(src string, fixing bool) (prog *Program, edits []Edit, err error) {
	toks, lerr := Lex(src, 1)
	if lerr != nil {
		return nil, nil, lerr
	}
	p := &parser{toks: toks, src: []rune(src), grouped: map[Expr]bool{}, fixing: fixing}
	defer func() {
		if r := recover(); r != nil {
			if se, ok := r.(*SyntaxError); ok {
				prog, edits, err = nil, nil, se
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
	return prog, p.edits, nil
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

// isBlockName reports whether the next words are "as <name> {", naming a block's input.
func (p *parser) isBlockName() bool {
	if p.pos+2 >= len(p.toks) {
		return false
	}
	a, n, b := p.toks[p.pos], p.toks[p.pos+1], p.toks[p.pos+2]
	return a.Type == IDENT && a.Text == "as" && n.Type == IDENT && b.Type == SYMBOL && b.Text == "{"
}

// transactionStmt reads: transaction with db as tx { ... }
// It means db.transaction() as tx { ... }: everything in the block happens, or none of it.
func (p *parser) transactionStmt() Stmt {
	t := p.next()
	p.next() // with
	p.noTrail++
	db := p.expr()
	p.noTrail--
	if !(p.peek().Type == IDENT && p.peek().Text == "as") {
		p.fail(t.Line, "name the transaction, like: transaction with db as tx { ... }")
	}
	p.next()
	name := p.expectIdent("a name for the transaction, like 'tx'")
	bt := p.peek()
	body := p.block("the transaction")
	call := &Call{Line: t.Line, Callee: &Get{t.Line, db, "transaction"}, Tx: true,
		Args: []Arg{{Value: &Lambda{Line: bt.Line, Params: []string{name.Text}, Body: body, As: true, Open: bt.Start}}}}
	return &ExprStmt{t.Line, call}
}

func (p *parser) statement() Stmt {
	t := p.peek()
	if t.Type == IDENT && t.Text == "transaction" && p.toks[p.pos+1].Type == IDENT && p.toks[p.pos+1].Text == "with" {
		return p.transactionStmt()
	}
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
			var te *TypeExpr
			if p.acceptSym(":") {
				te = p.typeExpr()
			}
			p.expectSym("=", "after the name of a locked value")
			return &AssignStmt{Line: t.Line, Target: &Ident{name.Line, name.Text}, Value: p.expr(), Lock: true, Type: te}
		case "object":
			return p.objectDecl()
		case "rule":
			return p.ruleDecl()
		case "choice":
			return p.choiceDecl()
		case "private":
			p.next()
			if !p.isKw("beam") {
				p.fail(t.Line, "'private' goes before a beam, or before a field or beam inside an object")
			}
			b := p.beamDecl()
			b.Private = true
			return b
		case "focus":
			return p.focusStmt()
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
			if p.toks[p.pos+1].Type == SYMBOL && p.toks[p.pos+1].Text == "{" {
				p.next()
				return &FastStmt{t.Line, p.block("the fast block")}
			}
			// fast work() and fast each ... are expressions; used alone they just run
		case "attempt":
			p.next()
			body := p.block("the attempt block")
			s := &AttemptStmt{Line: t.Line, Body: body}
			for p.keywordAfterNewlines("fails") {
				ft := p.next()
				h := &FailsClause{Line: ft.Line}
				if p.peek().Type == IDENT {
					h.Name = p.next().Text
					if p.acceptSym(":") {
						h.Type = p.typeExpr()
					}
				}
				h.Body = p.block("the fails block")
				s.Handlers = append(s.Handlers, h)
			}
			if len(s.Handlers) == 0 {
				p.fail(t.Line, "an 'attempt' block needs a 'fails' block after it")
			}
			return s
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
	if t.Type == IDENT && p.toks[p.pos+1].Type == SYMBOL && p.toks[p.pos+1].Text == ":" {
		p.next()
		p.next()
		te := p.typeExpr()
		p.expectSym("=", "after the type, like total: Decimal = 0")
		return &AssignStmt{Line: t.Line, Target: &Ident{t.Line, t.Text}, Value: p.expr(), Type: te}
	}
	first := p.peek()
	x := p.expr()
	if p.isSym("=") {
		eq := p.next()
		switch x.(type) {
		case *Ident, *Get, *Index:
		default:
			if first.Type == KEYWORD && p.toks[p.pos-2] == first {
				p.fail(eq.Line, "'%s' is one of Laser Feet's own words, so it can't be a name. Pick another, like '%sValue'", first.Text, first.Text)
			}
			p.fail(eq.Line, "the left side of '=' must be a name, like 'total = 5'")
		}
		return &AssignStmt{Line: eq.Line, Target: x, Value: p.expr()}
	}
	return &ExprStmt{x.Ln(), x}
}

// typeExpr reads a type: Int, Text?, List[Item], Map[Decimal], Beam(Int, Text) gives Flag.
func (p *parser) typeExpr() *TypeExpr {
	t := p.peek()
	if t.Type != IDENT {
		p.fail(t.Line, "I expected a type here, like Int, Text or List[Text], but found %s", describe(t))
	}
	p.next()
	te := &TypeExpr{Line: t.Line, Name: t.Text}
	if te.Name == "Beam" && p.acceptSym("(") {
		for !p.isSym(")") {
			te.Params = append(te.Params, p.typeExpr())
			if !p.acceptSym(",") {
				break
			}
		}
		p.expectSym(")", "to close the Beam's input types")
		if p.acceptKw("gives") {
			te.Result = p.typeExpr()
		}
	} else if p.acceptSym("[") {
		for {
			te.Args = append(te.Args, p.typeExpr())
			if !p.acceptSym(",") {
				break
			}
		}
		p.expectSym("]", "to close the [ ] of the type")
	}
	if p.acceptSym("?") {
		te.Optional = true
	}
	return te
}

// params reads a beam's inputs. Named beams must give each input a type.
func (p *parser) params(typed bool, what string) ([]string, []*TypeExpr) {
	var names []string
	var types []*TypeExpr
	if !p.acceptSym("(") {
		return names, types
	}
	for !p.isSym(")") {
		n := p.expectIdent("a name for each input")
		names = append(names, n.Text)
		if p.acceptSym(":") {
			types = append(types, p.typeExpr())
		} else if typed {
			p.fail(n.Line, "give the input '%s' of %s a type, like %s: Text", n.Text, what, n.Text)
		} else {
			types = append(types, nil)
		}
		if !p.acceptSym(",") {
			break
		}
	}
	p.expectSym(")", "to close the list of inputs")
	return names, types
}

// signature reads the inputs and the optional 'gives Type' of a beam.
func (p *parser) signature(typed bool, what string) ([]string, *Sig) {
	names, types := p.params(typed, what)
	sig := &Sig{Params: types}
	if p.acceptKw("gives") {
		sig.Result = p.typeExpr()
	}
	return names, sig
}

func (p *parser) beamDecl() *BeamDecl {
	t := p.next() // beam
	name := p.expectIdent("a name for the beam")
	tps := p.typeParams()
	ps, sig := p.signature(true, "beam "+name.Text)
	sig.TypeParams = tps
	return &BeamDecl{Line: t.Line, Name: name.Text, Params: ps, Sig: sig, Body: p.block("beam " + name.Text)}
}

func (p *parser) objectDecl() Stmt {
	t := p.next()
	name := p.expectIdent("a name for the object")
	d := &ObjectDecl{Line: t.Line, Name: name.Text, TypeParams: p.typeParams()}
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
		private := p.acceptKw("private")
		if p.isKw("beam") {
			b := p.beamDecl()
			b.Private = private
			d.Methods = append(d.Methods, b)
		} else {
			f := p.expectIdent("a field name or a beam inside the object")
			fd := FieldDecl{Name: f.Text, Line: f.Line, Private: private}
			if !p.acceptSym(":") {
				p.fail(f.Line, "give the field '%s' a type, like %s: Text", f.Text, f.Text)
			}
			fd.Type = p.typeExpr()
			if p.acceptSym("=") {
				fd.Default = p.expr()
			}
			d.Fields = append(d.Fields, fd)
		}
		p.endOfStatement()
	}
}

// typeParams reads the names of a generic's types: [T] or [K, V]. They must be
// written with a capital letter, like other type names.
func (p *parser) typeParams() []string {
	if !p.acceptSym("[") {
		return nil
	}
	var out []string
	for !p.isSym("]") {
		n := p.expectIdent("a name for the type, like T")
		if n.Text[0] < 'A' || n.Text[0] > 'Z' {
			p.fail(n.Line, "type names start with a capital letter, like T or Item, but '%s' doesn't", n.Text)
		}
		out = append(out, n.Text)
		if !p.acceptSym(",") {
			break
		}
	}
	p.expectSym("]", "to close the list of type names")
	return out
}

// choiceDecl reads: choice Status { Pending, Shipped(on: Text, carrier: Text), Lost }
// Options are separated by commas or new lines.
func (p *parser) choiceDecl() Stmt {
	t := p.next()
	name := p.expectIdent("a name for the choice")
	d := &ChoiceDecl{Line: t.Line, Name: name.Text, TypeParams: p.typeParams()}
	p.expectSym("{", "to start choice "+name.Text)
	for {
		p.skipNewlines()
		if p.acceptSym("}") {
			if len(d.Options) == 0 {
				p.fail(t.Line, "choice %s needs at least one option, like: choice %s { First, Second }", name.Text, name.Text)
			}
			return d
		}
		o := p.expectIdent("an option name, like Pending")
		opt := &ObjectDecl{Line: o.Line, Name: o.Text}
		if p.acceptSym("(") {
			for !p.isSym(")") {
				f := p.expectIdent("a name for the option's data")
				if !p.acceptSym(":") {
					p.fail(f.Line, "give '%s' a type, like %s: Text", f.Text, f.Text)
				}
				opt.Fields = append(opt.Fields, FieldDecl{Name: f.Text, Line: f.Line, Type: p.typeExpr()})
				if !p.acceptSym(",") {
					break
				}
			}
			p.expectSym(")", "to close the option's data")
		}
		d.Options = append(d.Options, opt)
		if !p.acceptSym(",") && !p.isSym("}") {
			p.endOfStatement()
		}
	}
}

// focusStmt reads: focus value { Option(names) { ... } 200 { ... } otherwise { ... } }
func (p *parser) focusStmt() Stmt {
	t := p.next()
	p.noTrail++
	subject := p.expr()
	p.noTrail--
	s := &FocusStmt{Line: t.Line, Subject: subject}
	p.expectSym("{", "to start the focus arms")
	for {
		p.skipNewlines()
		if p.acceptSym("}") {
			if len(s.Arms) == 0 {
				p.fail(t.Line, "a focus needs at least one arm, like: Pending { ... }")
			}
			return s
		}
		at := p.peek()
		arm := &FocusArm{Line: at.Line}
		switch {
		case p.isKw("otherwise"):
			p.next()
			arm.Otherwise = true
		case at.Type == IDENT && (p.toks[p.pos+1].Text == "{" || p.toks[p.pos+1].Text == "(") && p.toks[p.pos+1].Type == SYMBOL:
			p.next()
			arm.Option = at.Text
			if p.acceptSym("(") {
				for !p.isSym(")") {
					arm.Binds = append(arm.Binds, p.expectIdent("a name for the option's data").Text)
					if !p.acceptSym(",") {
						break
					}
				}
				p.expectSym(")", "to close the names")
			}
		default:
			p.noTrail++
			arm.Value = p.or()
			p.noTrail--
		}
		saved := p.noTrail
		p.noTrail = 0
		arm.Body = p.block("this focus arm")
		p.noTrail = saved
		s.Arms = append(s.Arms, arm)
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
		bt := p.peek()
		if !p.acceptKw("beam") {
			p.fail(bt.Line, "a rule can only list beams, like 'beam greet() gives Text'")
		}
		mname := p.expectIdent("a beam name").Text
		ps, sig := p.signature(true, "beam "+mname)
		d.Methods = append(d.Methods, mname)
		d.Sigs = append(d.Sigs, &BeamDecl{Line: bt.Line, Name: mname, Params: ps, Sig: sig})
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
	start := p.pos
	left := p.or()
	end := p.pos - 1
	for p.keywordAfterNewlines("then") {
		if p.mixedBeforeThen(left) {
			// a + b then f could mean (a + b) then f or a + (b then f): ask which
			from, to := p.toks[start].Start, p.toks[end].End
			if !p.fixing {
				p.fail(p.peek().Line, "it's unclear what 'then' takes here. Add brackets: (%s) then ... passes all of it, or put brackets around just the part 'then' should take (lf fix adds the first kind, which is how this code ran before)",
					string(p.src[from:to]))
			}
			p.edits = append(p.edits, Edit{from, "("}, Edit{to, ")"})
			p.grouped[left] = true
		}
		t := p.next()
		right := p.postfix()
		left = &Then{t.Line, left, right}
	}
	return left
}

// mixedBeforeThen reports whether the left side of 'then' is an unbracketed
// calculation like a + b or not x, where it's unclear what 'then' applies to.
// A range (1 to 10 then sum) is clear, so it's allowed.
func (p *parser) mixedBeforeThen(x Expr) bool {
	if p.grouped[x] {
		return false
	}
	switch e := x.(type) {
	case *Binary:
		return e.Op != "to"
	case *Unary:
		return true
	}
	return false
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
	if p.isKw("fast") {
		t := p.next()
		if p.acceptKw("each") {
			v := p.expectIdent("a name for each item, like 'fast each url in urls'")
			if !p.acceptKw("in") {
				p.fail(v.Line, "I expected 'in' after 'fast each %s'", v.Text)
			}
			p.noTrail++
			over := p.or()
			p.noTrail--
			saved := p.noTrail
			p.noTrail = 0
			body := p.block("the fast each block")
			p.noTrail = saved
			return &FastEach{Line: t.Line, Var: v.Text, Over: over, Body: body}
		}
		return &FastExpr{Line: t.Line, X: p.unary()}
	}
	if p.isKw("wait") {
		t := p.next()
		w := &WaitExpr{Line: t.Line, X: p.postfix()}
		if p.peek().Type == IDENT && p.peek().Text == "for" {
			p.next()
			w.Timeout = p.additive()
			if !(p.peek().Type == IDENT && (p.peek().Text == "seconds" || p.peek().Text == "second")) {
				p.fail(t.Line, "write the time limit in seconds, like: wait job for 5 seconds")
			}
			p.next()
		}
		return w
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
			if p.noTrail == 0 && p.isBlockName() {
				// server.get("/x") as request { ... }: the block gets one named input
				p.next()
				name := p.next()
				bt := p.peek()
				body := p.block("the beam block")
				c.Args = append(c.Args, Arg{Value: &Lambda{Line: bt.Line, Params: []string{name.Text}, Body: body, As: true, Open: bt.Start}})
			} else if p.isSym("{") && p.noTrail == 0 {
				bt := p.peek()
				body := p.block("the beam block")
				c.Args = append(c.Args, Arg{Value: &Lambda{Line: bt.Line, Body: body, Open: bt.Start}})
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
			ps, sig := p.signature(false, "this beam")
			saved := p.noTrail
			p.noTrail = 0
			body := p.block("the beam")
			p.noTrail = saved
			return &Lambda{Line: t.Line, Params: ps, Sig: sig, Body: body}
		}
		p.fail(t.Line, "I didn't expect the keyword '%s' here", t.Text)
	case SYMBOL:
		switch t.Text {
		case "(":
			p.next()
			x := p.expr()
			p.expectSym(")", "to close the '('")
			p.grouped[x] = true
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
				if k.Type != STRING && k.Type != IDENT && k.Type != KEYWORD { // keywords are fine as names in a map
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
	// inside text, 'then' keeps its old reading rather than turning the {...} into plain text
	sub := &parser{toks: toks, src: []rune(src), grouped: map[Expr]bool{}, fixing: true}
	x := sub.expr()
	sub.skipNewlines()
	if sub.peek().Type != EOF {
		return nil
	}
	return x
}
