package bothy

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
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

// importApp is fakeApp set up for db-import: backups go to a temp folder,
// the database reports size bytes, and every docker call, attached or piped,
// is logged in order as "run <last arg>" or "pipe <last arg>". Pipe calls
// return the next code from pipeCodes (0 when exhausted) and write nothing.
func importApp(t *testing.T, size string, pipeCodes ...int) (*App, *[]string, *bytes.Buffer, *bytes.Buffer, *[]pipeCall) {
	t.Helper()
	a, _, stdout, stderr := fakeApp(t, true)
	a.BackupDir = t.TempDir()
	var log []string
	var pipes []pipeCall
	a.Run = func(args []string) int {
		log = append(log, "run "+args[len(args)-1])
		return 0
	}
	a.Pipe = func(args []string, stdin io.Reader, stdout io.Writer) int {
		log = append(log, "pipe "+args[len(args)-1])
		c := pipeCall{args: args}
		if stdin != nil {
			b, _ := io.ReadAll(stdin)
			c.stdin = string(b)
		}
		pipes = append(pipes, c)
		if len(pipeCodes) == 0 {
			return 0
		}
		code := pipeCodes[0]
		pipeCodes = pipeCodes[1:]
		return code
	}
	a.Output = func(args []string) ([]byte, error) {
		if args[len(args)-1] == dbSize {
			return []byte(size + "\n"), nil
		}
		return fakeOutput(nil)(args)
	}
	return a, &log, stdout, stderr, &pipes
}

func backups(t *testing.T, a *App) []string {
	t.Helper()
	found, _ := filepath.Glob(filepath.Join(a.BackupDir, "bothy-"+filepath.Base(a.Dir)+"-*.sql.gz"))
	return found
}

