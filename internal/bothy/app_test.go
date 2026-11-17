package bothy

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeApp returns an App in a temp dir that records docker calls instead of
// running them. Each call returns the next code from codes (0 when exhausted).
func fakeApp(t *testing.T, withFile bool, codes ...int) (*App, *[][]string, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	if withFile {
		if err := os.WriteFile(filepath.Join(dir, ComposeFile), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var calls [][]string
	var stdout, stderr bytes.Buffer
	a := &App{
		Dir:         dir,
		Interactive: true,
		Stdout:      &stdout,
		Stderr:      &stderr,
		Run: func(args []string) int {
			calls = append(calls, args)
			if len(codes) == 0 {
				return 0
			}
			c := codes[0]
			codes = codes[1:]
			return c
		},
		Output: func([]string) ([]byte, error) { return nil, nil },
	}
	return a, &calls, &stdout, &stderr
}

func words(s string) []string { return strings.Fields(s) }

func TestCommandsExpand(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{words("start"), words("compose -f compose.dev.yml up -d --build")},
		{words("stop"), words("compose -f compose.dev.yml down")},
		{words("artisan route:list --path=api"), words("compose -f compose.dev.yml exec appserver php artisan route:list --path=api")},
		{words("tinker"), words("compose -f compose.dev.yml exec appserver php artisan tinker")},
		{words("composer require foo/bar"), words("compose -f compose.dev.yml exec appserver composer require foo/bar")},
		{words("test"), words("compose -f compose.dev.yml exec appserver php artisan test")},
		{words("test --parallel"), words("compose -f compose.dev.yml exec appserver php artisan test --parallel")},
		{words("testf UserTest --stop-on-failure"), words("compose -f compose.dev.yml exec appserver php artisan test --filter UserTest --stop-on-failure")},
		{words("horizon"), words("compose -f compose.dev.yml exec appserver php artisan horizon")},
		{words("npm run build"), words("compose -f compose.dev.yml run --rm node npm run build")},
		{words("shell"), words("compose -f compose.dev.yml exec appserver bash")},
		{words("ssh"), words("compose -f compose.dev.yml exec appserver bash")},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			a, calls, _, stderr := fakeApp(t, true)
			if code := a.Main(c.args); code != 0 {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0], c.want) {
				t.Fatalf("got %q, want %q", *calls, c.want)
			}
		})
	}
}

