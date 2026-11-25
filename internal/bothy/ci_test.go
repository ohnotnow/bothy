package bothy

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ciApp is fakeApp set up for ci: appserver and database running, a matching
// .env and .env.example, a clean app/ folder, and pint and paratest
// installed. Docker calls, attached or piped, are logged in order.
func ciApp(t *testing.T, codes ...int) (*App, *[][]string, *strings.Builder, *strings.Builder) {
	t.Helper()
	a, _, _, _ := fakeApp(t, true)
	var stdout, stderr strings.Builder
	a.Stdout, a.Stderr = &stdout, &stderr
	var calls [][]string
	a.Run = func(args []string) int {
		calls = append(calls, args)
		if len(codes) == 0 {
			return 0
		}
		c := codes[0]
		codes = codes[1:]
		return c
	}
	a.Pipe = func(args []string, stdin io.Reader, _ io.Writer) int {
		b, _ := io.ReadAll(stdin)
		calls = append(calls, append(append([]string{}, args...), "<<", string(b)))
		return 0
	}
	a.Output = func(args []string) ([]byte, error) {
		if strings.Join(args, " ") == "compose -f "+ComposeFile+" ps --status running --services" {
			return []byte("appserver\ndatabase\nsendmailhog\n"), nil
		}
		return nil, nil
	}
	write(t, a.Dir, ".env.example", "APP_NAME=Laravel\nMAIL_HOST=mail\n")
	write(t, a.Dir, ".env", "APP_NAME=Real\nMAIL_HOST=mail\n")
	write(t, a.Dir, "app/Models/User.php", "<?php\nfunction add($a) { return $a; }\n")
	write(t, a.Dir, "vendor/bin/pint", "#!/usr/bin/env php\n")
	write(t, a.Dir, "vendor/brianium/paratest/composer.json", "{}\n")
	return a, &calls, &stdout, &stderr
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCiRunsEveryCheckAndPasses(t *testing.T) {
	a, calls, stdout, _ := ciApp(t)
	if code := a.Main([]string{"ci"}); code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, stdout)
	}
	c := compose
	testCmd := c("exec", "-e", "DB_CONNECTION=mysql", "-e", "DB_HOST=database", "-e", "DB_PORT=3306",
		"-e", "DB_DATABASE=bothy_ci", "-e", "DB_USERNAME=bothy_ci", "-e", "DB_PASSWORD=bothy_ci",
		"-e", "DB_URL=", "-e", "DATABASE_URL=", "appserver", "php", "artisan", "test", "--parallel")
	want := [][]string{
		c("exec", "appserver", "composer", "audit"),
		c("exec", "appserver", "php", "vendor/bin/pint", "--test"),
		append(c("exec", "-T", "database", "sh", "-c", ciSetup), "<<", ciSetupSQL),
		testCmd,
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls:\n%q\nwant:\n%q", *calls, want)
	}
	if !strings.Contains(stdout.String(), "all checks passed") {
		t.Fatalf("no pass summary:\n%s", stdout)
	}
}

func TestCiTestUserCanOnlyReachItsOwnDatabases(t *testing.T) {
	// The guard against wiping the developer's data: the test login's rights
	// stop at bothy_ci and parallel testing's bothy_ci_test_N copies.
	if !strings.Contains(ciSetupSQL, "ON `bothy\\_ci%`.* TO 'bothy_ci'@'%'") {
		t.Fatalf("grant should be limited to bothy_ci databases:\n%s", ciSetupSQL)
	}
	if strings.Contains(ciSetup, "$MARIADB_DATABASE") {
		t.Fatal("setup must not name the app's own database")
	}
}

func TestCiSaysItIsCreatingTheDatabaseBeforeCreatingIt(t *testing.T) {
	// The notice is only printed while the database doesn't exist, so asked
	// after the CREATE it would never show.
	notice, create := strings.Index(ciSetupSQL, ciCreating), strings.Index(ciSetupSQL, "CREATE DATABASE")
	if notice < 0 || create < notice {
		t.Fatalf("the notice should come before the CREATE:\n%s", ciSetupSQL)
	}
}

