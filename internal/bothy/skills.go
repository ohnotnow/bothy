package bothy

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// The Claude Code skills that go with this bothy are built into the binary
// (claudefiles.go at the repo root), so a binary and its skill always come
// from the same release. `install-skills` copies them into ~/.claude, and
// `version` says when the installed copies differ. Ported from ait's
// internal/ait/skills.go; keep the two behaving the same, apart from
// optional skills, which only bothy has.

// baseSkill is the skill every install gets. Any other built-in skill is
// optional: install-skills adds it with --all, and from then on keeps it up
// to date like the base one, until the developer deletes its folder.
const baseSkill = "bothy"

// skillOf returns the skill a built-in file belongs to: "bothy-lando-migrate"
// for skills/bothy-lando-migrate/SKILL.md. Anything outside skills/<name>/
// goes with the base skill.
func skillOf(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) > 2 && parts[0] == "skills" {
		return parts[1]
	}
	return baseSkill
}

// wantedSkillFiles drops the files of optional skills that aren't installed
// in claudeDir, unless all is set, and returns those skills' names. list is
// sorted, so a skill's files sit together.
func wantedSkillFiles(list []skillFile, claudeDir string, all bool) (wanted []skillFile, skipped []string) {
	for _, f := range list {
		name := skillOf(f.rel)
		if all || name == baseSkill || dirExists(filepath.Join(claudeDir, "skills", name)) {
			wanted = append(wanted, f)
			continue
		}
		if len(skipped) == 0 || skipped[len(skipped)-1] != name {
			skipped = append(skipped, name)
		}
	}
	return wanted, skipped
}

// skillFile is one built-in file and where it would be installed.
type skillFile struct {
	rel     string // skills/bothy/SKILL.md
	content []byte
	dest    string
}

func skillFilesFor(files fs.FS, claudeDir string) ([]skillFile, error) {
	var out []skillFile
	err := fs.WalkDir(files, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		out = append(out, skillFile{rel: path, content: b, dest: filepath.Join(claudeDir, filepath.FromSlash(path))})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, err
}

// installed says how the copy at f.dest compares: "missing", "same" or "differs".
func (f skillFile) installed() string {
	b, err := os.ReadFile(f.dest)
	switch {
	case err != nil:
		return "missing"
	case bytes.Equal(b, f.content):
		return "same"
	}
	return "differs"
}

// DefaultClaudeDir is ~/.claude, where install-skills puts the skills.
func DefaultClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".claude"
	}
	return filepath.Join(home, ".claude")
}

// skillsStatus is one line for `bothy version`: whether the installed skills
// match the ones built into this binary. An optional skill that isn't
// installed is not counted.
func skillsStatus(files fs.FS, claudeDir string) string {
	list, err := skillFilesFor(files, claudeDir)
	if err != nil {
		return "skills: couldn't read the built-in copies: " + err.Error()
	}
	list, _ = wantedSkillFiles(list, claudeDir, false)
	var missing, differ []string
	for _, f := range list {
		switch f.installed() {
		case "missing":
			missing = append(missing, f.rel)
		case "differs":
			differ = append(differ, f.rel)
		}
	}
	switch {
	case len(missing) == len(list):
		return fmt.Sprintf("skills: not installed in %s; run bothy install-skills", claudeDir)
	case len(missing)+len(differ) == 0:
		return fmt.Sprintf("skills: up to date in %s", claudeDir)
	}
	return fmt.Sprintf("skills: %s in %s differ from this bothy's; run bothy install-skills to see and update them",
		strings.Join(append(differ, missing...), ", "), claudeDir)
}

// cmdInstallSkills copies each built-in file into place. A new file is
// written; an identical one is left alone; a changed one is replaced only
// with --force or a yes at the prompt ([y/N/d], d shows the diff). With no
// terminal to ask on, a changed file is left as it is and reported. Nothing
// is ever deleted. An optional skill is only added with --all; once it is
// installed, a plain run keeps it up to date.
func cmdInstallSkills(a *App, args []string) int {
	fset := flag.NewFlagSet("install-skills", flag.ContinueOnError)
	fset.SetOutput(io.Discard)
	dir := fset.String("dir", a.ClaudeDir, "")
	force := fset.Bool("force", false, "")
	dryRun := fset.Bool("dry-run", false, "")
	all := fset.Bool("all", false, "")
	if err := fset.Parse(args); err != nil || fset.NArg() > 0 {
		return a.usageError("usage: bothy install-skills [--all] [--dry-run] [--force] [--dir D]")
	}
	if a.Skills == nil {
		fmt.Fprintln(a.Stderr, "bothy: this build has no skills built in")
		return 1
	}
	if err := a.installSkills(*dir, *force, *dryRun || a.dryRun, *all); err != nil {
		fmt.Fprintf(a.Stderr, "bothy: %v\n", err)
		return 1
	}
	return 0
}

