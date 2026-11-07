package lang

// Try runs one piece of code typed into lf try. Beams, objects and names it
// makes stay for later pieces. A piece that is just a value shows it.
func (in *Interp) Try(src string) (err error) {
	prog, perr := Parse(src)
	if perr != nil {
		return perr
	}
	in.lockWhole()
	defer in.unlockWhole()
	defer in.catch(&err)
	if len(prog.Stmts) == 1 {
		if x, ok := prog.Stmts[0].(*ExprStmt); ok {
			if v := in.eval(x.X, in.globals); v != nil {
				in.print(Text(v))
			}
			return nil
		}
	}
	in.launch = nil
	in.loadInto(prog, in.globals, true)
	if in.launch != nil {
		l := in.launch
		in.launch = nil
		in.runBlock(l.Body, newScope(in.globals))
	}
	return nil
}
