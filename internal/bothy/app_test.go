package bothy

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
		Output:    fakeOutput(nil),
		ConfigDir: filepath.Join(dir, "config"),
		Mkcert:    fakeMkcert,
	}
	// the settled state: certificate made, proxy files current
	if code, _ := a.ensureCert(); code != 0 {
		t.Fatal("making the fake certificate failed")
	}
	if _, err := a.writeProxyFiles(); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	return a, &calls, &stdout, &stderr
}

// fakeMkcert writes a real self-signed certificate, valid for two years, and
// a key file where `mkcert -cert-file C -key-file K host` would put them.
func fakeMkcert(args ...string) error {
	return writeCert(args, time.Now().Add(2*365*24*time.Hour))
}

func writeCert(args []string, notAfter time.Time) error {
	var certPath, keyPath string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "-cert-file":
			certPath = args[i+1]
		case "-key-file":
			keyPath = args[i+1]
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: certHost},
		NotBefore:    notAfter.Add(-3 * 365 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	return os.WriteFile(keyPath, []byte("fake key\n"), 0o600)
}

// proxyUp is `docker compose ls` output with the bothy proxy running.
const proxyUp = `[{"Name":"bothy-proxy","Status":"running(1)","ConfigFiles":"/cfg/proxy-compose.yml"}]`

// fakeOutput answers `docker compose ls` with the proxy running, and every
// other captured docker command with other.
func fakeOutput(other []byte) func([]string) ([]byte, error) {
	return func(args []string) ([]byte, error) {
		if len(args) > 1 && args[0] == "compose" && args[1] == "ls" {
			return []byte(proxyUp), nil
		}
		return other, nil
	}
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
	want := [][]string{
		words("compose -f compose.dev.yml exec -T appserver php artisan about"),
	}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("got %q, want %q", *calls, want)
	}
}

func TestMfs(t *testing.T) {
	const prefix = "compose -f compose.dev.yml exec appserver php artisan migrate:fresh --seed "
	cases := []struct{ args, want string }{
		{"mfs", "--seeder=TestDataSeeder"},
		{"mfs --seeder=DemoSeeder", "--seeder=DemoSeeder"},
		{"mfs --seeder DemoSeeder", "--seeder DemoSeeder"},
		{"mfs --drop-views", "--seeder=TestDataSeeder --drop-views"},
	}
	for _, c := range cases {
		a, calls, _, _ := fakeApp(t, true)
		if code := a.Main(words(c.args)); code != 0 {
			t.Fatalf("%s: exit %d", c.args, code)
		}
		want := [][]string{words(prefix + c.want)}
		if !reflect.DeepEqual(*calls, want) {
			t.Errorf("%s: got %q, want %q", c.args, *calls, want)
		}
	}
}

func TestExitCodePassesThrough(t *testing.T) {
	a, _, _, _ := fakeApp(t, true, 2)
	if code := a.Main([]string{"test"}); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
}

func TestCachedConfigStopsTheTests(t *testing.T) {
	for _, args := range [][]string{{"test"}, {"testf", "UserTest"}, {"ci"}} {
		a, calls, _, stderr := fakeApp(t, true)
		a.Output = func([]string) ([]byte, error) { t.Errorf("%q: asked docker something", args); return nil, nil }
		write(t, a.Dir, "bootstrap/cache/config.php", "<?php return [];\n")
		if code := a.Main(args); code != 1 {
			t.Errorf("%q: exit %d, want 1", args, code)
		}
		if len(*calls) != 0 {
			t.Errorf("%q: docker was called: %q", args, *calls)
		}
		if !strings.Contains(stderr.String(), "bothy artisan config:clear") {
			t.Errorf("%q: stderr should say how to clear it: %q", args, stderr)
		}
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
	for _, args := range [][]string{{"testf"}, {"nope"}, {}} {
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
	want := "docker compose -f compose.dev.yml exec appserver php artisan migrate:fresh --seed --seeder=TestDataSeeder\n"
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
