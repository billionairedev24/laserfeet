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

server.get("/hello/:name") as request {
    send greet(request.params.name)
}

beam greet(name: Text) gives Text {
    send "Hello, {name}!"
}

launch {
    fast server.start()
}

test "greets people by name" {
    expect greet("Ada") is "Hello, Ada!"
}

test "the home page answers" {
    expect server.visit("/").body is "Hello from {{name}}!"
}
`

const starterReadme = `# {{name}}

A [Laser Feet](https://billionairedev24.github.io/laserfeet/) app.

    lf run app.lf     # start it, then open http://localhost:8080
    lf test app.lf    # run its tests

## Run it in Docker

    docker build -t {{name}} .
    docker run --rm -p 8080:8080 {{name}}

The image holds only your built program: no shell and no operating system
packages, running as an unprivileged user.
`

const starterDockerfile = `# Builds {{name}} into a tiny, locked-down container image.
#   docker build -t {{name}} .
#   docker run --rm -p 8080:8080 {{name}}
ARG LF_IMAGE=ghcr.io/billionairedev24/laserfeet:{{version}}

# 1. Check, test and build the app with the official Laser Feet image
FROM ${LF_IMAGE} AS build
USER 0:0
WORKDIR /app
COPY *.lf ./
RUN ["lf", "test", "app.lf"]
RUN ["lf", "build", "app.lf", "-o", "/app/server"]

# 2. The image you run holds only the built program: no shell, no packages
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /app/server /server
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["/server"]
`

const starterDockerignore = `*
!*.lf
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
	files := map[string]string{"app.lf": starterApp, "README.md": starterReadme, "Dockerfile": starterDockerfile, ".dockerignore": starterDockerignore}
	imageTag := version
	if imageTag == "dev" {
		imageTag = "latest"
	}
	for file, content := range files {
		text := regexp.MustCompile(`\{\{name\}\}`).ReplaceAllLiteralString(content, name)
		text = regexp.MustCompile(`\{\{version\}\}`).ReplaceAllLiteralString(text, imageTag)
		if err := os.WriteFile(filepath.Join(name, file), []byte(text), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "I couldn't write %s: %v\n", file, err)
			return 1
		}
	}
	fmt.Printf("Created %s/\n  app.lf       your app, with tests\n  Dockerfile   to run it in a container\n  README.md\n\nNext:\n  cd %s\n  lf run app.lf\n", name, name)
	return 0
}
