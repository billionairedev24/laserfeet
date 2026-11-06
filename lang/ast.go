package lang

// ---- Expressions ----

type Expr interface{ Ln() int }

// NumberLit is a number written in the code: 42 is an Int, 19.99 an exact Decimal.
type NumberLit struct {
	Line  int
	Text  string
	Value any // int64 or *Decimal, made once when the code is read
}

type TextLit struct {
	Line  int
	Value string
}
type InterpText struct {
	Line  int
	Parts []Expr // TextLit pieces and expressions
}
type BoolLit struct {
	Line  int
	Value bool
}
type NothingLit struct{ Line int }
type MeExpr struct{ Line int }
type Ident struct {
	Line int
	Name string
}
type ListLit struct {
	Line  int
	Items []Expr
}
type MapLit struct {
	Line   int
	Keys   []string
	Values []Expr
}
type Binary struct {
	Line        int
	Op          string
	Left, Right Expr
}
type Unary struct {
	Line    int
	Op      string
	Operand Expr
}
type Arg struct {
	Name  string // empty for positional
	Value Expr
}
type Call struct {
	Line   int
	Callee Expr
	Args   []Arg
}
type Get struct {
	Line   int
	Object Expr
	Name   string
}
type Index struct {
	Line   int
	Object Expr
	Key    Expr
}
type Then struct {
	Line  int
	Left  Expr
	Right Expr
}
type Lambda struct {
	Line   int
	Params []string
	Sig    *Sig   // types, when written (beam(x: Int) gives Int { ... })
	Body   []Stmt // block body
	Expr   Expr   // or single expression body
}

// Sig holds the written types of a beam's inputs and result, and their resolved
// form once the beam has been used (resolved once, then reused for every call).
type Sig struct {
	Params   []*TypeExpr // nil entries for inputs without a written type
	Result   *TypeExpr   // nil when the beam gives nothing
	resolved bool
	params   []*Type
	result   *Type
}

func (e *NumberLit) Ln() int  { return e.Line }
func (e *TextLit) Ln() int    { return e.Line }
func (e *InterpText) Ln() int { return e.Line }
func (e *BoolLit) Ln() int    { return e.Line }
func (e *NothingLit) Ln() int { return e.Line }
func (e *MeExpr) Ln() int     { return e.Line }
func (e *Ident) Ln() int      { return e.Line }
func (e *ListLit) Ln() int    { return e.Line }
func (e *MapLit) Ln() int     { return e.Line }
func (e *Binary) Ln() int     { return e.Line }
func (e *Unary) Ln() int      { return e.Line }
func (e *Call) Ln() int       { return e.Line }
func (e *Get) Ln() int        { return e.Line }
func (e *Index) Ln() int      { return e.Line }
func (e *Then) Ln() int       { return e.Line }
func (e *Lambda) Ln() int     { return e.Line }

// ---- Statements ----

type Stmt interface{ Ln() int }

type LaunchStmt struct {
	Line int
	Body []Stmt
}
type ShowStmt struct {
	Line  int
	Value Expr
}
type AssignStmt struct {
	Line   int
	Target Expr
	Value  Expr
	Lock   bool
	Type   *TypeExpr // optional, as in: names: List[Text] = []
}
type ExprStmt struct {
	Line int
	X    Expr
}
type BeamDecl struct {
	Line   int
	Name   string
	Params []string
	Sig    *Sig
	Body   []Stmt
}
type SendStmt struct {
	Line  int
	Value Expr // may be nil
}
type FieldDecl struct {
	Name    string
	Type    *TypeExpr
	Default Expr
	Line    int
}
type ObjectDecl struct {
	Line    int
	Name    string
	Follows []string
	Fields  []FieldDecl
	Methods []*BeamDecl
}
type RuleDecl struct {
	Line    int
	Name    string
	Methods []string
	Sigs    []*BeamDecl // the promised beams, with their types and no body
}
type WhenStmt struct {
	Line      int
	Cond      Expr
	Body      []Stmt
	Otherwise []Stmt // may hold a nested WhenStmt
}
type EachStmt struct {
	Line int
	Var  string
	Over Expr
	Body []Stmt
}
type LoopStmt struct {
	Line  int
	While Expr // either While or Times is set
	Times Expr
	Body  []Stmt
}
type StopStmt struct{ Line int }
type SkipStmt struct{ Line int }
type FastStmt struct {
	Line int
	Body []Stmt
}
type AttemptStmt struct {
	Line    int
	Body    []Stmt
	ErrName string
	Handler []Stmt
}
type FailStmt struct {
	Line  int
	Value Expr
}
type UseStmt struct {
	Line int
	Name string // a built-in library, like web
	Path string // or another .lf file, like "helpers.lf"
}
type TestStmt struct {
	Line int
	Name string
	Body []Stmt
}
type ExpectStmt struct {
	Line  int
	Value Expr
}

func (s *LaunchStmt) Ln() int  { return s.Line }
func (s *ShowStmt) Ln() int    { return s.Line }
func (s *AssignStmt) Ln() int  { return s.Line }
func (s *ExprStmt) Ln() int    { return s.Line }
func (s *BeamDecl) Ln() int    { return s.Line }
func (s *SendStmt) Ln() int    { return s.Line }
func (s *ObjectDecl) Ln() int  { return s.Line }
func (s *RuleDecl) Ln() int    { return s.Line }
func (s *WhenStmt) Ln() int    { return s.Line }
func (s *EachStmt) Ln() int    { return s.Line }
func (s *LoopStmt) Ln() int    { return s.Line }
func (s *StopStmt) Ln() int    { return s.Line }
func (s *SkipStmt) Ln() int    { return s.Line }
func (s *FastStmt) Ln() int    { return s.Line }
func (s *AttemptStmt) Ln() int { return s.Line }
func (s *FailStmt) Ln() int    { return s.Line }
func (s *UseStmt) Ln() int     { return s.Line }
func (s *TestStmt) Ln() int    { return s.Line }
func (s *ExpectStmt) Ln() int  { return s.Line }

// Program is a whole parsed .lf file.
type Program struct {
	Stmts []Stmt
}