func (a *App) installSkills(claudeDir string, force, dryRun, all bool) error {
	list, err := skillFilesFor(a.Skills, claudeDir)
	if err != nil {
		return err
	}
	list, skipped := wantedSkillFiles(list, claudeDir, all)
	out := a.Stdout
	if dryRun {
		fmt.Fprintf(out, "Dry run: nothing will be written. Comparing with %s:\n", claudeDir)
	} else {
		fmt.Fprintf(out, "Installing the skills from bothy %s into %s:\n", Version, claudeDir)
	}
	reader := bufio.NewReader(a.Stdin)
	var left []string
	for _, f := range list {
		state := f.installed()
		switch {
		case state == "same":
			fmt.Fprintf(out, "  %s: unchanged\n", f.rel)
			continue
		case dryRun && state == "missing":
			fmt.Fprintf(out, "  %s: would be added\n", f.rel)
			continue
		case dryRun:
			fmt.Fprintf(out, "  %s: differs, would ask before replacing\n", f.rel)
			continue
		case state == "differs" && !force && !a.Interactive:
			fmt.Fprintf(out, "  %s: differs, left as it is (no terminal to ask on; --force replaces it)\n", f.rel)
			left = append(left, f.rel)
			continue
		case state == "differs" && !force:
			yes, err := askReplace(out, reader, f)
			if err != nil {
				return err
			}
			if !yes {
				fmt.Fprintf(out, "  %s: kept your copy\n", f.rel)
				left = append(left, f.rel)
				continue
			}
		}
		if err := writeSkillFile(f); err != nil {
			return err
		}
		verb := "added"
		if state == "differs" {
			verb = "replaced"
		}
		fmt.Fprintf(out, "  %s: %s\n", f.rel, verb)
	}
	for _, name := range skipped {
		fmt.Fprintf(out, "  skills/%s: not installed (optional: --all adds it)\n", name)
	}
	if len(left) > 0 && !dryRun {
		fmt.Fprintf(out, "Left as they were: %s.\n", strings.Join(left, ", "))
	}
	return nil
}

// errAborted ends install-skills when the input runs out mid-prompt, so an
// end of input can never be taken as an answer to the questions after it.
var errAborted = errors.New("aborted: the input ended before every question was answered; nothing more was changed")

func askReplace(out io.Writer, in *bufio.Reader, f skillFile) (bool, error) {
	fmt.Fprintf(out, "  %s differs from the copy in bothy %s.\n", f.dest, Version)
	for {
		fmt.Fprint(out, "  Replace it with bothy's? [y/N/d] ")
		line, err := in.ReadString('\n')
		if err != nil && (err != io.EOF || line == "") {
			fmt.Fprintln(out)
			return false, errAborted
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "d", "diff":
			showDiff(out, f)
		case "y", "yes":
			return true, nil
		default:
			return false, nil
		}
	}
}

// showDiff prints `diff -u` of the installed copy against bothy's.
func showDiff(out io.Writer, f skillFile) {
	tmp, err := os.CreateTemp("", "bothy-skill-*")
	if err != nil {
		fmt.Fprintf(out, "  (can't show a diff: %v)\n", err)
		return
	}
	defer os.Remove(tmp.Name())
	tmp.Write(f.content)
	tmp.Close()
	cmd := exec.Command("diff", "-u", "--label", "installed "+f.rel, "--label", "bothy "+Version+" "+f.rel, f.dest, tmp.Name())
	b, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		fmt.Fprintf(out, "  (can't show a diff: %v)\n", err)
		return
	}
	out.Write(b)
}

func writeSkillFile(f skillFile) error {
	if err := os.MkdirAll(filepath.Dir(f.dest), 0o755); err != nil {
		return err
	}
	return os.WriteFile(f.dest, f.content, 0o644)
}
