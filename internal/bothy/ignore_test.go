package bothy

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAddIgnoreLines(t *testing.T) {
	for _, tc := range []struct {
		name  string
		old   string // "-" for no file
		added []string
		want  string
	}{
		{"no file", "-", []string{"*.sql", "*.sql.gz"}, "*.sql\n*.sql.gz\n"},
		{"file ending in a newline", "vendor\n", []string{"*.sql", "*.sql.gz"}, "vendor\n*.sql\n*.sql.gz\n"},
		{"file with no final newline", "vendor", []string{"*.sql", "*.sql.gz"}, "vendor\n*.sql\n*.sql.gz\n"},
		{"one line already there", "*.sql\nvendor\n", []string{"*.sql.gz"}, "*.sql\nvendor\n*.sql.gz\n"},
		{"both lines already there", "*.sql\n*.sql.gz\n", nil, "*.sql\n*.sql.gz\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), ".gitignore")
			if tc.old != "-" {
				os.WriteFile(file, []byte(tc.old), 0o644)
			}
			added, err := addIgnoreLines(file, gitIgnore.lines)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(added, tc.added) {
				t.Errorf("added %q, want %q", added, tc.added)
			}
			if got, _ := os.ReadFile(file); string(got) != tc.want {
				t.Errorf("file holds %q, want %q", got, tc.want)
			}
		})
	}
}

// inRepoWithDockerfile gives the app a Dockerfile and a fake git, which says
// a path is ignored when .gitignore has the line "*.sql" and records the
// paths it was asked about.
func inRepoWithDockerfile(t *testing.T, a *App) *[]string {
	t.Helper()
	os.WriteFile(filepath.Join(a.Dir, "Dockerfile"), []byte("FROM scratch\nCOPY . /app\n"), 0o644)
	var asked []string
	a.Git = func(args ...string) int {
		asked = append(asked, args[len(args)-1])
		text, _ := os.ReadFile(filepath.Join(a.Dir, ".gitignore"))
		if strings.Contains("\n"+string(text), "\n*.sql\n") {
			return 0
		}
		return 1
	}
	return &asked
}

// exportApp is fakeApp ready for db-export, in a git repo with a Dockerfile
// and no ignore files.
func exportApp(t *testing.T) (*App, *[]string, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	a, _, stdout, stderr := fakeApp(t, true)
	withPipe(a, "-- dump\n", 0)
	return a, inRepoWithDockerfile(t, a), stdout, stderr
}

// ignoreFiles returns what the app's .gitignore and .dockerignore hold, ""
// for one that isn't there.
func ignoreFiles(a *App) (git, docker string) {
	g, _ := os.ReadFile(filepath.Join(a.Dir, ".gitignore"))
	d, _ := os.ReadFile(filepath.Join(a.Dir, ".dockerignore"))
	return string(g), string(d)
}

