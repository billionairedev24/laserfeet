package lang

// Test coverage: which statements ran. lf test --cover turns it on before
// running, so the compiled code carries the bookkeeping only then.

import (
	"reflect"
	"sort"
	"sync"
)

// Coverage records the statements that ran, and the files they're in.
type Coverage struct {
	mu    sync.Mutex
	hit   map[Stmt]bool
	files map[string]*Program // "" is the program's own file
	order []string
}

// NewCoverage starts recording. Set it as Interp.Cover before running.
func NewCoverage() *Coverage {
	return &Coverage{hit: map[Stmt]bool{}, files: map[string]*Program{}}
}

func (c *Coverage) mark(s Stmt) {
	c.mu.Lock()
	c.hit[s] = true
	c.mu.Unlock()
}

func (c *Coverage) addFile(name string, prog *Program) {
	c.mu.Lock()
	if _, ok := c.files[name]; !ok {
		c.files[name] = prog
		c.order = append(c.order, name)
	}
	c.mu.Unlock()
}

// FileCoverage is how much of one file ran.
type FileCoverage struct {
	File   string // "" is the program's own file
	Total  int    // statements
	Run    int    // statements that ran
	Missed []int  // lines with a statement that never ran
}

// Report says, file by file, how many statements ran. Packages aren't
// included: they're tested in their own repositories.
func (c *Coverage) Report() []FileCoverage {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []FileCoverage
	for _, name := range c.order {
		if IsPackagePath(name) {
			continue
		}
		fc := FileCoverage{File: name}
		missed := map[int]bool{}
		for _, s := range statementsIn(c.files[name]) {
			fc.Total++
			if c.hit[s] {
				fc.Run++
			} else {
				missed[s.Ln()] = true
			}
		}
		for l := range missed {
			fc.Missed = append(fc.Missed, l)
		}
		sort.Ints(fc.Missed)
		out = append(out, fc)
	}
	return out
}

// statementsIn finds every statement that can run in a program: inside
// beams, objects, tests, blocks and beams written inline. Declarations
// themselves (beam, object, choice, rule, use) aren't counted.
func statementsIn(prog *Program) []Stmt {
	var out []Stmt
	stmtType := reflect.TypeOf((*Stmt)(nil)).Elem()
	var walk func(v reflect.Value, inBlock bool)
	walk = func(v reflect.Value, inBlock bool) {
		switch v.Kind() {
		case reflect.Interface, reflect.Pointer:
			if !v.IsNil() {
				walk(v.Elem(), inBlock)
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i), false)
				}
			}
		case reflect.Slice:
			isBlock := v.Type().Elem() == stmtType
			for i := 0; i < v.Len(); i++ {
				el := v.Index(i)
				if isBlock && !el.IsNil() {
					if s, ok := el.Interface().(Stmt); ok && runnable(s) {
						out = append(out, s)
					}
				}
				walk(el, isBlock)
			}
		}
	}
	if prog != nil {
		for _, s := range prog.Stmts {
			if runnable(s) {
				out = append(out, s)
			}
			walk(reflect.ValueOf(s), true)
		}
	}
	return out
}

func runnable(s Stmt) bool {
	switch s.(type) {
	case *BeamDecl, *ObjectDecl, *ChoiceDecl, *RuleDecl, *UseStmt, *TestStmt, *LaunchStmt:
		return false
	}
	return true
}
