package bothy

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type pipeCall struct {
	args  []string
	stdin string
}

// withPipe gives the app a fake Pipe that records calls, captures what was
// fed to stdin, writes output to stdout and returns code.
func withPipe(a *App, output string, code int) *[]pipeCall {
	var calls []pipeCall
	a.Pipe = func(args []string, stdin io.Reader, stdout io.Writer) int {
		c := pipeCall{args: args}
		if stdin != nil {
			b, _ := io.ReadAll(stdin)
			c.stdin = string(b)
		}
		if stdout != nil {
			io.WriteString(stdout, output)
		}
		calls = append(calls, c)
		return code
	}
	return &calls
}

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write([]byte(s))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestMysqlRunsClientWithContainerCredentials(t *testing.T) {
	for _, name := range []string{"mysql", "database"} {
		a, calls, _, _ := fakeApp(t, true)
		a.Main([]string{name})
		want := []string{"compose", "-f", ComposeFile, "exec", "database", "sh", "-c", dbClient}
		if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0], want) {
			t.Fatalf("%s: got %q, want %q", name, *calls, want)
		}
	}
	if !strings.Contains(dbClient, `"$MARIADB_ROOT_PASSWORD"`) {
		t.Fatalf("client should take the password from the container: %s", dbClient)
	}
}

func TestDbImport(t *testing.T) {
	for _, tc := range []struct {
		file string
		data func(t *testing.T) []byte
	}{
		{"dump.sql", func(*testing.T) []byte { return []byte("CREATE TABLE users;") }},
		{"dump.sql.gz", func(t *testing.T) []byte { return gzipped(t, "CREATE TABLE users;") }},
	} {
		t.Run(tc.file, func(t *testing.T) {
			a, runs, _, _ := fakeApp(t, true)
			pipes := withPipe(a, "", 0)
			os.WriteFile(filepath.Join(a.Dir, tc.file), tc.data(t), 0o644)

			if code := a.Main([]string{"db-import", tc.file}); code != 0 {
				t.Fatalf("exit %d", code)
			}
			wipe := []string{"compose", "-f", ComposeFile, "exec", "database", "sh", "-c", dbWipe}
			if len(*runs) != 1 || !reflect.DeepEqual((*runs)[0], wipe) {
				t.Fatalf("wipe: got %q, want %q", *runs, wipe)
			}
			load := []string{"compose", "-f", ComposeFile, "exec", "-T", "database", "sh", "-c", dbClient}
			if len(*pipes) != 1 || !reflect.DeepEqual((*pipes)[0].args, load) {
				t.Fatalf("load: got %+v, want %q", *pipes, load)
			}
			if got := (*pipes)[0].stdin; got != "CREATE TABLE users;" {
				t.Fatalf("fed %q to the client", got)
			}
		})
	}

	t.Run("missing file touches nothing", func(t *testing.T) {
		a, runs, _, _ := fakeApp(t, true)
		pipes := withPipe(a, "", 0)
		if code := a.Main([]string{"db-import", "nope.sql"}); code != exitNotFound {
			t.Fatalf("exit %d, want %d", code, exitNotFound)
		}
		if len(*runs)+len(*pipes) != 0 {
			t.Fatal("docker was called")
		}
	})

	t.Run("does not load when the wipe fails", func(t *testing.T) {
		a, _, _, _ := fakeApp(t, true, 1)
		pipes := withPipe(a, "", 0)
		os.WriteFile(filepath.Join(a.Dir, "dump.sql"), []byte("x"), 0o644)
		if code := a.Main([]string{"db-import", "dump.sql"}); code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if len(*pipes) != 0 {
			t.Fatal("dump was loaded after a failed wipe")
		}
	})
}

func TestDbExport(t *testing.T) {
	const dump = "-- MariaDB dump\nCREATE TABLE users;\n"

	t.Run("plain file", func(t *testing.T) {
		a, _, _, _ := fakeApp(t, true)
		pipes := withPipe(a, dump, 0)
		if code := a.Main([]string{"db-export", "out.sql"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		want := []string{"compose", "-f", ComposeFile, "exec", "-T", "database", "sh", "-c", dbDump}
		if !reflect.DeepEqual((*pipes)[0].args, want) {
			t.Fatalf("got %q, want %q", (*pipes)[0].args, want)
		}
		got, _ := os.ReadFile(filepath.Join(a.Dir, "out.sql"))
		if string(got) != dump {
			t.Fatalf("file holds %q", got)
		}
	})

	t.Run("default name is gzipped and named after the folder", func(t *testing.T) {
		a, _, _, _ := fakeApp(t, true)
		withPipe(a, dump, 0)
		if code := a.Main([]string{"db-export"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		matches, _ := filepath.Glob(filepath.Join(a.Dir, filepath.Base(a.Dir)+"-*.sql.gz"))
		if len(matches) != 1 {
			t.Fatalf("found %q", matches)
		}
		f, _ := os.Open(matches[0])
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(zr)
		if string(got) != dump {
			t.Fatalf("file holds %q", got)
		}
	})

	t.Run("refuses to overwrite", func(t *testing.T) {
		a, _, _, _ := fakeApp(t, true)
		pipes := withPipe(a, dump, 0)
		target := filepath.Join(a.Dir, "out.sql")
		os.WriteFile(target, []byte("precious"), 0o644)
		if code := a.Main([]string{"db-export", "out.sql"}); code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if len(*pipes) != 0 {
			t.Fatal("dump ran")
		}
		if got, _ := os.ReadFile(target); string(got) != "precious" {
			t.Fatalf("file was changed to %q", got)
		}
	})

	t.Run("failed dump leaves no file", func(t *testing.T) {
		a, _, _, _ := fakeApp(t, true)
		withPipe(a, "partial", 2)
		if code := a.Main([]string{"db-export", "out.sql"}); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
		if _, err := os.Stat(filepath.Join(a.Dir, "out.sql")); !os.IsNotExist(err) {
			t.Fatal("half-written dump was left behind")
		}
	})
}

func TestDbDryRunTouchesNoFiles(t *testing.T) {
	a, runs, stdout, _ := fakeApp(t, true)
	pipes := withPipe(a, "", 0)
	a.Main([]string{"--dry-run", "db-export", "out.sql"})
	a.Main([]string{"--dry-run", "db-import", "missing.sql"})
	if len(*runs)+len(*pipes) != 0 {
		t.Fatal("docker was called")
	}
	if _, err := os.Stat(filepath.Join(a.Dir, "out.sql")); !os.IsNotExist(err) {
		t.Fatal("dry run created the export file")
	}
	out := stdout.String()
	for _, s := range []string{"> out.sql", "< missing.sql", "DROP DATABASE"} {
		if !strings.Contains(out, s) {
			t.Errorf("dry run output missing %q:\n%s", s, out)
		}
	}
}