func TestCiKeepsGoingAfterAFailureAndFailsAtTheEnd(t *testing.T) {
	a, calls, stdout, _ := ciApp(t, 1) // composer audit fails
	if code := a.Main([]string{"ci"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(*calls) != 4 {
		t.Fatalf("every check should still run, got %d calls: %q", len(*calls), *calls)
	}
	out := stdout.String()
	if !strings.Contains(out, "1 of 5 checks failed") || !strings.Contains(out, "FAILED   composer audit") {
		t.Fatalf("summary should name the failure:\n%s", out)
	}
}

func TestCiStopsOnCtrlC(t *testing.T) {
	a, calls, _, _ := ciApp(t, exitInterrupted)
	if code := a.Main([]string{"ci"}); code != exitInterrupted {
		t.Fatalf("exit %d, want %d", code, exitInterrupted)
	}
	if len(*calls) != 1 {
		t.Fatalf("nothing should run after ctrl-c, got %q", *calls)
	}
}

func TestCiNeedsTheStackRunning(t *testing.T) {
	a, calls, _, stderr := ciApp(t)
	a.Output = func([]string) ([]byte, error) { return []byte("appserver\n"), nil }
	if code := a.Main([]string{"ci"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if len(*calls) != 0 || !strings.Contains(stderr.String(), "database isn't running") {
		t.Fatalf("calls %q, stderr %q", *calls, stderr)
	}
}

func TestCiFailsWithoutPintAndRunsSeriallyWithoutParatest(t *testing.T) {
	a, calls, stdout, _ := ciApp(t)
	os.RemoveAll(filepath.Join(a.Dir, "vendor"))
	if code := a.Main([]string{"ci"}); code != 1 {
		t.Fatalf("exit %d, want 1 (an app without Pint fails)\n%s", code, stdout)
	}
	for _, c := range *calls {
		if strings.Contains(strings.Join(c, " "), "pint") {
			t.Fatalf("pint ran without being installed: %q", c)
		}
	}
	last := (*calls)[len(*calls)-1]
	if last[len(last)-1] != "test" {
		t.Fatalf("tests should run serially without paratest: %q", last)
	}
	out := stdout.String()
	if !strings.Contains(out, "FAILED   pint") || !strings.Contains(out, "bothy composer require --dev laravel/pint") {
		t.Fatalf("summary should show pint failed, and the output say how to add it:\n%s", out)
	}
}

func TestCiEnvKeys(t *testing.T) {
	a, _, stdout, _ := ciApp(t)
	write(t, a.Dir, ".env.example", "# comment\nAPP_NAME=Laravel\nMAIL_HOST=mail\n\nexport QUEUE=sync\n")
	write(t, a.Dir, ".env", "APP_NAME=supersecretvalue\nMAIL_SERVER=mail\nQUEUE = sync\nBOTHY_FORWARD_DB_PORT=3307\n  line without equals\n")
	if code := a.Main([]string{"ci"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	out := stdout.String()
	for _, want := range []string{"not in your .env:\n  MAIL_HOST\n", "not in .env.example (add them there, even with an empty value):\n  MAIL_SERVER\n", "FAILED   env keys"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "supersecretvalue") || strings.Contains(out, "BOTHY_FORWARD_DB_PORT") || strings.Contains(out, "QUEUE") {
		t.Fatalf("printed a value, a BOTHY_ key or a matching key:\n%s", out)
	}
}

func TestCiEnvKeysWithoutFiles(t *testing.T) {
	a, _, stdout, _ := ciApp(t)
	os.Remove(filepath.Join(a.Dir, ".env"))
	a.Main([]string{"ci"})
	if !strings.Contains(stdout.String(), "FAILED   env keys") {
		t.Fatalf("a missing .env should fail:\n%s", stdout)
	}

	a, _, stdout, _ = ciApp(t)
	os.Remove(filepath.Join(a.Dir, ".env.example"))
	a.Main([]string{"ci"})
	if !strings.Contains(stdout.String(), "skipped  env keys") {
		t.Fatalf("a missing .env.example should skip:\n%s", stdout)
	}
}

func TestCiStrayDebug(t *testing.T) {
	a, _, stdout, _ := ciApp(t)
	write(t, a.Dir, "app/Http/Thing.php", "<?php\n    dd($x);\n    $c->dump();\n    $this->odd($y);\n    // dumpster(\n")
	if code := a.Main([]string{"ci"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	out := stdout.String()
	for _, want := range []string{"app/Http/Thing.php:2: dd($x);", "app/Http/Thing.php:3: $c->dump();", "FAILED   stray dd/dump"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "odd") || strings.Contains(out, "dumpster") {
		t.Fatalf("matched something that isn't dd or dump:\n%s", out)
	}
}

func TestCiDryRun(t *testing.T) {
	a, _, stdout, _ := ciApp(t)
	a.Output = func([]string) ([]byte, error) { t.Fatal("dry run asked docker something"); return nil, nil }
	if code := a.Main([]string{"--dry-run", "ci"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	out := stdout.String()
	for _, want := range []string{"docker compose -f compose.dev.yml exec appserver composer audit", "makes the bothy_ci database", "DB_DATABASE=bothy_ci", "--parallel"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCiTakesNoArguments(t *testing.T) {
	a, _, _, _ := ciApp(t)
	if code := a.Main([]string{"ci", "--filter", "x"}); code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
}
