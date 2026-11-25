package bothy

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A database dump in the app folder can be committed to git, or copied into
// the next image built from the folder (the apps' Dockerfiles do COPY . /app).
// db-export and db-import check the ignore files cover the dump, and offer to
// add these lines when they don't.
type ignoreFile struct {
	name  string
	lines []string
}

var (
	gitIgnore = ignoreFile{".gitignore", []string{"*.sql", "*.sql.gz"}}
	// In .dockerignore a bare *.sql only matches at the top of the folder.
	dockerIgnore = ignoreFile{".dockerignore", []string{"**/*.sql", "**/*.sql.gz"}}
)

func (f ignoreFile) wants() string {
	return strings.Join(f.lines, " and ") + " to " + f.name
}

// checkDumpIgnored looks at a dump db-export is about to write or db-import
// is about to read. If it is inside the app folder and an ignore file doesn't
// cover it, it asks whether to add the lines, or with no terminal to ask on
// warns, in words an agent can pass on. It never stops the export or import.
func (a *App) checkDumpIgnored(name string) {
	rel, err := filepath.Rel(a.Dir, a.path(name))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return // outside the app folder: it can't reach the repo or an image
	}
	// The lines only cover the usual names, so there is nothing to offer for
	// a dump called anything else.
	usualName := strings.HasSuffix(rel, ".sql") || strings.HasSuffix(rel, ".sql.gz")
	files := a.notIgnoring(filepath.ToSlash(rel), usualName)
	if len(files) == 0 {
		return
	}
	var names, wants []string
	for _, f := range files {
		names = append(names, f.name)
		wants = append(wants, f.wants())
	}
	problem := fmt.Sprintf("%s is in the app folder and isn't ignored by %s. A database dump can hold real people's data, and left there it can end up in git or in the next image built from this folder.",
		name, strings.Join(names, " or "))

	if !a.Interactive || !usualName {
		msg := "bothy: warning: " + problem
		if !a.Interactive {
			msg += " If an agent is running this, tell the developer."
		}
		if usualName {
			msg += " To fix it, run this from a terminal and answer yes, or add " + strings.Join(wants, ", and ") + "."
		} else {
			msg += " The lines bothy adds only cover names ending in .sql or .sql.gz, so ignore this file by hand or keep it outside the app folder."
		}
		fmt.Fprintln(a.Stderr, msg)
		return
	}

	if !a.askYes(problem + " Add " + strings.Join(wants, ", and ") + "?") {
		return
	}
	for _, f := range files {
		added, err := addIgnoreLines(filepath.Join(a.Dir, f.name), f.lines)
		switch {
		case err != nil:
			fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		case len(added) == 0:
			fmt.Fprintf(a.Stdout, "%s already has those lines, but something else in it puts the dump back in. Check it by hand.\n", f.name)
		default:
			fmt.Fprintf(a.Stdout, "Added %s to %s\n", strings.Join(added, " and "), f.name)
		}
	}
}

// notIgnoring returns the ignore files that don't cover rel, a slash-separated
// path inside the app folder. Git answers for .gitignore, and is skipped when
// it isn't installed or the folder isn't in a repo. .dockerignore only matters
// when something builds an image from the folder, and bothy doesn't work out
// what its patterns match: the file covers a dump with a usual name when it
// has bothy's own lines, and never covers one with another name.
func (a *App) notIgnoring(rel string, usualName bool) []ignoreFile {
	var files []ignoreFile
	// --no-index: go by the ignore rules alone, or a dump that is already
	// committed would never count as ignored and bothy would ask every time.
	if a.Git != nil && a.Git("check-ignore", "-q", "--no-index", "--", rel) == 1 {
		files = append(files, gitIgnore)
	}
	dockerFile := filepath.Join(a.Dir, dockerIgnore.name)
	if fileExists(dockerFile) || fileExists(filepath.Join(a.Dir, "Dockerfile")) {
		text, _ := os.ReadFile(dockerFile)
		if !usualName || len(linesMissing(text, dockerIgnore.lines)) > 0 {
			files = append(files, dockerIgnore)
		}
	}
	return files
}

// linesMissing returns the lines that text, an ignore file, doesn't have.
func linesMissing(text []byte, lines []string) []string {
	have := map[string]bool{}
	for _, line := range strings.Split(string(text), "\n") {
		have[strings.TrimSpace(line)] = true
	}
	var missing []string
	for _, line := range lines {
		if !have[line] {
			missing = append(missing, line)
		}
	}
	return missing
}

// addIgnoreLines appends to file the lines it doesn't already have, creating
// it if need be, and returns the ones it added.
func addIgnoreLines(file string, lines []string) ([]string, error) {
	old, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	add := linesMissing(old, lines)
	if len(add) == 0 {
		return nil, nil
	}
	text := strings.Join(add, "\n") + "\n"
	if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		text = "\n" + text
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	_, err = f.WriteString(text)
	return add, errors.Join(err, f.Close())
}

// findGit returns a function that runs git in dir, saying nothing, and
// returns its exit code; nil if git isn't on PATH.
func findGit(dir string) func(args ...string) int {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil
	}
	return func(args ...string) int {
		cmd := exec.Command(git, args...)
		cmd.Dir = dir
		err := cmd.Run()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			return 0
		case errors.As(err, &exitErr):
			return exitErr.ExitCode()
		default:
			return 127
		}
	}
}
