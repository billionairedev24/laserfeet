package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var validName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

const starterApp = `-- {{name}}: a Laser Feet web app
-- Run it:   lf run app.lf     then open http://localhost:8080
-- Test it:  lf test app.lf

use web

server = web.open(8080)

server.get("/") {
    send "Hello from {{name}}!"
}

server.get("/hello/:name") {
    send greet(request.params.name)
}

beam greet(name) {
    send "Hello, {name}!"
}

launch {
    fast server.start()
}

test "greets people by name" {
    expect greet("Ada") is "Hello, Ada!"
}

test "the home page answers" {
    expect server.visit("/") is "Hello from {{name}}!"
}
`

const starterReadme = `# {{name}}

A [Laser Feet](https://billionairedev24.github.io/laserfeet/) app.

    lf run app.lf     # start it, then open http://localhost:8080
    lf test app.lf    # run its tests
`

// newProject creates a starter project folder.
func newProject(name string) int {
	if !validName.MatchString(name) {
		fmt.Fprintln(os.Stderr, "A project name starts with a letter and uses only letters, numbers, - and _ (like: lf new my-app)")
		return 2
	}
	if _, err := os.Stat(name); err == nil {
		fmt.Fprintf(os.Stderr, "A folder called %s already exists, so I didn't touch it. Pick another name.\n", name)
		return 1
	}
	if err := os.Mkdir(name, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "I couldn't create %s: %v\n", name, err)
		return 1
	}
	files := map[string]string{"app.lf": starterApp, "README.md": starterReadme}
	for file, content := range files {
		text := regexp.MustCompile(`\{\{name\}\}`).ReplaceAllLiteralString(content, name)
		if err := os.WriteFile(filepath.Join(name, file), []byte(text), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "I couldn't write %s: %v\n", file, err)
			return 1
		}
	}
	fmt.Printf("Created %s/\n  app.lf      your app, with tests\n  README.md\n\nNext:\n  cd %s\n  lf run app.lf\n", name, name)
	return 0
}