func TestDbImport(t *testing.T) {
	for _, tc := range []struct {
		file string
		data func(t *testing.T) []byte
	}{
		{"dump.sql", func(*testing.T) []byte { return []byte("CREATE TABLE users;") }},
		{"dump.sql.gz", func(t *testing.T) []byte { return gzipped(t, "CREATE TABLE users;") }},
	} {
		t.Run(tc.file+" is loaded after a backup and a wipe", func(t *testing.T) {
			a, log, stdout, _, pipes := importApp(t, "1024")
			os.WriteFile(filepath.Join(a.Dir, tc.file), tc.data(t), 0o644)

			if code := a.Main([]string{"db-import", tc.file}); code != 0 {
				t.Fatalf("exit %d", code)
			}
			want := []string{"pipe " + dbDump, "run " + dbWipe, "pipe " + dbClient}
			if !reflect.DeepEqual(*log, want) {
				t.Fatalf("docker calls:\n got %q\nwant %q", *log, want)
			}
			load := []string{"compose", "-f", ComposeFile, "exec", "-T", "database", "sh", "-c", dbClient}
			if !reflect.DeepEqual((*pipes)[1].args, load) {
				t.Fatalf("load: got %q, want %q", (*pipes)[1].args, load)
			}
			if got := (*pipes)[1].stdin; got != "CREATE TABLE users;" {
				t.Fatalf("fed %q to the client", got)
			}
			b := backups(t, a)
			if len(b) != 1 {
				t.Fatalf("backups: %q", b)
			}
			if !strings.Contains(stdout.String(), b[0]) {
				t.Fatalf("backup path not shown:\n%s", stdout)
			}
			if info, _ := os.Stat(b[0]); info.Mode().Perm() != 0o600 {
				t.Fatalf("backup is readable by others: %v", info.Mode())
			}
		})
	}

	for _, tc := range []struct {
		name string
		file string
		make func(t *testing.T, path string)
		code int
	}{
		{"missing file", "nope.sql", func(*testing.T, string) {}, exitNotFound},
		{"folder", "dump.sql", func(_ *testing.T, p string) { os.Mkdir(p, 0o755) }, 1},
		{"empty file", "dump.sql", func(_ *testing.T, p string) { os.WriteFile(p, nil, 0o644) }, 1},
		{"not gzip", "dump.sql.gz", func(_ *testing.T, p string) { os.WriteFile(p, []byte("CREATE TABLE users;"), 0o644) }, 1},
		{"truncated gzip", "dump.sql.gz", func(t *testing.T, p string) {
			gz := gzipped(t, strings.Repeat("INSERT INTO users VALUES (1);\n", 1000))
			os.WriteFile(p, gz[:len(gz)/2], 0o644)
		}, 1},
		{"gzip of nothing", "dump.sql.gz", func(t *testing.T, p string) { os.WriteFile(p, gzipped(t, ""), 0o644) }, 1},
	} {
		t.Run(tc.name+" touches nothing", func(t *testing.T) {
			a, log, _, _, _ := importApp(t, "1024")
			tc.make(t, filepath.Join(a.Dir, tc.file))
			if code := a.Main([]string{"db-import", tc.file}); code != tc.code {
				t.Fatalf("exit %d, want %d", code, tc.code)
			}
			if len(*log) != 0 {
				t.Fatalf("docker was called: %q", *log)
			}
		})
	}

	writeDump := func(a *App) {
		os.WriteFile(filepath.Join(a.Dir, "dump.sql"), []byte("x"), 0o644)
	}
	big := strconv.Itoa(5 << 30)

	t.Run("big database: y skips the backup", func(t *testing.T) {
		a, log, stdout, _, _ := importApp(t, big)
		a.Stdin = strings.NewReader("y\n")
		writeDump(a)
		if code := a.Main([]string{"db-import", "dump.sql"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		want := []string{"run " + dbWipe, "pipe " + dbClient}
		if !reflect.DeepEqual(*log, want) {
			t.Fatalf("docker calls:\n got %q\nwant %q", *log, want)
		}
		if !strings.Contains(stdout.String(), "5.0 GB") {
			t.Fatalf("size not shown:\n%s", stdout)
		}
	})

	t.Run("big database: anything else backs up", func(t *testing.T) {
		a, log, _, _, _ := importApp(t, big)
		a.Stdin = strings.NewReader("\n")
		writeDump(a)
		if code := a.Main([]string{"db-import", "dump.sql"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if len(*log) != 3 || (*log)[0] != "pipe "+dbDump || len(backups(t, a)) != 1 {
			t.Fatalf("no backup first: %q", *log)
		}
	})

	t.Run("big database with no terminal refuses", func(t *testing.T) {
		a, log, _, stderr, _ := importApp(t, big)
		a.Interactive = false
		writeDump(a)
		if code := a.Main([]string{"db-import", "dump.sql"}); code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if len(*log) != 0 {
			t.Fatalf("docker was called: %q", *log)
		}
		if !strings.Contains(stderr.String(), "terminal") {
			t.Fatalf("no reason given:\n%s", stderr)
		}
	})

	t.Run("small database with no terminal backs up without asking", func(t *testing.T) {
		a, log, _, _, _ := importApp(t, "1024")
		a.Interactive = false
		writeDump(a)
		if code := a.Main([]string{"db-import", "dump.sql"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if len(*log) != 3 || len(backups(t, a)) != 1 {
			t.Fatalf("docker calls: %q", *log)
		}
	})

	t.Run("does not wipe when the size check fails", func(t *testing.T) {
		a, log, _, _, _ := importApp(t, "")
		a.Output = func([]string) ([]byte, error) { return nil, errors.New("no container") }
		writeDump(a)
		if code := a.Main([]string{"db-import", "dump.sql"}); code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if len(*log) != 0 {
			t.Fatalf("docker was called: %q", *log)
		}
	})

	t.Run("does not wipe when the backup fails", func(t *testing.T) {
		a, log, _, _, _ := importApp(t, "1024", 2)
		writeDump(a)
		if code := a.Main([]string{"db-import", "dump.sql"}); code != 2 {
			t.Fatalf("exit %d, want 2", code)
		}
		if want := []string{"pipe " + dbDump}; !reflect.DeepEqual(*log, want) {
			t.Fatalf("docker calls: %q", *log)
		}
		if b := backups(t, a); len(b) != 0 {
			t.Fatalf("failed backup left behind: %q", b)
		}
	})

	t.Run("does not load when the wipe fails", func(t *testing.T) {
		a, log, _, _, _ := importApp(t, "1024")
		a.Run = func(args []string) int {
			*log = append(*log, "run "+args[len(args)-1])
			return 1
		}
		writeDump(a)
		if code := a.Main([]string{"db-import", "dump.sql"}); code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		if want := []string{"pipe " + dbDump, "run " + dbWipe}; !reflect.DeepEqual(*log, want) {
			t.Fatalf("docker calls: %q", *log)
		}
	})

	t.Run("a failed load says how to put the backup back", func(t *testing.T) {
		a, _, _, stderr, _ := importApp(t, "1024", 0, 1)
		writeDump(a)
		if code := a.Main([]string{"db-import", "dump.sql"}); code != 1 {
			t.Fatalf("exit %d, want 1", code)
		}
		b := backups(t, a)
		if len(b) != 1 || !strings.Contains(stderr.String(), "bothy db-import "+b[0]) {
			t.Fatalf("backups %q, stderr:\n%s", b, stderr)
		}
	})
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{
		0:              "0 bytes",
		1023:           "1023 bytes",
		1536:           "1.5 KB",
		backupAskAbove: "1.0 GB",
		5 << 30:        "5.0 GB",
		3 * (1 << 40):  "3.0 TB",
	} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
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
	for _, s := range []string{"> out.sql", "< missing.sql", "DROP DATABASE", "| gzip > " + filepath.Join(a.BackupDir, "bothy-")} {
		if !strings.Contains(out, s) {
			t.Errorf("dry run output missing %q:\n%s", s, out)
		}
	}
}