func TestDumpIgnoreCheck(t *testing.T) {
	const gitLines = "*.sql\n*.sql.gz\n"
	const dockerLines = "**/*.sql\n**/*.sql.gz\n"

	t.Run("export: y adds the lines to both files, then exports, and the next run doesn't ask", func(t *testing.T) {
		a, _, stdout, _ := exportApp(t)
		a.Stdin = strings.NewReader("y\n")
		if code := a.Main([]string{"db-export", "out.sql.gz"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if g, d := ignoreFiles(a); g != gitLines || d != dockerLines {
			t.Errorf(".gitignore holds %q, .dockerignore holds %q", g, d)
		}
		if !fileExists(filepath.Join(a.Dir, "out.sql.gz")) {
			t.Error("no export")
		}
		for _, s := range []string{"out.sql.gz is in the app folder", ".gitignore or .dockerignore", "[y/N]", "Added *.sql and *.sql.gz to .gitignore", "Added **/*.sql and **/*.sql.gz to .dockerignore"} {
			if !strings.Contains(stdout.String(), s) {
				t.Errorf("output missing %q:\n%s", s, stdout)
			}
		}

		stdout.Reset()
		a.Stdin = strings.NewReader("")
		if code := a.Main([]string{"db-export", "again.sql.gz"}); code != 0 {
			t.Fatalf("second run: exit %d", code)
		}
		if strings.Contains(stdout.String(), "[y/N]") {
			t.Errorf("second run asked again:\n%s", stdout)
		}
	})

	t.Run("export: anything but y changes nothing and still exports", func(t *testing.T) {
		a, _, _, _ := exportApp(t)
		a.Stdin = strings.NewReader("\n")
		if code := a.Main([]string{"db-export"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if g, d := ignoreFiles(a); g+d != "" {
			t.Error("an ignore file was written")
		}
		if m, _ := filepath.Glob(filepath.Join(a.Dir, "*.sql.gz")); len(m) != 1 {
			t.Errorf("exports: %q", m)
		}
	})

	t.Run("export with no terminal warns for an agent, changes nothing and still exports", func(t *testing.T) {
		a, _, stdout, stderr := exportApp(t)
		a.Interactive = false
		if code := a.Main([]string{"db-export", "out.sql"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		for _, s := range []string{"bothy: warning: out.sql is in the app folder", "real people's data", "tell the developer", "*.sql and *.sql.gz to .gitignore", "**/*.sql and **/*.sql.gz to .dockerignore"} {
			if !strings.Contains(stderr.String(), s) {
				t.Errorf("warning missing %q:\n%s", s, stderr)
			}
		}
		if strings.Contains(stdout.String(), "[y/N]") {
			t.Error("asked with no terminal")
		}
		if g, d := ignoreFiles(a); g+d != "" {
			t.Error("an ignore file was written")
		}
		if !fileExists(filepath.Join(a.Dir, "out.sql")) {
			t.Error("no export")
		}
	})

	t.Run("only the file that lacks the lines is named and changed", func(t *testing.T) {
		a, _, stdout, _ := exportApp(t)
		os.WriteFile(filepath.Join(a.Dir, ".gitignore"), []byte(gitLines), 0o644)
		a.Stdin = strings.NewReader("y\n")
		a.Main([]string{"db-export", "out.sql"})
		if strings.Contains(stdout.String(), ".gitignore") || !strings.Contains(stdout.String(), "isn't ignored by .dockerignore.") {
			t.Errorf("output:\n%s", stdout)
		}
		if g, d := ignoreFiles(a); g != gitLines || d != dockerLines {
			t.Errorf(".gitignore holds %q, .dockerignore holds %q", g, d)
		}
	})

	t.Run("a .dockerignore with only the top-level lines gets the **/ ones added", func(t *testing.T) {
		a, asked, _, _ := exportApp(t)
		os.WriteFile(filepath.Join(a.Dir, ".gitignore"), []byte(gitLines), 0o644)
		os.WriteFile(filepath.Join(a.Dir, ".dockerignore"), []byte(gitLines), 0o644)
		os.Mkdir(filepath.Join(a.Dir, "storage"), 0o755)
		a.Stdin = strings.NewReader("y\n")
		a.Main([]string{"db-export", "storage/out.sql"})
		if _, d := ignoreFiles(a); d != gitLines+dockerLines {
			t.Errorf(".dockerignore holds %q", d)
		}
		if want := []string{"storage/out.sql"}; !reflect.DeepEqual(*asked, want) {
			t.Errorf("git was asked about %q, want %q", *asked, want)
		}
	})

	t.Run("quiet when both files cover the dump", func(t *testing.T) {
		a, _, stdout, stderr := exportApp(t)
		os.WriteFile(filepath.Join(a.Dir, ".gitignore"), []byte(gitLines), 0o644)
		os.WriteFile(filepath.Join(a.Dir, ".dockerignore"), []byte("vendor\n"+dockerLines), 0o644)
		a.Stdin = strings.NewReader("")
		a.Main([]string{"db-export", "out.sql"})
		if out := stdout.String() + stderr.String(); strings.Contains(out, "ignore") {
			t.Errorf("said something:\n%s", out)
		}
	})

	t.Run("a file outside the app folder is not checked", func(t *testing.T) {
		a, asked, _, stderr := exportApp(t)
		a.Interactive = false
		for _, name := range []string{filepath.Join(t.TempDir(), "out.sql"), "../out.sql"} {
			if code := a.Main([]string{"db-export", name}); code != 0 {
				t.Fatalf("%s: exit %d", name, code)
			}
		}
		if len(*asked) != 0 || stderr.String() != "" {
			t.Errorf("checked anyway: git asked about %q, stderr:\n%s", *asked, stderr)
		}
	})

	t.Run("no git repo, no Dockerfile and no .dockerignore: nothing to check", func(t *testing.T) {
		a, _, _, stderr := exportApp(t)
		a.Interactive = false
		a.Git = func(...string) int { return 128 }
		os.Remove(filepath.Join(a.Dir, "Dockerfile"))
		a.Main([]string{"db-export", "out.sql"})
		if stderr.String() != "" {
			t.Errorf("warned:\n%s", stderr)
		}
	})

	t.Run("a dump with another name gets a warning, not an offer, even on a terminal", func(t *testing.T) {
		a, _, stdout, stderr := exportApp(t)
		a.Stdin = strings.NewReader("y\n")
		a.Main([]string{"db-export", "backup.gz"})
		if !strings.Contains(stderr.String(), "only cover names ending in .sql or .sql.gz") || strings.Contains(stderr.String(), "tell the developer") {
			t.Errorf("warning:\n%s", stderr)
		}
		if strings.Contains(stdout.String(), "[y/N]") {
			t.Errorf("offered lines that would not cover it:\n%s", stdout)
		}
		if g, d := ignoreFiles(a); g+d != "" {
			t.Error("an ignore file was written")
		}
	})

	t.Run("--dry-run neither checks nor asks", func(t *testing.T) {
		a, asked, _, _ := exportApp(t)
		a.BackupDir = t.TempDir()
		a.Stdin = strings.NewReader("y\n")
		os.WriteFile(filepath.Join(a.Dir, "dump.sql"), []byte("x"), 0o644)
		a.Main([]string{"--dry-run", "db-export", "out.sql"})
		a.Main([]string{"--dry-run", "db-import", "dump.sql"})
		if g, d := ignoreFiles(a); len(*asked) != 0 || g+d != "" {
			t.Errorf("dry run checked: git asked about %q", *asked)
		}
	})

	t.Run("import: the lines go in before the backup and the wipe", func(t *testing.T) {
		a, log, stdout, _, _ := importApp(t, "1024")
		inRepoWithDockerfile(t, a)
		os.WriteFile(filepath.Join(a.Dir, "prod.sql"), []byte("x"), 0o644)
		a.Stdin = strings.NewReader("y\n")
		pipe := a.Pipe
		var linesInFirst bool
		a.Pipe = func(args []string, stdin io.Reader, out io.Writer) int {
			if len(*log) == 0 {
				g, d := ignoreFiles(a)
				linesInFirst = g == gitLines && d == dockerLines
			}
			return pipe(args, stdin, out)
		}
		if code := a.Main([]string{"db-import", "prod.sql"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if !linesInFirst {
			t.Errorf("the ignore lines were not there when the backup started:\n%s", stdout)
		}
		if want := []string{"pipe " + dbDump, "run " + dbWipe, "pipe " + dbClient}; !reflect.DeepEqual(*log, want) {
			t.Errorf("docker calls: %q", *log)
		}
	})

	t.Run("import with no terminal warns and still imports", func(t *testing.T) {
		a, log, _, stderr, _ := importApp(t, "1024")
		a.Interactive = false
		inRepoWithDockerfile(t, a)
		os.WriteFile(filepath.Join(a.Dir, "prod.sql"), []byte("x"), 0o644)
		if code := a.Main([]string{"db-import", "prod.sql"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if !strings.Contains(stderr.String(), "tell the developer") || len(*log) != 3 {
			t.Errorf("docker calls %q, stderr:\n%s", *log, stderr)
		}
	})

	t.Run("import: a dump that fails its checks is refused before any question", func(t *testing.T) {
		a, _, stdout, _, _ := importApp(t, "1024")
		asked := inRepoWithDockerfile(t, a)
		os.WriteFile(filepath.Join(a.Dir, "empty.sql"), nil, 0o644)
		if code := a.Main([]string{"db-import", "empty.sql"}); code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if len(*asked) != 0 || strings.Contains(stdout.String(), "[y/N]") {
			t.Errorf("checked a dump it then refused:\n%s", stdout)
		}
	})
}
