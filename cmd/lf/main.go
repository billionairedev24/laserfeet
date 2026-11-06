// Command lf runs Laser Feet programs.
package main

import (
	"fmt"
	"os"
	"strings"

	"laserfeet/lang"
)

var version = "0.1.0" // set by scripts/build.sh for releases

var help = `Laser Feet ` + version + `

Usage:
  lf run <file.lf>     run a program
  lf test <file.lf>    run the test blocks in a file
  lf version           show the version
  lf help              show this help

You can also just type: lf <file.lf>
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		fmt.Print(help)
		return
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Print(help)
	case "version", "--version":
		fmt.Println("Laser Feet " + version)
	case "run":
		need(args)
		os.Exit(run(args[1]))
	case "test":
		need(args)
		os.Exit(test(args[1]))
	default:
		if strings.HasSuffix(args[0], ".lf") {
			os.Exit(run(args[0]))
		}
		fmt.Fprintf(os.Stderr, "I don't know the command '%s'.\n\n%s", args[0], help)
		os.Exit(2)
	}
}

func need(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Please give a .lf file, like: lf %s hello.lf\n", args[0])
		os.Exit(2)
	}
}

func read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "I couldn't open %s: %v\n", path, err)
		os.Exit(2)
	}
	return string(b)
}

func run(path string) int {
	in := lang.NewInterp(os.Stdout)
	if err := in.Run(read(path)); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		return 1
	}
	return 0
}

func test(path string) int {
	in := lang.NewInterp(os.Stdout)
	results, err := in.RunTests(read(path))
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
		return 1
	}
	failed := 0
	for _, r := range results {
		if r.Passed {
			fmt.Printf("  ✓ %s\n", r.Name)
		} else {
			failed++
			fmt.Printf("  ✗ %s\n      %s\n", r.Name, r.Msg)
		}
	}
	fmt.Printf("\n%d passed, %d failed\n", len(results)-failed, failed)
	if failed > 0 {
		return 1
	}
	return 0
}
