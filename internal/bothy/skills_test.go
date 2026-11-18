package bothy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

var testSkills = fstest.MapFS{
	"skills/bothy/SKILL.md":  {Data: []byte("bothy skill v2\n")},
	"skills/bothy/EXTRA.md":  {Data: []byte("extra v2\n")},
	"agents/bothy-helper.md": {Data: []byte("helper v2\n")},
}

// skillsApp is a fakeApp with test skills, installing into a temp dir.
// Stdin is empty and there is no terminal unless the test changes them.
func skillsApp(t *testing.T) *App {
	t.Helper()
	a, _, _, _ := fakeApp(t, false)
	a.Skills = testSkills
	a.ClaudeDir = t.TempDir()
	a.Interactive = false
	a.Stdin = strings.NewReader("")
	return a
}

// installAgain runs install-skills with fresh output and the given stdin.
func installAgain(t *testing.T, a *App, stdin string, args ...string) (string, int) {
	t.Helper()
	a.Stdout.(interface{ Reset() }).Reset()
	a.Stdin = strings.NewReader(stdin)
	code := a.Main(append([]string{"install-skills"}, args...))
	return out(a), code
}

func readOr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func TestInstallSkills(t *testing.T) {
	t.Run("missing files are added and identical ones left alone", func(t *testing.T) {
		a := skillsApp(t)
		output, code := installAgain(t, a, "")
		if code != 0 || readOr(t, filepath.Join(a.ClaudeDir, "skills/bothy/SKILL.md")) != "bothy skill v2\n" {
			t.Fatalf("exit %d, out:\n%s", code, output)
		}
		if strings.Count(output, ": added") != 3 {
			t.Fatalf("first run:\n%s", output)
		}
		output, _ = installAgain(t, a, "")
		if strings.Count(output, "unchanged") != 3 {
			t.Fatalf("second run:\n%s", output)
		}
	})

	t.Run("with no terminal, a changed file is left as it is and reported", func(t *testing.T) {
		a := skillsApp(t)
		installAgain(t, a, "")
		mine := filepath.Join(a.ClaudeDir, "skills/bothy/SKILL.md")
		os.WriteFile(mine, []byte("my tuned skill\n"), 0o644)
		output, code := installAgain(t, a, "y\n")
		if code != 0 || readOr(t, mine) != "my tuned skill\n" || !strings.Contains(output, "skills/bothy/SKILL.md: differs, left as it is") {
			t.Fatalf("exit %d, file %q, out:\n%s", code, readOr(t, mine), output)
		}
	})

	t.Run("at a terminal: N keeps it, d shows the diff and asks again, y replaces it", func(t *testing.T) {
		a := skillsApp(t)
		installAgain(t, a, "")
		a.Interactive = true
		mine := filepath.Join(a.ClaudeDir, "skills/bothy/SKILL.md")
		os.WriteFile(mine, []byte("my tuned skill\n"), 0o644)

		output, _ := installAgain(t, a, "\n")
		if readOr(t, mine) != "my tuned skill\n" || !strings.Contains(output, "kept your copy") {
			t.Fatalf("N: file %q, out:\n%s", readOr(t, mine), output)
		}
		output, _ = installAgain(t, a, "d\ny\n")
		if readOr(t, mine) != "bothy skill v2\n" || !strings.Contains(output, "-my tuned skill") ||
			!strings.Contains(output, "+bothy skill v2") || strings.Count(output, "[y/N/d]") != 2 {
			t.Fatalf("d then y: file %q, out:\n%s", readOr(t, mine), output)
		}
	})

	t.Run("input ending mid-prompt stops, and never counts as an answer", func(t *testing.T) {
		a := skillsApp(t)
		installAgain(t, a, "")
		a.Interactive = true
		for _, f := range []string{"agents/bothy-helper.md", "skills/bothy/SKILL.md"} {
			os.WriteFile(filepath.Join(a.ClaudeDir, f), []byte("mine\n"), 0o644)
		}
		_, code := installAgain(t, a, "")
		if code != 1 || !strings.Contains(errOut(a), "aborted") {
			t.Fatalf("exit %d, stderr %q", code, errOut(a))
		}
		for _, f := range []string{"agents/bothy-helper.md", "skills/bothy/SKILL.md"} {
			if got := readOr(t, filepath.Join(a.ClaudeDir, f)); got != "mine\n" {
				t.Errorf("%s = %q, want it left alone", f, got)
			}
		}
	})

	t.Run("--force replaces without asking, and --dry-run changes nothing", func(t *testing.T) {
		a := skillsApp(t)
		installAgain(t, a, "")
		mine := filepath.Join(a.ClaudeDir, "skills/bothy/SKILL.md")
		gone := filepath.Join(a.ClaudeDir, "agents/bothy-helper.md")
		os.WriteFile(mine, []byte("mine\n"), 0o644)
		os.Remove(gone)

		output, _ := installAgain(t, a, "", "--dry-run")
		if readOr(t, mine) != "mine\n" || readOr(t, gone) != "<missing>" ||
			!strings.Contains(output, "differs, would ask") || !strings.Contains(output, "would be added") {
			t.Fatalf("dry run: out:\n%s", output)
		}
		installAgain(t, a, "", "--force")
		if readOr(t, mine) != "bothy skill v2\n" || readOr(t, gone) != "helper v2\n" {
			t.Fatalf("force: skill %q, helper %q", readOr(t, mine), readOr(t, gone))
		}
	})

	t.Run("bothy's own --dry-run changes nothing either", func(t *testing.T) {
		a := skillsApp(t)
		a.Main([]string{"--dry-run", "install-skills"})
		if got := readOr(t, filepath.Join(a.ClaudeDir, "skills/bothy/SKILL.md")); got != "<missing>" {
			t.Fatalf("skill written in a dry run: %q", got)
		}
	})

	t.Run("--dir installs somewhere else", func(t *testing.T) {
		a := skillsApp(t)
		other := t.TempDir()
		installAgain(t, a, "", "--dir", other)
		if readOr(t, filepath.Join(other, "skills/bothy/SKILL.md")) != "bothy skill v2\n" {
			t.Fatal("not installed in --dir")
		}
		if readOr(t, filepath.Join(a.ClaudeDir, "skills/bothy/SKILL.md")) != "<missing>" {
			t.Fatal("installed in the default dir too")
		}
	})

	t.Run("files bothy doesn't ship are never touched", func(t *testing.T) {
		a := skillsApp(t)
		retired := filepath.Join(a.ClaudeDir, "skills/bothy-old/SKILL.md")
		os.MkdirAll(filepath.Dir(retired), 0o755)
		os.WriteFile(retired, []byte("old\n"), 0o644)
		installAgain(t, a, "", "--force")
		if readOr(t, retired) != "old\n" {
			t.Fatalf("retired skill = %q", readOr(t, retired))
		}
	})
}

func TestVersionReportsSkills(t *testing.T) {
	setBuild(t, "dev", "", "")
	a := skillsApp(t)
	version := func() string {
		a.Stdout.(interface{ Reset() }).Reset()
		a.Main([]string{"version"})
		return out(a)
	}
	if s := version(); !strings.Contains(s, "skills: not installed in "+a.ClaudeDir+"; run bothy install-skills") {
		t.Fatal(s)
	}
	installAgain(t, a, "")
	if s := version(); !strings.Contains(s, "skills: up to date in "+a.ClaudeDir) {
		t.Fatal(s)
	}
	os.WriteFile(filepath.Join(a.ClaudeDir, "skills/bothy/SKILL.md"), []byte("old\n"), 0o644)
	if s := version(); !strings.Contains(s, "skills/bothy/SKILL.md in "+a.ClaudeDir+" differ") {
		t.Fatal(s)
	}
}