func TestRebuild(t *testing.T) {
	a, calls, _, _ := fakeApp(t, true)
	if code := a.Main([]string{"rebuild"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := [][]string{
		words("compose -f compose.dev.yml down"),
		words("compose -f compose.dev.yml build --pull"),
		words("compose -f compose.dev.yml up -d"),
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("got %q, want %q", *calls, want)
	}
}

func TestDestroy(t *testing.T) {
	destroy := [][]string{words("compose -f compose.dev.yml down -v")}
	for _, tc := range []struct {
		name        string
		interactive bool
		dryRun      bool
		input       string
		wantCode    int
		wantCalls   [][]string
	}{
		{"yes deletes", true, false, "y\n", 0, destroy},
		{"YES deletes", true, false, "YES\n", 0, destroy},
		{"no keeps", true, false, "n\n", 1, nil},
		{"enter keeps", true, false, "\n", 1, nil},
		{"input ends keeps", true, false, "", 1, nil},
		{"no terminal refuses", false, false, "y\n", 1, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, calls, _, _ := fakeApp(t, true)
			a.Interactive = tc.interactive
			a.Stdin = strings.NewReader(tc.input)
			if code := a.Main([]string{"destroy"}); code != tc.wantCode {
				t.Fatalf("exit %d, want %d", code, tc.wantCode)
			}
			if !reflect.DeepEqual(*calls, tc.wantCalls) {
				t.Fatalf("got %q, want %q", *calls, tc.wantCalls)
			}
		})
	}

	t.Run("dry run prints without asking", func(t *testing.T) {
		a, _, stdout, _ := fakeApp(t, true)
		a.Stdin = strings.NewReader("")
		if code := a.Main(words("--dry-run destroy")); code != 0 {
			t.Fatalf("exit %d", code)
		}
		if got := stdout.String(); got != "docker compose -f compose.dev.yml down -v\n" {
			t.Fatalf("got %q", got)
		}
	})
}

func TestNoTerminalAddsNoTTYFlag(t *testing.T) {
	a, calls, _, _ := fakeApp(t, true)
	a.Interactive = false
	a.Main(words("artisan about"))
	a.Main(words("npm ci"))
	want := [][]string{
		words("compose -f compose.dev.yml exec -T appserver php artisan about"),
		words("compose -f compose.dev.yml run --rm -T node npm ci"),
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("got %q, want %q", *calls, want)
	}
}

func TestMfs(t *testing.T) {
	t.Run("migrates then seeds", func(t *testing.T) {
		a, calls, _, _ := fakeApp(t, true)
		if code := a.Main([]string{"mfs"}); code != 0 {
			t.Fatalf("exit %d", code)
		}
		want := [][]string{
			words("compose -f compose.dev.yml exec appserver php artisan migrate:fresh"),
			words("compose -f compose.dev.yml exec appserver php artisan db:seed --class=TestDataSeeder"),
		}
		if !reflect.DeepEqual(*calls, want) {
			t.Fatalf("got %q, want %q", *calls, want)
		}
	})
	t.Run("does not seed when the migration fails", func(t *testing.T) {
		a, calls, _, _ := fakeApp(t, true, 3)
		if code := a.Main([]string{"mfs"}); code != 3 {
			t.Fatalf("exit %d, want 3", code)
		}
		if len(*calls) != 1 {
			t.Fatalf("got %d calls, want 1: %q", len(*calls), *calls)
		}
	})
}

func TestExitCodePassesThrough(t *testing.T) {
	a, _, _, _ := fakeApp(t, true, 2)
	if code := a.Main([]string{"test"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestMissingComposeFileBailsOut(t *testing.T) {
	a, calls, _, stderr := fakeApp(t, false)
	if code := a.Main([]string{"start"}); code != exitNotFound {
		t.Fatalf("exit %d, want %d", code, exitNotFound)
	}
	if len(*calls) != 0 {
		t.Fatalf("docker was called: %q", *calls)
	}
	if !strings.Contains(stderr.String(), "no compose.dev.yml in "+a.Dir) {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"testf"}, {"mfs", "extra"}, {"nope"}, {}} {
		a, calls, _, _ := fakeApp(t, true)
		if code := a.Main(args); code != exitUsage {
			t.Errorf("%q: exit %d, want %d", args, code, exitUsage)
		}
		if len(*calls) != 0 {
			t.Errorf("%q: docker was called", args)
		}
	}
}

func TestDryRunPrintsInsteadOfRunning(t *testing.T) {
	a, calls, stdout, _ := fakeApp(t, true)
	if code := a.Main(words("--dry-run mfs")); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(*calls) != 0 {
		t.Fatalf("docker was called: %q", *calls)
	}
	want := "docker compose -f compose.dev.yml exec appserver php artisan migrate:fresh\n" +
		"docker compose -f compose.dev.yml exec appserver php artisan db:seed --class=TestDataSeeder\n"
	if stdout.String() != want {
		t.Fatalf("got %q, want %q", stdout, want)
	}
}

func TestArgsAfterCommandAreNotBothyFlags(t *testing.T) {
	a, calls, _, _ := fakeApp(t, true)
	a.Main(words("artisan --dry-run --help"))
	want := words("compose -f compose.dev.yml exec appserver php artisan --dry-run --help")
	if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0], want) {
		t.Fatalf("got %q, want %q", *calls, want)
	}
}

func TestShellJoin(t *testing.T) {
	got := shellJoin([]string{"docker", "exec", "it's here", ""})
	want := `docker exec 'it'\''s here' ''`
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
