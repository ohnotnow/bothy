package bothy

import (
	"bytes"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Shaped like real `docker compose ps --format json` output (one object per
// line), trimmed to the fields bothy reads.
const psOutput = `{"Service":"appserver","State":"running","Labels":"com.docker.compose.project=alpha-dev,traefik.enable=true,traefik.http.routers.alpha.rule=Host(` + "`alpha.bothy.localhost`" + `),traefik.http.services.alpha.loadbalancer.server.port=6060","Publishers":[{"URL":"127.0.0.1","TargetPort":6060,"PublishedPort":54123,"Protocol":"tcp"}]}
{"Service":"cache","State":"running","Publishers":[{"URL":"","TargetPort":6379,"PublishedPort":0,"Protocol":"tcp"}]}
{"Service":"database","State":"running","Publishers":[{"URL":"127.0.0.1","TargetPort":3306,"PublishedPort":4406,"Protocol":"tcp"}]}
{"Service":"sendmailhog","State":"running","Labels":"com.docker.compose.project=alpha-dev,traefik.enable=true,traefik.http.routers.mail-alpha.rule=Host(` + "`mail-alpha.bothy.localhost`" + `),traefik.http.services.mail-alpha.loadbalancer.server.port=8025","Publishers":[{"URL":"0.0.0.0","TargetPort":8025,"PublishedPort":54124,"Protocol":"tcp"},{"URL":"::","TargetPort":8025,"PublishedPort":54124,"Protocol":"tcp"},{"URL":"","TargetPort":1025,"PublishedPort":0,"Protocol":"tcp"}]}
`

func TestParsePublished(t *testing.T) {
	got, hosts, err := parsePublished([]byte(psOutput))
	if err != nil {
		t.Fatal(err)
	}
	wantHosts := []routed{{"appserver", "alpha.bothy.localhost"}, {"sendmailhog", "mail-alpha.bothy.localhost"}}
	if !reflect.DeepEqual(hosts, wantHosts) {
		t.Fatalf("hosts %+v, want %+v", hosts, wantHosts)
	}
	want := []published{
		{"appserver", "127.0.0.1", 54123, 6060, false},
		{"database", "127.0.0.1", 4406, 3306, false},
		{"sendmailhog", "0.0.0.0", 54124, 8025, true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParsePublishedPutsTheAppFirst(t *testing.T) {
	// compose ps lists services by name, so one sorting before appserver
	// must not push the app's own address down the list
	lines := strings.Split(strings.TrimSpace(psOutput), "\n")
	admin := strings.Replace(lines[3], `"Service":"sendmailhog"`, `"Service":"adminer"`, 1)
	_, hosts, err := parsePublished([]byte(admin + "\n" + lines[0] + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 || hosts[0].Service != "appserver" {
		t.Fatalf("hosts %+v, want appserver first", hosts)
	}
}

// infoApp answers compose ps with psOutput and the database question with db.
func infoApp(t *testing.T, db string) (*App, *[][]string, *bytes.Buffer) {
	t.Helper()
	a, calls, stdout, _ := fakeApp(t, true)
	a.Output = func(args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "compose ls"):
			return []byte(proxyUp), nil
		case strings.Contains(joined, " ps "):
			return []byte(psOutput), nil
		case strings.Contains(joined, "RUSTFS_ACCESS_KEY"):
			return []byte("\n\n"), nil // not an S3 server: empty variables
		}
		return []byte(db), nil
	}
	return a, calls, stdout
}

// minioRow is a RustFS service with its console routed through the proxy.
const minioRow = `{"Service":"minio","State":"running","Labels":"com.docker.compose.project=alpha-dev,traefik.enable=true,traefik.http.routers.minio-alpha.rule=Host(` + "`minio-alpha.bothy.localhost`" + `),traefik.http.services.minio-alpha.loadbalancer.server.port=9001","Publishers":[{"URL":"127.0.0.1","TargetPort":9001,"PublishedPort":54130,"Protocol":"tcp"}]}
`

func TestInfoShowsTheS3ConsoleLogin(t *testing.T) {
	a, _, out := infoApp(t, "laravel\nlaravel\nsecret\n")
	var asked [][]string
	a.Output = func(args []string) ([]byte, error) {
		asked = append(asked, args)
		joined := strings.Join(args, " ")
		switch {
		case strings.Contains(joined, " ps "):
			return []byte(psOutput + minioRow), nil
		case strings.Contains(joined, "RUSTFS_ACCESS_KEY") && slices.Contains(args, "minio"):
			return []byte("minioadmin\nminiosecret\n"), nil
		case strings.Contains(joined, "RUSTFS_ACCESS_KEY"):
			return []byte("\n\n"), nil
		}
		return []byte("laravel\nlaravel\nsecret\n"), nil
	}
	if code := a.Main([]string{"info"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	if !strings.Contains(got, "minio        https://minio-alpha.bothy.localhost  (user minioadmin, password miniosecret)\n") {
		t.Errorf("no console login in:\n%s", got)
	}
	if !strings.Contains(got, "sendmailhog  https://mail-alpha.bothy.localhost\n") {
		t.Errorf("the mail catcher should have no login:\n%s", got)
	}
	for _, c := range asked {
		if strings.Contains(strings.Join(c, " "), "RUSTFS_ACCESS_KEY") && slices.Contains(c, appService) {
			t.Errorf("asked the app server for an S3 login: %v", c)
		}
	}
}

func TestInfo(t *testing.T) {
	a, _, out := infoApp(t, "laravel\nlaravel\nsecret with spaces\n")
	if code := a.Main([]string{"info"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	got := out.String()
	for _, s := range []string{
		"app          https://alpha.bothy.localhost\nsendmailhog  https://mail-alpha.bothy.localhost\n",
		"http://127.0.0.1:54123",
		"127.0.0.1:4406  (database laravel, user laravel, password secret with spaces)",
		"http://127.0.0.1:54124  (open to the network)",
	} {
		if !strings.Contains(got, s) {
			t.Errorf("missing %q in:\n%s", s, got)
		}
	}
	if strings.Contains(got, "http://127.0.0.1:4406") || strings.Contains(got, "cache") {
		t.Errorf("unexpected line in:\n%s", got)
	}
}

func TestInfoWithoutDbDetails(t *testing.T) {
	a, _, out := infoApp(t, "")
	a.Main([]string{"info"})
	if !strings.Contains(out.String(), "127.0.0.1:4406\n") {
		t.Fatalf("got:\n%s", out)
	}
}

func TestStartPrintsInfo(t *testing.T) {
	a, calls, out := infoApp(t, "laravel\nlaravel\nsecret\n")
	if code := a.Main([]string{"start"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls %q", *calls)
	}
	if !strings.Contains(out.String(), "http://127.0.0.1:54123") {
		t.Fatalf("start did not print the addresses:\n%s", out)
	}
}

func TestStartDoesNotPrintInfoWhenItFails(t *testing.T) {
	a, _, out := infoApp(t, "")
	a.Run = func([]string) int { return 1 }
	if code := a.Main([]string{"start"}); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Fatalf("printed %q", out)
	}
}

func TestParsePublishedOpenOnEitherFamily(t *testing.T) {
	// loopback on IPv4 but every interface on IPv6 is still open
	out := `{"Service":"appserver","Publishers":[{"URL":"127.0.0.1","TargetPort":6060,"PublishedPort":54123},{"URL":"::","TargetPort":6060,"PublishedPort":54123}]}`
	got, _, err := parsePublished([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	want := []published{{"appserver", "127.0.0.1", 54123, 6060, true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// lanApp is infoApp answering compose ps with ps, and keeping stderr. It
// leaves BOTHY_BIND as it found it.
func lanApp(t *testing.T, ps string) (*App, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	t.Setenv(bindEnv, "")
	a, _, stdout, stderr := fakeApp(t, true)
	a.Output = func(args []string) ([]byte, error) {
		joined := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(joined, "compose ls"):
			return []byte(proxyUp), nil
		case strings.Contains(joined, " ps "):
			return []byte(ps), nil
		}
		return nil, nil
	}
	return a, stdout, stderr
}

func TestLanSetsBothyBind(t *testing.T) {
	for _, cmd := range []string{"start", "rebuild"} {
		for _, tc := range []struct {
			args []string
			want string
		}{
			{nil, "127.0.0.1"},
			{[]string{"--lan"}, "0.0.0.0"},
		} {
			t.Run(cmd+" "+strings.Join(tc.args, " "), func(t *testing.T) {
				a, _, _ := lanApp(t, psOutput)
				// a value left in the shell must not open the app unasked
				os.Setenv(bindEnv, "0.0.0.0")
				var seen []string
				a.Run = func(args []string) int {
					if slices.Contains(args, "up") {
						seen = append(seen, os.Getenv(bindEnv))
					}
					return 0
				}
				if code := a.Main(append([]string{cmd}, tc.args...)); code != 0 {
					t.Fatalf("exit %d", code)
				}
				if len(seen) != 1 || seen[0] != tc.want {
					t.Fatalf("BOTHY_BIND at up was %q, want %q", seen, tc.want)
				}
			})
		}
	}
}

func TestLanRejectsOtherArguments(t *testing.T) {
	a, _, stderr := lanApp(t, psOutput)
	if code := a.Main([]string{"start", "--wide"}); code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
	if code := a.Main([]string{"start", "now"}); code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "only --lan") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestLanDryRunShowsBothyBind(t *testing.T) {
	a, stdout, _ := lanApp(t, psOutput)
	if code := a.Main([]string{"--dry-run", "start", "--lan"}); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout.String(), "BOTHY_BIND=0.0.0.0 docker compose -f compose.dev.yml up -d --build") {
		t.Fatalf("got:\n%s", stdout)
	}
}

// allLoopback is psOutput with the mail catcher published on loopback only.
var allLoopback = strings.NewReplacer(`"URL":"0.0.0.0"`, `"URL":"127.0.0.1"`, `"URL":"::"`, `"URL":"127.0.0.1"`).Replace(psOutput)

func TestStartWarnsAboutOpenPortsWithoutLan(t *testing.T) {
	a, _, stderr := lanApp(t, psOutput)
	a.Main([]string{"start"})
	if !strings.Contains(stderr.String(), "warning: open to the network without --lan: sendmailhog") {
		t.Fatalf("stderr %q", stderr)
	}

	a, _, stderr = lanApp(t, allLoopback)
	a.Main([]string{"start"})
	if stderr.Len() != 0 {
		t.Fatalf("warned with nothing open: %q", stderr)
	}
}

func TestStartWithLanSaysWhatItOpened(t *testing.T) {
	a, stdout, stderr := lanApp(t, psOutput)
	a.Main([]string{"start", "--lan"})
	if !strings.Contains(stdout.String(), "Open to the network until the next start without --lan: sendmailhog.") || stderr.Len() != 0 {
		t.Fatalf("stdout:\n%s\nstderr %q", stdout, stderr)
	}

	a, _, stderr = lanApp(t, allLoopback)
	a.Main([]string{"start", "--lan"})
	if !strings.Contains(stderr.String(), "--lan opened nothing") {
		t.Fatalf("stderr %q", stderr)
	}
}

func TestPlainInfoDoesNotWarn(t *testing.T) {
	a, stdout, stderr := lanApp(t, psOutput)
	a.Main([]string{"info"})
	if stderr.Len() != 0 || strings.Contains(stdout.String(), "until the next start") {
		t.Fatalf("stdout:\n%s\nstderr %q", stdout, stderr)
	}
}
