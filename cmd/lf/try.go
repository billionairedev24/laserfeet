package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// tryCommand is lf try: type Laser Feet and see what it gives. A piece with
// an open { carries on over lines until it's closed.
func tryCommand(stdin io.Reader, out io.Writer) int {
	in := newInterp("try.lf")
	in.Out = out
	fmt.Fprintln(out, "Laser Feet "+version+". Type code, or a value to see it. Ctrl-D (Ctrl-Z on Windows) to leave.")
	sc := bufio.NewScanner(stdin)
	var piece []string
	depth := 0
	prompt := func() {
		if depth > 0 {
			fmt.Fprint(out, "... ")
		} else {
			fmt.Fprint(out, "lf> ")
		}
	}
	prompt()
	for sc.Scan() {
		line := sc.Text()
		piece = append(piece, line)
		depth += openBraces(line)
		if depth > 0 {
			prompt()
			continue
		}
		depth = 0
		src := strings.Join(piece, "\n")
		piece = piece[:0]
		if strings.TrimSpace(src) != "" {
			if err := in.Try(src); err != nil {
				fmt.Fprintln(out, describeTry(err))
			}
		}
		prompt()
	}
	fmt.Fprintln(out)
	return 0
}

// openBraces counts { minus } outside text and comments.
func openBraces(line string) int {
	n, inText := 0, false
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		switch r := rs[i]; {
		case r == '"':
			inText = !inText
		case inText:
		case r == '-' && i+1 < len(rs) && rs[i+1] == '-':
			return n
		case r == '{':
			n++
		case r == '}':
			n--
		}
	}
	return n
}

func describeTry(err error) string {
	return strings.Replace(describeError("line", err), "line, line", "line", 1)
}
